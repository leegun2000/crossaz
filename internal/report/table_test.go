package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/leegun2000/crossaz/internal/model"
)

var threeZones = model.AZDistribution{"ap-northeast-2a": 2, "ap-northeast-2b": 2, "ap-northeast-2c": 2}

func billablePath() model.Path {
	return model.Path{
		Source:         model.Workload{Name: "*", Kind: "cluster", AZDist: threeZones},
		Dest:           model.Target{Kind: model.TargetService, Ref: "payments/api", AZDist: threeZones},
		Route:          model.RouteClusterIP,
		Facts:          model.Facts{Route: model.RouteClusterIP, AZSpread: "multi", Evidence: model.EvidenceK8s},
		Billable:       model.TriTrue,
		RuleID:         "clusterip-cross-az",
		RateGB:         0.02,
		RuleConfidence: model.High,
		BytesPerMo:     ptr(4.2e12),
		CrossAZFrac:    0.667,
		CostPerMo:      ptr(56.03),
		Confidence:     model.Medium,
		Fix:            &model.Fix{Kind: "PreferClose", Description: "set trafficDistribution"},
	}
}

func excludedPath() model.Path {
	return model.Path{
		Dest:       model.Target{Kind: model.TargetService, Ref: "ingress/web", AZDist: threeZones},
		Route:      model.RouteALB,
		Facts:      model.Facts{TargetType: "instance"},
		Billable:   model.TriFalse,
		RuleID:     "alb-private-same-vpc",
		BytesPerMo: ptr(6e12),
		Confidence: model.High,
	}
}

func uncertainPath() model.Path {
	return model.Path{
		Dest:       model.Target{Kind: model.TargetRDS, Ref: "orders-db", AZDist: model.AZDistribution{"ap-northeast-2a": 1}},
		Route:      model.RouteExternal,
		Billable:   model.TriTrue,
		RuleID:     "rds-cross-az",
		Confidence: model.Low,
	}
}

func likelyResult() model.ScanResult {
	return model.ScanResult{
		Cluster:   "prod-a",
		Region:    "ap-northeast-2",
		Zones:     []string{"ap-northeast-2a", "ap-northeast-2b", "ap-northeast-2c"},
		NodeCount: 24,
		Layers:    []string{"L0", "L1", "L2", "L3"},
		Exposure:  model.ExposureLikely,
		Paths:     []model.Path{billablePath()},
		Excluded:  []model.Path{excludedPath()},
		Uncertain: []model.Path{uncertainPath()},
		Hidden:    12,
		Threshold: 1,
		TotalUSD:  ptr(133.2),
		Warnings:  []string{"L1: rds: AccessDenied"},
		Verify: &model.VerifyResult{
			Window:        "2026-08-18..2026-08-31",
			Days:          14,
			EstimatedDays: 1,
			EstimatedUSD:  ptr(133.2),
			ActualUSD:     ptr(118),
			Ratio:         ptr(1.1288),
			Note:          "제외 규칙이 대체로 맞음",
		},
	}
}

func renderTable(t *testing.T, r model.ScanResult) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Table(&buf, r); err != nil {
		t.Fatalf("Table() error = %v", err)
	}
	return buf.String()
}

// column returns the display column (in runes) where tok starts in line, or -1.
func column(line, tok string) int {
	i := strings.Index(line, tok)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:i])
}

func lineWith(t *testing.T, out, prefix string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line starting with %q in:\n%s", prefix, out)
	return ""
}

func TestTableExposureNonePrintsExactlyTwoLines(t *testing.T) {
	// Arrange
	r := model.ScanResult{Cluster: "dev-b", Zones: []string{"us-east-1a"}, NodeCount: 3, Exposure: model.ExposureNone}

	// Act
	out := renderTable(t, r)

	// Assert
	want := "Cluster: dev-b   AZs: 1   Nodes: 3\nExposure: NONE — 모든 노드가 단일 AZ에 있고 과금 경로가 없습니다.\n"
	if out != want {
		t.Errorf("Table() =\n%q\nwant\n%q", out, want)
	}
}

