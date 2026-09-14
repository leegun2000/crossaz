package classify

import (
	"slices"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func TestDefaultLoadsAndValidates(t *testing.T) {
	// Act
	rs := Default()

	// Assert
	if err := Validate(rs); err != nil {
		t.Fatalf("Validate(Default()) = %v", err)
	}
	if got := rs.Rules[len(rs.Rules)-1].ID; got != "unclassified" {
		t.Errorf("last rule = %q, want unclassified", got)
	}
	if rs.DefaultRateGB != 0.02 {
		t.Errorf("default_rate_gb = %v, want 0.02", rs.DefaultRateGB)
	}
}

func validRuleSet() model.RuleSet {
	return model.RuleSet{
		DefaultRateGB: 0.02,
		Regions:       map[string]model.RegionRate{"eu-west-1": {RateGB: 0.02}},
		Rules: []model.Rule{
			{ID: "a", Match: model.Match{Route: model.StringSet{"clusterip"}}, Billable: true, Confidence: model.High,
				Fix: &model.FixTemplate{Kind: "PreferClose", Command: "kubectl -n {{.Namespace}} {{.Name}}"}},
			{ID: "catch-all", Confidence: model.Low},
		},
	}
}

func TestValidateRejectsBrokenRuleSets(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(rs model.RuleSet) model.RuleSet
		wantErr string
	}{
		{"duplicate id", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{rs.Rules[0], rs.Rules[0], rs.Rules[1]}
			return rs
		}, `duplicate id "a"`},
		{"empty id", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{Confidence: model.Low}, rs.Rules[1]}
			return rs
		}, "id is required"},
		{"missing catch-all", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = rs.Rules[:1]
			return rs
		}, "catch-all"},
		{"catch-all with false bool condition", func(rs model.RuleSet) model.RuleSet {
			no := false
			rs.Rules = []model.Rule{rs.Rules[0], {ID: "last", Confidence: model.Low, Match: model.Match{SameVPC: &no}}}
			return rs
		}, "catch-all"},
		{"no rules", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = nil
			return rs
		}, "at least one rule"},
		{"bad confidence", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: "certain"}, rs.Rules[1]}
			return rs
		}, "confidence must be"},
		{"negative rule rate", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, RateGB: -0.01}, rs.Rules[1]}
			return rs
		}, "rate_gb must be >= 0"},
		{"negative region rate", func(rs model.RuleSet) model.RuleSet {
			rs.Regions = map[string]model.RegionRate{"eu-west-1": {RateGB: -1}}
			return rs
		}, "regions.eu-west-1.rate_gb"},
		{"zero default rate", func(rs model.RuleSet) model.RuleSet {
			rs.DefaultRateGB = 0
			return rs
		}, "default_rate_gb must be > 0"},
		{"unparsable template", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Fix: &model.FixTemplate{Command: "{{.Name"}}, rs.Rules[1]}
			return rs
		}, "fix.command"},
		{"template references unknown field", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Fix: &model.FixTemplate{Command: "{{.Cluster}}"}}, rs.Rules[1]}
			return rs
		}, "fix.command"},
		{"route label printed by the table is not a route", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Match: model.Match{Route: model.StringSet{"alb", "nlb-xz"}}}, rs.Rules[1]}
			return rs
		}, `match.route: unknown value "nlb-xz"`},
		{"unknown evidence value", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Match: model.Match{Evidence: model.StringSet{"L1"}}}, rs.Rules[1]}
			return rs
		}, `match.evidence: unknown value "L1"`},
		{"case matters for traffic policy", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Match: model.Match{ExternalTrafficPolicy: model.StringSet{"cluster"}}}, rs.Rules[1]}
			return rs
		}, `match.external_traffic_policy: unknown value "cluster"`},
		{"empty value in a set", func(rs model.RuleSet) model.RuleSet {
			rs.Rules = []model.Rule{{ID: "a", Confidence: model.High, Match: model.Match{AZSpread: model.StringSet{""}}}, rs.Rules[1]}
			return rs
		}, `match.az_spread: unknown value ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			rs := tt.mutate(validRuleSet())

			// Act
			err := Validate(rs)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAcceptsValidRuleSet(t *testing.T) {
	if err := Validate(validRuleSet()); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateAcceptsEveryFactValue(t *testing.T) {
	// Arrange
	rs := validRuleSet()
	rs.Rules[0].Match = model.Match{
		Route:                 model.StringSet{"clusterip", "headless", "alb", "clb", "nlb-cross-zone", "nlb-zonal", "nlb", "external"},
		TargetKind:            model.StringSet{"service", "rds", "elasticache", "msk"},
		Scheme:                model.StringSet{"internal", "internet-facing"},
		TargetType:            model.StringSet{"ip", "instance"},
		ExternalTrafficPolicy: model.StringSet{"Cluster", "Local"},
		Traffic:               model.StringSet{"client", "replication"},
		AZSpread:              model.StringSet{"single", "multi", "unknown"},
		ZoneCoverage:          model.StringSet{"full", "partial", "unknown"},
		Evidence:              model.StringSet{"k8s", "aws", "annotation"},
	}

	// Act
	err := Validate(rs)

	// Assert
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestLoad(t *testing.T) {
	const base = "version: 1\ndefault_rate_gb: 0.02\nrules:\n"
	tests := []struct {
		name      string
		yaml      string
		wantErr   string
		wantRoute model.StringSet
	}{
		{"scalar string set", base + "- {id: a, match: {route: alb}, confidence: high}\n- {id: z, match: {}, confidence: low}\n", "", model.StringSet{"alb"}},
		{"list string set", base + "- {id: a, match: {route: [alb, clb]}, confidence: high}\n- {id: z, match: {}, confidence: low}\n", "", model.StringSet{"alb", "clb"}},
		{"hint and ingress bool keys", base + "- {id: a, match: {route: clusterip, hints_configured: true, direct_ingress_backend: false}, confidence: low}\n- {id: z, match: {}, confidence: low}\n", "", model.StringSet{"clusterip"}},
		{"unknown match value", base + "- {id: a, match: {route: nlb-xz}, confidence: high}\n- {id: z, match: {}, confidence: low}\n", "invalid rules", nil},
		{"unknown rule key", base + "- {id: a, match: {}, confidence: low, severity: 3}\n", "parse rules", nil},
		{"unknown match key", base + "- {id: a, match: {color: red}, confidence: low}\n", "parse rules", nil},
		{"string set of wrong type", base + "- {id: a, match: {route: {x: 1}}, confidence: low}\n", "parse rules", nil},
		{"malformed yaml", "rules: [", "parse rules", nil},
		{"valid yaml, invalid rules", base + "- {id: a, match: {route: alb}, confidence: high}\n", "invalid rules", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			rs, err := Load([]byte(tt.yaml))

			// Assert
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if !slices.Equal(rs.Rules[0].Match.Route, tt.wantRoute) {
				t.Errorf("route = %v, want %v", rs.Rules[0].Match.Route, tt.wantRoute)
			}
		})
	}
}

func TestMatches(t *testing.T) {
	yes, no := true, false
	facts := model.Facts{Route: model.RouteALB, TargetType: "ip", SameVPC: true, AZSpread: "multi"}
	tests := []struct {
		name  string
		match model.Match
		facts model.Facts
		want  bool
	}{
		{"empty match matches everything", model.Match{}, facts, true},
		{"empty match matches zero facts", model.Match{}, model.Facts{}, true},
		{"route in set", model.Match{Route: model.StringSet{"clb", "alb"}}, facts, true},
		{"route not in set", model.Match{Route: model.StringSet{"clb"}}, facts, false},
		{"empty fact never matches required value", model.Match{Scheme: model.StringSet{"internal"}}, facts, false},
		{"empty fact never matches empty listed value", model.Match{Scheme: model.StringSet{""}}, facts, false},
		{"bool true matches", model.Match{SameVPC: &yes}, facts, true},
		{"bool false does not match true fact", model.Match{SameVPC: &no}, facts, false},
		{"bool false matches false fact", model.Match{PrivateIP: &no}, facts, true},
		{"AND: all conditions hold", model.Match{Route: model.StringSet{"alb"}, TargetType: model.StringSet{"ip"}, SameVPC: &yes}, facts, true},
		{"AND: one condition fails", model.Match{Route: model.StringSet{"alb"}, TargetType: model.StringSet{"instance"}, SameVPC: &yes}, facts, false},
		{"every string field is checked", model.Match{TargetKind: model.StringSet{"rds"}}, facts, false},
		{"etp checked", model.Match{ExternalTrafficPolicy: model.StringSet{"Local"}}, model.Facts{ExternalTrafficPolicy: "Local"}, true},
		{"traffic checked", model.Match{Traffic: model.StringSet{"client"}}, model.Facts{Traffic: "replication"}, false},
		{"coverage checked", model.Match{ZoneCoverage: model.StringSet{"full"}}, model.Facts{ZoneCoverage: "full"}, true},
		{"evidence checked", model.Match{Evidence: model.StringSet{"aws"}}, model.Facts{Evidence: "k8s"}, false},
		{"hints checked", model.Match{TopologyHints: &yes}, model.Facts{}, false},
		{"zone_local checked", model.Match{ZoneLocal: &yes}, model.Facts{ZoneLocal: true}, true},
		{"hints_configured checked", model.Match{HintsConfigured: &yes}, model.Facts{}, false},
		{"hints_configured matches", model.Match{HintsConfigured: &yes}, model.Facts{HintsConfigured: true}, true},
		{"direct_ingress_backend checked", model.Match{DirectIngressBackend: &no}, model.Facts{DirectIngressBackend: true}, false},
		{"direct_ingress_backend matches", model.Match{DirectIngressBackend: &yes}, model.Facts{DirectIngressBackend: true}, true},
		{"az_spread checked", model.Match{AZSpread: model.StringSet{"single"}}, facts, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Matches(tt.match, tt.facts); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRateFor(t *testing.T) {
	rs := model.RuleSet{DefaultRateGB: 0.05, Regions: map[string]model.RegionRate{
		"eu-west-1": {RateGB: 0.02},
		"zero-1":    {RateGB: 0},
	}}
	tests := []struct {
		name        string
		region      string
		override    float64
		wantRate    float64
		wantBuiltin bool
	}{
		{"built-in region", "eu-west-1", 0, 0.02, true},
		{"unknown region falls back to default", "cn-north-1", 0, 0.05, false},
		{"override beats region, builtin still reported", "eu-west-1", 0.03, 0.03, true},
		{"override on unknown region", "cn-north-1", 0.03, 0.03, false},
		{"zero region rate is not built-in", "zero-1", 0, 0.05, false},
		{"negative override ignored", "eu-west-1", -1, 0.02, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, builtin := RateFor(rs, tt.region, tt.override)
			if rate != tt.wantRate || builtin != tt.wantBuiltin {
				t.Errorf("RateFor() = (%v, %v), want (%v, %v)", rate, builtin, tt.wantRate, tt.wantBuiltin)
			}
		})
	}
}

type ruleCase struct {
	name   string
	facts  model.Facts
	wantID string
}

// defaultRuleCases covers every rule in rules/rules.yaml at least once, plus ordering boundaries.
func defaultRuleCases() []ruleCase {
	svc := func(route model.RouteKind) model.Facts {
		return model.Facts{Route: route, TargetKind: "service", Traffic: "client", SameVPC: true, PrivateIP: true,
			AZSpread: "multi", ZoneCoverage: "full", Evidence: "aws"}
	}
	with := func(f model.Facts, mod func(*model.Facts)) model.Facts {
		mod(&f)
		return f
	}
	ext := func(kind, traffic string) model.Facts {
		return model.Facts{Route: model.RouteExternal, TargetKind: kind, Traffic: traffic, SameVPC: true, PrivateIP: true,
			AZSpread: "multi", ZoneCoverage: "full", Evidence: "aws"}
	}
	lb := func(route model.RouteKind, targetType, etp, scheme string) model.Facts {
		return with(svc(route), func(f *model.Facts) { f.TargetType, f.ExternalTrafficPolicy, f.Scheme = targetType, etp, scheme })
	}
	return []ruleCase{
		{"single zone", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.AZSpread = "single" }), "same-az"},
		{"boundary: nlb cross-zone in one AZ", with(svc(model.RouteNLBXZ), func(f *model.Facts) { f.AZSpread = "single" }), "same-az"},
		{"boundary: hints configured in one AZ", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.AZSpread, f.HintsConfigured = "single", true }), "same-az"},
		{"unknown endpoint zone", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.AZSpread = "unknown" }), "zone-unknown"},
		{"nlb cross-zone", lb(model.RouteNLBXZ, "ip", "Cluster", "internal"), "nlb-cross-zone"},
		{"boundary: nlb cross-zone instance Cluster is the cross-zone rule", lb(model.RouteNLBXZ, "instance", "Cluster", "internal"), "nlb-cross-zone"},
		{"clb instance Cluster", lb(model.RouteCLB, "instance", "Cluster", "internet-facing"), "nodeport-hop-cross-az"},
		{"nlb zonal instance Cluster", lb(model.RouteNLBZonal, "instance", "Cluster", "internal"), "nodeport-hop-cross-az"},
		{"boundary: nlb unknown cross-zone instance Cluster pays the NodePort hop", lb(model.RouteNLB, "instance", "Cluster", "internal"), "nodeport-hop-cross-az"},
		{"nlb cross-zone unknown with ip targets", lb(model.RouteNLB, "ip", "Cluster", "internal"), "nlb-cross-zone-unknown"},
		{"boundary: nlb unknown cross-zone zone-local", with(lb(model.RouteNLB, "instance", "Cluster", "internal"), func(f *model.Facts) { f.ZoneLocal = true }), "nlb-cross-zone-unknown"},
		{"boundary: nlb unknown cross-zone Local policy", lb(model.RouteNLB, "instance", "Local", "internal"), "nlb-cross-zone-unknown"},
		{"clusterip zone-local behind a pod-IP ingress", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.ZoneLocal, f.DirectIngressBackend = true, true }), "ingress-direct-backend"},
		{"boundary: pod-IP ingress backend that is not zone-local", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.DirectIngressBackend = true }), "clusterip-cross-az"},
		{"boundary: alb zone-local ignores direct ingress fact", with(lb(model.RouteALB, "instance", "Cluster", "internal"), func(f *model.Facts) { f.ZoneLocal, f.DirectIngressBackend = true, true }), "topology-aware-routing"},
		{"clusterip zone-local", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.ZoneLocal = true }), "topology-aware-routing"},
		{"boundary: alb instance Cluster zone-local", with(lb(model.RouteALB, "instance", "Cluster", "internal"), func(f *model.Facts) { f.ZoneLocal = true }), "topology-aware-routing"},
		{"boundary: clb instance Cluster zone-local", with(lb(model.RouteCLB, "instance", "Cluster", "internal"), func(f *model.Facts) { f.ZoneLocal = true }), "topology-aware-routing"},
		{"boundary: internal nlb zonal zone-local still pays the client leg", with(lb(model.RouteNLBZonal, "instance", "Cluster", "internal"), func(f *model.Facts) { f.ZoneLocal = true }), "nlb-client-cross-az"},
		{"boundary: internet-facing nlb zonal zone-local", with(lb(model.RouteNLBZonal, "instance", "Cluster", "internet-facing"), func(f *model.Facts) { f.ZoneLocal = true }), "nlb-zonal-internet"},
		{"alb ip target", lb(model.RouteALB, "ip", "Cluster", "internal"), "alb-ip-target-mode"},
		{"boundary: alb instance Local", lb(model.RouteALB, "instance", "Local", "internal"), "alb-private-same-vpc"},
		{"clb instance Local", lb(model.RouteCLB, "instance", "Local", "internal"), "clb-private-same-vpc"},
		{"boundary: alb ip with public or IPv6 endpoint", with(lb(model.RouteALB, "ip", "Cluster", "internal"), func(f *model.Facts) { f.PrivateIP = false }), "lb-exemption-unverified"},
		{"clb other VPC", with(lb(model.RouteCLB, "instance", "Local", "internal"), func(f *model.Facts) { f.SameVPC = false }), "lb-exemption-unverified"},
		{"nlb zonal internal", lb(model.RouteNLBZonal, "ip", "Cluster", "internal"), "nlb-client-cross-az"},
		{"nlb zonal internet-facing", lb(model.RouteNLBZonal, "instance", "Local", "internet-facing"), "nlb-zonal-internet"},
		{"boundary: nlb zonal without scheme", lb(model.RouteNLBZonal, "ip", "Cluster", ""), "unclassified"},
		{"hints configured but not used", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.HintsConfigured = true }), "hints-unverified"},
		{"boundary: partial coverage is billable even when hints are configured but unverified", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.HintsConfigured, f.ZoneCoverage = true, "partial" }), "clusterip-cross-az-partial"},
		{"boundary: hints configured and used but missing a caller zone", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.HintsConfigured, f.TopologyHints = true, true }), "clusterip-cross-az"},
		{"boundary: headless with hints configured", with(svc(model.RouteHeadless), func(f *model.Facts) { f.HintsConfigured = true }), "headless-cross-az"},
		{"boundary: clusterip partial coverage", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.ZoneCoverage = "partial" }), "clusterip-cross-az-partial"},
		{"clusterip unknown coverage", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.ZoneCoverage = "unknown" }), "clusterip-cross-az-partial"},
		{"clusterip full coverage", svc(model.RouteClusterIP), "clusterip-cross-az"},
		{"boundary: clusterip hints without full caller coverage", with(svc(model.RouteClusterIP), func(f *model.Facts) { f.TopologyHints = true }), "clusterip-cross-az"},
		{"headless", svc(model.RouteHeadless), "headless-cross-az"},
		{"boundary: headless zone_local still billed", with(svc(model.RouteHeadless), func(f *model.Facts) { f.ZoneLocal = true }), "headless-cross-az"},
		{"rds replication", ext("rds", "replication"), "rds-multiaz-replication"},
		{"msk replication", ext("msk", "replication"), "msk-inter-broker"},
		{"rds client", ext("rds", "client"), "rds-cross-az"},
		{"elasticache client", ext("elasticache", "client"), "elasticache-cross-az"},
		{"msk client", ext("msk", "client"), "msk-client-cross-az"},
		{"elasticache replication has no rule", ext("elasticache", "replication"), "unclassified"},
		{"zero facts", model.Facts{}, "unclassified"},
	}
}

func TestFirstMatchingRuleForFacts(t *testing.T) {
	rs := Default()
	for _, tc := range defaultRuleCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			rule, ok := firstMatch(rs.Rules, tc.facts)

			// Assert
			if !ok || rule.ID != tc.wantID {
				t.Errorf("first match = %q (ok=%v), want %q", rule.ID, ok, tc.wantID)
			}
		})
	}
}

func TestEveryDefaultRuleHasACase(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range defaultRuleCases() {
		covered[tc.wantID] = true
	}
	for _, r := range Default().Rules {
		if !covered[r.ID] {
			t.Errorf("rule %q has no case in defaultRuleCases", r.ID)
		}
	}
}

func TestDefaultRuleVerdicts(t *testing.T) {
	tests := []struct {
		id         string
		billable   bool
		confidence model.Level
	}{
		{"nodeport-hop-cross-az", true, model.Medium},
		{"ingress-direct-backend", false, model.Low},
		{"hints-unverified", true, model.Low},
		{"lb-exemption-unverified", true, model.Low},
	}
	rules := Default().Rules
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			// Act
			i := slices.IndexFunc(rules, func(r model.Rule) bool { return r.ID == tt.id })

			// Assert
			if i < 0 {
				t.Fatalf("rule %q not found", tt.id)
			}
			if r := rules[i]; r.Billable != tt.billable || r.Confidence != tt.confidence || r.Source == "" || r.Verified == "" {
				t.Errorf("rule %q = billable %v confidence %q source %q verified %q, want billable %v confidence %q with source",
					tt.id, r.Billable, r.Confidence, r.Source, r.Verified, tt.billable, tt.confidence)
			}
		})
	}
}
