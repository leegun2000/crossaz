package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/leegun2000/crossaz/internal/model"
)

func ptr(v float64) *float64 { return &v }

var errWrite = errors.New("disk full")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestBytes(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, "0 B"},
		{"plain bytes are integers", 999, "999 B"},
		{"one decimal below ten of the unit", 4.2e12, "4.2 TB"},
		{"integer at or above ten", 9e11, "900 GB"},
		{"megabytes", 12e6, "12 MB"},
		{"exactly one unit", 1000, "1.0 KB"},
		{"rounding up crosses into next unit", 999.6e9, "1.0 TB"},
		{"rounding up crosses into integer display", 9.96e9, "10 GB"},
		{"largest unit keeps growing", 1.5e18, "1500 PB"},
		{"negative keeps sign", -4.2e12, "-4.2 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := Bytes(tt.in)

			// Assert
			if got != tt.want {
				t.Errorf("Bytes(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUSD(t *testing.T) {
	tests := []struct {
		name     string
		in       *float64
		billable bool
		want     string
	}{
		{"billable unknown", nil, true, "?"},
		{"not billable unknown", nil, false, "—"},
		{"not billable ignores value", ptr(56), false, "—"},
		{"rounded at ten or more", ptr(56.4), true, "~$56"},
		{"two decimals below ten", ptr(4.2), true, "~$4.20"},
		{"cents", ptr(0.4), true, "~$0.40"},
		{"rounds up into integer display", ptr(9.999), true, "~$10"},
		{"zero", ptr(0), true, "~$0.00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := USD(tt.in, tt.billable)

			// Assert
			if got != tt.want {
				t.Errorf("USD() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfLabel(t *testing.T) {
	tests := []struct {
		in   model.Level
		want string
	}{
		{model.High, "high"},
		{model.Medium, "med"},
		{model.Low, "low"},
		{"", "low"},
	}
	for _, tt := range tests {
		t.Run(string(tt.in), func(t *testing.T) {
			if got := confLabel(tt.in); got != tt.want {
				t.Errorf("confLabel(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRouteLabel(t *testing.T) {
	tests := []struct {
		name       string
		route      model.RouteKind
		targetType string
		want       string
	}{
		{"alb ip target", model.RouteALB, "ip", "alb (ip)"},
		{"alb instance target", model.RouteALB, "instance", "alb (inst)"},
		{"nlb cross-zone shortened with target type", model.RouteNLBXZ, "ip", "nlb-xz (ip)"},
		{"nlb unknown target type verbatim", model.RouteNLB, "", "nlb"},
		{"clb is always instance so no suffix", model.RouteCLB, "instance", "clb"},
		{"other routes verbatim", model.RouteClusterIP, "", "clusterip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			p := model.Path{Route: tt.route, Facts: model.Facts{TargetType: tt.targetType}}

			// Act
			got := routeLabel(p)

			// Assert
			if got != tt.want {
				t.Errorf("routeLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAZSpread(t *testing.T) {
	tests := []struct {
		name  string
		dest  model.AZDistribution
		zones int
		want  string
	}{
		{"even multi-zone", model.AZDistribution{"a": 2, "b": 2, "c": 2}, 3, "3/3 even"},
		{"uneven multi-zone", model.AZDistribution{"a": 5, "b": 1}, 3, "2/3"},
		{"single zone never even", model.AZDistribution{"a": 3}, 3, "1/3"},
		{"no zones", nil, 2, "0/2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := azSpread(tt.dest, tt.zones); got != tt.want {
				t.Errorf("azSpread() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestZoneList(t *testing.T) {
	tests := []struct {
		name string
		in   model.AZDistribution
		want string
	}{
		{"sorted and zero counts dropped", model.AZDistribution{"b": 1, "a": 2, "c": 0}, "a=2 b=1"},
		{"empty", nil, "—"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := zoneList(tt.in); got != tt.want {
				t.Errorf("zoneList() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextOutputsReplaceControlCharacters(t *testing.T) {
	const evil = "\x1b]0;evil\x07\r\n\t\u009b"
	const safe = "?]0;evil?????"
	tainted := func() model.Path {
		p := billablePath()
		p.Dest.Ref = evil + "/api"
		p.Dest.AZDist = model.AZDistribution{evil: 1}
		p.Source.Name = evil
		p.Facts.Scheme, p.Facts.TargetType, p.Facts.Evidence = evil, evil, evil
		p.RuleID, p.Reason, p.SourceURL, p.ConfidenceNote = evil, evil, evil, evil
		p.Fix = &model.Fix{Kind: evil, Description: evil, Command: "kubectl " + evil + "\nkubectl patch"}
		return p
	}
	tests := []struct {
		name   string
		render func(*bytes.Buffer) error
	}{
		{"table", func(b *bytes.Buffer) error {
			r := likelyResult()
			r.Cluster, r.Region = evil, evil
			r.Paths = []model.Path{tainted()}
			r.Uncertain = []model.Path{tainted()}
			r.Warnings = []string{"L1: " + evil}
			r.Verify.Month, r.Verify.UsageType, r.Verify.Note = evil, evil, evil
			return Table(b, r)
		}},
		{"table with exposure none", func(b *bytes.Buffer) error {
			return Table(b, model.ScanResult{Cluster: evil, Exposure: model.ExposureNone, Warnings: []string{evil}})
		}},
		{"explain", func(b *bytes.Buffer) error {
			return Explain(b, evil+"/api", []model.Path{tainted()}, model.RuleSet{Rules: []model.Rule{{ID: evil, Verified: evil}}})
		}},
		{"rules", func(b *bytes.Buffer) error {
			rs := smallRuleSet()
			rs.Rules[0].ID, rs.Rules[0].Source, rs.Rules[0].Verified = evil, evil, evil
			rs.Rules[1].Match.Scheme = model.StringSet{evil}
			return Rules(b, rs)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			var buf bytes.Buffer

			// Act
			if err := tt.render(&buf); err != nil {
				t.Fatalf("render: %v", err)
			}

			// Assert
			out := buf.String()
			for _, r := range out {
				if r != '\n' && unicode.IsControl(r) {
					t.Fatalf("output carries control character %U:\n%q", r, out)
				}
			}
			if !strings.Contains(out, safe) {
				t.Errorf("output lacks sanitized %q:\n%s", safe, out)
			}
		})
	}
}
