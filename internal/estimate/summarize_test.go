package estimate_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

// summaryPath is an estimated path for Summarize tests.
func summaryPath(ref string, billable model.Tri, conf model.Level, cost *float64) model.Path {
	p := billablePath(ref)
	p.Billable = billable
	p.Confidence = conf
	p.CostPerMo = cost
	return p
}

func externalPath(kind, ref string, billable model.Tri, conf model.Level) model.Path {
	p := summaryPath("", billable, conf, nil)
	p.Dest = model.Target{Kind: kind, Ref: ref, AZDist: dist("a")}
	return p
}

func localZone(p model.Path) model.Path {
	out := p
	out.ConfidenceNote = model.NoteLocalZone
	return out
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
		summaryPath("z/unknown-cost", model.TriTrue, model.Medium, nil),
		summaryPath("b/three", model.TriTrue, model.High, ptr(3)),
		externalPath(model.TargetRDS, "db", model.TriTrue, model.Low),
		summaryPath("c/ten", model.TriTrue, model.Medium, ptr(10)),
		summaryPath("y/excluded", model.TriFalse, model.High, nil),
		summaryPath("a/three", model.TriTrue, model.Medium, ptr(3)),
		summaryPath("x/low-billable", model.TriTrue, model.Low, ptr(100)),
		summaryPath("a/excluded", model.TriFalse, model.Medium, nil),
		summaryPath("h/cheap", model.TriTrue, model.High, ptr(0.5)),
		summaryPath("b/low-free", model.TriFalse, model.Low, nil),
		summaryPath("a/unknown-cost", model.TriTrue, model.High, nil),
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
		{"all layers", model.Snapshot{AWS: &model.AWSInventory{}, Volume: &model.VolumeData{}, Bill: &model.Bill{}}, []string{"L0", "L1", "L2", "L3"}},
		{"unknown bill is not a layer", model.Snapshot{Bill: &model.Bill{Unknown: true}}, []string{"L0"}},
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
		{"shown billable path is likely", oneZone, []model.Path{summaryPath("ns/a", model.TriTrue, model.Medium, ptr(5))}, model.ExposureLikely},
		{"only hidden billable paths is likely", oneZone, []model.Path{summaryPath("ns/a", model.TriTrue, model.Medium, ptr(0.1))}, model.ExposureLikely},
		{"single zone without billable paths is none", oneZone, []model.Path{summaryPath("ns/a", model.TriFalse, model.Low, nil)}, model.ExposureNone},
		{"no nodes and no paths is none", nil, nil, model.ExposureNone},
		{"single zone with billable low external path is unlikely", oneZone, []model.Path{externalPath(model.TargetRDS, "db", model.TriTrue, model.Low)}, model.ExposureUnlikely},
		{"multi zone without billable paths is unlikely", twoZones, []model.Path{summaryPath("ns/a", model.TriFalse, model.High, nil)}, model.ExposureUnlikely},
		{"multi zone with billable service demoted for missing metrics is likely", twoZones, []model.Path{summaryPath("ns/a", model.TriTrue, model.Low, nil)}, model.ExposureLikely},
		{"multi zone with billable service whose rule is low is unlikely", twoZones, []model.Path{lowRulePath("ns/a")}, model.ExposureUnlikely},
		{"single zone with only unknown-billable paths is unlikely, not none", oneZone, []model.Path{summaryPath("ns/a", model.TriUnknown, model.Low, nil)}, model.ExposureUnlikely},
		{"multi zone with unknown-billable service is unlikely, not likely", twoZones, []model.Path{summaryPath("ns/a", model.TriUnknown, model.Low, nil)}, model.ExposureUnlikely},
		{"multi zone with low external path whose rule is high is unlikely", twoZones, []model.Path{externalPath(model.TargetRDS, "db", model.TriTrue, model.Low)}, model.ExposureUnlikely},
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
	p := summaryPath(ref, model.TriTrue, model.Low, nil)
	p.RuleConfidence = model.Low
	return p
}

// measuredButBlanked is a billable path whose amount was dropped for another reason than volume (e.g. Local Zone rate).
func measuredButBlanked(ref string) model.Path {
	p := summaryPath(ref, model.TriTrue, model.Low, nil)
	p.BytesPerMo = ptr(1e12)
	return p
}

