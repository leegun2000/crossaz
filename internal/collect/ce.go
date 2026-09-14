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

const (
	ceRegion = "us-east-1" // Cost Explorer's only endpoint
	ceMetric = "UnblendedCost"
	// ceUsageTypeSuffix matches "DataTransfer-Regional-Bytes" (us-east-1) and "APN2-DataTransfer-Regional-Bytes" etc.,
	// but not the $0 "-xAZ-In/Out-Bytes" PrivateLink/TGW usage types.
	ceUsageTypeSuffix = "DataTransfer-Regional-Bytes"
	ceDateLayout      = "2006-01-02"
	ceMonthLayout     = "2006-01"
)

type ceCollector struct{ client CostExplorerReadOnly }

// NewCostExplorer returns a CostCollector backed by the given read-only client.
func NewCostExplorer(c CostExplorerReadOnly) CostCollector { return ceCollector{client: c} }

// NewCostExplorerFromConfig builds a Cost Explorer client from a copy of cfg pinned to us-east-1.
func NewCostExplorerFromConfig(cfg aws.Config) CostCollector {
	ceCfg := cfg.Copy()
	ceCfg.Region = ceRegion
	return NewCostExplorer(costexplorer.NewFromConfig(ceCfg))
}

func (c ceCollector) InterAZDataTransferUSD(ctx context.Context, region string, month time.Time) (model.CostData, error) {
	if region == "" {
		return model.CostData{}, errors.New("ce:GetCostAndUsage: region is required")
	}
	start, end := ceMonthBounds(month)
	total := 0.0
	var usageTypes []string
	var token *string
	for {
		out, err := c.client.GetCostAndUsage(ctx, ceInput(region, start, end, token))
		if err != nil {
			return model.CostData{}, fmt.Errorf("ce:GetCostAndUsage: %w", err)
		}
		sum, keys, err := ceSumRegional(out.ResultsByTime)
		if err != nil {
			return model.CostData{}, fmt.Errorf("ce:GetCostAndUsage: %w", err)
		}
		total += sum
		usageTypes = append(usageTypes, keys...)
		next := aws.ToString(out.NextPageToken)
		if next == "" || next == aws.ToString(token) {
			break
		}
		token = aws.String(next)
	}
	slices.Sort(usageTypes)
	return model.CostData{
		Month:     start.Format(ceMonthLayout),
		ActualUSD: total,
		UsageType: strings.Join(slices.Compact(usageTypes), ","),
	}, nil
}

// ceMonthBounds returns the first day of month's calendar month (UTC) and the first day of the next one.
func ceMonthBounds(month time.Time) (time.Time, time.Time) {
	u := month.UTC()
	start := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

func ceInput(region string, start, end time.Time, token *string) *costexplorer.GetCostAndUsageInput {
	return &costexplorer.GetCostAndUsageInput{
		TimePeriod: &cetypes.DateInterval{
			Start: aws.String(start.Format(ceDateLayout)),
			End:   aws.String(end.Format(ceDateLayout)),
		},
		Granularity: cetypes.GranularityMonthly,
		Metrics:     []string{ceMetric},
		Filter: &cetypes.Expression{
			Dimensions: &cetypes.DimensionValues{Key: cetypes.DimensionRegion, Values: []string{region}},
		},
		GroupBy: []cetypes.GroupDefinition{
			{Type: cetypes.GroupDefinitionTypeDimension, Key: aws.String(string(cetypes.DimensionUsageType))},
		},
		NextPageToken: token,
	}
}

// ceSumRegional sums the unblended cost of inter-AZ usage-type groups and returns their keys.
func ceSumRegional(results []cetypes.ResultByTime) (float64, []string, error) {
	sum := 0.0
	var keys []string
	for _, r := range results {
		for _, g := range r.Groups {
			if len(g.Keys) == 0 || !strings.HasSuffix(g.Keys[0], ceUsageTypeSuffix) {
				continue
			}
			amount, err := strconv.ParseFloat(aws.ToString(g.Metrics[ceMetric].Amount), 64)
			if err != nil {
				return 0, nil, fmt.Errorf("%s amount for %s: %w", ceMetric, g.Keys[0], err)
			}
			sum += amount
			keys = append(keys, g.Keys[0])
		}
	}
	return sum, keys, nil
}
