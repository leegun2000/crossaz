package classify

import (
	"maps"
	"slices"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func nodesIn(zones ...string) []model.Node {
	nodes := make([]model.Node, 0, len(zones))
	for i, z := range zones {
		nodes = append(nodes, model.Node{Name: "n" + string(rune('0'+i)), Zone: z})
	}
	return nodes
}

func readyEP(ip, zone string, forZones ...string) model.Endpoint {
	return model.Endpoint{IP: ip, Zone: zone, Ready: true, ForZones: forZones}
}

func clusterIPService(name string, eps ...model.Endpoint) model.Service {
	return model.Service{Namespace: "app", Name: name, Type: "ClusterIP", ClusterIP: "172.20.0.10", Endpoints: eps}
}

func onlyPath(t *testing.T, s model.Snapshot) model.Path {
	t.Helper()
	paths := BuildPaths(s)
	if len(paths) != 1 {
		t.Fatalf("BuildPaths() returned %d paths, want 1: %+v", len(paths), paths)
	}
	return paths[0]
}

func TestBuildPathsPlacementFacts(t *testing.T) {
	tests := []struct {
		name         string
		nodes        []model.Node
		endpoints    []model.Endpoint
		ipZones      map[string]string
		wantSpread   string
		wantCoverage string
		wantDest     model.AZDistribution
	}{
		{"endpoints in every caller zone", nodesIn("a", "b", "c"),
			[]model.Endpoint{readyEP("10.0.0.1", "a"), readyEP("10.0.0.2", "b"), readyEP("10.0.0.3", "c")},
			nil, "multi", "full", model.AZDistribution{"a": 1, "b": 1, "c": 1}},
		{"endpoints in one of three caller zones", nodesIn("a", "b", "c"),
			[]model.Endpoint{readyEP("10.0.0.1", "a"), readyEP("10.0.0.2", "a")},
			nil, "multi", "partial", model.AZDistribution{"a": 2}},
		{"single-zone cluster and endpoints", nodesIn("a", "a"),
			[]model.Endpoint{readyEP("10.0.0.1", "a")},
			nil, "single", "full", model.AZDistribution{"a": 1}},
		{"single-zone cluster, endpoint zone unknown", nodesIn("a"),
			[]model.Endpoint{readyEP("10.0.0.1", "")},
			nil, "unknown", "unknown", model.AZDistribution{}},
		{"unknown endpoint does not hide known spread or coverage", nodesIn("a", "b"),
			[]model.Endpoint{readyEP("10.0.0.1", "a"), readyEP("10.0.0.2", "b"), readyEP("10.0.0.3", "")},
			nil, "multi", "full", model.AZDistribution{"a": 1, "b": 1}},
		{"all endpoint zones unknown in a multi-zone cluster", nodesIn("a", "b", "c"),
			[]model.Endpoint{readyEP("10.0.0.1", ""), readyEP("10.0.0.2", "")},
			nil, "unknown", "unknown", model.AZDistribution{}},
		{"unknown endpoint makes coverage unknown", nodesIn("a", "b", "c"),
			[]model.Endpoint{readyEP("10.0.0.1", "a"), readyEP("10.0.0.2", "")},
			nil, "multi", "unknown", model.AZDistribution{"a": 1}},
		{"nodes without zone are not callers", nodesIn("a", ""),
			[]model.Endpoint{readyEP("10.0.0.1", "a")},
			nil, "single", "full", model.AZDistribution{"a": 1}},
		{"not-ready endpoints are ignored", nodesIn("a"),
			[]model.Endpoint{readyEP("10.0.0.1", "a"), {IP: "10.0.0.2", Zone: "b"}},
			nil, "single", "full", model.AZDistribution{"a": 1}},
		{"endpoint zone resolved from AWS IPZones", nodesIn("a"),
			[]model.Endpoint{readyEP("10.0.0.1", "")},
			map[string]string{"10.0.0.1": "b"}, "multi", "partial", model.AZDistribution{"b": 1}},
		{"endpoint zone label beats IPZones", nodesIn("a"),
			[]model.Endpoint{readyEP("10.0.0.1", "a")},
			map[string]string{"10.0.0.1": "b"}, "single", "full", model.AZDistribution{"a": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Nodes: tt.nodes, Services: []model.Service{clusterIPService("api", tt.endpoints...)}}
			if tt.ipZones != nil {
				s.AWS = &model.AWSInventory{IPZones: tt.ipZones}
			}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.AZSpread != tt.wantSpread || p.Facts.ZoneCoverage != tt.wantCoverage {
				t.Errorf("spread/coverage = %s/%s, want %s/%s", p.Facts.AZSpread, p.Facts.ZoneCoverage, tt.wantSpread, tt.wantCoverage)
			}
			if !maps.Equal(p.Dest.AZDist, tt.wantDest) {
				t.Errorf("Dest.AZDist = %v, want %v", p.Dest.AZDist, tt.wantDest)
			}
		})
	}
}

