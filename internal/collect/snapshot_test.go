package collect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/model"
)

type snapTestK8sCollector struct {
	nodes          []model.Node
	services       []model.Service
	endpointSlices []EndpointSlice
	pods           []model.Pod
	ingresses      []model.Ingress
	classes        []model.IngressClass
	serverVersion  string
	kubeProxy      string
	failing        string // method name that returns an error
}

var errSnapTest = errors.New("boom")

func (f snapTestK8sCollector) fail(method string) error {
	if f.failing == method {
		return errSnapTest
	}
	return nil
}

func (f snapTestK8sCollector) Nodes(context.Context) ([]model.Node, error) {
	return f.nodes, f.fail("Nodes")
}

func (f snapTestK8sCollector) Services(context.Context) ([]model.Service, error) {
	return f.services, f.fail("Services")
}

func (f snapTestK8sCollector) EndpointSlices(context.Context) ([]EndpointSlice, error) {
	return f.endpointSlices, f.fail("EndpointSlices")
}

func (f snapTestK8sCollector) Pods(context.Context) ([]model.Pod, error) {
	return f.pods, f.fail("Pods")
}

func (f snapTestK8sCollector) Ingresses(context.Context) ([]model.Ingress, error) {
	return f.ingresses, f.fail("Ingresses")
}

func (f snapTestK8sCollector) IngressClasses(context.Context) ([]model.IngressClass, error) {
	return f.classes, f.fail("IngressClasses")
}

func (f snapTestK8sCollector) ServerVersion(context.Context) (string, error) {
	if err := f.fail("ServerVersion"); err != nil {
		return "", err
	}
	return f.serverVersion, nil
}

func (f snapTestK8sCollector) KubeProxyVersion(context.Context) (string, error) {
	if err := f.fail("KubeProxyVersion"); err != nil {
		return "", err
	}
	return f.kubeProxy, nil
}

type fakeAWSCollector struct {
	vpcs       []string
	vpcsErr    error
	subnets    map[string]model.Subnet
	subnetsErr error
	lbs        []model.LoadBalancer
	lbsErr     error
	rds        []model.RDSInstance
	rdsErr     error
	caches     []model.CacheCluster
	cachesErr  error
	msk        []model.MSKCluster
	mskErr     error
	ipZones    map[string]string
	ipZonesErr error

	gotInstanceIDs []string
	gotSubnetVPCs  []string
	gotLBVPCs      []string
	gotMSKSubnets  map[string]model.Subnet
	gotIPVPCs      []string
	gotIPs         []string
	instanceCalls  int
	ipZonesCalls   int
}

func (f *fakeAWSCollector) InstanceVPCs(_ context.Context, ids []string) ([]string, error) {
	f.instanceCalls++
	f.gotInstanceIDs = ids
	return f.vpcs, f.vpcsErr
}

func (f *fakeAWSCollector) Subnets(_ context.Context, vpcs []string) (map[string]model.Subnet, error) {
	f.gotSubnetVPCs = vpcs
	return f.subnets, f.subnetsErr
}

func (f *fakeAWSCollector) IPZones(_ context.Context, vpcs, ips []string) (map[string]string, error) {
	f.ipZonesCalls++
	f.gotIPVPCs = vpcs
	f.gotIPs = ips
	return f.ipZones, f.ipZonesErr
}

func (f *fakeAWSCollector) LoadBalancers(_ context.Context, vpcs []string) ([]model.LoadBalancer, error) {
	f.gotLBVPCs = vpcs
	return f.lbs, f.lbsErr
}

func (f *fakeAWSCollector) RDSInstances(context.Context) ([]model.RDSInstance, error) {
	return f.rds, f.rdsErr
}

func (f *fakeAWSCollector) ElastiCacheClusters(context.Context) ([]model.CacheCluster, error) {
	return f.caches, f.cachesErr
}

func (f *fakeAWSCollector) MSKClusters(_ context.Context, subnets map[string]model.Subnet) ([]model.MSKCluster, error) {
	f.gotMSKSubnets = subnets
	return f.msk, f.mskErr
}

type fakeVolumeCollector struct {
	recv      map[string]float64
	err       error
	gotWindow string
}

