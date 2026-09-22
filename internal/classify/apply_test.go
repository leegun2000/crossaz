package classify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

const patchCommand = "kubectl patch svc -n {{.Namespace}} {{.Name}} -p '{{.TrafficDistribution}}' # {{.Kind}} {{.Ref}}\n"

func applyRuleSet() model.RuleSet {
	return model.RuleSet{
		DefaultRateGB: 0.05,
		Regions:       map[string]model.RegionRate{"eu-west-1": {RateGB: 0.02}},
		Rules: []model.Rule{
			{ID: "fixed-rate", Match: model.Match{TargetKind: model.StringSet{"rds"}}, Billable: model.TriTrue, RateGB: 0.01,
				Confidence: model.High, Reason: "rds", Source: "https://rds",
				Fix: &model.FixTemplate{Kind: "relocate", Description: "move", Command: "echo {{.Kind}} {{.Name}} {{.Namespace}}"}},
			{ID: "region-rate", Match: model.Match{Route: model.StringSet{"clusterip"}}, Billable: model.TriTrue,
				Confidence: model.High, Reason: "cip", Source: "https://cip",
				Fix: &model.FixTemplate{Kind: "PreferClose", Description: "patch", Command: patchCommand}},
			{ID: "free", Billable: model.TriFalse, Match: model.Match{Route: model.StringSet{"alb"}}, RateGB: 0.03, Confidence: model.High, Reason: "free"},
			{ID: "catch-all", Billable: model.TriUnknown, Confidence: model.Low, Reason: "?"},
		},
	}
}

func servicePathFor(ref string, route model.RouteKind, evidence string) model.Path {
	return model.Path{
		Dest:  model.Target{Kind: model.TargetService, Ref: ref},
		Route: route,
		Facts: model.Facts{Route: route, TargetKind: model.TargetService, Evidence: evidence},
	}
}

func rdsPathFor(ref string) model.Path {
	return model.Path{
		Dest:  model.Target{Kind: model.TargetRDS, Ref: ref},
		Route: model.RouteExternal,
		Facts: model.Facts{Route: model.RouteExternal, TargetKind: model.TargetRDS, Evidence: model.EvidenceAWS},
	}
}

func applyOne(t *testing.T, p model.Path, s model.Snapshot, override float64) model.Path {
	t.Helper()
	out, err := Apply([]model.Path{p}, applyRuleSet(), s, override)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return out[0]
}

func TestApplyRatePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		path     model.Path
		region   string
		override float64
		want     float64
	}{
		{"override beats rule rate", rdsPathFor("db"), "eu-west-1", 0.07, 0.07},
		{"rule rate beats region rate", rdsPathFor("db"), "eu-west-1", 0, 0.01},
		{"region rate when rule has none", servicePathFor("a/b", model.RouteClusterIP, "k8s"), "eu-west-1", 0, 0.02},
		{"default rate for unlisted region", servicePathFor("a/b", model.RouteClusterIP, "k8s"), "cn-north-1", 0, 0.05},
		{"non-billable has zero rate even with override", servicePathFor("a/b", model.RouteALB, "aws"), "eu-west-1", 0.07, 0},
		{"unknown-billable keeps the rate it would have if billable", servicePathFor("a/b", model.RouteNLB, "aws"), "eu-west-1", 0, 0.02},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := applyOne(t, tt.path, model.Snapshot{Region: tt.region}, tt.override)

			// Assert
			if got.RateGB != tt.want {
				t.Errorf("RateGB = %v, want %v", got.RateGB, tt.want)
			}
		})
	}
}

func TestApplyCopiesRuleVerdict(t *testing.T) {
	// Act
	got := applyOne(t, rdsPathFor("db"), model.Snapshot{}, 0)

	// Assert
	if got.Billable != model.TriTrue || got.RuleID != "fixed-rate" || got.Reason != "rds" || got.SourceURL != "https://rds" || got.RuleConfidence != model.High {
		t.Errorf("verdict = %+v", got)
	}
	want := &model.Fix{Kind: "relocate", Description: "move", Command: "echo rds db "}
	if !reflect.DeepEqual(got.Fix, want) {
		t.Errorf("Fix = %+v, want %+v (non-service: Name = Ref, no Namespace)", got.Fix, want)
	}
}

