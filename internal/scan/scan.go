// Package scan ties the pure pipeline together: classify, estimate, summarize.
package scan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leegun2000/crossaz/internal/classify"
	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

// Options tune a scan.
type Options struct {
	Threshold float64 // billable paths with a known monthly cost below this (USD) are hidden
	RateGB    float64 // > 0 overrides every billable rate
}

// Analyze judges and estimates every path of s. It returns the summary and all estimated paths (for explain).
func Analyze(s model.Snapshot, rs model.RuleSet, o Options) (model.ScanResult, []model.Path, error) {
	judged, err := classify.Apply(classify.BuildPaths(s), rs, s, o.RateGB)
	if err != nil {
		return model.ScanResult{}, nil, fmt.Errorf("classify paths: %w", err)
	}
	zones := localZones(s.Region, s.Nodes)
	paths := withoutLocalZoneAmounts(estimate.Estimate(judged, s), zones)
	r := estimate.Summarize(s, paths, o.Threshold)
	if extra := analysisWarnings(s, rs, o, zones); len(extra) > 0 {
		r.Warnings = append(slices.Clone(r.Warnings), extra...)
	}
	return r, paths, nil
}

func analysisWarnings(s model.Snapshot, rs model.RuleSet, o Options, zones []string) []string {
	var out []string
	if w := rateWarning(s.Region, rs, o.RateGB); w != "" {
		out = append(out, w)
	}
	if len(zones) > 0 {
		out = append(out, fmt.Sprintf("Local/Wavelength Zone nodes found (%s): their rate differs, so amounts of paths touching them are omitted", strings.Join(zones, ", ")))
	}
	return out
}

// withoutLocalZoneAmounts blanks the amount of billable or unknown paths that touch a Local/Wavelength Zone: its
// per-GB rate is not the parent region's (billing-rules.md §1.2, §4 local-zone-rate), so an unknown path's upper
// bound would be too low. Paths already low keep their own note.
func withoutLocalZoneAmounts(paths []model.Path, zones []string) []model.Path {
	if len(zones) == 0 {
		return paths
	}
	out := make([]model.Path, len(paths))
	for i, p := range paths {
		if p.Billable != model.TriFalse && p.Confidence != model.Low && (touches(p.Source.AZDist, zones) || touches(p.Dest.AZDist, zones)) {
			p.CostPerMo = nil
			p.Confidence = model.Low
			p.ConfidenceNote = model.NoteLocalZone
		}
		out[i] = p
	}
	return out
}

func touches(d model.AZDistribution, zones []string) bool {
	return slices.ContainsFunc(zones, func(z string) bool { return d[z] > 0 })
}

func rateWarning(region string, rs model.RuleSet, override float64) string {
	if _, builtin := classify.RateFor(rs, region, override); builtin || override > 0 {
		return ""
	}
	if region == "" {
		return fmt.Sprintf("region unknown, using the default $%g/GB; set --region or --rate-gb", rs.DefaultRateGB)
	}
	return fmt.Sprintf("no built-in rate for region %s, using the default $%g/GB; set --rate-gb", region, rs.DefaultRateGB)
}

// localZones returns the sorted distinct node zones that are not a regular AZ "<region><letter>".
func localZones(region string, nodes []model.Node) []string {
	if region == "" {
		return nil
	}
	var out []string
	for _, n := range nodes {
		if n.Zone != "" && !isRegularAZ(region, n.Zone) {
			out = append(out, n.Zone)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func isRegularAZ(region, zone string) bool {
	suffix, ok := strings.CutPrefix(zone, region)
	return ok && len(suffix) == 1 && suffix[0] >= 'a' && suffix[0] <= 'z'
}