func (f *fakeVolumeCollector) PodRecvBytesPerSecond(_ context.Context, window string) (map[string]float64, error) {
	f.gotWindow = window
	return f.recv, f.err
}

type fakeCostCollector struct {
	cost      model.CostData
	err       error
	calls     int
	gotRegion string
	gotMonth  time.Time
}

func (f *fakeCostCollector) InterAZDataTransferUSD(_ context.Context, region string, month time.Time) (model.CostData, error) {
	f.calls++
	f.gotRegion, f.gotMonth = region, month
	return f.cost, f.err
}

// snapTestSnapshot builds a fresh L0 snapshot; call it twice to get an untouched copy for comparison.
func snapTestSnapshot() model.Snapshot {
	return model.Snapshot{
		Cluster: "prod",
		Region:  "us-west-2",
		Nodes: []model.Node{
			{Name: "a", Zone: "us-west-2a", InstanceID: "i-b"},
			{Name: "b", Zone: "us-west-2b", InstanceID: "i-a"},
			{Name: "c", Zone: "us-west-2b", InstanceID: "i-b"},
			{Name: "d", Zone: "us-west-2c"},
		},
		Services: []model.Service{
			{Namespace: "web", Name: "front", Endpoints: []model.Endpoint{
				{IP: "10.0.3.1", Ready: true},
				{IP: "10.0.1.1", Ready: true, Zone: "us-west-2a"},
				{IP: "10.0.2.1", Ready: false},
			}},
			{Namespace: "web", Name: "api", Endpoints: []model.Endpoint{
				{IP: "10.0.3.1", Ready: true},
				{IP: "10.0.0.9", Ready: true},
				{IP: "", Ready: true},
			}},
		},
		Warnings: []string{"existing"},
	}
}

func TestGatherK8sJoinsEndpointsSortsAndPicksRegion(t *testing.T) {
	// Arrange
	k := snapTestK8sCollector{
		nodes: []model.Node{
			{Name: "n3", Region: "us-east-1"},
			{Name: "n1", Region: "us-west-2"},
			{Name: "n2", Region: "us-west-2"},
		},
		services: []model.Service{
			{Namespace: "web", Name: "front"},
			{Namespace: "api", Name: "front"},
			{Namespace: "web", Name: "empty"},
		},
		endpointSlices: []EndpointSlice{
			{Namespace: "web", ServiceName: "front", Endpoints: []model.Endpoint{
				{IP: "10.0.2.2", Ready: false, PodName: "front-2"},
				{IP: "10.0.1.1", Ready: true},
			}},
			{Namespace: "web", ServiceName: "front", Endpoints: []model.Endpoint{
				{IP: "10.0.2.2", Ready: true},
				{IP: "10.0.1.1", Ready: true, PodName: "front-1"},
			}},
			{Namespace: "api", ServiceName: "front", Endpoints: []model.Endpoint{{IP: "10.9.9.9", Ready: true}}},
			{Namespace: "web", ServiceName: "gone", Endpoints: []model.Endpoint{{IP: "10.8.8.8", Ready: true}}},
		},
		pods:          []model.Pod{{Namespace: "web", Name: "b"}, {Namespace: "api", Name: "z"}, {Namespace: "web", Name: "a"}},
		ingresses:     []model.Ingress{{Namespace: "web", Name: "y"}, {Namespace: "api", Name: "x"}},
		classes:       []model.IngressClass{{Name: "nginx"}, {Name: "alb"}},
		serverVersion: "v1.33.4-eks-1",
		kubeProxy:     "v1.33.0-eksbuild.2",
	}
	want := model.Snapshot{
		Cluster:          "prod",
		Region:           "us-west-2",
		ServerVersion:    "v1.33.4-eks-1",
		KubeProxyVersion: "v1.33.0-eksbuild.2",
		Nodes:            []model.Node{{Name: "n1", Region: "us-west-2"}, {Name: "n2", Region: "us-west-2"}, {Name: "n3", Region: "us-east-1"}},
		Services: []model.Service{
			{Namespace: "api", Name: "front", Endpoints: []model.Endpoint{{IP: "10.9.9.9", Ready: true}}},
			{Namespace: "web", Name: "empty", Endpoints: []model.Endpoint{}},
			{Namespace: "web", Name: "front", Endpoints: []model.Endpoint{
				{IP: "10.0.1.1", Ready: true, PodName: "front-1"},
				{IP: "10.0.2.2", Ready: true},
			}},
		},
		Pods:           []model.Pod{{Namespace: "api", Name: "z"}, {Namespace: "web", Name: "a"}, {Namespace: "web", Name: "b"}},
		Ingresses:      []model.Ingress{{Namespace: "api", Name: "x"}, {Namespace: "web", Name: "y"}},
		IngressClasses: []model.IngressClass{{Name: "alb"}, {Name: "nginx"}},
	}

	// Act
	got, err := GatherK8s(context.Background(), k, "prod")

	// Assert
	if err != nil {
		t.Fatalf("GatherK8s() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GatherK8s() =\n%+v\nwant\n%+v", got, want)
	}
	if k.nodes[0].Name != "n3" || k.pods[0].Name != "b" {
		t.Error("GatherK8s() sorted the collector's slices in place")
	}
}

