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
type fakeInventory struct{ noVPCs bool }

func (f fakeInventory) InstanceVPCs(context.Context, []string) ([]string, error) {
	if f.noVPCs {
		return nil, nil
	}
	return []string{"vpc-1"}, nil
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

type costCall struct {
	region string
	month  time.Time
}

type fakeCost struct{ calls *[]costCall }

func (f fakeCost) InterAZDataTransferUSD(_ context.Context, region string, month time.Time) (model.CostData, error) {
	*f.calls = append(*f.calls, costCall{region, month})
	return model.CostData{Month: month.Format("2006-01"), ActualUSD: 1, UsageType: "DataTransfer-Regional-Bytes"}, nil
}

// liveFake records what the live flow asked for.
type liveFake struct {
	kubeErr    error
	nodesErr   error
	awsErr     error
	noVPCs     bool
	awsRegion  string
	awsRegions []string
	costCalls  []costCall
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
			return awsSession{region: f.awsRegion, inventory: fakeInventory{noVPCs: f.noVPCs}, cost: fakeCost{calls: &f.costCalls}}, nil
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
		wantCost      []costCall
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
			wantCost:      []costCall{{"us-east-1", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
		},
		{
			name:          "flags override cluster and region",
			fake:          liveFake{},
			args:          []string{"--cluster", "prod-x", "--region", "eu-west-1", "--verify"},
			wantLayers:    []string{"L0", "L1", "L3"},
			wantCluster:   "prod-x",
			wantRegion:    "eu-west-1",
			wantAWSRegion: []string{"eu-west-1"},
			wantCost:      []costCall{{"eu-west-1", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
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
			if !slices.Equal(fake.costCalls, tt.wantCost) {
				t.Errorf("cost calls = %v, want %v", fake.costCalls, tt.wantCost)
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

func TestPreviousMonth(t *testing.T) {
	seoul := time.FixedZone("KST", 9*3600)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"mid month", time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{"january wraps to december", time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)},
		{"local time already in the next month uses UTC", time.Date(2026, 10, 1, 5, 0, 0, 0, seoul), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := previousMonth(tt.now); !got.Equal(tt.want) {
				t.Errorf("previousMonth(%v) = %v, want %v", tt.now, got, tt.want)
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
			if err != nil || sess.region != tt.wantRegion || sess.inventory == nil || sess.cost == nil {
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