func TestApplyRendersServiceFix(t *testing.T) {
	tests := []struct {
		name          string
		serverVersion string
		wantTD        string
	}{
		{"1.34 control plane", "v1.34.0-eks-1", "PreferSameZone"},
		{"1.33 control plane", "v1.33.4-eks-abc", "PreferClose"},
		{"unknown control plane", "", "PreferClose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			p := servicePathFor("shop/api", model.RouteClusterIP, "k8s")

			// Act
			got := applyOne(t, p, model.Snapshot{ServerVersion: tt.serverVersion}, 0)

			// Assert
			want := "kubectl patch svc -n shop api -p '" + tt.wantTD + "' # service shop/api"
			if got.Fix == nil || got.Fix.Command != want {
				t.Errorf("Fix = %+v, want command %q", got.Fix, want)
			}
		})
	}
}

func TestApplyConfidenceIsLoweredByEvidence(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		want     model.Level
	}{
		{"k8s keeps high", model.EvidenceK8s, model.High},
		{"aws keeps high", model.EvidenceAWS, model.High},
		{"annotation lowers high to medium", model.EvidenceAnnotation, model.Medium},
		{"missing evidence is low", "", model.Low},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyOne(t, servicePathFor("a/b", model.RouteClusterIP, tt.evidence), model.Snapshot{}, 0)
			if got.Confidence != tt.want || got.RuleConfidence != model.High {
				t.Errorf("Confidence = %q (rule %q), want %q", got.Confidence, got.RuleConfidence, tt.want)
			}
		})
	}
}

func TestApplyCatchAllKeepsLowAndClearsStaleFix(t *testing.T) {
	// Arrange
	p := servicePathFor("a/b", model.RouteHeadless, model.EvidenceK8s)
	p.Fix = &model.Fix{Kind: "stale"}
	p.RateGB = 9

	// Act
	got := applyOne(t, p, model.Snapshot{}, 0)

	// Assert: the unknown catch-all replaces the stale rate with the default one
	if got.RuleID != "catch-all" || got.Confidence != model.Low || got.Fix != nil || got.RateGB != 0.05 {
		t.Errorf("got %+v", got)
	}
}

func TestApplyDoesNotMutateInput(t *testing.T) {
	// Arrange
	paths := []model.Path{
		servicePathFor("a/b", model.RouteClusterIP, "k8s"),
		rdsPathFor("db"),
	}
	before := []model.Path{
		servicePathFor("a/b", model.RouteClusterIP, "k8s"),
		rdsPathFor("db"),
	}

	// Act
	out, err := Apply(paths, applyRuleSet(), model.Snapshot{}, 0)

	// Assert
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !reflect.DeepEqual(paths, before) {
		t.Errorf("input mutated: %+v", paths)
	}
	if len(out) != 2 || out[0].RuleID != "region-rate" || out[1].RuleID != "fixed-rate" {
		t.Errorf("output = %+v", out)
	}
}

func TestApplyErrors(t *testing.T) {
	tests := []struct {
		name    string
		rs      model.RuleSet
		wantErr string
	}{
		{"no rule matches", model.RuleSet{DefaultRateGB: 0.02, Rules: []model.Rule{
			{ID: "only-alb", Match: model.Match{Route: model.StringSet{"alb"}}, Confidence: model.High},
		}}, "no rule matches path a/b"},
		{"fix fails to render", model.RuleSet{DefaultRateGB: 0.02, Rules: []model.Rule{
			{ID: "bad", Confidence: model.High, Fix: &model.FixTemplate{Command: "{{.Nope}}"}},
		}}, `render fix of rule "bad"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			out, err := Apply([]model.Path{servicePathFor("a/b", model.RouteClusterIP, "k8s")}, tt.rs, model.Snapshot{}, 0)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || out != nil {
				t.Fatalf("Apply() = (%v, %v), want error containing %q", out, err, tt.wantErr)
			}
		})
	}
}

func TestApplyWithDefaultRulesRendersRealTemplates(t *testing.T) {
	// Arrange
	p := servicePathFor("shop/api", model.RouteClusterIP, model.EvidenceK8s)
	p.Facts.AZSpread, p.Facts.ZoneCoverage = spreadMulti, coverageFull

	// Act
	out, err := Apply([]model.Path{p}, Default(), model.Snapshot{Region: "us-east-1", ServerVersion: "v1.34.1"}, 0)

	// Assert
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	got := out[0]
	want := "kubectl patch svc -n shop api --type=merge \\\n  -p '{\"spec\":{\"trafficDistribution\":\"PreferSameZone\"}}'"
	if got.RuleID != "clusterip-cross-az" || got.RateGB != 0.02 || got.Fix == nil || got.Fix.Command != want {
		t.Errorf("got rule %q rate %v fix %+v", got.RuleID, got.RateGB, got.Fix)
	}
}