func TestBuildPathsPrivateIP(t *testing.T) {
	tests := []struct {
		name string
		ips  []string
		want bool
	}{
		{"10/8", []string{"10.1.2.3"}, true},
		{"172.16/12", []string{"172.31.255.1"}, true},
		{"192.168/16", []string{"192.168.0.9"}, true},
		{"100.64/10 (EKS custom networking)", []string{"100.127.0.1"}, true},
		{"just outside 172.16/12", []string{"172.32.0.1"}, false},
		{"just outside 100.64/10", []string{"100.128.0.1"}, false},
		{"public IPv4", []string{"10.0.0.1", "54.1.2.3"}, false},
		{"IPv6 endpoint", []string{"10.0.0.1", "fd00::1"}, false},
		{"unparsable IP", []string{"not-an-ip"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			var eps []model.Endpoint
			for _, ip := range tt.ips {
				eps = append(eps, readyEP(ip, "a"))
			}
			s := model.Snapshot{Nodes: nodesIn("a"), Services: []model.Service{clusterIPService("api", eps...)}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.PrivateIP != tt.want {
				t.Errorf("PrivateIP = %v, want %v", p.Facts.PrivateIP, tt.want)
			}
		})
	}
}

func TestBuildPathsSkipsExternalNameAndUnreadyServices(t *testing.T) {
	// Arrange
	external := clusterIPService("ext", readyEP("10.0.0.1", "a"))
	external.Type = "ExternalName"
	unready := clusterIPService("down", model.Endpoint{IP: "10.0.0.2", Zone: "a"})
	empty := clusterIPService("empty")
	s := model.Snapshot{Nodes: nodesIn("a"), Services: []model.Service{external, unready, empty}}

	// Act
	paths := BuildPaths(s)

	// Assert
	if len(paths) != 0 {
		t.Errorf("BuildPaths() = %+v, want none", paths)
	}
}

func TestBuildPathsInClusterSourceAndDest(t *testing.T) {
	// Arrange
	s := model.Snapshot{
		Nodes: nodesIn("a", "b", "b"),
		Services: []model.Service{
			clusterIPService("api", readyEP("10.0.0.1", "a")),
			clusterIPService("web", readyEP("10.0.0.2", "b")),
		},
	}

	// Act
	paths := BuildPaths(s)

	// Assert
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	p := paths[0]
	wantSource := model.Workload{Name: "*", Kind: "cluster", AZDist: model.AZDistribution{"a": 1, "b": 2}}
	if p.Source.Name != wantSource.Name || p.Source.Kind != wantSource.Kind || !maps.Equal(p.Source.AZDist, wantSource.AZDist) {
		t.Errorf("Source = %+v, want %+v", p.Source, wantSource)
	}
	wantFacts := model.Facts{Route: model.RouteClusterIP, TargetKind: "service", Traffic: "client", SameVPC: true, PrivateIP: true,
		AZSpread: "multi", ZoneCoverage: "partial", Evidence: "k8s"}
	if p.Dest.Kind != "service" || p.Dest.Ref != "app/api" || p.Route != model.RouteClusterIP || p.Facts != wantFacts {
		t.Errorf("path = %+v, want dest app/api facts %+v", p, wantFacts)
	}
	paths[0].Source.AZDist["a"] = 99
	if paths[1].Source.AZDist["a"] != 1 {
		t.Error("paths share one caller distribution map")
	}
}

func TestBuildPathsTopologyHints(t *testing.T) {
	hinted := []model.Endpoint{readyEP("10.0.0.1", "a", "a"), readyEP("10.0.0.2", "b", "b")}
	tests := []struct {
		name          string
		kubeProxy     string
		annotations   map[string]string
		mutate        func(model.Service) model.Service
		endpoints     []model.Endpoint
		wantHints     bool
		wantZoneLocal bool
		wantProxy     bool // Facts.KubeProxyHints: kube-proxy is on the path and known to honor hints
	}{
		{"kube-proxy 1.31 honors hints without annotation", "v1.31.2-eksbuild.3", nil, nil, hinted, true, true, true},
		{"kube-proxy 1.29 without annotation ignores hints", "v1.29.0-eksbuild.1", nil, nil, hinted, false, false, false},
		{"kube-proxy 1.29 with topology-mode Auto", "v1.29.0-eksbuild.1", map[string]string{annTopologyMode: "Auto"}, nil, hinted, true, true, true},
		{"kube-proxy 1.29 with deprecated hints annotation", "v1.29.0", map[string]string{annTopologyAwareHints: "auto"}, nil, hinted, true, true, true},
		{"kube-proxy 1.29 with topology-mode Disabled", "v1.29.0", map[string]string{annTopologyMode: "Disabled"}, nil, hinted, false, false, false},
		{"kube-proxy 1.23 is too old even with annotation", "v1.23.9", map[string]string{annTopologyMode: "Auto"}, nil, hinted, false, false, false},
		{"unknown kube-proxy version (replaced)", "", nil, nil, hinted, false, false, false},
		{"one endpoint without hints", "v1.31.0", nil, nil, []model.Endpoint{readyEP("10.0.0.1", "a", "a"), readyEP("10.0.0.2", "b")}, false, false, true},
		{"hints that miss a caller zone", "v1.31.0", nil, nil, []model.Endpoint{readyEP("10.0.0.1", "a", "a")}, true, false, true},
		{"endpoint hinted for another zone is not zone-local", "v1.31.0", nil, nil, []model.Endpoint{readyEP("10.0.0.1", "a", "a", "b")}, true, false, true},
		{"headless bypasses kube-proxy", "v1.31.0", nil, func(s model.Service) model.Service {
			s.ClusterIP = "None"
			return s
		}, hinted, false, false, false},
		{"internalTrafficPolicy Local is zone-local without hints", "", nil, func(s model.Service) model.Service {
			s.InternalTrafficPolicy = "Local"
			return s
		}, []model.Endpoint{readyEP("10.0.0.1", "a")}, false, true, false},
		{"internalTrafficPolicy Local on headless is not zone-local", "", nil, func(s model.Service) model.Service {
			s.ClusterIP, s.InternalTrafficPolicy = "None", "Local"
			return s
		}, []model.Endpoint{readyEP("10.0.0.1", "a")}, false, false, false},
		{"CLB instance targets with Cluster policy pass through kube-proxy", "v1.31.0", nil, func(s model.Service) model.Service {
			s.Type = "LoadBalancer"
			return s
		}, hinted, true, true, true},
		{"LB with Local policy skips the kube-proxy hop", "v1.31.0", nil, func(s model.Service) model.Service {
			s.Type, s.ExternalTrafficPolicy = "LoadBalancer", "Local"
			return s
		}, hinted, false, false, false},
		{"NLB ip targets bypass kube-proxy", "v1.31.0", map[string]string{annLBType: "nlb-ip"}, func(s model.Service) model.Service {
			s.Type = "LoadBalancer"
			return s
		}, hinted, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := clusterIPService("api", tt.endpoints...)
			svc.Annotations = tt.annotations
			if tt.mutate != nil {
				svc = tt.mutate(svc)
			}
			s := model.Snapshot{KubeProxyVersion: tt.kubeProxy, Nodes: nodesIn("a", "b"), Services: []model.Service{svc}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.TopologyHints != tt.wantHints || p.Facts.ZoneLocal != tt.wantZoneLocal || p.Facts.KubeProxyHints != tt.wantProxy {
				t.Errorf("hints/zoneLocal/kubeProxyHints = %v/%v/%v, want %v/%v/%v",
					p.Facts.TopologyHints, p.Facts.ZoneLocal, p.Facts.KubeProxyHints, tt.wantHints, tt.wantZoneLocal, tt.wantProxy)
			}
		})
	}
}

func TestBuildPathsDeterministicOrder(t *testing.T) {
	// Arrange
	aws := &model.AWSInventory{
		VPCIDs: []string{"vpc-1"},
		RDS:    []model.RDSInstance{{ID: "db", AZ: "a", SecondaryAZ: "b", MultiAZ: true, VPCID: "vpc-1"}},
		MSK:    []model.MSKCluster{{Name: "events", BrokerZones: []string{"a", "b"}, VPCID: "vpc-1"}},
	}
	services := []model.Service{
		clusterIPService("zeta", readyEP("10.0.0.1", "a")),
		{Namespace: "alpha", Name: "api", Type: "ClusterIP", ClusterIP: "172.20.0.1", Endpoints: []model.Endpoint{readyEP("10.0.0.2", "a")}},
		clusterIPService("beta", readyEP("10.0.0.3", "a")),
	}
	reversed := slices.Clone(services)
	slices.Reverse(reversed)

	// Act
	first := BuildPaths(model.Snapshot{Nodes: nodesIn("a"), Services: services, AWS: aws})
	second := BuildPaths(model.Snapshot{Nodes: nodesIn("a"), Services: reversed, AWS: aws})

	// Assert
	var got []string
	for i, p := range first {
		got = append(got, p.Key()+" "+p.Facts.Traffic)
		if second[i].Key() != p.Key() || second[i].Facts.Traffic != p.Facts.Traffic {
			t.Errorf("order differs at %d: %s vs %s", i, p.Key(), second[i].Key())
		}
	}
	want := []string{"alpha/api client", "app/beta client", "app/zeta client",
		"msk/events client", "msk/events replication", "rds/db client", "rds/db replication"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestMinorVersion(t *testing.T) {
	tests := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"v1.33.4-eks-abc", 33, true},
		{"v1.31.2-eksbuild.3", 31, true},
		{"1.30", 30, true},
		{"v1.29.0-minimal-eksbuild.1", 29, true},
		{"v1.33+", 33, true},
		{"", 0, false},
		{"latest", 0, false},
		{"v1.", 0, false},
		{"vX.30", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := minorVersion(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("minorVersion(%q) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
