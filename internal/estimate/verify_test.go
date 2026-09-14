package estimate_test

import (
	"testing"

	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

func TestVerify(t *testing.T) {
	tests := []struct {
		name      string
		est       *float64
		actual    float64
		wantRatio *float64
		wantNote  string
	}{
		{"estimate nil cannot compare", nil, 118, nil, "L2(--prometheus-url) 없이는 비교 불가"},
		{"actual zero", ptr(133), 0, nil, "해당 월 inter-AZ 실제 비용 0"},
		{"ratio above 1.5 is over-estimate", ptr(200), 100, ptr(2), "추정 과대: 제외 규칙 누락 의심"},
		{"ratio below 0.5 is leak", ptr(10), 100, ptr(0.1), "클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심"},
		{"ratio in range", ptr(133), 118, ptr(133.0 / 118), "제외 규칙이 대체로 맞음"},
		{"ratio exactly 1.5 is in range", ptr(150), 100, ptr(1.5), "제외 규칙이 대체로 맞음"},
		{"ratio exactly 0.5 is in range", ptr(50), 100, ptr(0.5), "제외 규칙이 대체로 맞음"},
		{"zero estimate has no ratio but suggests leak", ptr(0), 100, nil, "클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			cost := model.CostData{Month: "2026-08", ActualUSD: tt.actual, UsageType: "APN2-DataTransfer-Regional-Bytes"}

			// Act
			got := estimate.Verify(tt.est, cost)

			// Assert
			if got.Month != "2026-08" || got.ActualUSD != tt.actual {
				t.Errorf("Month/ActualUSD = %q/%v, want 2026-08/%v", got.Month, got.ActualUSD, tt.actual)
			}
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