func TestGatherK8sFailsWhenObjectListingFails(t *testing.T) {
	for _, method := range []string{"Nodes", "Services", "EndpointSlices", "Pods", "Ingresses", "IngressClasses"} {
		t.Run(method, func(t *testing.T) {
			// Arrange
			k := snapTestK8sCollector{failing: method}

			// Act
			_, err := GatherK8s(context.Background(), k, "prod")

			// Assert
			if !errors.Is(err, errSnapTest) {
				t.Errorf("GatherK8s() error = %v, want wrapped %v", err, errSnapTest)
			}
		})
	}
}

func TestGatherK8sTurnsVersionErrorsIntoWarnings(t *testing.T) {
	tests := []struct {
		method      string
		wantWarning string
	}{
		{"ServerVersion", "L0: 컨트롤 플레인 버전: boom"},
		{"KubeProxyVersion", "L0: kube-proxy 버전: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			// Arrange
			k := snapTestK8sCollector{failing: tt.method, nodes: []model.Node{{Name: "n1"}}, serverVersion: "v1.33.0", kubeProxy: "v1.33.0"}

			// Act
			got, err := GatherK8s(context.Background(), k, "prod")

			// Assert
			if err != nil {
				t.Fatalf("GatherK8s() error = %v, want nil", err)
			}
			if !reflect.DeepEqual(got.Warnings, []string{tt.wantWarning}) {
				t.Errorf("Warnings = %q, want [%q]", got.Warnings, tt.wantWarning)
			}
			if len(got.Nodes) != 1 {
				t.Errorf("Nodes = %+v, want the listed node kept", got.Nodes)
			}
		})
	}
}

func TestSnapMajorityRegion(t *testing.T) {
	tests := []struct {
		name    string
		regions []string
		want    string
	}{
		{"most common wins", []string{"us-east-1", "us-west-2", "us-west-2"}, "us-west-2"},
		{"tie goes to smallest", []string{"us-west-2", "eu-west-1"}, "eu-west-1"},
		{"tie with more nodes", []string{"b", "a", "b", "a", "c"}, "a"},
		{"empty labels ignored", []string{"", "", "ap-northeast-2"}, "ap-northeast-2"},
		{"no labels", []string{"", ""}, ""},
		{"no nodes", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			nodes := make([]model.Node, 0, len(tt.regions))
			for _, r := range tt.regions {
				nodes = append(nodes, model.Node{Region: r})
			}

			// Act
			got := snapMajorityRegion(nodes)

			// Assert
			if got != tt.want {
				t.Errorf("snapMajorityRegion(%q) = %q, want %q", tt.regions, got, tt.want)
			}
		})
	}
}

