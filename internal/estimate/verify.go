package estimate

import (
	"slices"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	noteNoEstimate   = "cannot compare without L2 (--prometheus-url)"
	noteNoActual     = "actual InterZone cost for the period is 0"
	noteBillUnknown  = "Cost Explorer bill unknown: cannot compare"
	noteOverEstimate = "overestimate: suspect a missing exclusion rule"
	noteLeak         = "suspect leaks outside the cluster (RDS/MSK/NLB/other workloads)"
	noteInRange      = "exclusion rules look broadly right"

	daySeconds   = 24 * 3600
	windowLayout = "2006-01-02"
)

// Verify compares the estimated monthly total against the bill's InterZone lines of every day, scaled to a
// 30-day month. CE marks every day of an open month Estimated, so those days count too and are reported in
// EstimatedDays (ActualUSD is provisional). Other families (PublicIP, LB, peering, $0 xAZ) are not cross-AZ
// traffic of the paths.
func Verify(estimatedUSD *float64, bill model.Bill) model.VerifyResult {
	r := model.VerifyResult{Window: bill.From.Format(windowLayout) + ".." + bill.To.AddDate(0, 0, -1).Format(windowLayout)}
	if estimatedUSD != nil {
		est := *estimatedUSD
		r.EstimatedUSD = &est
	}
	if bill.Unknown || bill.Days() <= 0 {
		r.Note = noteBillUnknown
		return r
	}
	r.Days, r.EstimatedDays = bill.Days(), len(bill.NotFinalDays())
	sum, usageTypes := interZone(bill.Lines)
	actual := sum / float64(r.Days) * MonthSeconds / daySeconds
	r.ActualUSD, r.UsageType = &actual, strings.Join(usageTypes, ",")
	switch {
	case r.EstimatedUSD == nil:
		r.Note = noteNoEstimate
	case actual <= 0:
		r.Note = noteNoActual
	default:
		ratio := *r.EstimatedUSD / actual
		if ratio > 0 {
			r.Ratio = &ratio
		}
		r.Note = ratioNote(ratio)
	}
	return r
}

// interZone sums the InterZone lines and returns their sorted distinct usage types.
func interZone(lines []model.BillLine) (float64, []string) {
	sum := 0.0
	var usageTypes []string
	for _, l := range lines {
		if l.Family != model.FamilyInterZone {
			continue
		}
		sum += l.USD
		usageTypes = append(usageTypes, l.UsageType)
	}
	slices.Sort(usageTypes)
	return sum, slices.Compact(usageTypes)
}

func ratioNote(ratio float64) string {
	switch {
	case ratio > model.VerifyRatioHigh:
		return noteOverEstimate
	case ratio < model.VerifyRatioLow:
		return noteLeak
	default:
		return noteInRange
	}
}
