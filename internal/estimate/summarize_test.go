package estimate_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

// summaryPath is an estimated path for Summarize tests.
func summaryPath(ref string, billable bool, conf model.Level, cost *float64) model.Path {
	p := billablePath(ref)
	p.Billable = billable
	p.Confidence = conf
	p.CostPerMo = cost
	return p
}

func externalPath(kind, ref string, billable bool, conf model.Level) model.Path {
	p := summaryPath("", billable, conf, nil)
	p.Dest = model.Target{Kind: kind, Ref: ref, AZDist: dist("a")}
	return p
}

func keys(paths []model.Path) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, p.Key())
	}
	return out
}

func threeZoneSnapshot() model.Snapshot {
	return model.Snapshot{
		Cluster: "prod-a",
		Region:  "ap-northeast-2",
		Nodes: []model.Node{
			{Name: "n1", Zone: "b"}, {Name: "n2", Zone: "a"}, {Name: "n3", Zone: ""}, {Name: "n4", Zone: "b"}, {Name: "n5", Zone: "c"},
		},
		Volume:   &model.VolumeData{Window: "7d"},
		Warnings: []string{"L1: rds: denied"},
	}
}

func TestSummarizeSplitsSectionsAndSorts(t *testing.T) {
	// Arrange
	s := threeZoneSnapshot()
	paths := []model.Path{
		summaryPath("z/unknown-cost", true, model.Medium, nil),
		summaryPath("b/three", true, model.High, ptr(3)),
		externalPath(model.TargetRDS, "db", true, model.Low),
		summaryPath("c/ten", true, model.Medium, ptr(10)),
		summaryPath("y/excluded", false, model.High, nil),
		summaryPath("a/three", true, model.Medium, ptr(3)),
		summaryPath("x/low-billable", true, model.Low, ptr(100)),
		summaryPath("a/excluded", false, model.Medium, nil),
		summaryPath("h/cheap", true, model.High, ptr(0.5)),
		summaryPath("b/low-free", false, model.Low, nil),
		summaryPath("a/unknown-cost", true, model.High, nil),
	}

	// Act
	r := estimate.Summarize(s, paths, 1)

	// Assert
	if got, want := keys(r.Paths), []string{"c/ten", "a/three", "b/three", "a/unknown-cost", "z/unknown-cost"}; !slices.Equal(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
	if got, want := keys(r.Excluded), []string{"a/excluded", "y/excluded"}; !slices.Equal(got, want) {
		t.Errorf("Excluded = %v, want %v", got, want)
	}
	if got, want := keys(r.Uncertain), []string{"b/low-free", "rds/db", "x/low-billable"}; !slices.Equal(got, want) {
		t.Errorf("Uncertain = %v, want %v", got, want)
	}
	if r.Hidden != 1 {
		t.Errorf("Hidden = %d, want 1", r.Hidden)
	}
	assertFloatPtr(t, "TotalUSD", r.TotalUSD, ptr(16.5), tolerance)
	if r.Exposure != model.ExposureLikely {
		t.Errorf("Exposure = %q, want likely", r.Exposure)
	}
	if r.Threshold != 1 {
		t.Errorf("Threshold = %v, want 1", r.Threshold)
	}
}

func TestSummarizeHeader(t *testing.T) {
	// Arrange
	s := threeZoneSnapshot()

	// Act
	r := estimate.Summarize(s, nil, 1)

	// Assert
	if r.Cluster != "prod-a" || r.Region != "ap-northeast-2" {
		t.Errorf("Cluster/Region = %q/%q, want prod-a/ap-northeast-2", r.Cluster, r.Region)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(r.Zones, want) {
		t.Errorf("Zones = %v, want %v", r.Zones, want)
	}
	if r.NodeCount != 5 {
		t.Errorf("NodeCount = %d, want 5", r.NodeCount)
	}
	if r.Paths == nil || r.Excluded == nil || r.Uncertain == nil {
		t.Errorf("sections must be non-nil empty slices, got %v %v %v", r.Paths, r.Excluded, r.Uncertain)
	}
}

func TestSummarizeLayers(t *testing.T) {
	tests := []struct {
		name string
		snap model.Snapshot
		want []string
	}{
		{"kubernetes only", model.Snapshot{}, []string{"L0"}},
		{"with aws", model.Snapshot{AWS: &model.AWSInventory{}}, []string{"L0", "L1"}},
		{"volume without aws", model.Snapshot{Volume: &model.VolumeData{}}, []string{"L0", "L2"}},
		{"all layers", model.Snapshot{AWS: &model.AWSInventory{}, Volume: &model.VolumeData{}, Cost: &model.CostData{}}, []string{"L0", "L1", "L2", "L3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			r := estimate.Summarize(tt.snap, nil, 1)

			// Assert
			if !slices.Equal(r.Layers, tt.want) {
				t.Errorf("Layers = %v, want %v", r.Layers, tt.want)
			}
		})
	}
}

func TestSummarizeExposure(t *testing.T) {
	oneZone := []model.Node{{Name: "n1", Zone: "a"}, {Name: "n2", Zone: "a"}}
	twoZones := []model.Node{{Name: "n1", Zone: "a"}, {Name: "n2", Zone: "b"}}
	tests := []struct {
		name  string
		nodes []model.Node
		paths []model.Path
		want  string
	}{
		{"shown billable path is likely", oneZone, []model.Path{summaryPath("ns/a", true, model.Medium, ptr(5))}, model.ExposureLikely},
		{"only hidden billable paths is likely", oneZone, []model.Path{summaryPath("ns/a", true, model.Medium, ptr(0.1))}, model.ExposureLikely},
		{"single zone without billable paths is none", oneZone, []model.Path{summaryPath("ns/a", false, model.Low, nil)}, model.ExposureNone},
		{"no nodes and no paths is none", nil, nil, model.ExposureNone},
		{"single zone with billable low external path is unlikely", oneZone, []model.Path{externalPath(model.TargetRDS, "db", true, model.Low)}, model.ExposureUnlikely},
		{"multi zone without billable paths is unlikely", twoZones, []model.Path{summaryPath("ns/a", false, model.High, nil)}, model.ExposureUnlikely},
		{"multi zone with billable service demoted for missing metrics is likely", twoZones, []model.Path{summaryPath("ns/a", true, model.Low, nil)}, model.ExposureLikely},
		{"multi zone with billable service whose rule is low is unlikely", twoZones, []model.Path{lowRulePath("ns/a")}, model.ExposureUnlikely},
		{"multi zone with low external path whose rule is high is unlikely", twoZones, []model.Path{externalPath(model.TargetRDS, "db", true, model.Low)}, model.ExposureUnlikely},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Nodes: tt.nodes, Volume: &model.VolumeData{}}

			// Act
			r := estimate.Summarize(s, tt.paths, 1)

			// Assert
			if r.Exposure != tt.want {
				t.Errorf("Exposure = %q, want %q", r.Exposure, tt.want)
			}
		})
	}
}

