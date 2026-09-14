package estimate_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

const tolerance = 1e-9

func dist(zones ...string) model.AZDistribution {
	d := model.AZDistribution{}
	for _, z := range zones {
		d[z]++
	}
	return d
}

func ptr(v float64) *float64 { return &v }

func readyEP(pod, zone string) model.Endpoint {
	return model.Endpoint{IP: "10.0.0.1", PodName: pod, Zone: zone, Ready: true}
}

func service(ns, name string, eps ...model.Endpoint) model.Service {
	return model.Service{Namespace: ns, Name: name, Type: "ClusterIP", ClusterIP: "10.100.0.1", Endpoints: eps}
}

// billablePath is a classified clusterip path to ns/name over 3 even zones.
func billablePath(ref string) model.Path {
	return model.Path{
		Source:         model.Workload{Name: "*", Kind: "cluster", AZDist: dist("a", "b", "c")},
		Dest:           model.Target{Kind: model.TargetService, Ref: ref, AZDist: dist("a", "b", "c")},
		Route:          model.RouteClusterIP,
		Facts:          model.Facts{Route: model.RouteClusterIP, TargetKind: model.TargetService, AZSpread: "multi"},
		Billable:       true,
		RuleID:         "clusterip-cross-az",
		RateGB:         0.02,
		RuleConfidence: model.High,
		Confidence:     model.High,
	}
}

func assertFloatPtr(t *testing.T, field string, got, want *float64, tol float64) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want nil", field, *got)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %v", field, *want)
	case want != nil && math.Abs(*got-*want) > tol:
		t.Errorf("%s = %v, want %v (±%v)", field, *got, *want, tol)
	}
}

func estimateOne(t *testing.T, p model.Path, s model.Snapshot) model.Path {
	t.Helper()
	out := estimate.Estimate([]model.Path{p}, s)
	if len(out) != 1 {
		t.Fatalf("Estimate returned %d paths, want 1", len(out))
	}
	return out[0]
}

// selfHints hints every endpoint for its own zone (what PreferClose/PreferSameZone produce).
func selfHints(dests model.AZDistribution) map[string]model.AZDistribution {
	out := map[string]model.AZDistribution{}
	for z, c := range dests {
		out[z] = model.AZDistribution{z: c}
	}
	return out
}

func TestEstimateCrossAZFracUsesForeignHints(t *testing.T) {
	// Arrange: topology-mode Auto hinted the zone-b endpoint for zone c callers.
	p := billablePath("ns/svc")
	p.Source.AZDist = dist("a", "b", "c")
	p.Dest.AZDist = dist("a", "b")
	p.Facts.TopologyHints = true
	p.Dest.HintedFor = map[string]model.AZDistribution{"a": dist("a"), "b": dist("b"), "c": dist("b")}

	// Act
	got := estimateOne(t, p, model.Snapshot{})

	// Assert
	if math.Abs(got.CrossAZFrac-1.0/3) > tolerance {
		t.Errorf("CrossAZFrac = %v, want 1/3", got.CrossAZFrac)
	}
}

