package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/model"
)

var billTestFrom = time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)

func billTestReport() model.BillReport {
	day := func(d int) time.Time { return billTestFrom.AddDate(0, 0, d) }
	const regional = "APN2-DataTransfer-Regional-Bytes"
	line := func(d int, usageType, op string, family model.Family, usd, gb float64, estimated bool) model.BillLine {
		return model.BillLine{Day: day(d), UsageType: usageType, Operation: op, Family: family, USD: usd, GB: gb, Estimated: estimated}
	}
	return model.BillReport{
		Region:   "ap-northeast-2",
		Accounts: []string{"111122223333", "444455556666"},
		Metric:   "UnblendedCost",
		Bill: model.Bill{From: billTestFrom, To: billTestFrom.AddDate(0, 0, 14), Lines: []model.BillLine{
			line(0, regional, "InterZone-In", model.FamilyInterZone, 1.25, 125, false),
			line(1, regional, "InterZone-In", model.FamilyInterZone, 1.25, 125, false),
			line(0, regional, "InterZone-Out", model.FamilyInterZone, 2, 200, false),
			line(13, regional, "InterZone-Out", model.FamilyInterZone, 0.5, 50, true),
			line(0, regional, "PublicIP-In", model.FamilyPublicIP, 0.05, 5, false),
			line(0, regional, "LB-PublicIP-Out", model.FamilyLBPublicIP, 0.02, 2, false),
			line(0, "APN2-VpcPeering-In-Bytes", "VPCPeering", model.FamilyVPCPeering, 0.4, 40, false),
			line(0, regional, "Mystery", model.FamilyOther, 0.1, 10, false),
			line(0, "APN2-DataTransfer-xAZ-In-Bytes", "Unknown", model.FamilyXAZFree, 0, 310.5, false),
		}, Warnings: []string{`모르는 operation "Mystery": family other로 분류(총액 포함)`}},
	}
}

func TestBillTableWaterfall(t *testing.T) {
	// Arrange
	var b bytes.Buffer

	// Act
	if err := BillTable(&b, billTestReport()); err != nil {
		t.Fatalf("BillTable: %v", err)
	}

	// Assert
	out := b.String()
	wantLines := []string{
		"Region: ap-northeast-2   Accounts: 111122223333,444455556666   Window: 2026-09-08..2026-09-21 (UTC, 14일)   Metric: UnblendedCost",
		"WATERFALL",
		"FAMILY        SIDE   USD     GB       $/GB",
		"interzone     In     $2.50   250.00   0.0100",
		"interzone     Out    $2.50   250.00   0.0100",
		"publicip      In     $0.05   5.00     0.0100",
		"lb-publicip   Out    $0.02   2.00     0.0100",
		"vpcpeering    In     $0.40   40.00    0.0100",
		"other         ?      $0.10   10.00    0.0100",
		"TOTAL                $5.57   557.00   0.0100",
		"xaz-free      In     $0.00   310.50   0.0000",
		"xaz-free는 $0(PrivateLink/TGW/Client VPN)이라 TOTAL에서 제외",
		"미확정(Estimated) 날: 2026-09-21 — 금액이 바뀔 수 있음(TOTAL 포함)",
		"WARNINGS",
		`  - 모르는 operation "Mystery": family other로 분류(총액 포함)`,
	}
	lines := strings.Split(out, "\n")
	for _, want := range wantLines {
		if !containsLine(lines, want) {
			t.Errorf("missing line %q in:\n%s", want, out)
		}
	}
}

func TestBillTableUnknownIsNeverZero(t *testing.T) {
	// Arrange
	r := billTestReport()
	r.Accounts = nil
	r.Bill = model.Bill{From: billTestFrom, To: billTestFrom.AddDate(0, 0, 1), Unknown: true, Warnings: []string{"Cost Explorer 청구 모름(AccessDeniedException)"}}
	var b bytes.Buffer

	// Act
	if err := BillTable(&b, r); err != nil {
		t.Fatalf("BillTable: %v", err)
	}

	// Assert
	out := b.String()
	for _, want := range []string{
		"Accounts: ? (REGION만 필터)",
		"Window: 2026-09-08..2026-09-08 (UTC, 1일)",
		"청구: 모름 — $0이 아님. 이유는 WARNINGS",
		"  - Cost Explorer 청구 모름(AccessDeniedException)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, absent := range []string{"WATERFALL", "TOTAL", "$0.00"} {
		if strings.Contains(out, absent) {
			t.Errorf("unknown bill prints %q:\n%s", absent, out)
		}
	}
}

func TestBillTableCleansControlCharacters(t *testing.T) {
	// Arrange
	r := billTestReport()
	r.Region = "ap\x1b[31m"
	r.Warnings = []string{"x\ny"}
	var b bytes.Buffer

	// Act
	if err := BillTable(&b, r); err != nil {
		t.Fatalf("BillTable: %v", err)
	}

	// Assert
	if out := b.String(); strings.Contains(out, "\x1b") || !strings.Contains(out, "  - x?y") {
		t.Errorf("control characters leaked:\n%q", out)
	}
}

func TestBillJSONNeverNullLists(t *testing.T) {
	// Arrange
	r := model.BillReport{Region: "us-east-1", Metric: "UnblendedCost", Bill: model.Bill{Unknown: true}}
	var b bytes.Buffer

	// Act
	err := BillJSON(&b, r)

	// Assert
	var got map[string]any
	if err != nil || json.Unmarshal(b.Bytes(), &got) != nil {
		t.Fatalf("BillJSON = %q, %v", b.String(), err)
	}
	if got["accounts"] == nil || got["lines"] == nil || got["unknown"] != true || got["region"] != "us-east-1" {
		t.Errorf("BillJSON = %s", b.String())
	}
}