func TestTableExposureNoneAppendsWarnings(t *testing.T) {
	// Arrange
	r := model.ScanResult{Cluster: "dev-b", NodeCount: 3, Exposure: model.ExposureNone, Warnings: []string{"L1: no credentials"}}

	// Act
	out := renderTable(t, r)

	// Assert
	if !strings.HasSuffix(out, "\n\nWARNINGS\n  - L1: no credentials\n") {
		t.Errorf("missing warnings block:\n%s", out)
	}
}

func TestTableLikelyResultShowsAllBlocks(t *testing.T) {
	// Arrange
	r := likelyResult()

	// Act
	out := renderTable(t, r)

	// Assert
	wantLines := []string{
		"Cluster: prod-a   Region: ap-northeast-2   AZs: 3   Nodes: 24   Layers: L0+L1+L2+L3",
		"Exposure: LIKELY",
		"BILLABLE",
		"(12 paths below $1/mo hidden — use --threshold 0)",
		"NOT BILLABLE",
		"UNCERTAIN",
		"Estimated total: ~$133/mo",
		"Run `crossaz explain payments/api` for rule details.",
		"VERIFY",
		"  Estimated:  ~$133/mo",
		"  Actual:     $118/mo   (Cost Explorer DataTransfer-Regional-Bytes InterZone, 2026-08-18..2026-08-31 14일 기준, 미확정 1일 포함(잠정) — 계정·리전 전체)",
		"  Ratio:      1.13   제외 규칙이 대체로 맞음",
		"  Ratio > 1.5 → 제외 규칙 누락 의심",
		"  Ratio < 0.5 → 클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심",
		"WARNINGS",
		"  - L1: rds: AccessDenied",
	}
	lines := strings.Split(out, "\n")
	for _, want := range wantLines {
		if !containsLine(lines, want) {
			t.Errorf("missing line %q in:\n%s", want, out)
		}
	}
	order := []string{"\nBILLABLE\n", "\nNOT BILLABLE\n", "\nUNCERTAIN\n", "\nEstimated total", "\nVERIFY\n", "\nWARNINGS\n"}
	last := -1
	for _, s := range order {
		i := strings.Index(out, s)
		if i <= last {
			t.Errorf("block %q out of order in:\n%s", s, out)
		}
		last = i
	}
}

func TestTableRowsCarryColumnsAndAlign(t *testing.T) {
	tests := []struct {
		name      string
		rowPrefix string
		tokens    []string
	}{
		{"billable row", "payments/api", []string{"clusterip", "4.2 TB", "3/3 even", "~$56", "med", "PreferClose"}},
		{"excluded row", "ingress/web", []string{"alb", "6.0 TB", "3/3 even", "—", "high", "alb-private-same-vpc"}},
		{"uncertain row", "rds/orders-db", []string{"external", "?", "1/3", "?", "low", "rds-cross-az"}},
	}
	out := renderTable(t, likelyResult())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			row := lineWith(t, out, tt.rowPrefix)

			// Act
			fields := strings.Split(row, "   ")

			// Assert
			for _, tok := range tt.tokens {
				if !strings.Contains(row, tok) {
					t.Errorf("row %q missing %q", row, tok)
				}
			}
			if len(fields) < len(tt.tokens)+1 {
				t.Errorf("row %q not split into padded columns", row)
			}
		})
	}

	header := lineWith(t, out, "SERVICE")
	row := lineWith(t, out, "payments/api")
	for _, pair := range [][2]string{{"ROUTE", "clusterip"}, {"AZ SPREAD", "3/3 even"}, {"CONF", "med"}, {"FIX", "PreferClose"}} {
		if column(header, pair[0]) != column(row, pair[1]) {
			t.Errorf("column %s at %d but value %q at %d", pair[0], column(header, pair[0]), pair[1], column(row, pair[1]))
		}
	}
}

