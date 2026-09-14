package report

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func boolPtr(b bool) *bool { return &b }

func smallRuleSet() model.RuleSet {
	return model.RuleSet{
		Version:       1,
		DefaultRateGB: 0.02,
		Regions:       map[string]model.RegionRate{"us-east-1": {RateGB: 0.02}, "eu-west-1": {RateGB: 0.02}},
		Rules: []model.Rule{
			{
				ID: "alb-private-same-vpc", Confidence: model.High,
				Match:    model.Match{Route: model.StringSet{"alb", "clb"}, SameVPC: boolPtr(true)},
				Source:   "https://aws.amazon.com/ec2/pricing/on-demand/",
				Verified: "2026-09-14",
			},
			{
				ID: "rds-cross-az", Billable: true, RateGB: 0.01, Confidence: model.Medium,
				Match:  model.Match{TargetKind: model.StringSet{"rds"}, AZSpread: model.StringSet{"multi"}},
				Source: "https://aws.amazon.com/rds/pricing/",
			},
			{ID: "unclassified", Confidence: model.Low},
		},
	}
}

func TestRulesListsEveryRule(t *testing.T) {
	// Arrange
	rs := smallRuleSet()
	var buf bytes.Buffer

	// Act
	err := Rules(&buf, rs)

	// Assert
	if err != nil {
		t.Fatalf("Rules() error = %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "rules v1  default rate $0.02/GB  (2 regions built in)\n") {
		t.Errorf("bad header:\n%s", out)
	}
	rows := []struct {
		prefix string
		tokens []string
	}{
		{"ID ", []string{"BILLABLE", "CONF", "RATE", "MATCH", "VERIFIED"}},
		{"alb-private-same-vpc ", []string{"false", "high", "region", "route=alb,clb same_vpc=true", "2026-09-14"}},
		{"rds-cross-az ", []string{"true", "med", "$0.01", "target_kind=rds az_spread=multi", "unverified"}},
		{"unclassified ", []string{"false", "low", "region", " * ", "unverified"}},
	}
	for _, r := range rows {
		row := lineWith(t, out, r.prefix)
		for _, tok := range r.tokens {
			if !strings.Contains(row, tok) {
				t.Errorf("row %q missing %q", row, tok)
			}
		}
	}
	if !strings.HasSuffix(out, "\nSOURCES\n  alb-private-same-vpc: https://aws.amazon.com/ec2/pricing/on-demand/\n  rds-cross-az: https://aws.amazon.com/rds/pricing/\n") {
		t.Errorf("bad sources block:\n%s", out)
	}
}

func TestMatchSummaryCoversEveryMatchField(t *testing.T) {
	// Arrange: every field set, so a new Match field that matchSummary cannot render fails here.
	var m model.Match
	v := reflect.ValueOf(&m).Elem()
	for i := range v.NumField() {
		switch f := v.Field(i); f.Interface().(type) {
		case model.StringSet:
			f.Set(reflect.ValueOf(model.StringSet{"a"}))
		case *bool:
			f.Set(reflect.ValueOf(boolPtr(true)))
		default:
			t.Fatalf("Match field %s has unhandled type %s", v.Type().Field(i).Name, f.Type())
		}
	}
	lastName, _, _ := strings.Cut(v.Type().Field(v.NumField()-1).Tag.Get("json"), ",")

	// Act
	got := strings.Fields(matchSummary(m))

	// Assert
	if want := v.NumField(); len(got) != want {
		t.Errorf("matchSummary rendered %d conditions %v, want %d", len(got), got, want)
	}
	if got[0] != "route=a" || !strings.HasPrefix(got[len(got)-1], lastName+"=") {
		t.Errorf("matchSummary order = %v", got)
	}
}

func TestRulesReturnsWriteError(t *testing.T) {
	// Act
	err := Rules(failingWriter{}, smallRuleSet())

	// Assert
	if !errors.Is(err, errWrite) {
		t.Errorf("Rules() error = %v, want wrapped %v", err, errWrite)
	}
}