func TestSnapDedupeEndpointsPrefersReadyThenPodName(t *testing.T) {
	tests := []struct {
		name string
		eps  []model.Endpoint
		want model.Endpoint
	}{
		{
			"ready beats not ready with pod name",
			[]model.Endpoint{{IP: "10.0.0.1", PodName: "p"}, {IP: "10.0.0.1", Ready: true}},
			model.Endpoint{IP: "10.0.0.1", Ready: true},
		},
		{
			"ready kept over later not ready",
			[]model.Endpoint{{IP: "10.0.0.1", Ready: true}, {IP: "10.0.0.1", PodName: "p"}},
			model.Endpoint{IP: "10.0.0.1", Ready: true},
		},
		{
			"pod name breaks readiness tie",
			[]model.Endpoint{{IP: "10.0.0.1", Ready: true}, {IP: "10.0.0.1", Ready: true, PodName: "p"}},
			model.Endpoint{IP: "10.0.0.1", Ready: true, PodName: "p"},
		},
		{
			"first kept when equivalent",
			[]model.Endpoint{{IP: "10.0.0.1", PodName: "first"}, {IP: "10.0.0.1", PodName: "second"}},
			model.Endpoint{IP: "10.0.0.1", PodName: "first"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := snapDedupeEndpoints(tt.eps)

			// Assert
			if !reflect.DeepEqual(got, []model.Endpoint{tt.want}) {
				t.Errorf("snapDedupeEndpoints() = %+v, want [%+v]", got, tt.want)
			}
		})
	}
}

func TestWithAWSBuildsInventory(t *testing.T) {
	// Arrange
	s := snapTestSnapshot()
	a := &fakeAWSCollector{
		vpcs:    []string{"vpc-2", "", "vpc-1", "vpc-2"},
		subnets: map[string]model.Subnet{"subnet-1": {AZ: "us-west-2a", VPCID: "vpc-1"}},
		lbs:     []model.LoadBalancer{{Name: "lb", VPCID: "vpc-other"}},
		rds:     []model.RDSInstance{{ID: "keep", VPCID: "vpc-1"}, {ID: "drop", VPCID: "vpc-9"}},
		caches:  []model.CacheCluster{{ID: "drop", VPCID: ""}, {ID: "keep", VPCID: "vpc-2"}},
		msk:     []model.MSKCluster{{Name: "keep", VPCID: "vpc-2"}, {Name: "drop", VPCID: "vpc-3"}},
		ipZones: map[string]string{"10.0.3.1": "us-west-2c"},
	}
	want := &model.AWSInventory{
		VPCIDs:        []string{"vpc-1", "vpc-2"},
		Subnets:       a.subnets,
		IPZones:       a.ipZones,
		LoadBalancers: a.lbs,
		RDS:           []model.RDSInstance{{ID: "keep", VPCID: "vpc-1"}},
		Caches:        []model.CacheCluster{{ID: "keep", VPCID: "vpc-2"}},
		MSK:           []model.MSKCluster{{Name: "keep", VPCID: "vpc-2"}},
	}

	// Act
	got := WithAWS(context.Background(), s, a)

	// Assert
	if !reflect.DeepEqual(got.AWS, want) {
		t.Errorf("AWS = %+v, want %+v", got.AWS, want)
	}
	if !reflect.DeepEqual(got.Warnings, []string{"existing"}) {
		t.Errorf("Warnings = %q, want only the existing one", got.Warnings)
	}
	wantCalls := map[string][]string{
		"instance IDs":   {"i-a", "i-b"},
		"subnet VPCs":    {"vpc-1", "vpc-2"},
		"LB VPCs":        {"vpc-1", "vpc-2"},
		"unzoned ready":  {"10.0.0.9", "10.0.3.1"},
		"IP zone VPCs":   {"vpc-1", "vpc-2"},
		"MSK subnet IDs": {"subnet-1"},
	}
	gotCalls := map[string][]string{
		"instance IDs":   a.gotInstanceIDs,
		"subnet VPCs":    a.gotSubnetVPCs,
		"LB VPCs":        a.gotLBVPCs,
		"unzoned ready":  a.gotIPs,
		"IP zone VPCs":   a.gotIPVPCs,
		"MSK subnet IDs": snapTestKeys(a.gotMSKSubnets),
	}
	if !reflect.DeepEqual(gotCalls, wantCalls) {
		t.Errorf("collector calls = %q, want %q", gotCalls, wantCalls)
	}
}

