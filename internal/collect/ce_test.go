package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/leegun2000/crossaz/internal/model"
)

var ceUpdate = flag.Bool("update", false, "rewrite testdata/golden/ce-*.json from the current output")

var (
	ceTestFrom = time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	ceTestTo   = ceTestFrom.AddDate(0, 0, 14)
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

func ceTestGroup(usageType, operation, usd, gb string) cetypes.Group {
	return cetypes.Group{Keys: []string{usageType, operation}, Metrics: map[string]cetypes.MetricValue{
		"UnblendedCost": {Amount: aws.String(usd), Unit: aws.String("USD")},
		"UsageQuantity": {Amount: aws.String(gb), Unit: aws.String("GB")},
	}}
}

func ceTestPage(next string, groups ...cetypes.Group) *costexplorer.GetCostAndUsageOutput {
	out := &costexplorer.GetCostAndUsageOutput{ResultsByTime: []cetypes.ResultByTime{{
		TimePeriod: &cetypes.DateInterval{Start: aws.String("2026-09-08"), End: aws.String("2026-09-09")},
		Groups:     groups,
	}}}
	if next != "" {
		out.NextPageToken = aws.String(next)
	}
	return out
}

// ceTestAPIError is a modeled AWS error as the SDK surfaces it (smithy.APIError shape).
type ceTestAPIError struct{ code, message string }

func (e ceTestAPIError) Error() string     { return e.code + ": " + e.message }
func (e ceTestAPIError) ErrorCode() string { return e.code }

// ceRecording replays a recorded GetCostAndUsage exchange from testdata/ce: a JSON array of response bodies
// (one per page, in order), or one JSON 1.1 error body {"__type": ..., "Message": ...}.
type ceRecording struct {
	pages []*costexplorer.GetCostAndUsageOutput
	err   error
	calls []*costexplorer.GetCostAndUsageInput
}

func ceLoadRecording(t *testing.T, name string) *ceRecording {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "ce", name+".json"))
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	rec := &ceRecording{}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		if err := json.Unmarshal(data, &rec.pages); err != nil {
			t.Fatalf("parse recording %s: %v", name, err)
		}
		return rec
	}
	var body struct {
		Type    string `json:"__type"`
		Message string
	}
	if err := json.Unmarshal(data, &body); err != nil || body.Type == "" {
		t.Fatalf("parse error recording %s: %v", name, err)
	}
	rec.err = ceTestAPIError{code: body.Type, message: body.Message}
	return rec
}

func (r *ceRecording) GetCostAndUsage(_ context.Context, in *costexplorer.GetCostAndUsageInput, _ ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error) {
	r.calls = append(r.calls, in)
	if r.err != nil {
		return nil, r.err
	}
	i := len(r.calls) - 1
	if i >= len(r.pages) {
		return nil, fmt.Errorf("page %d not recorded", i)
	}
	want := ""
	if i > 0 {
		want = aws.ToString(r.pages[i-1].NextPageToken)
	}
	if got := aws.ToString(in.NextPageToken); got != want {
		return nil, fmt.Errorf("page %d requested with token %q, want %q", i, got, want)
	}
	return r.pages[i], nil
}

