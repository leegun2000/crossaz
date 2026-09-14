package model

import (
	"encoding/json"
	"math"
	"testing"
)

func TestCrossAZFraction(t *testing.T) {
	even3 := AZDistribution{"a": 8, "b": 8, "c": 8}
	tests := []struct {
		name    string
		callers AZDistribution
		dests   AZDistribution
		want    float64
	}{
		{"three even zones", even3, AZDistribution{"a": 2, "b": 2, "c": 2}, 2.0 / 3},
		{"all endpoints in one zone still crosses for other callers", even3, AZDistribution{"a": 5}, 2.0 / 3},
		{"single zone cluster and endpoints", AZDistribution{"a": 3}, AZDistribution{"a": 2}, 0},
		{"destination outside caller zones", AZDistribution{"a": 1}, AZDistribution{"b": 1}, 1},
		{"skewed endpoints", AZDistribution{"a": 1, "b": 1}, AZDistribution{"a": 3, "b": 1}, 0.5},
		{"no callers", AZDistribution{}, AZDistribution{"a": 1}, 0},
		{"no destinations", even3, AZDistribution{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CrossAZFraction(tt.callers, tt.dests); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("CrossAZFraction = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHintedCrossAZFraction(t *testing.T) {
	callers := AZDistribution{"a": 1, "b": 1, "c": 1}
	dests := AZDistribution{"a": 1, "b": 1, "c": 1}
	tests := []struct {
		name      string
		dests     AZDistribution
		hintedFor map[string]AZDistribution
		want      float64
	}{
		{"self hints in every zone", dests, map[string]AZDistribution{"a": {"a": 1}, "b": {"b": 1}, "c": {"c": 1}}, 0},
		{"zone without hinted endpoint falls back cluster-wide",
			AZDistribution{"a": 1, "b": 1}, map[string]AZDistribution{"a": {"a": 1}, "b": {"b": 1}}, 1.0 / 3},
		{"auto mode hints another zone", dests, map[string]AZDistribution{"a": {"a": 1}, "b": {"b": 1}, "c": {"b": 1}}, 1.0 / 3},
		{"half of zone c pool is foreign", dests, map[string]AZDistribution{"a": {"a": 1}, "b": {"b": 1}, "c": {"b": 1, "c": 1}}, 1.0 / 6},
		{"no hints equals CrossAZFraction", dests, nil, 2.0 / 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HintedCrossAZFraction(callers, tt.dests, tt.hintedFor); math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("HintedCrossAZFraction = %v, want %v", got, tt.want)
			}
		})
	}
	if got := HintedCrossAZFraction(AZDistribution{}, dests, nil); got != 0 {
		t.Errorf("no callers = %v, want 0", got)
	}
}

func TestAZDistributionIsEven(t *testing.T) {
	tests := []struct {
		name string
		d    AZDistribution
		want bool
	}{
		{"equal", AZDistribution{"a": 5, "b": 5, "c": 5}, true},
		{"within 20 percent", AZDistribution{"a": 10, "b": 8}, true},
		{"beyond 20 percent", AZDistribution{"a": 10, "b": 7}, false},
		{"single zone", AZDistribution{"a": 3}, true},
		{"empty", AZDistribution{}, false},
		{"zero counts ignored", AZDistribution{"a": 4, "b": 0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.IsEven(); got != tt.want {
				t.Errorf("IsEven = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMinLevel(t *testing.T) {
	tests := []struct {
		name   string
		levels []Level
		want   Level
	}{
		{"none is high", nil, High},
		{"lowest wins", []Level{High, Medium, High}, Medium},
		{"low", []Level{Medium, Low}, Low},
		{"unknown counts as low", []Level{High, ""}, Low},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MinLevel(tt.levels...); got != tt.want {
				t.Errorf("MinLevel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPathKey(t *testing.T) {
	svc := Path{Dest: Target{Kind: TargetService, Ref: "payments/api"}}
	rds := Path{Dest: Target{Kind: TargetRDS, Ref: "orders-db"}}
	if svc.Key() != "payments/api" || rds.Key() != "rds/orders-db" {
		t.Errorf("Key() = %q, %q", svc.Key(), rds.Key())
	}
}

func TestStringSetUnmarshal(t *testing.T) {
	var m Match
	if err := json.Unmarshal([]byte(`{"route":"alb","target_kind":["rds","msk"]}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !m.Route.Has("alb") || !m.TargetKind.Has("msk") || m.TargetKind.Has("alb") {
		t.Errorf("unexpected match %+v", m)
	}
	if err := json.Unmarshal([]byte(`{"route":3}`), &m); err == nil {
		t.Error("expected error for non-string route")
	}
}
