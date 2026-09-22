package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/collect"
	"github.com/leegun2000/crossaz/internal/model"
)

type fakeKube struct{ nodesErr error }

func (f fakeKube) Nodes(context.Context) ([]model.Node, error) {
	return []model.Node{
		{Name: "n1", Zone: "us-east-1a", Region: "us-east-1", InstanceID: "i-1"},
		{Name: "n2", Zone: "us-east-1b", Region: "us-east-1", InstanceID: "i-2"},
	}, f.nodesErr
}

func (fakeKube) Services(context.Context) ([]model.Service, error) {
	return []model.Service{{Namespace: "payments", Name: "api", Type: "ClusterIP", ClusterIP: "172.20.0.9"}}, nil
}

func (fakeKube) EndpointSlices(context.Context) ([]collect.EndpointSlice, error) {
	return []collect.EndpointSlice{{Namespace: "payments", ServiceName: "api", Endpoints: []model.Endpoint{
		{IP: "10.0.1.5", PodName: "api-a", NodeName: "n1", Zone: "us-east-1a", Ready: true},
		{IP: "10.0.2.5", PodName: "api-b", NodeName: "n2", Zone: "us-east-1b", Ready: true},
	}}}, nil
}

func (fakeKube) Pods(context.Context) ([]model.Pod, error) {
	return []model.Pod{
		{Namespace: "payments", Name: "api-a", NodeName: "n1", IP: "10.0.1.5"},
		{Namespace: "payments", Name: "api-b", NodeName: "n2", IP: "10.0.2.5"},
	}, nil
}

func (fakeKube) Ingresses(context.Context) ([]model.Ingress, error)           { return nil, nil }
func (fakeKube) IngressClasses(context.Context) ([]model.IngressClass, error) { return nil, nil }
func (fakeKube) ServerVersion(context.Context) (string, error)                { return "v1.33.1-eks-1", nil }
func (fakeKube) KubeProxyVersion(context.Context) (string, error)             { return "v1.33.1-eksbuild.1", nil }

// fakeInventory with noVPCs behaves like credentials for another account: the node filter matches nothing.
type fakeInventory struct {
	noVPCs bool
	err    error
}

func (f fakeInventory) InstanceVPCs(context.Context, []string) ([]string, []string, error) {
	if f.err != nil {
		return nil, nil, f.err
	}
	if f.noVPCs {
		return nil, nil, nil
	}
	return []string{"vpc-1"}, []string{"111122223333"}, nil
}
func (fakeInventory) IPZones(context.Context, []string, []string) (map[string]string, error) {
	return nil, nil
}
func (fakeInventory) LoadBalancers(context.Context, []string) ([]model.LoadBalancer, error) {
	return nil, nil
}
func (fakeInventory) ElastiCacheClusters(context.Context) ([]model.CacheCluster, error) {
	return nil, nil
}
func (fakeInventory) MSKClusters(context.Context, map[string]model.Subnet) ([]model.MSKCluster, error) {
	return nil, nil
}
func (fakeInventory) Subnets(context.Context, []string) (map[string]model.Subnet, error) {
	return map[string]model.Subnet{"subnet-1": {AZ: "us-east-1a", VPCID: "vpc-1"}}, nil
}
func (fakeInventory) RDSInstances(context.Context) ([]model.RDSInstance, error) {
	return []model.RDSInstance{{ID: "orders-db", AZ: "us-east-1a", VPCID: "vpc-1"}}, nil
}

// fakeBill serves testBill (or err) over whatever window it is asked for, and records "region [accounts] from..to".
type fakeBill struct {
	calls *[]string
	err   error
}

func (f fakeBill) DailyBill(_ context.Context, region string, accounts []string, from, to time.Time) (model.Bill, error) {
	*f.calls = append(*f.calls, fmt.Sprintf("%s %v %s..%s", region, accounts, from.Format(time.DateOnly), to.Format(time.DateOnly)))
	if f.err != nil {
		return model.Bill{}, f.err
	}
	return testBill(from, to), nil
}

