package collect

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/leegun2000/crossaz/internal/model"
)

type stubCostExplorer struct {
	pages map[string]*costexplorer.GetCostAndUsageOutput
	err   error
	calls []*costexplorer.GetCostAndUsageInput
}

func (s *stubCostExplorer) GetCostAndUsage(_ context.Context, in *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	return awsTestPage(s.pages, in.NextPageToken)
}

func ceTestGroup(usageType, amount string) cetypes.Group {
	return cetypes.Group{Keys: []string{usageType}, Metrics: map[string]cetypes.MetricValue{
		"UnblendedCost": {Amount: aws.String(amount), Unit: aws.String("USD")},
	}}
}

func ceTestPage(next string, groups ...cetypes.Group) *costexplorer.GetCostAndUsageOutput {
	out := &costexplorer.GetCostAndUsageOutput{ResultsByTime: []cetypes.ResultByTime{{Groups: groups}}}
	if next != "" {
		out.NextPageToken = aws.String(next)
	}
	return out
}

func TestInterAZDataTransferUSDMonthBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		month     time.Time
		wantStart string
		wantEnd   string
		wantMonth string
	}{
		{name: "mid month", month: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), wantStart: "2026-08-01", wantEnd: "2026-09-01", wantMonth: "2026-08"},
		{name: "December rolls into next year", month: time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), wantStart: "2025-12-01", wantEnd: "2026-01-01", wantMonth: "2025-12"},
		{name: "non-UTC input uses its UTC month", month: time.Date(2026, 3, 1, 1, 0, 0, 0, time.FixedZone("KST", 9*3600)), wantStart: "2026-02-01", wantEnd: "2026-03-01", wantMonth: "2026-02"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{"": ceTestPage("")}}

			// Act
			got, err := NewCostExplorer(stub).InterAZDataTransferUSD(t.Context(), "ap-northeast-2", tt.month)

			// Assert
			if err != nil {
				t.Fatalf("InterAZDataTransferUSD: %v", err)
			}
			period := stub.calls[0].TimePeriod
			if aws.ToString(period.Start) != tt.wantStart || aws.ToString(period.End) != tt.wantEnd {
				t.Errorf("TimePeriod = %s..%s, want %s..%s", aws.ToString(period.Start), aws.ToString(period.End), tt.wantStart, tt.wantEnd)
			}
			if got.Month != tt.wantMonth {
				t.Errorf("Month = %q, want %q", got.Month, tt.wantMonth)
			}
		})
	}
}

func TestInterAZDataTransferUSDSumsRegionalUsageAcrossPages(t *testing.T) {
	// Arrange
	stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
		"": ceTestPage("page-2",
			ceTestGroup("APN2-DataTransfer-Regional-Bytes", "12.50"),
			ceTestGroup("APN2-DataTransfer-xAZ-In-Bytes", "99"),
			ceTestGroup("APN2-DataTransfer-xAZ-Out-Bytes", "99"),
			ceTestGroup("APN2-BoxUsage:m5.large", "300"),
		),
		"page-2": ceTestPage("",
			ceTestGroup("DataTransfer-Regional-Bytes", "0.25"),
			ceTestGroup("APN2-DataTransfer-Regional-Bytes", "1.25"),
			cetypes.Group{},
		),
	}}
	want := model.CostData{
		Month:     "2026-08",
		ActualUSD: 14,
		UsageType: "APN2-DataTransfer-Regional-Bytes,DataTransfer-Regional-Bytes",
	}

	// Act
	got, err := NewCostExplorer(stub).InterAZDataTransferUSD(t.Context(), "ap-northeast-2", time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))

	// Assert
	if err != nil {
		t.Fatalf("InterAZDataTransferUSD: %v", err)
	}
	if got.Month != want.Month || got.UsageType != want.UsageType || math.Abs(got.ActualUSD-want.ActualUSD) > 1e-9 {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if len(stub.calls) != 2 || aws.ToString(stub.calls[1].NextPageToken) != "page-2" {
		t.Fatalf("calls = %d (second token %q), want 2 with token page-2", len(stub.calls), aws.ToString(stub.calls[len(stub.calls)-1].NextPageToken))
	}
}

