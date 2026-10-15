package collect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"

	"github.com/leegun2000/crossaz/internal/model"
)

// BillMetric is the Cost Explorer cost metric in model.BillLine.USD.
const BillMetric = "UnblendedCost"

const (
	ceRegion      = "us-east-1" // Cost Explorer's only endpoint
	ceUsageMetric = "UsageQuantity"
	// ceUsageTypeSuffix matches "DataTransfer-Regional-Bytes" (us-east-1) and "APN2-DataTransfer-Regional-Bytes" etc.
	ceUsageTypeSuffix = "DataTransfer-Regional-Bytes"
	ceDateLayout      = "2006-01-02"

	ceWarnNoAccounts       = "accounts unknown, so the query filters by REGION only (no LINKED_ACCOUNT): charges of other accounts in the region may be included"
	ceWarnUnknownOperation = "unknown operation %q: classified as family other (included in the total)"
	ceWarnUnknownBill      = "Cost Explorer bill unknown (%s), not $0: %v"
	ceWarnEmptyBill        = "Cost Explorer bill unknown (empty result), not $0: check permissions, filters and data delay"
)

// ceUnknownCodes are the API error codes that mean "the bill is unknown", not a failure of the run.
var ceUnknownCodes = []string{"AccessDenied", "AccessDeniedException", "DataUnavailableException"}

// ceFreeSuffixes are the $0 PrivateLink/TGW/Client VPN usage types; ceVPCPeeringSuffixes the AmazonVPC
// peering ones split out of Regional-Bytes on 2025-04-11 (docs/billing-rules.md appendix A).
var (
	ceFreeSuffixes       = []string{"DataTransfer-xAZ-In-Bytes", "DataTransfer-xAZ-Out-Bytes"}
	ceVPCPeeringSuffixes = []string{"VpcPeering-In-Bytes", "VpcPeering-Out-Bytes"}
	ceNATGWSuffix        = "NatGateway-Bytes"
	// ceOperationFamilies maps a Regional-Bytes operation without its -In/-Out side to its family.
	// Neither ELB name is verified against a real bill (billing-rules.md appendix A), so both are mapped.
	ceOperationFamilies = map[string]model.Family{
		"InterZone":              model.FamilyInterZone,
		"PublicIP":               model.FamilyPublicIP,
		"LB-PublicIP":            model.FamilyLBPublicIP,
		"LoadBalancing-PublicIP": model.FamilyLBPublicIP,
	}
)

type ceCollector struct{ client CostExplorerReadOnly }

// NewCostExplorer returns a BillCollector backed by the given read-only client.
func NewCostExplorer(c CostExplorerReadOnly) BillCollector { return ceCollector{client: c} }

// NewCostExplorerFromConfig builds a Cost Explorer client from a copy of cfg pinned to us-east-1.
func NewCostExplorerFromConfig(cfg aws.Config) BillCollector {
	ceCfg := cfg.Copy()
	ceCfg.Region = ceRegion
	return NewCostExplorer(costexplorer.NewFromConfig(ceCfg))
}

func (c ceCollector) DailyBill(ctx context.Context, region string, accounts []string, from, to time.Time) (model.Bill, error) {
	if region == "" {
		return model.Bill{}, errors.New("ce:GetCostAndUsage: region is required")
	}
	bill := model.Bill{From: from, To: to, Lines: []model.BillLine{}}
	if len(accounts) == 0 {
		bill.Warnings = []string{ceWarnNoAccounts}
	}
	results, err := c.results(ctx, ceInput(region, accounts, from, to))
	if code, ok := ceUnknownCode(err); ok {
		return ceUnknown(bill, fmt.Sprintf(ceWarnUnknownBill, code, err)), nil
	}
	if err != nil {
		return model.Bill{}, fmt.Errorf("ce:GetCostAndUsage: %w", err)
	}
	if !slices.ContainsFunc(results, func(r cetypes.ResultByTime) bool { return len(r.Groups) > 0 }) {
		return ceUnknown(bill, ceWarnEmptyBill), nil
	}
	// CE cannot group by LINKED_ACCOUNT as well (two GroupBy at most), so a line names its account only when one was filtered.
	account := ""
	if len(accounts) == 1 {
		account = accounts[0]
	}
	for _, r := range results {
		day, err := ceDay(r)
		if err != nil {
			return model.Bill{}, fmt.Errorf("ce:GetCostAndUsage: %w", err)
		}
		lines, err := ceDayLines(r, day, account)
		if err != nil {
			return model.Bill{}, fmt.Errorf("ce:GetCostAndUsage: %w", err)
		}
		bill.Lines = append(bill.Lines, lines...)
		// CE marks the day, not the group: a not-final day without an in-scope line is still not final.
		if r.Estimated {
			bill.NotFinal = append(bill.NotFinal, day)
		}
	}
	bill.Warnings = append(bill.Warnings, ceUnknownOperationWarnings(bill.Lines)...)
	return bill, nil
}