func TestEstimateCrossAZFrac(t *testing.T) {
	abc, partial := dist("a", "b", "c"), dist("a", "a", "b")
	tests := []struct {
		name          string
		route         model.RouteKind
		callers       model.AZDistribution
		dests         model.AZDistribution
		zoneLocal     bool
		topologyHints bool
		want          float64
	}{
		{"zone local is zero", model.RouteClusterIP, abc, abc, true, true, 0},
		{"self topology hints with partial coverage count only uncovered caller zones", model.RouteClusterIP, abc, partial, false, true, 1.0 / 3},
		{"topology hints without callers is zero", model.RouteClusterIP, model.AZDistribution{}, dist("a"), false, true, 0},
		{"default formula over three even zones", model.RouteClusterIP, abc, dist("a", "a", "b", "b", "c", "c"), false, false, 2.0 / 3},
		{"all endpoints in one zone of a three-zone cluster still cross", model.RouteClusterIP, abc, dist("a", "a", "a"), false, false, 2.0 / 3},
		{"dest zone outside caller zones", model.RouteClusterIP, dist("a", "b"), dist("a", "d"), false, false, 0.75},
		{"alb instance hop that is zone local is zero", model.RouteALB, abc, abc, true, true, 0},
		{"clb instance hop that is zone local is zero", model.RouteCLB, abc, abc, true, true, 0},
		{"zonal nlb instance hop uses hinted fraction", model.RouteNLBZonal, abc, partial, false, true, 1.0 / 3},
		{"nlb cross-zone ignores zone local because the LB-to-node leg still crosses", model.RouteNLBXZ, abc, abc, true, true, 2.0 / 3},
		{"nlb cross-zone ignores partial hints", model.RouteNLBXZ, abc, partial, false, true, 2.0 / 3},
		{"nlb with unknown cross-zone ignores zone local", model.RouteNLB, abc, abc, true, true, 2.0 / 3},
		{"headless ignores zone local", model.RouteHeadless, abc, abc, true, false, 2.0 / 3},
		{"external target ignores zone local", model.RouteExternal, abc, abc, true, false, 2.0 / 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			p := billablePath("ns/svc")
			p.Billable = false
			p.Route, p.Facts.Route = tt.route, tt.route
			p.Source.AZDist = tt.callers
			p.Dest.AZDist = tt.dests
			p.Facts.ZoneLocal = tt.zoneLocal
			p.Facts.TopologyHints = tt.topologyHints
			if tt.topologyHints {
				p.Dest.HintedFor = selfHints(tt.dests)
			}

			// Act
			got := estimateOne(t, p, model.Snapshot{})

			// Assert
			if math.Abs(got.CrossAZFrac-tt.want) > tolerance {
				t.Errorf("CrossAZFrac = %v, want %v", got.CrossAZFrac, tt.want)
			}
		})
	}
}

func TestEstimateReproducesSpecExample(t *testing.T) {
	// Arrange: 4.2 TB/mo received over 3 pods in 3 even zones at $0.02/GB -> ~$56.
	perPod := 4.2e12 / estimate.MonthSeconds / 3
	s := model.Snapshot{
		Services: []model.Service{service("payments", "api", readyEP("api-1", "a"), readyEP("api-2", "b"), readyEP("api-3", "c"))},
		Volume: &model.VolumeData{Window: "7d", RecvBytesPerS: map[string]float64{
			"payments/api-1": perPod, "payments/api-2": perPod, "payments/api-3": perPod,
		}},
	}

	// Act
	got := estimateOne(t, billablePath("payments/api"), s)

	// Assert
	assertFloatPtr(t, "BytesPerMo", got.BytesPerMo, ptr(4.2e12), 1)
	assertFloatPtr(t, "CostPerMo", got.CostPerMo, ptr(56.0), 0.05)
	if got.Confidence != model.Medium {
		t.Errorf("Confidence = %q, want medium", got.Confidence)
	}
	if got.ConfidenceNote != "" {
		t.Errorf("ConfidenceNote = %q, want empty", got.ConfidenceNote)
	}
}

