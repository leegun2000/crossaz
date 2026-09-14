package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func explainRules() model.RuleSet {
	return model.RuleSet{Rules: []model.Rule{
		{ID: "clusterip-cross-az", Source: "https://rule.example/clusterip", Verified: "2026-09-14"},
		{ID: "rds-cross-az", Source: "https://rule.example/rds"},
		{ID: "rds-multiaz-replication", Verified: "2026-09-01"},
	}}
}

func renderExplain(t *testing.T, key string, paths []model.Path) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Explain(&buf, key, paths, explainRules()); err != nil {
		t.Fatalf("Explain() error = %v", err)
	}
	return buf.String()
}

func TestExplainKnownCostPath(t *testing.T) {
	// Arrange
	p := billablePath()
	p.SourceURL = "https://path.example/clusterip"
	p.ConfidenceNote = "3/3 pods measured"
	p.Facts.HintsConfigured = true
	p.Facts.DirectIngressBackend = true
	p.Fix.Command = "kubectl patch svc -n payments api \\\n  -p '{}'\n"
	paths := []model.Path{excludedPath(), p}

	// Act
	out := renderExplain(t, "payments/api", paths)

	// Assert
	want := []string{
		"payments/api  (service)\n",
		"  route:       clusterip  (evidence: k8s)\n",
		"  callers:     cluster/*  ap-northeast-2a=2 ap-northeast-2b=2 ap-northeast-2c=2\n",
		"  destination: ap-northeast-2a=2 ap-northeast-2b=2 ap-northeast-2c=2\n",
		"  facts:       az_spread=multi topology_hints=false hints_configured=true zone_local=false direct_ingress_backend=true same_vpc=false private_ip=false\n",
		"  rule:        clusterip-cross-az  (billable: true, rule confidence: high)\n",
		"  source:      https://path.example/clusterip  (verified 2026-09-14)\n",
		"  estimate:    recv 4.2 TB/mo × cross-AZ 0.667 × $0.02/GB = ~$56\n",
		"  confidence:  medium  (3/3 pods measured)\n",
		"  fix:         PreferClose — set trafficDistribution\n",
		"                 kubectl patch svc -n payments api \\\n                   -p '{}'\n",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	if strings.Contains(out, "ingress/web") {
		t.Errorf("explained a path with another key:\n%s", out)
	}
}

func TestExplainUnknownCostAndRuleFallbacks(t *testing.T) {
	// Arrange
	client := uncertainPath()
	client.Facts = model.Facts{Traffic: "client", Scheme: "internal", Evidence: model.EvidenceAWS}
	client.ConfidenceNote = "클러스터 외부 대상: 통신 여부와 볼륨을 알 수 없음"
	replication := uncertainPath()
	replication.RuleID = "rds-multiaz-replication"
	replication.Billable = false
	replication.Confidence = model.High
	replication.Facts = model.Facts{Traffic: "replication"}
	paths := []model.Path{client, replication}

	// Act
	out := renderExplain(t, "rds/orders-db", paths)

	// Assert
	want := []string{
		"  facts:       topology_hints=false hints_configured=false zone_local=false direct_ingress_backend=false scheme=internal same_vpc=false private_ip=false traffic=client\n",
		"  source:      https://rule.example/rds  (unverified)\n",
		"  estimate:    클러스터 외부 대상: 통신 여부와 볼륨을 알 수 없음\n",
		"  confidence:  low\n",
		"\nrds/orders-db  (rds)\n",
		"  source:      —  (verified 2026-09-01)\n",
		"  estimate:    —\n",
		"  confidence:  high\n",
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	if strings.Count(out, "rds/orders-db  (rds)") != 2 {
		t.Errorf("want both paths for the key:\n%s", out)
	}
	if strings.Contains(out, "fix:") {
		t.Errorf("fix line without fix:\n%s", out)
	}
}

func TestExplainUnknownKeyReturnsErrorWithoutOutput(t *testing.T) {
	// Arrange
	var buf bytes.Buffer

	// Act
	err := Explain(&buf, "nope/missing", []model.Path{billablePath()}, explainRules())

	// Assert
	want := "no path for nope/missing; run `crossaz scan` to list keys"
	if err == nil || err.Error() != want {
		t.Errorf("Explain() error = %v, want %q", err, want)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote output on error: %q", buf.String())
	}
}

func TestExplainReturnsWriteError(t *testing.T) {
	// Act
	err := Explain(failingWriter{}, "payments/api", []model.Path{billablePath()}, explainRules())

	// Assert
	if !errors.Is(err, errWrite) {
		t.Errorf("Explain() error = %v, want wrapped %v", err, errWrite)
	}
}