func TestDailyBillRecordedGoldens(t *testing.T) {
	tests := []struct {
		recording string
		region    string
		accounts  []string
		wantCalls int
	}{
		{"multi-page", "ap-northeast-2", []string{"111122223333", "444455556666"}, 2},
		{"us-east-1", "us-east-1", []string{"111122223333"}, 1},
		{"unknown-operation", "ap-northeast-2", []string{"111122223333"}, 1},
		{"estimated", "ap-northeast-2", nil, 1},
		{"all-estimated", "ap-northeast-2", []string{"111122223333"}, 1},
		{"empty", "ap-northeast-2", []string{"111122223333"}, 1},
		{"access-denied", "ap-northeast-2", []string{"111122223333"}, 1},
		{"data-unavailable", "ap-northeast-2", []string{"111122223333"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.recording, func(t *testing.T) {
			// Arrange
			rec := ceLoadRecording(t, tt.recording)
			path := filepath.Join("..", "..", "testdata", "golden", "ce-"+tt.recording+".json")

			// Act
			bill, err := NewCostExplorer(rec).DailyBill(t.Context(), tt.region, tt.accounts, ceTestFrom, ceTestTo)

			// Assert
			if err != nil {
				t.Fatalf("DailyBill: %v", err)
			}
			if len(rec.calls) != tt.wantCalls {
				t.Errorf("calls = %d, want %d", len(rec.calls), tt.wantCalls)
			}
			got, err := json.MarshalIndent(bill, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')
			if *ceUpdate {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(got) != string(want) {
				t.Errorf("output differs from %s (run go test ./internal/collect -run DailyBill -update after checking)\n%s", path, got)
			}
		})
	}
}

func TestDailyBillClassifiesLines(t *testing.T) {
	// Arrange
	stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{"": ceTestPage("",
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "InterZone-In", "1.5", "150"),
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "PublicIP-Out", "0.1", "10"),
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "LB-PublicIP-In", "0.2", "20"),
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "LoadBalancing-PublicIP-Out", "0.25", "25"),
		ceTestGroup("APN2-NatGateway-Bytes", "NatGateway", "0.45", "10"),
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "Mystery", "0.3", "30"),
		ceTestGroup("APN2-DataTransfer-Regional-Bytes", "Mystery", "0.4", "40"),
		ceTestGroup("APN2-VpcPeering-Out-Bytes", "VPCPeering", "0.5", "50"),
		ceTestGroup("APN2-DataTransfer-xAZ-In-Bytes", "Unknown", "0", "60"),
		ceTestGroup("APN2-BoxUsage:m5.large", "RunInstances", "300", "72"),
		ceTestGroup("APN2-DataTransfer-Out-Bytes", "InterZone-In", "9", "90"),
		cetypes.Group{Keys: []string{"APN2-DataTransfer-Regional-Bytes"}},
	)}}
	day := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	line := func(op string, family model.Family, usageType string, usd, gb float64) model.BillLine {
		return model.BillLine{Day: day, Account: "111122223333", UsageType: usageType, Operation: op, Family: family, USD: usd, GB: gb}
	}
	const regional = "APN2-DataTransfer-Regional-Bytes"
	want := model.Bill{From: ceTestFrom, To: ceTestTo, Lines: []model.BillLine{
		line("InterZone-In", model.FamilyInterZone, regional, 1.5, 150),
		line("PublicIP-Out", model.FamilyPublicIP, regional, 0.1, 10),
		line("LB-PublicIP-In", model.FamilyLBPublicIP, regional, 0.2, 20),
		line("LoadBalancing-PublicIP-Out", model.FamilyLBPublicIP, regional, 0.25, 25),
		line("NatGateway", model.FamilyNATGWBytes, "APN2-NatGateway-Bytes", 0.45, 10),
		line("Mystery", model.FamilyOther, regional, 0.3, 30),
		line("Mystery", model.FamilyOther, regional, 0.4, 40),
		line("VPCPeering", model.FamilyVPCPeering, "APN2-VpcPeering-Out-Bytes", 0.5, 50),
		line("Unknown", model.FamilyXAZFree, "APN2-DataTransfer-xAZ-In-Bytes", 0, 60),
	}, Warnings: []string{`모르는 operation "Mystery": family other로 분류(총액 포함)`}}

	// Act
	got, err := NewCostExplorer(stub).DailyBill(t.Context(), "ap-northeast-2", []string{"111122223333"}, ceTestFrom, ceTestTo)

	// Assert
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("DailyBill = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestDailyBillRequestShape(t *testing.T) {
	region := &cetypes.Expression{Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionRegion, Values: []string{"eu-west-1"}}}
	tests := []struct {
		name         string
		accounts     []string
		wantFilter   *cetypes.Expression
		wantWarnings []string
	}{
		{
			name:     "accounts add a LINKED_ACCOUNT filter",
			accounts: []string{"111122223333", "444455556666"},
			wantFilter: &cetypes.Expression{And: []cetypes.Expression{*region, {Dimensions: &cetypes.DimensionValues{
				Key: cetypes.DimensionLinkedAccount, Values: []string{"111122223333", "444455556666"},
			}}}},
		},
		{name: "no accounts filter by region only and warn", wantFilter: region, wantWarnings: []string{ceWarnNoAccounts}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{"": ceTestPage("",
				ceTestGroup("EU-DataTransfer-Regional-Bytes", "InterZone-In", "1", "100"))}}

			// Act
			got, err := NewCostExplorer(stub).DailyBill(t.Context(), "eu-west-1", tt.accounts, ceTestFrom, ceTestTo)

			// Assert
			if err != nil {
				t.Fatalf("DailyBill: %v", err)
			}
			in := stub.calls[0]
			if in.Granularity != cetypes.GranularityDaily || !reflect.DeepEqual(in.Metrics, []string{"UnblendedCost", "UsageQuantity"}) {
				t.Errorf("Granularity/Metrics = %q %v", in.Granularity, in.Metrics)
			}
			if aws.ToString(in.TimePeriod.Start) != "2026-09-08" || aws.ToString(in.TimePeriod.End) != "2026-09-22" {
				t.Errorf("TimePeriod = %s..%s", aws.ToString(in.TimePeriod.Start), aws.ToString(in.TimePeriod.End))
			}
			wantGroupBy := []cetypes.GroupDefinition{
				{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("USAGE_TYPE")},
				{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String("OPERATION")},
			}
			if !reflect.DeepEqual(in.GroupBy, wantGroupBy) {
				t.Errorf("GroupBy = %+v", in.GroupBy)
			}
			if !reflect.DeepEqual(in.Filter, tt.wantFilter) {
				t.Errorf("Filter = %+v, want %+v", in.Filter, tt.wantFilter)
			}
			if in.NextPageToken != nil {
				t.Errorf("first request NextPageToken = %q, want nil", aws.ToString(in.NextPageToken))
			}
			if !reflect.DeepEqual(got.Warnings, tt.wantWarnings) {
				t.Errorf("Warnings = %q, want %q", got.Warnings, tt.wantWarnings)
			}
			if len(got.Lines) != 1 || got.Lines[0].Account != "" {
				t.Errorf("Lines = %+v, want one line without Account (several or no accounts filtered)", got.Lines)
			}
		})
	}
}