func TestEstimateVolumeAndConfidence(t *testing.T) {
	const ms = estimate.MonthSeconds
	vol := func(m map[string]float64) *model.VolumeData { return &model.VolumeData{Window: "7d", RecvBytesPerS: m} }
	oneSvc := []model.Service{service("ns", "svc", readyEP("p1", "a"))}
	// cost for bytes/s over 3 even zones (2/3) at $0.02/GB
	costOf := func(bps float64) *float64 { return ptr(bps * ms / 1e9 * 2.0 / 3 * 0.02) }

	tests := []struct {
		name      string
		path      func() model.Path
		snap      model.Snapshot
		wantBytes *float64
		wantCost  *float64
		wantConf  model.Level
		wantNote  string
	}{
		{
			name:      "non-billable shows observed bytes but leaves cost nil and confidence unchanged",
			path:      func() model.Path { p := billablePath("ns/svc"); p.Billable = false; return p },
			snap:      model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{"ns/p1": 100})},
			wantBytes: ptr(100 * ms),
			wantConf:  model.High,
		},
		{
			name:     "non-billable without metrics has no bytes",
			path:     func() model.Path { p := billablePath("ns/svc"); p.Billable = false; return p },
			snap:     model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{})},
			wantConf: model.High,
		},
		{
			name: "billable external target is low with note",
			path: func() model.Path {
				p := billablePath("")
				p.Dest = model.Target{Kind: model.TargetRDS, Ref: "db-1", AZDist: dist("a")}
				return p
			},
			snap:     model.Snapshot{Volume: vol(map[string]float64{})},
			wantConf: model.Low,
			wantNote: "클러스터 외부 대상: 통신 여부와 볼륨을 알 수 없음",
		},
		{
			name:     "billable without volume layer keeps confidence and sets note",
			path:     func() model.Path { return billablePath("ns/svc") },
			snap:     model.Snapshot{Services: oneSvc},
			wantConf: model.High,
			wantNote: "L2 없음: --prometheus-url로 볼륨 추정",
		},
		{
			name: "pod shared by two services is split in half",
			path: func() model.Path { return billablePath("ns/svc") },
			snap: model.Snapshot{
				Services: []model.Service{service("ns", "svc", readyEP("p1", "a")), service("ns", "other", readyEP("p1", "a"))},
				Volume:   vol(map[string]float64{"ns/p1": 200}),
			},
			wantBytes: ptr(100 * ms),
			wantCost:  costOf(100),
			wantConf:  model.Medium,
		},
		{
			name: "hostNetwork pod is unmeasured",
			path: func() model.Path { return billablePath("ns/svc") },
			snap: model.Snapshot{
				Services: []model.Service{service("ns", "svc", readyEP("p1", "a"), readyEP("p2", "b"))},
				Pods:     []model.Pod{{Namespace: "ns", Name: "p1", HostNetwork: true}, {Namespace: "ns", Name: "p2"}},
				Volume:   vol(map[string]float64{"ns/p1": 5000, "ns/p2": 10}),
			},
			wantBytes: ptr(20 * ms),
			wantCost:  costOf(20),
			wantConf:  model.Medium,
			wantNote:  "1/2 pods measured",
		},
		{
			name: "partial measurement is scaled by total over measured",
			path: func() model.Path { return billablePath("ns/svc") },
			snap: model.Snapshot{
				Services: []model.Service{service("ns", "svc", readyEP("p1", "a"), readyEP("p2", "b"), readyEP("p3", "c"))},
				Volume:   vol(map[string]float64{"ns/p1": 10, "ns/p2": 20}),
			},
			wantBytes: ptr(45 * ms),
			wantCost:  costOf(45),
			wantConf:  model.Medium,
			wantNote:  "2/3 pods measured",
		},
		{
			name:     "zero measured pods is low with nil cost",
			path:     func() model.Path { return billablePath("ns/svc") },
			snap:     model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{"ns/unrelated": 10})},
			wantConf: model.Low,
			wantNote: "엔드포인트 pod 메트릭 없음",
		},
		{
			name:     "service missing from snapshot is low with nil cost",
			path:     func() model.Path { return billablePath("ns/gone") },
			snap:     model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{"ns/p1": 10})},
			wantConf: model.Low,
			wantNote: "엔드포인트 pod 메트릭 없음",
		},
		{
			name:      "low rule confidence shows bytes but never an amount",
			path:      func() model.Path { p := billablePath("ns/svc"); p.Confidence = model.Low; return p },
			snap:      model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{"ns/p1": 30})},
			wantBytes: ptr(30 * ms),
			wantConf:  model.Low,
			wantNote:  "판정 신뢰도 low: 금액 생략",
		},
		{
			name: "billable service whose endpoint zones are all unknown is low without amount",
			path: func() model.Path {
				p := billablePath("ns/svc")
				p.Dest.AZDist = model.AZDistribution{}
				return p
			},
			snap:     model.Snapshot{Services: oneSvc, Volume: vol(map[string]float64{"ns/p1": 30})},
			wantConf: model.Low,
			wantNote: "엔드포인트 AZ를 알 수 없어 cross-AZ 비율 계산 불가",
		},
		{
			name: "billable service whose endpoint zones are all unknown is low without volume layer too",
			path: func() model.Path {
				p := billablePath("ns/svc")
				p.Dest.AZDist = model.AZDistribution{}
				return p
			},
			snap:     model.Snapshot{Services: oneSvc},
			wantConf: model.Low,
			wantNote: "엔드포인트 AZ를 알 수 없어 cross-AZ 비율 계산 불가",
		},
		{
			name: "not-ready and pod-less endpoints are ignored, duplicate pod IPs counted once",
			path: func() model.Path { return billablePath("ns/svc") },
			snap: model.Snapshot{
				Services: []model.Service{service("ns", "svc",
					readyEP("p1", "a"),
					readyEP("p1", "a"),
					model.Endpoint{IP: "10.0.0.2", PodName: "p2", Zone: "b", Ready: false},
					model.Endpoint{IP: "10.0.0.3", Zone: "c", Ready: true},
				)},
				Volume: vol(map[string]float64{"ns/p1": 10, "ns/p2": 1000}),
			},
			wantBytes: ptr(10 * ms),
			wantCost:  costOf(10),
			wantConf:  model.Medium,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			p := tt.path()

			// Act
			got := estimateOne(t, p, tt.snap)

			// Assert
			assertFloatPtr(t, "BytesPerMo", got.BytesPerMo, tt.wantBytes, 1e-3)
			assertFloatPtr(t, "CostPerMo", got.CostPerMo, tt.wantCost, 1e-9)
			if got.Confidence != tt.wantConf {
				t.Errorf("Confidence = %q, want %q", got.Confidence, tt.wantConf)
			}
			if got.ConfidenceNote != tt.wantNote {
				t.Errorf("ConfidenceNote = %q, want %q", got.ConfidenceNote, tt.wantNote)
			}
		})
	}
}

