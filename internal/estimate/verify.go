package estimate

import "github.com/leegun2000/crossaz/internal/model"

const (
	noteNoEstimate   = "L2(--prometheus-url) 없이는 비교 불가"
	noteNoActual     = "해당 월 inter-AZ 실제 비용 0"
	noteOverEstimate = "추정 과대: 제외 규칙 누락 의심"
	noteLeak         = "클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심"
	noteInRange      = "제외 규칙이 대체로 맞음"
)

// Verify compares the estimated monthly total against the actual Cost Explorer amount.
func Verify(estimatedUSD *float64, cost model.CostData) model.VerifyResult {
	r := model.VerifyResult{Month: cost.Month, UsageType: cost.UsageType, ActualUSD: cost.ActualUSD}
	if estimatedUSD == nil {
		r.Note = noteNoEstimate
		return r
	}
	est := *estimatedUSD
	r.EstimatedUSD = &est
	if cost.ActualUSD <= 0 {
		r.Note = noteNoActual
		return r
	}
	ratio := est / cost.ActualUSD
	if est > 0 {
		r.Ratio = &ratio
	}
	r.Note = ratioNote(ratio)
	return r
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