func lowRulePath(ref string) model.Path {
	p := summaryPath(ref, true, model.Low, nil)
	p.RuleConfidence = model.Low
	return p
}

// measuredButBlanked is a billable path whose amount was dropped for another reason than volume (e.g. Local Zone rate).
func measuredButBlanked(ref string) model.Path {
	p := summaryPath(ref, true, model.Low, nil)
	p.BytesPerMo = ptr(1e12)
	return p
}

func TestSummarizeWarnsWhenTotalMissesUnmeasuredPaths(t *testing.T) {
	measured := summaryPath("ns/measured", true, model.Medium, ptr(5))
	unmeasured := summaryPath("ns/unmeasured", true, model.Low, nil)
	tests := []struct {
		name         string
		hasVolume    bool
		paths        []model.Path
		wantTotal    *float64
		wantWarnings []string
	}{
		{
			name:         "demoted billable service paths are counted in a warning next to the partial total",
			hasVolume:    true,
			paths:        []model.Path{measured, unmeasured, summaryPath("ns/unmeasured-2", true, model.Low, nil)},
			wantTotal:    ptr(5),
			wantWarnings: []string{"L1: rds: denied", "추정 총액에 볼륨을 측정하지 못한 과금 경로 2개가 빠져 있음"},
		},
		{
			name:         "every path measured adds no warning",
			hasVolume:    true,
			paths:        []model.Path{measured},
			wantTotal:    ptr(5),
			wantWarnings: []string{"L1: rds: denied"},
		},
		{
			name:         "low rule, external and free paths do not count as missing",
			hasVolume:    true,
			paths:        []model.Path{measured, lowRulePath("ns/low-rule"), externalPath(model.TargetMSK, "kafka", true, model.Low), summaryPath("ns/free", false, model.Low, nil)},
			wantTotal:    ptr(5),
			wantWarnings: []string{"L1: rds: denied"},
		},
		{
			name:         "demoted path whose volume was measured is not reported as unmeasured",
			hasVolume:    true,
			paths:        []model.Path{measured, measuredButBlanked("ns/local-zone")},
			wantTotal:    ptr(5),
			wantWarnings: []string{"L1: rds: denied"},
		},
		{
			name:         "without volume the total is unknown and no warning is added",
			paths:        []model.Path{measured, unmeasured},
			wantWarnings: []string{"L1: rds: denied"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := threeZoneSnapshot()
			if !tt.hasVolume {
				s.Volume = nil
			}

			// Act
			r := estimate.Summarize(s, tt.paths, 1)

			// Assert
			assertFloatPtr(t, "TotalUSD", r.TotalUSD, tt.wantTotal, tolerance)
			if !slices.Equal(r.Warnings, tt.wantWarnings) {
				t.Errorf("Warnings = %q, want %q", r.Warnings, tt.wantWarnings)
			}
			if len(s.Warnings) != 1 {
				t.Errorf("snapshot warnings mutated: %q", s.Warnings)
			}
		})
	}
}

