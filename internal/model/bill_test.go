package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTriUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Tri
		wantErr string
	}{
		{"bool true", `true`, TriTrue, ""},
		{"bool false", `false`, TriFalse, ""},
		{"string true", `"true"`, TriTrue, ""},
		{"string false", `"false"`, TriFalse, ""},
		{"string unknown", `"unknown"`, TriUnknown, ""},
		{"other string", `"maybe"`, "", `billable must be true, false or "unknown", got "maybe"`},
		{"empty string", `""`, "", `billable must be true, false or "unknown", got ""`},
		{"number", `1`, "", `billable must be true, false or "unknown", got 1`},
		{"null", `null`, "", `billable must be true, false or "unknown", got null`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			var got Tri
			err := json.Unmarshal([]byte(tt.in), &got)

			// Assert
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestTriMarshalsAsString(t *testing.T) {
	// Arrange
	v := struct {
		B Tri `json:"b"`
	}{TriUnknown}

	// Act
	got, err := json.Marshal(v)

	// Assert
	if err != nil || string(got) != `{"b":"unknown"}` {
		t.Errorf("Marshal = %s, %v", got, err)
	}
}

func TestBillLineSide(t *testing.T) {
	tests := []struct {
		name      string
		usageType string
		operation string
		want      Side
	}{
		{"InterZone-In", "APN2-DataTransfer-Regional-Bytes", "InterZone-In", SideIn},
		{"InterZone-Out", "DataTransfer-Regional-Bytes", "InterZone-Out", SideOut},
		{"LB-PublicIP-Out", "APN2-DataTransfer-Regional-Bytes", "LB-PublicIP-Out", SideOut},
		{"xAZ usage type carries the side", "USE1-DataTransfer-xAZ-In-Bytes", "Unknown", SideIn},
		{"VpcPeering usage type carries the side", "APN2-VpcPeering-Out-Bytes", "VPCPeering", SideOut},
		{"no side anywhere", "APN2-DataTransfer-Regional-Bytes", "Mystery", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := BillLine{UsageType: tt.usageType, Operation: tt.operation}.Side()

			// Assert
			if got != tt.want {
				t.Errorf("Side() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBillNotFinalDays(t *testing.T) {
	// Arrange
	d1 := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	noLines := d1.AddDate(0, 0, -3) // not final at the result level, no in-scope line
	b := Bill{NotFinal: []time.Time{d2, noLines}, Lines: []BillLine{
		{Day: d2, Estimated: true},
		{Day: d1},
		{Day: d2, Estimated: true, Operation: "InterZone-Out"},
		{Day: d1.AddDate(0, 0, -1), Estimated: true},
	}}

	// Act
	got := b.NotFinalDays()

	// Assert
	want := []time.Time{noLines, d1.AddDate(0, 0, -1), d2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NotFinalDays() = %v, want %v", got, want)
	}
}

func TestBillDays(t *testing.T) {
	// Arrange
	from := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

	// Act
	got := Bill{From: from, To: from.AddDate(0, 0, 14)}.Days()

	// Assert
	if got != 14 {
		t.Errorf("Days() = %d, want 14", got)
	}
}