// testBill has $2/day of InterZone (the last day not final), PublicIP and $0 xAZ lines, and an unknown operation.
func testBill(from, to time.Time) model.Bill {
	const regional = "DataTransfer-Regional-Bytes"
	b := model.Bill{From: from, To: to, Warnings: []string{`모르는 operation "Mystery": family other로 분류(총액 포함)`}}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		estimated := day.Equal(to.AddDate(0, 0, -1))
		b.Lines = append(b.Lines,
			model.BillLine{Day: day, UsageType: regional, Operation: "InterZone-In", Family: model.FamilyInterZone, USD: 1, GB: 100, Estimated: estimated},
			model.BillLine{Day: day, UsageType: regional, Operation: "InterZone-Out", Family: model.FamilyInterZone, USD: 1, GB: 100, Estimated: estimated},
			model.BillLine{Day: day, UsageType: regional, Operation: "PublicIP-Out", Family: model.FamilyPublicIP, USD: 0.5, GB: 50, Estimated: estimated},
			model.BillLine{Day: day, UsageType: "USE1-DataTransfer-xAZ-In-Bytes", Operation: "Unknown", Family: model.FamilyXAZFree, GB: 30, Estimated: estimated},
		)
	}
	b.Lines = append(b.Lines, model.BillLine{Day: from, UsageType: regional, Operation: "Mystery", Family: model.FamilyOther, USD: 0.25, GB: 25})
	return b
}

// liveFake records what the live flow asked for.
type liveFake struct {
	kubeErr    error
	nodesErr   error
	awsErr     error
	noVPCs     bool
	vpcErr     error
	awsRegion  string
	awsRegions []string
	billErr    error
	billCalls  []string
}

func (f *liveFake) sources() sources {
	return sources{
		kube: func(string, string) (collect.K8sCollector, string, error) {
			return fakeKube{nodesErr: f.nodesErr}, "ctx-cluster", f.kubeErr
		},
		aws: func(_ context.Context, region string) (awsSession, error) {
			f.awsRegions = append(f.awsRegions, region)
			if f.awsErr != nil {
				return awsSession{}, f.awsErr
			}
			return awsSession{region: f.awsRegion, inventory: fakeInventory{noVPCs: f.noVPCs, err: f.vpcErr}, bill: fakeBill{calls: &f.billCalls, err: f.billErr}}, nil
		},
		now: func() time.Time { return time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC) },
	}
}

func promServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[`+
			`{"metric":{"namespace":"payments","pod":"api-a"},"value":[1,"1000000"]},`+
			`{"metric":{"namespace":"payments","pod":"api-b"},"value":[1,"1000000"]}]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestRunLiveCollection(t *testing.T) {
	prom := promServer(t)
	tests := []struct {
		name          string
		fake          liveFake
		args          []string
		wantLayers    []string
		wantCluster   string
		wantRegion    string
		wantAWSRegion []string // regions passed to the AWS loader; nil = never called
		wantBill      []string
		wantWarnings  []string // prefixes that must each match a warning
	}{
		{
			name:          "every layer with node region and context cluster",
			fake:          liveFake{awsRegion: "us-west-2"},
			args:          []string{"--prometheus-url", prom, "--verify"},
			wantLayers:    []string{"L0", "L1", "L2", "L3"},
			wantCluster:   "ctx-cluster",
			wantRegion:    "us-east-1",
			wantAWSRegion: []string{"us-east-1"},
			wantBill:      []string{"us-east-1 [111122223333] 2026-08-31..2026-09-14"},
		},
		{
			name:          "flags override cluster and region",
			fake:          liveFake{},
			args:          []string{"--cluster", "prod-x", "--region", "eu-west-1", "--verify"},
			wantLayers:    []string{"L0", "L1", "L3"},
			wantCluster:   "prod-x",
			wantRegion:    "eu-west-1",
			wantAWSRegion: []string{"eu-west-1"},
			wantBill:      []string{"eu-west-1 [111122223333] 2026-08-31..2026-09-14"},
		},
		{
			name:          "missing credentials fall back to L0 and skip verify",
			fake:          liveFake{awsErr: errors.New("no credentials")},
			args:          []string{"--verify", "--prometheus-url", prom},
			wantLayers:    []string{"L0", "L2"},
			wantCluster:   "ctx-cluster",
			wantRegion:    "us-east-1",
			wantAWSRegion: []string{"us-east-1"},
			wantWarnings:  []string{warnNoAWSCredentials, warnVerifyWithoutL1},
		},
		{
			name:          "credentials that cannot see the nodes skip verify",
			fake:          liveFake{noVPCs: true},
			args:          []string{"--verify"},
			wantLayers:    []string{"L0"},
			wantCluster:   "ctx-cluster",
			wantRegion:    "us-east-1",
			wantAWSRegion: []string{"us-east-1"},
			wantWarnings:  []string{warnVerifyWithoutL1},
		},
		{
			name:        "no-aws never loads AWS",
			fake:        liveFake{},
			args:        []string{"--no-aws"},
			wantLayers:  []string{"L0"},
			wantCluster: "ctx-cluster",
			wantRegion:  "us-east-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			fake := tt.fake

			// Act
			code, stdout, stderr := runTest(t, fake.sources(), append([]string{"scan", "--json"}, tt.args...)...)

			// Assert
			if code != exitOK {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			var r model.ScanResult
			if err := json.Unmarshal([]byte(stdout), &r); err != nil {
				t.Fatalf("parse output: %v", err)
			}
			if !slices.Equal(r.Layers, tt.wantLayers) || r.Cluster != tt.wantCluster || r.Region != tt.wantRegion {
				t.Errorf("layers %v cluster %q region %q; want %v %q %q", r.Layers, r.Cluster, r.Region, tt.wantLayers, tt.wantCluster, tt.wantRegion)
			}
			if !slices.Equal(fake.awsRegions, tt.wantAWSRegion) {
				t.Errorf("AWS loader regions = %q, want %q", fake.awsRegions, tt.wantAWSRegion)
			}
			if !slices.Equal(fake.billCalls, tt.wantBill) {
				t.Errorf("bill calls = %q, want %q", fake.billCalls, tt.wantBill)
			}
			for _, want := range tt.wantWarnings {
				if !slices.ContainsFunc(r.Warnings, func(w string) bool { return strings.HasPrefix(w, want) }) {
					t.Errorf("warnings = %q, want %q", r.Warnings, want)
				}
			}
		})
	}
}

func TestRunLiveCollectionUsesAWSConfigRegionWhenNodesHaveNone(t *testing.T) {
	// Arrange
	fake := &liveFake{awsRegion: "ap-south-1"}
	src := fake.sources()
	src.kube = func(string, string) (collect.K8sCollector, string, error) { return regionlessKube{}, "c", nil }

	// Act
	code, stdout, stderr := runTest(t, src, "scan", "--json")

	// Assert
	var r model.ScanResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil || code != exitOK {
		t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
	}
	if r.Region != "ap-south-1" || !slices.Equal(fake.awsRegions, []string{""}) {
		t.Errorf("region = %q, loader regions = %q", r.Region, fake.awsRegions)
	}
}

type regionlessKube struct{ fakeKube }

func (regionlessKube) Nodes(context.Context) ([]model.Node, error) {
	return []model.Node{{Name: "n1", Zone: "ap-south-1a", InstanceID: "i-1"}}, nil
}