func TestInterAZDataTransferUSDRequestShape(t *testing.T) {
	// Arrange
	stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{"": ceTestPage("")}}

	// Act
	_, err := NewCostExplorer(stub).InterAZDataTransferUSD(t.Context(), "eu-west-1", time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))

	// Assert
	if err != nil {
		t.Fatalf("InterAZDataTransferUSD: %v", err)
	}
	in := stub.calls[0]
	if in.Granularity != cetypes.GranularityMonthly {
		t.Errorf("Granularity = %q", in.Granularity)
	}
	if !reflect.DeepEqual(in.Metrics, []string{"UnblendedCost"}) {
		t.Errorf("Metrics = %v", in.Metrics)
	}
	wantFilter := &cetypes.Expression{Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionRegion, Values: []string{"eu-west-1"}}}
	if !reflect.DeepEqual(in.Filter, wantFilter) {
		t.Errorf("Filter = %+v, want %+v", in.Filter, wantFilter)
	}
	wantGroupBy := []cetypes.GroupDefinition{{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("USAGE_TYPE")}}
	if !reflect.DeepEqual(in.GroupBy, wantGroupBy) {
		t.Errorf("GroupBy = %+v, want %+v", in.GroupBy, wantGroupBy)
	}
	if in.NextPageToken != nil {
		t.Errorf("first request NextPageToken = %q, want nil", aws.ToString(in.NextPageToken))
	}
}

func TestInterAZDataTransferUSDErrors(t *testing.T) {
	month := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		region  string
		stub    *stubCostExplorer
		wantErr string
	}{
		{name: "API error is wrapped", region: "us-east-1", stub: &stubCostExplorer{err: awsTestErr}, wantErr: "ce:GetCostAndUsage: access denied"},
		{name: "bad amount", region: "us-east-1", stub: &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
			"": ceTestPage("", ceTestGroup("DataTransfer-Regional-Bytes", "twelve")),
		}}, wantErr: "UnblendedCost amount for DataTransfer-Regional-Bytes"},
		{name: "missing metric", region: "us-east-1", stub: &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
			"": ceTestPage("", cetypes.Group{Keys: []string{"DataTransfer-Regional-Bytes"}}),
		}}, wantErr: "UnblendedCost amount"},
		{name: "empty region", region: "", stub: &stubCostExplorer{}, wantErr: "region is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := NewCostExplorer(tt.stub).InterAZDataTransferUSD(t.Context(), tt.region, month)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestInterAZDataTransferUSDStopsOnRepeatedToken(t *testing.T) {
	// Arrange
	stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
		"":     ceTestPage("same", ceTestGroup("DataTransfer-Regional-Bytes", "1")),
		"same": ceTestPage("same", ceTestGroup("DataTransfer-Regional-Bytes", "2")),
	}}

	// Act
	got, err := NewCostExplorer(stub).InterAZDataTransferUSD(t.Context(), "us-east-1", time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC))

	// Assert
	if err != nil || len(stub.calls) != 2 || got.ActualUSD != 3 {
		t.Fatalf("got %+v, %v after %d calls; want 3 USD after 2 calls", got, err, len(stub.calls))
	}
}

func TestNewCostExplorerFromConfigForcesUSEast1WithoutMutatingInput(t *testing.T) {
	// Arrange
	cfg := aws.Config{Region: "ap-northeast-2"}

	// Act
	collector, ok := NewCostExplorerFromConfig(cfg).(ceCollector)

	// Assert
	if !ok {
		t.Fatal("NewCostExplorerFromConfig did not return a ceCollector")
	}
	client, ok := collector.client.(*costexplorer.Client)
	if !ok {
		t.Fatalf("client = %T, want *costexplorer.Client", collector.client)
	}
	if region := client.Options().Region; region != "us-east-1" {
		t.Errorf("client region = %q, want us-east-1", region)
	}
	if cfg.Region != "ap-northeast-2" {
		t.Errorf("input config region changed to %q", cfg.Region)
	}
}