func TestDailyBillUnknownIsNotZero(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantWarning string
	}{
		{"SDK DataUnavailableException", &cetypes.DataUnavailableException{Message: aws.String("not yet")}, "Cost Explorer 청구 모름(DataUnavailableException)"},
		{"wrapped AccessDenied", fmt.Errorf("operation error: %w", ceTestAPIError{code: "AccessDenied", message: "no"}), "Cost Explorer 청구 모름(AccessDenied)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got, err := NewCostExplorer(&stubCostExplorer{err: tt.err}).DailyBill(t.Context(), "us-east-1", []string{"1"}, ceTestFrom, ceTestTo)

			// Assert
			if err != nil || !got.Unknown || len(got.Lines) != 0 || len(got.Warnings) != 1 || !strings.HasPrefix(got.Warnings[0], tt.wantWarning) {
				t.Errorf("DailyBill = %+v, %v; want Unknown with warning %q", got, err, tt.wantWarning)
			}
		})
	}
}

func TestDailyBillErrors(t *testing.T) {
	tests := []struct {
		name    string
		region  string
		stub    *stubCostExplorer
		wantErr string
	}{
		{name: "other API errors propagate", region: "us-east-1", stub: &stubCostExplorer{err: awsTestErr}, wantErr: "ce:GetCostAndUsage: access denied"},
		{name: "other modeled errors propagate", region: "us-east-1", stub: &stubCostExplorer{err: ceTestAPIError{code: "LimitExceededException", message: "slow down"}}, wantErr: "ce:GetCostAndUsage: LimitExceededException"},
		{name: "bad amount", region: "us-east-1", stub: &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
			"": ceTestPage("", ceTestGroup("DataTransfer-Regional-Bytes", "InterZone-In", "twelve", "1")),
		}}, wantErr: "UnblendedCost amount for DataTransfer-Regional-Bytes/InterZone-In"},
		{name: "missing usage metric", region: "us-east-1", stub: &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
			"": ceTestPage("", cetypes.Group{Keys: []string{"DataTransfer-Regional-Bytes", "InterZone-In"}, Metrics: map[string]cetypes.MetricValue{
				"UnblendedCost": {Amount: aws.String("1")},
			}}),
		}}, wantErr: "UsageQuantity amount"},
		{name: "result without a day", region: "us-east-1", stub: &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
			"": {ResultsByTime: []cetypes.ResultByTime{{Groups: []cetypes.Group{ceTestGroup("DataTransfer-Regional-Bytes", "InterZone-In", "1", "1")}}}},
		}}, wantErr: "ce:GetCostAndUsage: result day"},
		{name: "empty region", region: "", stub: &stubCostExplorer{}, wantErr: "region is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := NewCostExplorer(tt.stub).DailyBill(t.Context(), tt.region, []string{"1"}, ceTestFrom, ceTestTo)

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDailyBillStopsOnRepeatedToken(t *testing.T) {
	// Arrange
	stub := &stubCostExplorer{pages: map[string]*costexplorer.GetCostAndUsageOutput{
		"":     ceTestPage("same", ceTestGroup("DataTransfer-Regional-Bytes", "InterZone-In", "1", "1")),
		"same": ceTestPage("same", ceTestGroup("DataTransfer-Regional-Bytes", "InterZone-Out", "2", "2")),
	}}

	// Act
	got, err := NewCostExplorer(stub).DailyBill(t.Context(), "us-east-1", []string{"1"}, ceTestFrom, ceTestTo)

	// Assert
	if err != nil || len(stub.calls) != 2 || len(got.Lines) != 2 {
		t.Fatalf("got %+v, %v after %d calls; want 2 lines after 2 calls", got, err, len(stub.calls))
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