func TestRunLiveCommandsAndFailures(t *testing.T) {
	tests := []struct {
		name       string
		fake       liveFake
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"explain without L1 has no external targets", liveFake{}, []string{"explain", "rds/orders-db", "--no-aws"}, exitError, "", "no path for rds/orders-db"},
		{"explain sees L1 targets", liveFake{}, []string{"explain", "rds/orders-db"}, exitOK, "rds-cross-az", ""},
		{"explain shows why L1 is missing", liveFake{awsErr: errors.New("expired token")}, []string{"explain", "rds/orders-db"}, exitError, "", "crossaz: warning: " + warnNoAWSCredentials + ": expired token"},
		{"dump-fixture anonymizes live data", liveFake{}, []string{"scan", "--dump-fixture"}, exitOK, `"cluster": "ctx-cluster"`, ""},
		{"kubeconfig failure is fatal", liveFake{kubeErr: errors.New("load kubeconfig: boom")}, []string{"scan"}, exitError, "", "load kubeconfig: boom"},
		{"listing failure is fatal", liveFake{nodesErr: errors.New("forbidden")}, []string{"scan", "--no-aws"}, exitError, "", "gather kubernetes objects: forbidden"},
		{"dump-fixture reports collection failure", liveFake{kubeErr: errors.New("boom")}, []string{"scan", "--dump-fixture"}, exitError, "", "boom"},
		{"explain reports collection failure", liveFake{kubeErr: errors.New("boom")}, []string{"explain", "a/b"}, exitError, "", "boom"},
		{"bill reports collection failure", liveFake{kubeErr: errors.New("boom")}, []string{"bill"}, exitError, "", "boom"},
		{"bill needs AWS credentials", liveFake{awsErr: errors.New("expired token")}, []string{"bill"}, exitError, "", "bill needs AWS credentials: " + warnNoAWSCredentials + ": expired token"},
		{"bill fails on a Cost Explorer error", liveFake{billErr: errors.New("ce:GetCostAndUsage: throttled")}, []string{"bill"}, exitError, "", "crossaz: ce:GetCostAndUsage: throttled"},
		{"verify turns a Cost Explorer error into a warning", liveFake{billErr: errors.New("throttled")}, []string{"scan", "--verify"}, exitOK, "L3: 일별 청구: throttled", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			fake := tt.fake

			// Act
			code, stdout, stderr := runTest(t, fake.sources(), tt.args...)

			// Assert
			if code != tt.wantCode || !strings.Contains(stdout, tt.wantStdout) || !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("exit %d (want %d)\nstdout: %s\nstderr: %s", code, tt.wantCode, stdout, stderr)
			}
			if strings.Contains(stdout, "10.0.1.5") {
				t.Errorf("output leaks a raw pod IP:\n%s", stdout)
			}
		})
	}
}