func estimateFixture() ([]model.Path, model.Snapshot) {
	shared := billablePath("ns/svc")
	shared.Fix = &model.Fix{Kind: "PreferClose"}
	external := billablePath("")
	external.Dest = model.Target{Kind: model.TargetMSK, Ref: "kafka", AZDist: dist("a", "b")}
	s := model.Snapshot{
		Nodes:    []model.Node{{Name: "n1", Zone: "a"}, {Name: "n2", Zone: "b"}},
		Services: []model.Service{service("ns", "svc", readyEP("p1", "a"), readyEP("p2", "b")), service("ns", "other", readyEP("p1", "a"))},
		Pods:     []model.Pod{{Namespace: "ns", Name: "p2", HostNetwork: true}},
		Volume:   &model.VolumeData{Window: "7d", RecvBytesPerS: map[string]float64{"ns/p1": 10, "ns/p2": 20}},
		Warnings: []string{"w"},
	}
	return []model.Path{shared, external}, s
}

func TestEstimateDoesNotMutateInputs(t *testing.T) {
	// Arrange
	paths, s := estimateFixture()
	wantPaths, wantSnap := estimateFixture()

	// Act
	out := estimate.Estimate(paths, s)

	// Assert
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("Estimate mutated paths:\n got %+v\nwant %+v", paths, wantPaths)
	}
	if !reflect.DeepEqual(s, wantSnap) {
		t.Errorf("Estimate mutated snapshot:\n got %+v\nwant %+v", s, wantSnap)
	}
	if len(out) != len(paths) || &out[0] == &paths[0] {
		t.Errorf("Estimate must return a new slice of the same length")
	}
}