func TestSummarizeTotalNilWithoutVolume(t *testing.T) {
	// Arrange
	s := threeZoneSnapshot()
	s.Volume = nil
	paths := []model.Path{summaryPath("ns/a", true, model.Medium, ptr(5))}

	// Act
	r := estimate.Summarize(s, paths, 1)

	// Assert
	if r.TotalUSD != nil {
		t.Errorf("TotalUSD = %v, want nil", *r.TotalUSD)
	}
}

func TestSummarizeCopiesWarnings(t *testing.T) {
	// Arrange
	s := threeZoneSnapshot()

	// Act
	r := estimate.Summarize(s, nil, 1)
	r.Warnings[0] = "changed"

	// Assert
	if s.Warnings[0] != "L1: rds: denied" {
		t.Errorf("result Warnings aliases snapshot Warnings: snapshot now %q", s.Warnings[0])
	}
}

func TestSummarizeVerify(t *testing.T) {
	tests := []struct {
		name      string
		cost      *model.CostData
		wantNil   bool
		wantRatio *float64
	}{
		{"no cost layer leaves verify nil", nil, true, nil},
		{"cost layer attaches verify against total", &model.CostData{Month: "2026-08", ActualUSD: 10}, false, ptr(0.8)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := threeZoneSnapshot()
			s.Cost = tt.cost
			paths := []model.Path{summaryPath("ns/a", true, model.Medium, ptr(8))}

			// Act
			r := estimate.Summarize(s, paths, 1)

			// Assert
			if tt.wantNil {
				if r.Verify != nil {
					t.Errorf("Verify = %+v, want nil", *r.Verify)
				}
				return
			}
			if r.Verify == nil {
				t.Fatal("Verify = nil, want result")
			}
			if r.Verify.Month != "2026-08" {
				t.Errorf("Verify.Month = %q, want 2026-08", r.Verify.Month)
			}
			assertFloatPtr(t, "Verify.EstimatedUSD", r.Verify.EstimatedUSD, r.TotalUSD, tolerance)
			assertFloatPtr(t, "Verify.Ratio", r.Verify.Ratio, tt.wantRatio, tolerance)
		})
	}
}

func summarizeFixture() (model.Snapshot, []model.Path) {
	s := threeZoneSnapshot()
	s.Cost = &model.CostData{Month: "2026-08", ActualUSD: 10}
	p := summaryPath("ns/a", true, model.Medium, ptr(8))
	p.Fix = &model.Fix{Kind: "PreferClose"}
	return s, []model.Path{
		summaryPath("ns/b", true, model.Medium, ptr(20)),
		p,
		summaryPath("ns/c", false, model.High, nil),
		externalPath(model.TargetMSK, "kafka", true, model.Low),
	}
}

func TestSummarizeDoesNotMutateInputs(t *testing.T) {
	// Arrange
	s, paths := summarizeFixture()
	wantSnap, wantPaths := summarizeFixture()

	// Act
	estimate.Summarize(s, paths, 1)

	// Assert
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("Summarize mutated paths:\n got %+v\nwant %+v", paths, wantPaths)
	}
	if !reflect.DeepEqual(s, wantSnap) {
		t.Errorf("Summarize mutated snapshot:\n got %+v\nwant %+v", s, wantSnap)
	}
}