func TestSummarizeWarnsWhenTotalMissesUnmeasuredPaths(t *testing.T) {
	measured := summaryPath("ns/measured", model.TriTrue, model.Medium, ptr(5))
	unmeasured := summaryPath("ns/unmeasured", model.TriTrue, model.Low, nil)
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
			paths:        []model.Path{measured, unmeasured, summaryPath("ns/unmeasured-2", model.TriTrue, model.Low, nil)},
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
			paths:        []model.Path{measured, lowRulePath("ns/low-rule"), externalPath(model.TargetMSK, "kafka", model.TriTrue, model.Low), summaryPath("ns/free", model.TriFalse, model.Low, nil)},
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

// unknownPath is an unknown-billable path; bytes nil = volume not measured.
func unknownPath(ref string, conf model.Level, bytes *float64) model.Path {
	p := summaryPath(ref, model.TriUnknown, conf, nil)
	p.BytesPerMo = bytes
	return p
}

func TestSummarizeUnconfirmed(t *testing.T) {
	billed := summaryPath("ns/billed", model.TriTrue, model.Medium, ptr(5))
	tests := []struct {
		name          string
		hasVolume     bool
		paths         []model.Path
		wantUncertain []string
		wantMax       *float64
	}{
		{
			name:          "unknown paths are uncertain whatever their confidence, and bound the max at full volume",
			hasVolume:     true,
			paths:         []model.Path{billed, unknownPath("ns/u1", model.Low, ptr(100e9)), unknownPath("ns/u2", model.Medium, ptr(50e9))},
			wantUncertain: []string{"ns/u1", "ns/u2"},
			wantMax:       ptr(150 * 0.02),
		},
		{
			name:          "one unmeasured unknown path makes the max unknown",
			hasVolume:     true,
			paths:         []model.Path{billed, unknownPath("ns/u1", model.Low, ptr(100e9)), unknownPath("ns/u2", model.Low, nil)},
			wantUncertain: []string{"ns/u1", "ns/u2"},
		},
		{
			name:          "an unknown path at a Local Zone rate makes the max unknown",
			hasVolume:     true,
			paths:         []model.Path{billed, unknownPath("ns/u1", model.Low, ptr(100e9)), localZone(unknownPath("ns/u2", model.Low, ptr(50e9)))},
			wantUncertain: []string{"ns/u1", "ns/u2"},
		},
		{
			name:          "without volume the max is unknown",
			paths:         []model.Path{billed, unknownPath("ns/u1", model.Low, nil)},
			wantUncertain: []string{"ns/u1"},
		},
		{
			name:          "no unknown paths, no max",
			hasVolume:     true,
			paths:         []model.Path{billed, summaryPath("ns/free", model.TriFalse, model.Low, nil)},
			wantUncertain: []string{"ns/free"},
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
			if got := keys(r.Uncertain); !slices.Equal(got, tt.wantUncertain) {
				t.Errorf("Uncertain = %v, want %v", got, tt.wantUncertain)
			}
			if len(r.Excluded) != 0 {
				t.Errorf("Excluded = %v, want none", keys(r.Excluded))
			}
			assertFloatPtr(t, "UnconfirmedMaxUSD", r.UnconfirmedMaxUSD, tt.wantMax, tolerance)
			if tt.hasVolume {
				assertFloatPtr(t, "TotalUSD", r.TotalUSD, ptr(5), tolerance)
			}
		})
	}
}

func TestSummarizeTotalNilWithoutVolume(t *testing.T) {
	// Arrange
	s := threeZoneSnapshot()
	s.Volume = nil
	paths := []model.Path{summaryPath("ns/a", model.TriTrue, model.Medium, ptr(5))}

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
		bill      *model.Bill
		wantNil   bool
		wantRatio *float64
	}{
		{"no bill leaves verify nil", nil, true, nil},
		{"bill attaches verify against total", summaryBill(), false, ptr(0.8)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := threeZoneSnapshot()
			s.Bill = tt.bill
			paths := []model.Path{summaryPath("ns/a", model.TriTrue, model.Medium, ptr(8))}

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
			if r.Verify.Window != "2026-08-01..2026-08-30" {
				t.Errorf("Verify.Window = %q, want 2026-08-01..2026-08-30", r.Verify.Window)
			}
			assertFloatPtr(t, "Verify.EstimatedUSD", r.Verify.EstimatedUSD, r.TotalUSD, tolerance)
			assertFloatPtr(t, "Verify.Ratio", r.Verify.Ratio, tt.wantRatio, tolerance)
		})
	}
}

// summaryBill is 30 closed days billing $10 of InterZone in all, i.e. $10/mo.
func summaryBill() *model.Bill {
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	return &model.Bill{From: from, To: from.AddDate(0, 0, 30), Lines: []model.BillLine{
		{Day: from, UsageType: "APN2-DataTransfer-Regional-Bytes", Operation: "InterZone-In", Family: model.FamilyInterZone, USD: 10},
	}}
}

func summarizeFixture() (model.Snapshot, []model.Path) {
	s := threeZoneSnapshot()
	s.Bill = summaryBill()
	p := summaryPath("ns/a", model.TriTrue, model.Medium, ptr(8))
	p.Fix = &model.Fix{Kind: "PreferClose"}
	return s, []model.Path{
		summaryPath("ns/b", model.TriTrue, model.Medium, ptr(20)),
		p,
		summaryPath("ns/c", model.TriFalse, model.High, nil),
		externalPath(model.TargetMSK, "kafka", model.TriTrue, model.Low),
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