func snapTestKeys(m map[string]model.Subnet) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestWithAWSSkipsWhenVPCsUnavailable(t *testing.T) {
	tests := []struct {
		name          string
		nodes         []model.Node
		aws           *fakeAWSCollector
		wantWarning   string
		wantInstCalls int
	}{
		{
			name:        "no instance IDs",
			nodes:       []model.Node{{Name: "kind-node"}},
			aws:         &fakeAWSCollector{vpcs: []string{"vpc-1"}},
			wantWarning: "L1: 노드에 EC2 instance ID가 없어 AWS 조회를 건너뜀",
		},
		{
			name:          "instance lookup fails",
			nodes:         []model.Node{{Name: "n", InstanceID: "i-1"}},
			aws:           &fakeAWSCollector{vpcsErr: errSnapTest},
			wantWarning:   "L1: 노드 인스턴스 VPC: boom",
			wantInstCalls: 1,
		},
		{
			name:          "no VPCs found",
			nodes:         []model.Node{{Name: "n", InstanceID: "i-1"}},
			aws:           &fakeAWSCollector{},
			wantWarning:   snapWarnNoVPCs,
			wantInstCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Nodes: tt.nodes, AWS: &model.AWSInventory{VPCIDs: []string{"stale"}}}

			// Act
			got := WithAWS(context.Background(), s, tt.aws)

			// Assert
			if got.AWS != nil {
				t.Errorf("AWS = %+v, want nil", got.AWS)
			}
			if !reflect.DeepEqual(got.Warnings, []string{tt.wantWarning}) {
				t.Errorf("Warnings = %q, want [%q]", got.Warnings, tt.wantWarning)
			}
			if tt.aws.instanceCalls != tt.wantInstCalls {
				t.Errorf("InstanceVPCs calls = %d, want %d", tt.aws.instanceCalls, tt.wantInstCalls)
			}
		})
	}
}

func TestWithAWSPartialFailuresKeepOtherParts(t *testing.T) {
	// Arrange
	s := snapTestSnapshot()
	a := &fakeAWSCollector{
		vpcs:       []string{"vpc-1"},
		subnetsErr: errSnapTest,
		lbs:        []model.LoadBalancer{{Name: "lb", VPCID: "vpc-1"}},
		lbsErr:     errors.Join(errors.New("load balancer gone: not found"), errors.New("classic: boom")),
		rdsErr:     errSnapTest,
		caches:     []model.CacheCluster{{ID: "c", VPCID: "vpc-1"}},
		mskErr:     errSnapTest,
		ipZonesErr: errSnapTest,
	}
	wantAWS := &model.AWSInventory{
		VPCIDs:        []string{"vpc-1"},
		Subnets:       map[string]model.Subnet{},
		LoadBalancers: []model.LoadBalancer{{Name: "lb", VPCID: "vpc-1"}},
		RDS:           []model.RDSInstance{},
		Caches:        []model.CacheCluster{{ID: "c", VPCID: "vpc-1"}},
		MSK:           []model.MSKCluster{},
	}
	wantWarnings := []string{
		"existing",
		"L1: 서브넷: boom",
		"L1: 로드밸런서: load balancer gone: not found",
		"L1: 로드밸런서: classic: boom",
		"L1: RDS 인스턴스: boom",
		"L1: MSK 클러스터: boom",
		"L1: 엔드포인트 IP의 AZ: boom",
	}

	// Act
	got := WithAWS(context.Background(), s, a)

	// Assert
	if !reflect.DeepEqual(got.AWS, wantAWS) {
		t.Errorf("AWS = %+v, want %+v", got.AWS, wantAWS)
	}
	if !reflect.DeepEqual(got.Warnings, wantWarnings) {
		t.Errorf("Warnings = %q, want %q", got.Warnings, wantWarnings)
	}
}

func TestWithAWSKeepsEmptyLoadBalancersWhenNothingWasDescribed(t *testing.T) {
	// Arrange
	a := &fakeAWSCollector{vpcs: []string{"vpc-1"}, lbsErr: errSnapTest}

	// Act
	got := WithAWS(context.Background(), model.Snapshot{Nodes: []model.Node{{Name: "n", InstanceID: "i-1"}}}, a)

	// Assert
	if got.AWS == nil || got.AWS.LoadBalancers == nil || len(got.AWS.LoadBalancers) != 0 {
		t.Errorf("AWS = %+v, want an empty non-nil load balancer list", got.AWS)
	}
	if !reflect.DeepEqual(got.Warnings, []string{"L1: 로드밸런서: boom"}) {
		t.Errorf("Warnings = %q, want the load balancer warning", got.Warnings)
	}
}