// results follows NextPageToken until it is empty or repeats; each page is a billed request.
func (c ceCollector) results(ctx context.Context, in *costexplorer.GetCostAndUsageInput) ([]cetypes.ResultByTime, error) {
	var results []cetypes.ResultByTime
	var token *string
	for {
		req := *in
		req.NextPageToken = token
		out, err := c.client.GetCostAndUsage(ctx, &req)
		if err != nil {
			return nil, err
		}
		results = append(results, out.ResultsByTime...)
		next := aws.ToString(out.NextPageToken)
		if next == "" || next == aws.ToString(token) {
			return results, nil
		}
		token = aws.String(next)
	}
}

func ceInput(region string, accounts []string, from, to time.Time) *costexplorer.GetCostAndUsageInput {
	filter := &cetypes.Expression{Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionRegion, Values: []string{region}}}
	if len(accounts) > 0 {
		filter = &cetypes.Expression{And: []cetypes.Expression{*filter, {Dimensions: &cetypes.DimensionValues{
			Key: cetypes.DimensionLinkedAccount, Values: slices.Clone(accounts),
		}}}}
	}
	return &costexplorer.GetCostAndUsageInput{
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(from.UTC().Format(ceDateLayout)),
			End:   aws.String(to.UTC().Format(ceDateLayout)),
		},
		Granularity: cetypes.GranularityDaily,
		Metrics:     []string{BillMetric, ceUsageMetric},
		Filter:      filter,
		GroupBy: []cetypes.GroupDefinition{
			{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String(string(cetypes.DimensionUsageType))},
			{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String(string(cetypes.DimensionOperation))},
		},
	}
}

// ceUnknownCode returns the API error code of err when it means the bill is unknown (see ceUnknownCodes).
func ceUnknownCode(err error) (string, bool) {
	var apiErr interface{ ErrorCode() string } // smithy.APIError, without importing smithy
	if !errors.As(err, &apiErr) || !slices.Contains(ceUnknownCodes, apiErr.ErrorCode()) {
		return "", false
	}
	return apiErr.ErrorCode(), true
}

func ceUnknown(bill model.Bill, warning string) model.Bill {
	out := bill
	out.Unknown = true
	out.Warnings = append(slices.Clone(bill.Warnings), warning)
	return out
}

// ceDay parses the UTC day a result covers.
func ceDay(r cetypes.ResultByTime) (time.Time, error) {
	var start *string
	if r.TimePeriod != nil {
		start = r.TimePeriod.Start
	}
	day, err := time.Parse(ceDateLayout, aws.ToString(start))
	if err != nil {
		return time.Time{}, fmt.Errorf("result day: %w", err)
	}
	return day, nil
}

// ceDayLines keeps the in-scope groups of one day: Regional-Bytes, NAT gateway, VPC peering and the $0 xAZ usage types.
func ceDayLines(r cetypes.ResultByTime, day time.Time, account string) ([]model.BillLine, error) {
	var lines []model.BillLine
	for _, g := range r.Groups {
		if len(g.Keys) < 2 {
			continue
		}
		family, ok := ceFamily(g.Keys[0], g.Keys[1])
		if !ok {
			continue
		}
		usd, errUSD := ceAmount(g, BillMetric)
		gb, errGB := ceAmount(g, ceUsageMetric)
		if err := errors.Join(errUSD, errGB); err != nil {
			return nil, err
		}
		lines = append(lines, model.BillLine{
			Day: day, Account: account, UsageType: g.Keys[0], Operation: g.Keys[1],
			Family: family, USD: usd, GB: gb, Estimated: r.Estimated,
		})
	}
	return lines, nil
}

// ceFamily classifies a group by usage type suffix, then a Regional-Bytes group by operation; ok is false out of scope.
func ceFamily(usageType, operation string) (model.Family, bool) {
	hasSuffix := func(s string) bool { return strings.HasSuffix(usageType, s) }
	switch {
	case slices.ContainsFunc(ceFreeSuffixes, hasSuffix):
		return model.FamilyXAZFree, true
	case slices.ContainsFunc(ceVPCPeeringSuffixes, hasSuffix):
		return model.FamilyVPCPeering, true
	case hasSuffix(ceNATGWSuffix):
		return model.FamilyNATGWBytes, true
	case !hasSuffix(ceUsageTypeSuffix):
		return "", false
	}
	for _, side := range []model.Side{model.SideIn, model.SideOut} {
		if base, ok := strings.CutSuffix(operation, "-"+string(side)); ok && ceOperationFamilies[base] != "" {
			return ceOperationFamilies[base], true
		}
	}
	return model.FamilyOther, true
}

func ceAmount(g cetypes.Group, metric string) (float64, error) {
	v, err := strconv.ParseFloat(aws.ToString(g.Metrics[metric].Amount), 64)
	if err != nil {
		return 0, fmt.Errorf("%s amount for %s: %w", metric, strings.Join(g.Keys, "/"), err)
	}
	return v, nil
}

// ceUnknownOperationWarnings warns once per operation classified as other, in sorted order.
func ceUnknownOperationWarnings(lines []model.BillLine) []string {
	var ops []string
	for _, l := range lines {
		if l.Family == model.FamilyOther {
			ops = append(ops, l.Operation)
		}
	}
	slices.Sort(ops)
	warnings := make([]string, 0, len(ops))
	for _, op := range slices.Compact(ops) {
		warnings = append(warnings, fmt.Sprintf(ceWarnUnknownOperation, op))
	}
	return warnings
}
