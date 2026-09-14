package report

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	explainLabelWidth = 12
	unverifiedMark    = "unverified"
)

// commandIndent lines fix commands up two columns right of the explain value column.
var commandIndent = strings.Repeat(" ", 2+explainLabelWidth+1+2)

// Explain writes the rule details of every path whose Key() equals key.
func Explain(w io.Writer, key string, paths []model.Path, rs model.RuleSet) error {
	var b bytes.Buffer
	found := 0
	for _, p := range paths {
		if p.Key() != key {
			continue
		}
		if found > 0 {
			b.WriteString("\n")
		}
		writeExplanation(&b, p, findRule(rs, p.RuleID))
		found++
	}
	if found == 0 {
		return fmt.Errorf("no path for %s; run `crossaz scan` to list keys", key)
	}
	return flushTo(w, &b)
}

func writeExplanation(b *bytes.Buffer, p model.Path, rule model.Rule) {
	line := func(label, value string) {
		fmt.Fprintf(b, "  %-*s %s\n", explainLabelWidth, label+":", clean(value))
	}
	fmt.Fprintf(b, "%s  (%s)\n", clean(p.Key()), clean(p.Dest.Kind))
	line("route", fmt.Sprintf("%s  (evidence: %s)", p.Route, orDash(p.Facts.Evidence)))
	line("callers", fmt.Sprintf("%s/%s  %s", p.Source.Kind, p.Source.Name, zoneList(p.Source.AZDist)))
	line("destination", zoneList(p.Dest.AZDist))
	line("facts", factsLine(p.Facts))
	line("rule", fmt.Sprintf("%s  (billable: %t, rule confidence: %s)", orDash(p.RuleID), p.Billable, orDash(string(p.RuleConfidence))))
	line("reason", orDash(p.Reason))
	line("source", sourceLine(p, rule))
	line("estimate", estimateLine(p))
	line("confidence", confidenceLine(p))
	if p.Fix == nil {
		return
	}
	line("fix", fmt.Sprintf("%s — %s", p.Fix.Kind, p.Fix.Description))
	for _, cmd := range strings.Split(strings.TrimRight(p.Fix.Command, "\n"), "\n") {
		if cmd != "" {
			fmt.Fprintf(b, "%s%s\n", commandIndent, clean(cmd))
		}
	}
}

func findRule(rs model.RuleSet, id string) model.Rule {
	for _, r := range rs.Rules {
		if r.ID == id {
			return r
		}
	}
	return model.Rule{}
}

func factsLine(f model.Facts) string {
	pairs := []struct{ key, value string }{
		{"az_spread", f.AZSpread},
		{"zone_coverage", f.ZoneCoverage},
		{"topology_hints", strconv.FormatBool(f.TopologyHints)},
		{"hints_configured", strconv.FormatBool(f.HintsConfigured)},
		{"zone_local", strconv.FormatBool(f.ZoneLocal)},
		{"direct_ingress_backend", strconv.FormatBool(f.DirectIngressBackend)},
		{"scheme", f.Scheme},
		{"target_type", f.TargetType},
		{"external_traffic_policy", f.ExternalTrafficPolicy},
		{"same_vpc", strconv.FormatBool(f.SameVPC)},
		{"private_ip", strconv.FormatBool(f.PrivateIP)},
		{"traffic", f.Traffic},
	}
	parts := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		if kv.value != "" {
			parts = append(parts, kv.key+"="+kv.value)
		}
	}
	return strings.Join(parts, " ")
}

func sourceLine(p model.Path, rule model.Rule) string {
	url := p.SourceURL
	if url == "" {
		url = rule.Source
	}
	verified := unverifiedMark
	if rule.Verified != "" {
		verified = "verified " + rule.Verified
	}
	return fmt.Sprintf("%s  (%s)", orDash(url), verified)
}

func costKnown(p model.Path) bool {
	return p.CostPerMo != nil && p.BytesPerMo != nil
}

func estimateLine(p model.Path) string {
	if !costKnown(p) {
		return orDash(p.ConfidenceNote)
	}
	return fmt.Sprintf("recv %s/mo × cross-AZ %.3f × $%g/GB = %s",
		Bytes(*p.BytesPerMo), p.CrossAZFrac, p.RateGB, USD(p.CostPerMo, true))
}

// confidenceLine carries the note only when the estimate line did not already show it.
func confidenceLine(p model.Path) string {
	level := orDash(string(p.Confidence))
	if !costKnown(p) || p.ConfidenceNote == "" {
		return level
	}
	return fmt.Sprintf("%s  (%s)", level, p.ConfidenceNote)
}
