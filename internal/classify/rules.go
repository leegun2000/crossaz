// Package classify turns a snapshot into paths (BuildPaths) and judges them against billing rules (Apply).
// Everything here is pure: no I/O.
package classify

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/leegun2000/crossaz/internal/model"
	"github.com/leegun2000/crossaz/rules"
)

var validLevels = []model.Level{model.High, model.Medium, model.Low}

// matchValues lists, by match key (json tag), every value BuildPaths can put in that fact.
// A value outside the list can never match, so Validate rejects it.
var matchValues = map[string][]string{
	"route": {string(model.RouteClusterIP), string(model.RouteHeadless), string(model.RouteALB), string(model.RouteCLB),
		string(model.RouteNLBXZ), string(model.RouteNLBZonal), string(model.RouteNLB), string(model.RouteExternal)},
	"target_kind":             {model.TargetService, model.TargetRDS, model.TargetElastiCache, model.TargetMSK},
	"scheme":                  {schemeInternal, schemeInternetFacing},
	"target_type":             {targetTypeIP, targetTypeInstance},
	"external_traffic_policy": {policyCluster, policyLocal},
	"traffic":                 {trafficClient, trafficReplication},
	"az_spread":               {spreadSingle, spreadMulti, factUnknown},
	"zone_coverage":           {coverageFull, coveragePartial, factUnknown},
	"evidence":                {model.EvidenceK8s, model.EvidenceAWS, model.EvidenceAnnotation},
}

// Load parses a rules document strictly (unknown or duplicate keys are errors) and validates it.
func Load(data []byte) (model.RuleSet, error) {
	var rs model.RuleSet
	if err := yaml.UnmarshalStrict(data, &rs); err != nil {
		return model.RuleSet{}, fmt.Errorf("parse rules: %w", err)
	}
	if err := Validate(rs); err != nil {
		return model.RuleSet{}, fmt.Errorf("invalid rules: %w", err)
	}
	return rs, nil
}

// Default returns the embedded rules. It panics if they are invalid; the tests guarantee they are not.
func Default() model.RuleSet {
	rs, err := Load(rules.YAML)
	if err != nil {
		panic(fmt.Sprintf("embedded rules.yaml: %v", err))
	}
	return rs
}

// Validate reports every structural problem of a rule set at once.
func Validate(rs model.RuleSet) error {
	var errs []error
	if rs.DefaultRateGB <= 0 {
		errs = append(errs, fmt.Errorf("default_rate_gb must be > 0, got %v", rs.DefaultRateGB))
	}
	for _, region := range slices.Sorted(maps.Keys(rs.Regions)) {
		if rate := rs.Regions[region].RateGB; rate < 0 {
			errs = append(errs, fmt.Errorf("regions.%s.rate_gb must be >= 0, got %v", region, rate))
		}
	}
	if len(rs.Rules) == 0 {
		errs = append(errs, errors.New("at least one rule is required"))
	} else if last := rs.Rules[len(rs.Rules)-1]; !isCatchAll(last.Match) {
		errs = append(errs, fmt.Errorf("last rule %q must have an empty match (catch-all)", last.ID))
	}
	seen := make(map[string]bool, len(rs.Rules))
	for i, r := range rs.Rules {
		if r.ID != "" && seen[r.ID] {
			errs = append(errs, fmt.Errorf("rule[%d]: duplicate id %q", i, r.ID))
		}
		seen[r.ID] = true
		errs = append(errs, validateRule(i, r)...)
	}
	return errors.Join(errs...)
}

func validateRule(i int, r model.Rule) []error {
	var errs []error
	name := fmt.Sprintf("rule[%d] %q", i, r.ID)
	if r.ID == "" {
		errs = append(errs, fmt.Errorf("%s: id is required", name))
	}
	if !slices.Contains(validLevels, r.Confidence) {
		errs = append(errs, fmt.Errorf("%s: confidence must be high|medium|low, got %q", name, r.Confidence))
	}
	if r.RateGB < 0 {
		errs = append(errs, fmt.Errorf("%s: rate_gb must be >= 0, got %v", name, r.RateGB))
	}
	errs = append(errs, validateMatchValues(name, r.Match)...)
	if r.Fix != nil {
		// Executing against zero vars also catches references to fields FixVars does not have.
		if _, err := renderCommand(r.ID, r.Fix.Command, model.FixVars{}); err != nil {
			errs = append(errs, fmt.Errorf("%s: fix.command: %w", name, err))
		}
	}
	return errs
}

// validateMatchValues reports every listed value that no fact can have.
func validateMatchValues(name string, m model.Match) []error {
	var errs []error
	v, t := reflect.ValueOf(m), reflect.TypeOf(m)
	for i := range t.NumField() {
		set, ok := v.Field(i).Interface().(model.StringSet)
		if !ok {
			continue
		}
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		allowed := matchValues[key]
		for _, value := range set {
			if !slices.Contains(allowed, value) {
				errs = append(errs, fmt.Errorf("%s: match.%s: unknown value %q (allowed: %s)", name, key, value, strings.Join(allowed, "|")))
			}
		}
	}
	return errs
}

// isCatchAll reports whether every condition of m is unset (empty list or nil pointer).
func isCatchAll(m model.Match) bool {
	v := reflect.ValueOf(m)
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Slice && f.Len() > 0 || f.Kind() != reflect.Slice && !f.IsZero() {
			return false
		}
	}
	return true
}

// RateFor returns the per-GB rate: override when > 0, else the region's built-in rate, else default_rate_gb.
// builtin reports whether the region has a built-in rate, independent of override.
func RateFor(rs model.RuleSet, region string, override float64) (rate float64, builtin bool) {
	r, ok := rs.Regions[region]
	builtin = ok && r.RateGB > 0
	switch {
	case override > 0:
		return override, builtin
	case builtin:
		return r.RateGB, true
	default:
		return rs.DefaultRateGB, false
	}
}

// Matches reports whether every non-empty condition of m holds for f. An empty fact never matches a
// required value.
func Matches(m model.Match, f model.Facts) bool {
	return matchSet(m.Route, string(f.Route)) &&
		matchSet(m.TargetKind, f.TargetKind) &&
		matchSet(m.Scheme, f.Scheme) &&
		matchSet(m.TargetType, f.TargetType) &&
		matchSet(m.ExternalTrafficPolicy, f.ExternalTrafficPolicy) &&
		matchSet(m.Traffic, f.Traffic) &&
		matchSet(m.AZSpread, f.AZSpread) &&
		matchSet(m.ZoneCoverage, f.ZoneCoverage) &&
		matchSet(m.Evidence, f.Evidence) &&
		matchBool(m.SameVPC, f.SameVPC) &&
		matchBool(m.PrivateIP, f.PrivateIP) &&
		matchBool(m.TopologyHints, f.TopologyHints) &&
		matchBool(m.ZoneLocal, f.ZoneLocal) &&
		matchBool(m.HintsConfigured, f.HintsConfigured) &&
		matchBool(m.DirectIngressBackend, f.DirectIngressBackend)
}

func matchSet(want model.StringSet, fact string) bool {
	return len(want) == 0 || fact != "" && want.Has(fact)
}

func matchBool(want *bool, fact bool) bool {
	return want == nil || *want == fact
}