func TestWithAWSSkipsIPZonesWhenEveryReadyEndpointHasZone(t *testing.T) {
	// Arrange
	s := model.Snapshot{
		Nodes: []model.Node{{Name: "n", InstanceID: "i-1"}},
		Services: []model.Service{{Endpoints: []model.Endpoint{
			{IP: "10.0.0.1", Ready: true, Zone: "us-west-2a"},
			{IP: "10.0.0.2", Ready: false},
		}}},
	}
	a := &fakeAWSCollector{vpcs: []string{"vpc-1"}, ipZones: map[string]string{"x": "y"}}

	// Act
	got := WithAWS(context.Background(), s, a)

	// Assert
	if a.ipZonesCalls != 0 {
		t.Errorf("IPZones calls = %d, want 0", a.ipZonesCalls)
	}
	if got.AWS == nil || got.AWS.IPZones != nil {
		t.Errorf("AWS = %+v, want inventory without IPZones", got.AWS)
	}
}

func TestEnrichersDoNotMutateInput(t *testing.T) {
	tests := []struct {
		name   string
		enrich func(model.Snapshot) model.Snapshot
	}{
		{"WithAWS", func(s model.Snapshot) model.Snapshot {
			return WithAWS(context.Background(), s, &fakeAWSCollector{vpcs: []string{"vpc-1"}, rdsErr: errSnapTest})
		}},
		{"WithVolume", func(s model.Snapshot) model.Snapshot {
			return WithVolume(context.Background(), s, &fakeVolumeCollector{err: errSnapTest}, "7d")
		}},
		{"WithCost", func(s model.Snapshot) model.Snapshot {
			return WithCost(context.Background(), s, &fakeCostCollector{err: errSnapTest}, time.Now())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := snapTestSnapshot()
			s.Warnings = append(make([]string, 0, 8), s.Warnings...)
			pristine := snapTestSnapshot()

			// Act
			first := tt.enrich(s)
			firstWarnings := append([]string(nil), first.Warnings...)
			_ = WithVolume(context.Background(), s, &fakeVolumeCollector{err: errors.New("second")}, "1d")

			// Assert
			if !reflect.DeepEqual(s, pristine) {
				t.Errorf("input changed:\n%+v\nwant\n%+v", s, pristine)
			}
			if !reflect.DeepEqual(first.Warnings, firstWarnings) {
				t.Errorf("result warnings aliased by a later call: %q, want %q", first.Warnings, firstWarnings)
			}
			if len(first.Warnings) != 2 {
				t.Errorf("Warnings = %q, want existing plus one", first.Warnings)
			}
		})
	}
}

func TestWithVolume(t *testing.T) {
	t.Run("success sets volume", func(t *testing.T) {
		// Arrange
		v := &fakeVolumeCollector{recv: map[string]float64{"web/front-1": 1024}}

		// Act
		got := WithVolume(context.Background(), snapTestSnapshot(), v, "7d")

		// Assert
		want := &model.VolumeData{Window: "7d", RecvBytesPerS: map[string]float64{"web/front-1": 1024}}
		if !reflect.DeepEqual(got.Volume, want) || v.gotWindow != "7d" {
			t.Errorf("Volume = %+v (window %q), want %+v", got.Volume, v.gotWindow, want)
		}
		if !reflect.DeepEqual(got.Warnings, []string{"existing"}) {
			t.Errorf("Warnings = %q, want unchanged", got.Warnings)
		}
	})
	t.Run("failure warns and leaves volume nil", func(t *testing.T) {
		// Arrange
		v := &fakeVolumeCollector{err: errSnapTest}
		s := snapTestSnapshot()
		s.Volume = &model.VolumeData{Window: "stale"}

		// Act
		got := WithVolume(context.Background(), s, v, "7d")

		// Assert
		if got.Volume != nil {
			t.Errorf("Volume = %+v, want nil", got.Volume)
		}
		if len(got.Warnings) != 2 || !strings.HasPrefix(got.Warnings[1], "L2: ") || !strings.HasSuffix(got.Warnings[1], "boom") {
			t.Errorf("Warnings = %q, want an L2 warning", got.Warnings)
		}
	})
}

