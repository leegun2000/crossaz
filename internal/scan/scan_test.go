package scan

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/classify"
	"github.com/leegun2000/crossaz/internal/model"
	"github.com/leegun2000/crossaz/internal/report"
)

var update = flag.Bool("update", false, "rewrite testdata/golden from the current output")

var testdataDir = filepath.Join("..", "..", "testdata")

func loadFixture(t *testing.T, name string) model.Snapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, "fixtures", name+".json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var s model.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return s
}

func TestGoldenOutputs(t *testing.T) {
	tests := []struct {
		fixture string
		golden  string
		render  func(*bytes.Buffer, model.ScanResult) error
	}{
		{"prod-a", "prod-a.txt", func(b *bytes.Buffer, r model.ScanResult) error { return report.Table(b, r) }},
		{"prod-a", "prod-a.json", func(b *bytes.Buffer, r model.ScanResult) error { return report.JSON(b, r) }},
		{"dev-b", "dev-b.txt", func(b *bytes.Buffer, r model.ScanResult) error { return report.Table(b, r) }},
		{"dev-b", "dev-b.json", func(b *bytes.Buffer, r model.ScanResult) error { return report.JSON(b, r) }},
		{"l0-only", "l0-only.txt", func(b *bytes.Buffer, r model.ScanResult) error { return report.Table(b, r) }},
		// kube-proxy add-on left at 1.30: PreferClose hints unused (hints-unverified), topology-mode Auto honored but withheld (hints-withheld).
		{"stale-proxy", "stale-proxy.json", func(b *bytes.Buffer, r model.ScanResult) error { return report.JSON(b, r) }},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			// Arrange
			s := loadFixture(t, tt.fixture)
			path := filepath.Join(testdataDir, "golden", tt.golden)

			// Act
			r, _, err := Analyze(s, classify.Default(), Options{Threshold: 1})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			var got bytes.Buffer
			if err := tt.render(&got, r); err != nil {
				t.Fatalf("render: %v", err)
			}

			// Assert
			if *update {
				if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if got.String() != string(want) {
				t.Errorf("output differs from %s (run go test ./internal/scan -update after checking)\n--- got ---\n%s", path, got.String())
			}
		})
	}
}

// goldenRuleID matches a rule id in a JSON golden ("ruleId") or in a table golden's last column.
var goldenRuleID = regexp.MustCompile(`(?m)"ruleId": "([^"]+)"|   ([a-z0-9-]+)$`)

func TestGoldenOutputsCoverEveryRule(t *testing.T) {
	// Arrange
	files, err := filepath.Glob(filepath.Join(testdataDir, "golden", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("list goldens: %v (%d files)", err, len(files))
	}

	// Act
	seen := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read golden: %v", err)
		}
		for _, m := range goldenRuleID.FindAllStringSubmatch(string(data), -1) {
			seen[m[1]+m[2]] = true
		}
	}

	// Assert
	for _, r := range classify.Default().Rules {
		if !seen[r.ID] {
			t.Errorf("rule %s appears in no golden output; extend a testdata/fixtures file so a path matches it", r.ID)
		}
	}
}

func TestAnonymizePreservesVerdictsOfEveryFixture(t *testing.T) {
	for _, name := range []string{"prod-a", "dev-b", "l0-only", "stale-proxy"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s := loadFixture(t, name)

			// Act
			plain, plainPaths, err := Analyze(s, classify.Default(), Options{Threshold: 1})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			anon, anonPaths, err := Analyze(Anonymize(s), classify.Default(), Options{Threshold: 1})
			if err != nil {
				t.Fatalf("Analyze anonymized: %v", err)
			}

			// Assert
			var want, got bytes.Buffer
			if err := report.Table(&want, plain); err != nil {
				t.Fatal(err)
			}
			if err := report.Table(&got, anon); err != nil {
				t.Fatal(err)
			}
			if got.String() != want.String() {
				t.Errorf("anonymization changed the report\n--- plain ---\n%s\n--- anonymized ---\n%s", want.String(), got.String())
			}
			if !slices.EqualFunc(plainPaths, anonPaths, sameVerdict) {
				t.Errorf("anonymization changed path verdicts")
			}
		})
	}
}

