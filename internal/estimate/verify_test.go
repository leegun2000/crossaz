package estimate_test

import (
	"slices"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

var verifyFrom = time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

// verifyBill is 14 closed days with $interzone of InterZone lines per day, plus PublicIP and $0 xAZ lines
// that verify must ignore; the last estimatedDays days are not final (Estimated) yet.
func verifyBill(interzone float64, estimatedDays int) model.Bill {
	b := model.Bill{From: verifyFrom, To: verifyFrom.AddDate(0, 0, 14)}
	for d := range 14 {
		day := verifyFrom.AddDate(0, 0, d)
		estimated := d >= 14-estimatedDays
		line := func(usageType, op string, family model.Family, usd float64) model.BillLine {
			return model.BillLine{Day: day, UsageType: usageType, Operation: op, Family: family, USD: usd, Estimated: estimated}
		}
		b.Lines = append(b.Lines,
			line("APN2-DataTransfer-Regional-Bytes", "InterZone-In", model.FamilyInterZone, interzone/2),
			line("APN2-DataTransfer-Regional-Bytes", "InterZone-Out", model.FamilyInterZone, interzone/2),
			line("APN2-DataTransfer-Regional-Bytes", "PublicIP-Out", model.FamilyPublicIP, 5),
			line("APN2-DataTransfer-xAZ-In-Bytes", "Unknown", model.FamilyXAZFree, 0),
		)
	}
	return b
}

// withNotFinal marks day not final at the result level, as CE does for a day with no in-scope group.
func withNotFinal(b model.Bill, day time.Time) model.Bill {
	out := b
	out.NotFinal = append(slices.Clone(b.NotFinal), day)
	return out
}

func TestVerify(t *testing.T) {
	unknown := model.Bill{From: verifyFrom, To: verifyFrom.AddDate(0, 0, 14), Unknown: true}
	tests := []struct {
		name          string
		est           *float64
		bill          model.Bill
		wantActual    *float64
		wantEstimated int
		wantRatio     *float64
		wantNote      string
	}{
		{"estimate nil cannot compare", nil, verifyBill(2, 0), ptr(60), 0, nil, "cannot compare without L2 (--prometheus-url)"},
		{"actual zero", ptr(133), verifyBill(0, 0), ptr(0), 0, nil, "actual InterZone cost for the period is 0"},
		{"ratio above 1.5 is over-estimate", ptr(120), verifyBill(2, 0), ptr(60), 0, ptr(2), "overestimate: suspect a missing exclusion rule"},
		{"ratio below 0.5 is leak", ptr(6), verifyBill(2, 0), ptr(60), 0, ptr(0.1), "suspect leaks outside the cluster (RDS/MSK/NLB/other workloads)"},
		{"ratio in range", ptr(66), verifyBill(2, 0), ptr(60), 0, ptr(1.1), "exclusion rules look broadly right"},
		{"ratio exactly 1.5 is in range", ptr(90), verifyBill(2, 0), ptr(60), 0, ptr(1.5), "exclusion rules look broadly right"},
		{"ratio exactly 0.5 is in range", ptr(30), verifyBill(2, 0), ptr(60), 0, ptr(0.5), "exclusion rules look broadly right"},
		{"zero estimate has no ratio but suggests leak", ptr(0), verifyBill(2, 0), ptr(60), 0, nil, "suspect leaks outside the cluster (RDS/MSK/NLB/other workloads)"},
		{"estimated days are summed and counted", ptr(60), verifyBill(2, 2), ptr(60), 2, ptr(1), "exclusion rules look broadly right"},
		{"all days estimated still compares", ptr(60), verifyBill(2, 14), ptr(60), 14, ptr(1), "exclusion rules look broadly right"},
		{"estimated day without lines is counted", ptr(60), withNotFinal(verifyBill(2, 1), verifyFrom), ptr(60), 2, ptr(1), "exclusion rules look broadly right"},
		{"unknown bill is unknown, not zero", ptr(60), unknown, nil, 0, nil, "Cost Explorer bill unknown: cannot compare"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := estimate.Verify(tt.est, tt.bill)

			// Assert
			wantDays := 14 // days summed; none for an unknown bill
			if tt.bill.Unknown {
				wantDays = 0
			}
			if got.Window != "2026-09-08..2026-09-21" || got.Days != wantDays || got.EstimatedDays != tt.wantEstimated {
				t.Errorf("Window/Days/EstimatedDays = %q/%d/%d, want 2026-09-08..2026-09-21/%d/%d", got.Window, got.Days, got.EstimatedDays, wantDays, tt.wantEstimated)
			}
			assertFloatPtr(t, "ActualUSD", got.ActualUSD, tt.wantActual, tolerance)
			assertFloatPtr(t, "EstimatedUSD", got.EstimatedUSD, tt.est, tolerance)
			if tt.est != nil && got.EstimatedUSD == tt.est {
				t.Error("EstimatedUSD aliases the input pointer")
			}
			assertFloatPtr(t, "Ratio", got.Ratio, tt.wantRatio, tolerance)
			if got.Note != tt.wantNote {
				t.Errorf("Note = %q, want %q", got.Note, tt.wantNote)
			}
		})
	}
}

func TestVerifyNamesInterZoneUsageTypesOnly(t *testing.T) {
	// Act
	got := estimate.Verify(ptr(60), verifyBill(2, 0))

	// Assert
	if got.UsageType != "APN2-DataTransfer-Regional-Bytes" {
		t.Errorf("UsageType = %q, want only the InterZone lines' usage type", got.UsageType)
	}
}
