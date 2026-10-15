package classify

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	trafficDistributionClose    = "PreferClose"
	trafficDistributionSameZone = "PreferSameZone"
	// preferSameZoneMinMinor is the first control plane minor that accepts PreferSameZone.
	preferSameZoneMinMinor = 34
)

// Apply judges each path with the first matching rule and returns new paths.
func Apply(paths []model.Path, rs model.RuleSet, s model.Snapshot, rateOverride float64) ([]model.Path, error) {
	td := trafficDistribution(s.ServerVersion)
	out := make([]model.Path, 0, len(paths))
	for _, p := range paths {
		rule, ok := firstMatch(rs.Rules, p.Facts)
		if !ok {
			return nil, fmt.Errorf("no rule matches path %s (traffic %q): rule set lacks a catch-all", p.Key(), p.Facts.Traffic)
		}
		judged, err := applyRule(p, rule, ruleRate(rule, rs, s.Region, rateOverride), td)
		if err != nil {
			return nil, err
		}
		out = append(out, judged)
	}
	return out, nil
}

func firstMatch(rules []model.Rule, f model.Facts) (model.Rule, bool) {
	for _, r := range rules {
		if Matches(r.Match, f) {
			return r, true
		}
	}
	return model.Rule{}, false
}

func applyRule(p model.Path, r model.Rule, rate float64, td string) (model.Path, error) {
	out := p
	out.Billable = r.Billable
	out.RuleID = r.ID
	out.Reason = r.Reason
	out.SourceURL = r.Source
	out.RuleConfidence = r.Confidence
	out.Confidence = model.MinLevel(r.Confidence, evidenceLevel(p.Facts.Evidence))
	// An unknown verdict keeps the rate it would have if billable: the summary's "Unconfirmed" upper bound uses it.
	out.RateGB = 0
	if r.Billable != model.TriFalse {
		out.RateGB = rate
	}
	out.Fix = nil
	if r.Fix == nil {
		return out, nil
	}
	cmd, err := renderCommand(r.ID, r.Fix.Command, fixVars(p, td))
	if err != nil {
		return model.Path{}, fmt.Errorf("render fix of rule %q for %s: %w", r.ID, p.Key(), err)
	}
	out.Fix = &model.Fix{Kind: r.Fix.Kind, Description: r.Fix.Description, Command: cmd}
	return out, nil
}

// ruleRate applies the precedence override > rule.rate_gb > region rate > default rate.
func ruleRate(r model.Rule, rs model.RuleSet, region string, override float64) float64 {
	if override > 0 {
		return override
	}
	if r.RateGB > 0 {
		return r.RateGB
	}
	rate, _ := RateFor(rs, region, 0)
	return rate
}

func evidenceLevel(evidence string) model.Level {
	switch evidence {
	case model.EvidenceK8s, model.EvidenceAWS:
		return model.High
	case model.EvidenceAnnotation:
		return model.Medium
	default:
		return model.Low
	}
}

func trafficDistribution(serverVersion string) string {
	if minor, ok := minorVersion(serverVersion); ok && minor >= preferSameZoneMinMinor {
		return trafficDistributionSameZone
	}
	return trafficDistributionClose
}

func fixVars(p model.Path, td string) model.FixVars {
	vars := model.FixVars{Name: p.Dest.Ref, Ref: p.Dest.Ref, Kind: p.Dest.Kind, TrafficDistribution: td}
	if p.Dest.Kind != model.TargetService {
		return vars
	}
	if ns, name, ok := strings.Cut(p.Dest.Ref, "/"); ok {
		vars.Namespace, vars.Name = ns, name
	}
	return vars
}

// renderCommand executes a fix command template; trailing newlines (from YAML block scalars) are dropped.
func renderCommand(name, command string, vars model.FixVars) (string, error) {
	if command == "" {
		return "", nil
	}
	tmpl, err := template.New(name).Parse(command)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, vars); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