func sameVerdict(a, b model.Path) bool {
	return a.Key() == b.Key() && a.RuleID == b.RuleID && a.Billable == b.Billable &&
		a.Confidence == b.Confidence && reflect.DeepEqual(a.Facts, b.Facts)
}

func TestAnalyzeReturnsEveryEstimatedPath(t *testing.T) {
	// Arrange
	s := loadFixture(t, "prod-a")

	// Act
	r, paths, err := Analyze(s, classify.Default(), Options{Threshold: 1})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Assert
	shown := len(r.Paths) + len(r.Excluded) + len(r.Uncertain) + r.Hidden
	if len(paths) != shown {
		t.Errorf("len(paths) = %d, want every path (%d shown + hidden)", len(paths), shown)
	}
	if !slices.ContainsFunc(paths, func(p model.Path) bool { return p.Key() == "kube-system/kube-dns" }) {
		t.Errorf("hidden path kube-system/kube-dns missing from returned paths")
	}
}

func TestAnalyzeWarnings(t *testing.T) {
	zonal := func(zones ...string) []model.Node {
		nodes := make([]model.Node, 0, len(zones))
		for i, z := range zones {
			nodes = append(nodes, model.Node{Name: "node-" + string(rune('a'+i)), Zone: z})
		}
		return nodes
	}
	tests := []struct {
		name     string
		snapshot model.Snapshot
		rateGB   float64
		want     []string
	}{
		{
			name:     "built-in region with regular zones adds nothing",
			snapshot: model.Snapshot{Region: "ap-northeast-2", Nodes: zonal("ap-northeast-2a", "ap-northeast-2c")},
		},
		{
			name:     "region without a built-in rate warns with the default rate",
			snapshot: model.Snapshot{Region: "cn-north-1", Nodes: zonal("cn-north-1a")},
			want:     []string{"리전 cn-north-1 요율이 내장되어 있지 않아 기본값 $0.02/GB 사용 — --rate-gb로 지정"},
		},
		{
			name:     "rate override silences the missing region rate",
			snapshot: model.Snapshot{Region: "cn-north-1", Nodes: zonal("cn-north-1a")},
			rateGB:   0.0266,
		},
		{
			name:     "unknown region asks for --region or --rate-gb and skips the zone check",
			snapshot: model.Snapshot{Nodes: zonal("us-west-2-lax-1a")},
			want:     []string{"리전을 알 수 없어 기본값 $0.02/GB 사용 — --region 또는 --rate-gb로 지정"},
		},
		{
			name:     "local and wavelength zone nodes are listed once, sorted",
			snapshot: model.Snapshot{Region: "us-west-2", Nodes: zonal("us-west-2a", "us-west-2-wl1-sea-wlz-1", "us-west-2-lax-1a", "us-west-2-lax-1a", "")},
			want:     []string{"Local/Wavelength Zone 노드 발견(us-west-2-lax-1a, us-west-2-wl1-sea-wlz-1): 요율이 달라 해당 경로 금액 생략"},
		},
		{
			name:     "collection warnings come first",
			snapshot: model.Snapshot{Region: "cn-north-1", Warnings: []string{"L1: 서브넷: denied"}},
			want:     []string{"L1: 서브넷: denied", "리전 cn-north-1 요율이 내장되어 있지 않아 기본값 $0.02/GB 사용 — --rate-gb로 지정"},
		},
		{
			name:     "a zone that only shares the region prefix is not regular",
			snapshot: model.Snapshot{Region: "us-east-1", Nodes: zonal("us-east-1A", "us-east-1bb")},
			want:     []string{"Local/Wavelength Zone 노드 발견(us-east-1A, us-east-1bb): 요율이 달라 해당 경로 금액 생략"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			before := slices.Clone(tt.snapshot.Warnings)

			// Act
			r, _, err := Analyze(tt.snapshot, classify.Default(), Options{Threshold: 1, RateGB: tt.rateGB})

			// Assert
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if !slices.Equal(r.Warnings, tt.want) {
				t.Errorf("warnings = %q, want %q", r.Warnings, tt.want)
			}
			if !slices.Equal(tt.snapshot.Warnings, before) {
				t.Errorf("input warnings mutated: %q", tt.snapshot.Warnings)
			}
		})
	}
}