func TestWithVolumeRejectsResultsMatchingNoEndpointPod(t *testing.T) {
	services := []model.Service{
		{Namespace: "shop", Name: "api", Endpoints: []model.Endpoint{
			{IP: "10.0.1.1", Ready: true, PodName: "api-1"},
			{IP: "10.0.1.2", Ready: true, PodName: "api-2"},
			{IP: "10.0.1.3", Ready: false, PodName: "api-old"},
		}},
		{Namespace: "shop", Name: "legacy", Endpoints: []model.Endpoint{{IP: "10.0.9.9", Ready: true}}},
	}
	tests := []struct {
		name        string
		services    []model.Service
		recv        map[string]float64
		wantVolume  bool
		wantWarning bool
	}{
		{name: "empty result", services: services, recv: map[string]float64{}, wantWarning: true},
		{name: "only another cluster's pods", services: services, recv: map[string]float64{"other/pod-x": 1234}, wantWarning: true},
		{name: "same pod name in another namespace", services: services, recv: map[string]float64{"web/api-1": 1}, wantWarning: true},
		{name: "only a not-ready endpoint pod", services: services, recv: map[string]float64{"shop/api-old": 5}, wantWarning: true},
		{name: "partial overlap keeps volume", services: services, recv: map[string]float64{"shop/api-2": 5, "other/x": 1}, wantVolume: true},
		{name: "no pod-named ready endpoints keeps volume", services: services[1:], recv: map[string]float64{"other/x": 1}, wantVolume: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Services: tt.services, Warnings: []string{"existing"}}
			v := &fakeVolumeCollector{recv: tt.recv}

			// Act
			got := WithVolume(context.Background(), s, v, "7d")

			// Assert
			if gotVolume := got.Volume != nil; gotVolume != tt.wantVolume {
				t.Errorf("Volume = %+v, want set=%v", got.Volume, tt.wantVolume)
			}
			wantWarnings := []string{"existing"}
			if tt.wantWarning {
				wantWarnings = append(wantWarnings, "L2: Prometheus 결과에 이 클러스터의 엔드포인트 pod가 없음 — 다른 클러스터, cAdvisor 메트릭 누락, 또는 수집한 지 10분 미만")
			}
			if !reflect.DeepEqual(got.Warnings, wantWarnings) {
				t.Errorf("Warnings = %q, want %q", got.Warnings, wantWarnings)
			}
		})
	}
}

func TestWithCost(t *testing.T) {
	month := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	cost := model.CostData{Month: "2026-08", ActualUSD: 42.5, UsageType: "USW2-DataTransfer-Regional-Bytes"}
	tests := []struct {
		name        string
		region      string
		collector   *fakeCostCollector
		wantCost    *model.CostData
		wantWarning string
		wantCalls   int
	}{
		{"success sets cost", "us-west-2", &fakeCostCollector{cost: cost}, &cost, "", 1},
		{"failure warns", "us-west-2", &fakeCostCollector{err: errSnapTest}, nil, "L3: inter-AZ 실제 비용: boom", 1},
		{"unknown region skips call", "", &fakeCostCollector{cost: cost}, nil, snapWarnNoRegion, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Region: tt.region}

			// Act
			got := WithCost(context.Background(), s, tt.collector, month)

			// Assert
			if !reflect.DeepEqual(got.Cost, tt.wantCost) {
				t.Errorf("Cost = %+v, want %+v", got.Cost, tt.wantCost)
			}
			var wantWarnings []string
			if tt.wantWarning != "" {
				wantWarnings = []string{tt.wantWarning}
			}
			if !reflect.DeepEqual(got.Warnings, wantWarnings) {
				t.Errorf("Warnings = %q, want %q", got.Warnings, wantWarnings)
			}
			if tt.collector.calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", tt.collector.calls, tt.wantCalls)
			}
			if tt.wantCalls > 0 && (tt.collector.gotRegion != tt.region || !tt.collector.gotMonth.Equal(month)) {
				t.Errorf("called with %q %v, want %q %v", tt.collector.gotRegion, tt.collector.gotMonth, tt.region, month)
			}
		})
	}
}