func TestTableOmitsEmptySections(t *testing.T) {
	// Arrange
	r := likelyResult()
	r.Exposure = model.ExposureUnlikely
	r.Paths = nil
	r.Uncertain = nil
	r.Hidden = 0
	r.Verify = nil
	r.Warnings = nil

	// Act
	out := renderTable(t, r)

	// Assert
	for _, absent := range []string{"\nBILLABLE\n", "UNCERTAIN", "Run `crossaz explain", "VERIFY", "WARNINGS", "FIX"} {
		if strings.Contains(out, absent) {
			t.Errorf("unexpected %q in:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "\nNOT BILLABLE\n") || !strings.Contains(out, "NOTE") {
		t.Errorf("NOT BILLABLE section missing:\n%s", out)
	}
	if !strings.Contains(out, "Exposure: UNLIKELY") {
		t.Errorf("exposure not upper-cased:\n%s", out)
	}
}

func TestTableHiddenOnlyStillPrintsBillableTitle(t *testing.T) {
	// Arrange
	r := likelyResult()
	r.Paths = nil
	r.Threshold = 2.5

	// Act
	out := renderTable(t, r)

	// Assert
	if !strings.Contains(out, "\nBILLABLE\n(12 paths below $2.5/mo hidden — use --threshold 0)\n") {
		t.Errorf("hidden-only BILLABLE block wrong:\n%s", out)
	}
}

func TestTableFooterWithoutVolumeLayer(t *testing.T) {
	// Arrange
	r := likelyResult()
	r.TotalUSD = nil
	r.Verify.EstimatedUSD = nil
	r.Verify.ActualUSD = nil
	r.Verify.Ratio = nil
	r.Paths[0].Fix = nil

	// Act
	out := renderTable(t, r)

	// Assert
	for _, want := range []string{
		"Estimated total: ? (L2 없음 — --prometheus-url)",
		"  Estimated:  ?",
		"  Actual:     ?   (Cost Explorer",
		"  Ratio:      ?   ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if row := lineWith(t, out, "payments/api"); !strings.HasSuffix(row, "—") {
		t.Errorf("fix column without fix should be a dash: %q", row)
	}
}

func unknownPath(ref string, bytes *float64) model.Path {
	return model.Path{
		Dest:       model.Target{Kind: model.TargetService, Ref: ref, AZDist: threeZones},
		Route:      model.RouteClusterIP,
		Billable:   model.TriUnknown,
		RuleID:     "zone-unknown",
		BytesPerMo: bytes,
		Confidence: model.Low,
	}
}

func TestTableUnconfirmedLine(t *testing.T) {
	tests := []struct {
		name      string
		uncertain []model.Path
		max       *float64
		wantLine  string
		wantRow   []string
	}{
		{"known max", []model.Path{uncertainPath(), unknownPath("ns/u", ptr(150e9))}, ptr(3),
			"미확정: $0–$3.00/mo (billable 미확정 경로 1개, 총액 제외)\n", []string{"150 GB", "?", "low", "zone-unknown"}},
		{"unknown max", []model.Path{unknownPath("ns/u", nil), unknownPath("ns/v", nil)}, nil,
			"미확정: $0–? (billable 미확정 경로 2개, 총액 제외)\n", []string{"?", "?", "low", "zone-unknown"}},
		{"no unknown paths, no line", []model.Path{uncertainPath()}, nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			r := likelyResult()
			r.Uncertain = tt.uncertain
			r.UnconfirmedMaxUSD = tt.max

			// Act
			out := renderTable(t, r)

			// Assert
			if tt.wantLine == "" {
				if strings.Contains(out, "\n미확정:") {
					t.Errorf("unexpected 미확정 line:\n%s", out)
				}
				return
			}
			if !strings.Contains(out, "Estimated total: ~$133/mo\n"+tt.wantLine) {
				t.Errorf("want %q right after the total:\n%s", tt.wantLine, out)
			}
			row := lineWith(t, out, "ns/u ")
			for _, tok := range tt.wantRow {
				if !strings.Contains(row, tok) {
					t.Errorf("row %q missing %q", row, tok)
				}
			}
			if strings.Contains(row, notBilledMark) {
				t.Errorf("unknown-billable row must not show %q: %q", notBilledMark, row)
			}
		})
	}
}

func TestTableReturnsWriteError(t *testing.T) {
	tests := []struct {
		name string
		r    model.ScanResult
	}{
		{"full report", likelyResult()},
		{"exposure none", model.ScanResult{Exposure: model.ExposureNone}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			err := Table(failingWriter{}, tt.r)

			// Assert
			if !errors.Is(err, errWrite) {
				t.Errorf("Table() error = %v, want wrapped %v", err, errWrite)
			}
		})
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