func TestAnalyzeFailsWhenNoRuleMatches(t *testing.T) {
	// Arrange
	rs := model.RuleSet{DefaultRateGB: 0.02, Rules: []model.Rule{{ID: "nlb-only", Match: model.Match{Route: model.StringSet{"nlb"}}}}}
	s := model.Snapshot{
		Nodes:    []model.Node{{Name: "n1", Zone: "us-east-1a"}},
		Services: []model.Service{{Namespace: "ns", Name: "svc", Type: "ClusterIP", ClusterIP: "10.0.0.1", Endpoints: []model.Endpoint{{IP: "10.1.0.1", Zone: "us-east-1a", Ready: true}}}},
	}

	// Act
	_, paths, err := Analyze(s, rs, Options{})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "classify paths") {
		t.Fatalf("err = %v, want a classify error", err)
	}
	if paths != nil {
		t.Errorf("paths = %v, want nil on error", paths)
	}
}

func TestAnalyzeBlanksLocalZoneAmounts(t *testing.T) {
	snapshot := func(zones ...string) model.Snapshot {
		s := model.Snapshot{Region: "us-west-2", Volume: &model.VolumeData{Window: "7d", RecvBytesPerS: map[string]float64{}}}
		svc := model.Service{Namespace: "app", Name: "api", Type: "ClusterIP", ClusterIP: "10.100.0.1"}
		for i, z := range zones {
			node, pod := "node-"+strconv.Itoa(i), "api-"+strconv.Itoa(i)
			ip := "10.0.0." + strconv.Itoa(i+1)
			s.Nodes = append(s.Nodes, model.Node{Name: node, Zone: z})
			s.Pods = append(s.Pods, model.Pod{Namespace: "app", Name: pod, NodeName: node, IP: ip})
			svc.Endpoints = append(svc.Endpoints, model.Endpoint{IP: ip, PodName: pod, NodeName: node, Zone: z, Ready: true})
			s.Volume.RecvBytesPerS["app/"+pod] = 1e6
		}
		s.Services = []model.Service{svc}
		return s
	}
	tests := []struct {
		name         string
		snapshot     model.Snapshot
		wantCost     bool
		wantConf     model.Level
		wantNote     string
		wantExposure string
	}{
		{
			name:         "path touching a Local Zone keeps bytes but loses its amount and confidence",
			snapshot:     snapshot("us-west-2a", "us-west-2-lax-1a"),
			wantConf:     model.Low,
			wantNote:     "Local/Wavelength Zone 포함: 요율이 달라 금액 생략",
			wantExposure: model.ExposureLikely,
		},
		{
			name:         "regular zones keep the amount",
			snapshot:     snapshot("us-west-2a", "us-west-2b"),
			wantCost:     true,
			wantConf:     model.Medium,
			wantExposure: model.ExposureLikely,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			r, paths, err := Analyze(tt.snapshot, classify.Default(), Options{Threshold: 1})

			// Assert
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			i := slices.IndexFunc(paths, func(p model.Path) bool { return p.Key() == "app/api" })
			if i < 0 {
				t.Fatalf("no app/api path in %d paths", len(paths))
			}
			p := paths[i]
			if !p.Billable || p.BytesPerMo == nil || (p.CostPerMo != nil) != tt.wantCost {
				t.Errorf("billable=%t bytes=%v cost=%v, want billable with bytes and cost known=%t", p.Billable, p.BytesPerMo, p.CostPerMo, tt.wantCost)
			}
			if p.Confidence != tt.wantConf || p.ConfidenceNote != tt.wantNote {
				t.Errorf("confidence = %q (%q), want %q (%q)", p.Confidence, p.ConfidenceNote, tt.wantConf, tt.wantNote)
			}
			if r.Exposure != tt.wantExposure {
				t.Errorf("Exposure = %q, want %q", r.Exposure, tt.wantExposure)
			}
			if !tt.wantCost && (r.TotalUSD == nil || *r.TotalUSD != 0) {
				t.Errorf("TotalUSD = %v, want known 0 without the Local Zone amount", r.TotalUSD)
			}
		})
	}
}
