package estimate

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const warnTotalMissesPaths = "the estimated total leaves out %d billable path(s) whose volume could not be measured"

// sections is the split of paths into ScanResult sections.
type sections struct {
	shown, excluded, uncertain []model.Path
	hidden                     int
	knownUSD                   float64 // known cost of billable, non-low paths (hidden included)
}

// Summarize builds the scan result from estimated paths. Inputs are not modified.
func Summarize(s model.Snapshot, paths []model.Path, threshold float64) model.ScanResult {
	sec := split(paths, threshold)
	r := model.ScanResult{
		Cluster:   s.Cluster,
		Region:    s.Region,
		Zones:     nodeZones(s.Nodes),
		NodeCount: len(s.Nodes),
		Layers:    layers(s),
		Paths:     sec.shown,
		Excluded:  sec.excluded,
		Uncertain: sec.uncertain,
		Hidden:    sec.hidden,
		Threshold: threshold,
		Warnings:  slices.Clone(s.Warnings),
	}
	if s.Volume != nil {
		total := sec.knownUSD
		r.TotalUSD = &total
		r.UnconfirmedMaxUSD = unconfirmedMax(sec.uncertain)
		if n := countUnmeasured(sec.uncertain); n > 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf(warnTotalMissesPaths, n))
		}
	}
	r.Exposure = exposure(r)
	if s.Bill != nil {
		v := Verify(r.TotalUSD, *s.Bill)
		r.Verify = &v
	}
	return r
}

func split(paths []model.Path, threshold float64) sections {
	sec := sections{shown: []model.Path{}, excluded: []model.Path{}, uncertain: []model.Path{}}
	for _, p := range paths {
		switch {
		case p.Confidence == model.Low || p.Billable == model.TriUnknown:
			sec.uncertain = append(sec.uncertain, p)
		case p.Billable == model.TriFalse:
			sec.excluded = append(sec.excluded, p)
		case p.CostPerMo == nil:
			sec.shown = append(sec.shown, p)
		default:
			sec.knownUSD += *p.CostPerMo
			if *p.CostPerMo < threshold {
				sec.hidden++
				continue
			}
			sec.shown = append(sec.shown, p)
		}
	}
	slices.SortStableFunc(sec.shown, byCostDescThenKey)
	slices.SortStableFunc(sec.excluded, byKey)
	slices.SortStableFunc(sec.uncertain, byKey)
	return sec
}

// unconfirmedMax is what the unknown-billable paths would cost if billable, at full volume (cross-AZ
// fraction 1: their zone facts are what is unknown). nil when there are none, or one has no measured volume or
// no usable rate (Local/Wavelength Zone).
func unconfirmedMax(uncertain []model.Path) *float64 {
	var sum float64
	n := 0
	for _, p := range uncertain {
		if p.Billable != model.TriUnknown {
			continue
		}
		if p.BytesPerMo == nil || p.ConfidenceNote == model.NoteLocalZone {
			return nil
		}
		sum += *p.BytesPerMo / bytesPerGB * p.RateGB
		n++
	}
	if n == 0 {
		return nil
	}
	return &sum
}

func byKey(a, b model.Path) int {
	return strings.Compare(a.Key(), b.Key())
}

// byCostDescThenKey orders known costs high to low, unknown costs last, ties by Key.
func byCostDescThenKey(a, b model.Path) int {
	switch {
	case a.CostPerMo == nil && b.CostPerMo != nil:
		return 1
	case a.CostPerMo != nil && b.CostPerMo == nil:
		return -1
	case a.CostPerMo != nil && b.CostPerMo != nil:
		if c := cmp.Compare(*b.CostPerMo, *a.CostPerMo); c != 0 {
			return c
		}
	}
	return byKey(a, b)
}

func nodeZones(nodes []model.Node) []string {
	zones := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Zone != "" {
			zones = append(zones, n.Zone)
		}
	}
	slices.Sort(zones)
	return slices.Compact(zones)
}

func layers(s model.Snapshot) []string {
	out := []string{"L0"}
	if s.AWS != nil {
		out = append(out, "L1")
	}
	if s.Volume != nil {
		out = append(out, "L2")
	}
	if s.Bill != nil && !s.Bill.Unknown {
		out = append(out, "L3")
	}
	return out
}

// demotedBillable reports a billable service path whose rule is confident but whose estimate went low
// (no endpoint metrics, no endpoint zones, ...): the verdict still stands, only the amount is missing.
func demotedBillable(p model.Path) bool {
	return p.Billable == model.TriTrue && p.Confidence == model.Low && p.RuleConfidence != model.Low && p.Dest.Kind == model.TargetService
}

func countUnmeasured(uncertain []model.Path) int {
	n := 0
	for _, p := range uncertain {
		if demotedBillable(p) && p.BytesPerMo == nil {
			n++
		}
	}
	return n
}

func exposure(r model.ScanResult) string {
	if len(r.Paths) > 0 || r.Hidden > 0 || slices.ContainsFunc(r.Uncertain, demotedBillable) {
		return model.ExposureLikely
	}
	// An unknown path may be cross-AZ even when all nodes share one AZ (e.g. EKS control-plane ENIs elsewhere).
	hasBillableUncertain := slices.ContainsFunc(r.Uncertain, func(p model.Path) bool { return p.Billable != model.TriFalse })
	if len(r.Zones) <= 1 && !hasBillableUncertain {
		return model.ExposureNone
	}
	return model.ExposureUnlikely
}