func TestClosedDays(t *testing.T) {
	seoul := time.FixedZone("KST", 9*3600)
	tests := []struct {
		name     string
		now      time.Time
		days     int
		wantFrom string
		wantTo   string
	}{
		{"mid day ends at today's UTC midnight", time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC), 14, "2026-08-31", "2026-09-14"},
		{"exactly midnight excludes the new day", time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), 1, "2026-09-13", "2026-09-14"},
		{"local time already tomorrow uses the UTC day", time.Date(2026, 3, 1, 5, 0, 0, 0, seoul), 90, "2025-11-30", "2026-02-28"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			from, to := closedDays(tt.now, tt.days)

			// Assert
			if from.Format(time.DateOnly) != tt.wantFrom || to.Format(time.DateOnly) != tt.wantTo || to.Location() != time.UTC || to.Hour() != 0 {
				t.Errorf("closedDays = %v..%v, want %s..%s UTC midnight", from, to, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestClusterName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"arn:aws:eks:ap-northeast-2:111122223333:cluster/prod-a", "prod-a"},
		{"prod-a.ap-northeast-2.eksctl.io", "prod-a.ap-northeast-2.eksctl.io"},
		{"arn:aws:iam::111122223333:role/x", "arn:aws:iam::111122223333:role/x"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := clusterName(tt.in); got != tt.want {
			t.Errorf("clusterName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

const testKubeconfig = `apiVersion: v1
kind: Config
current-context: prod
clusters:
- name: arn:aws:eks:ap-northeast-2:111122223333:cluster/prod-a
  cluster: {server: "https://127.0.0.1:1"}
- name: dev-b
  cluster: {server: "https://127.0.0.1:2"}
contexts:
- name: prod
  context: {cluster: "arn:aws:eks:ap-northeast-2:111122223333:cluster/prod-a", user: u}
- name: dev
  context: {cluster: dev-b, user: u}
users:
- name: u
  user: {token: t}
`

func TestLiveKubeResolvesContextCluster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, kubeconfig, context, wantCluster, wantErr string
	}{
		{name: "current context EKS ARN", kubeconfig: path, wantCluster: "prod-a"},
		{name: "explicit context", kubeconfig: path, context: "dev", wantCluster: "dev-b"},
		{name: "unknown context", kubeconfig: path, context: "nope", wantErr: "load kubeconfig"},
		{name: "missing file", kubeconfig: filepath.Join(t.TempDir(), "absent"), wantErr: "load kubeconfig"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			k, cluster, err := liveKube(tt.kubeconfig, tt.context)

			// Assert
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || k == nil || cluster != tt.wantCluster {
				t.Errorf("liveKube = %v, %q, %v; want cluster %q", k, cluster, err, tt.wantCluster)
			}
		})
	}
}

func TestLiveAWSChecksCredentials(t *testing.T) {
	isolateAWSEnv(t)
	tests := []struct {
		name       string
		env        map[string]string
		region     string
		wantRegion string
		wantErr    string
	}{
		{name: "static credentials with explicit region", env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret"}, region: "ap-northeast-2", wantRegion: "ap-northeast-2"},
		{name: "region from environment", env: map[string]string{"AWS_ACCESS_KEY_ID": "AKIDEXAMPLE", "AWS_SECRET_ACCESS_KEY": "secret", "AWS_REGION": "eu-central-1"}, wantRegion: "eu-central-1"},
		{name: "no credentials anywhere", region: "us-east-1", wantErr: "retrieve AWS credentials"},
		{name: "unknown profile", env: map[string]string{"AWS_PROFILE": "missing"}, wantErr: "load AWS config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			// Act
			sess, err := liveAWS(context.Background(), tt.region)

			// Assert
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || sess.region != tt.wantRegion || sess.inventory == nil || sess.bill == nil {
				t.Errorf("liveAWS = %+v, %v; want region %q", sess, err, tt.wantRegion)
			}
		})
	}
}

// isolateAWSEnv keeps the developer's AWS setup (and IMDS) out of the test.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "AWS_DEFAULT_PROFILE",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_ROLE_ARN",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// promCountingServer answers every query with one payments pod and records the queries it saw.
func promCountingServer(t *testing.T) (string, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query"))
		mu.Unlock()
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[`+
			`{"metric":{"namespace":"payments","pod":"api-a"},"value":[1,"1000000"]}]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &queries
}

func TestRunPromSelector(t *testing.T) {
	tests := []struct {
		name        string
		selector    string
		wantCode    int
		wantQueries int
		wantInQuery string
	}{
		{"invalid selector exits 2 before any request", `cluster="prod-a"}`, exitUsage, 0, ""},
		{"unterminated string exits 2 before any request", `cluster="prod-a`, exitUsage, 0, ""},
		{"valid selector reaches every vector selector", `{cluster="prod-a"}`, exitOK, 1, `{pod!="",cluster="prod-a"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			prom, queries := promCountingServer(t)
			src := noSources(t)
			if tt.wantCode == exitOK {
				src = (&liveFake{}).sources()
			}

			// Act
			code, _, stderr := runTest(t, src, "scan", "--json", "--no-aws", "--prometheus-url", prom, "--prom-selector", tt.selector)

			// Assert
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d, stderr = %q", code, tt.wantCode, stderr)
			}
			if len(*queries) != tt.wantQueries {
				t.Fatalf("requests = %d, want %d", len(*queries), tt.wantQueries)
			}
			if tt.wantInQuery != "" && strings.Count((*queries)[0], tt.wantInQuery) != 2 {
				t.Errorf("query %q, want %q in both vector selectors", (*queries)[0], tt.wantInQuery)
			}
		})
	}
}

func TestRunScanVerifyUsesDailyInterZoneLines(t *testing.T) {
	// Arrange
	fake := &liveFake{}

	// Act
	code, stdout, stderr := runTest(t, fake.sources(), "scan", "--json", "--verify", "--prometheus-url", promServer(t))

	// Assert
	var r model.ScanResult
	if err := json.Unmarshal([]byte(stdout), &r); err != nil || code != exitOK || r.Verify == nil {
		t.Fatalf("exit = %d, err = %v, stderr = %q, verify = %v", code, err, stderr, r.Verify)
	}
	v := r.Verify
	// 14 days of $2 InterZone, the not-final one counted (PublicIP, xAZ and other left out) = $60 per 30 days.
	if v.Window != "2026-08-31..2026-09-13" || v.Days != 14 || v.EstimatedDays != 1 || v.ActualUSD == nil || *v.ActualUSD != 60 || v.UsageType != "DataTransfer-Regional-Bytes" {
		t.Errorf("Verify = %+v (actual %v)", *v, v.ActualUSD)
	}
}

func TestRunBillMatchesGoldenFiles(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		golden string
	}{
		{"table", []string{"bill"}, "bill.txt"},
		{"json", []string{"bill", "--json", "--days", "3"}, "bill.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			fake := &liveFake{}

			// Act
			code, stdout, stderr := runTest(t, fake.sources(), tt.args...)

			// Assert
			if code != exitOK || stderr != "" {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			assertGolden(t, tt.golden, stdout)
		})
	}
}

func TestRunBillRefusesWhenL1FindsNoNodeVPC(t *testing.T) {
	// Arrange
	fake := &liveFake{noVPCs: true}

	// Act
	code, _, stderr := runTest(t, fake.sources(), "bill")

	// Assert
	if code != exitError || !strings.Contains(stderr, "bill: L1이 클러스터 노드를 찾지 못함") {
		t.Errorf("exit = %d, stderr = %q, want exit %d and the no-VPC error", code, stderr, exitError)
	}
	if len(fake.billCalls) != 0 {
		t.Errorf("bill calls = %q, want none: the credentials may belong to another account", fake.billCalls)
	}
}

func TestRunBillQuery(t *testing.T) {
	tests := []struct {
		name         string
		fake         liveFake
		args         []string
		wantCall     string
		wantWarnings []string
	}{
		{"accounts from L1 and the default 14 closed days", liveFake{}, []string{"bill"}, "us-east-1 [111122223333] 2026-08-31..2026-09-14", nil},
		{"--days and --region", liveFake{}, []string{"bill", "--days", "90", "--region", "eu-west-1"}, "eu-west-1 [111122223333] 2026-06-16..2026-09-14", nil},
		{"without L1 accounts the query is region only", liveFake{vpcErr: errors.New("throttled")}, []string{"bill"}, "us-east-1 [] 2026-08-31..2026-09-14",
			[]string{"L1: 노드 인스턴스 VPC: throttled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			fake := tt.fake

			// Act
			code, stdout, stderr := runTest(t, fake.sources(), append(tt.args, "--json")...)

			// Assert
			var r model.BillReport
			if err := json.Unmarshal([]byte(stdout), &r); err != nil || code != exitOK {
				t.Fatalf("exit = %d, err = %v, stderr = %q", code, err, stderr)
			}
			if !slices.Equal(fake.billCalls, []string{tt.wantCall}) {
				t.Errorf("bill calls = %q, want [%q]", fake.billCalls, tt.wantCall)
			}
			for _, want := range tt.wantWarnings {
				if !slices.Contains(r.Warnings, want) {
					t.Errorf("warnings = %q, want %q", r.Warnings, want)
				}
			}
		})
	}
}
