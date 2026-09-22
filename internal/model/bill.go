package model

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Tri is a three-valued verdict. rules.yaml writes billable as a YAML bool or "unknown"; JSON output is a string.
type Tri string

// Tri values. Unknown is neither $0 nor billed: it gets its own "미확정" line, never the total.
const (
	TriTrue    Tri = "true"
	TriFalse   Tri = "false"
	TriUnknown Tri = "unknown"
)

// UnmarshalJSON accepts true, false, "true", "false" and "unknown".
func (t *Tri) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "true", `"true"`:
		*t = TriTrue
	case "false", `"false"`:
		*t = TriFalse
	case `"unknown"`:
		*t = TriUnknown
	default:
		return fmt.Errorf(`billable must be true, false or "unknown", got %s`, b)
	}
	return nil
}

// Side is the direction a data transfer line meters: "In" or "Out".
type Side string

// Side values.
const (
	SideIn  Side = "In"
	SideOut Side = "Out"
)

// Family groups Cost Explorer data transfer lines by what they bill (docs/billing-rules.md appendix A).
type Family string

// Family values. XAZFree is metered at $0 and never part of a billed total.
const (
	FamilyInterZone  Family = "interzone"   // InterZone-In/Out: EC2/ENI traffic between AZs
	FamilyPublicIP   Family = "publicip"    // PublicIP-In/Out: public or Elastic IPv4 in the region
	FamilyLBPublicIP Family = "lb-publicip" // LB-PublicIP-In/Out or LoadBalancing-PublicIP-In/Out (unverified)
	FamilyVPCPeering Family = "vpcpeering"  // <prefix>-VpcPeering-In/Out-Bytes, a separate usage type since 2025-04-11
	FamilyNATGWBytes Family = "natgw-bytes" // <prefix>-NatGateway-Bytes: NAT gateway processing, not cross-AZ
	FamilyXAZFree    Family = "xaz-free"    // <prefix>-DataTransfer-xAZ-In/Out-Bytes: PrivateLink, TGW, Client VPN ($0)
	FamilyOther      Family = "other"       // a Regional-Bytes operation crossaz does not know
)

// BillLine is one Cost Explorer group: a closed UTC day x usage type x operation.
type BillLine struct {
	Day       time.Time `json:"day"`               // UTC, closed day
	Account   string    `json:"account,omitempty"` // "" when the query covered several accounts (CE cannot group by it too)
	UsageType string    `json:"usageType"`
	Operation string    `json:"operation"`
	Family    Family    `json:"family"`
	USD       float64   `json:"usd"`                 // UnblendedCost
	GB        float64   `json:"gb"`                  // UsageQuantity
	Estimated bool      `json:"estimated,omitempty"` // CE ResultByTime.Estimated: the day is not final yet
}

// Side reads the direction from the operation ("InterZone-In"), else from the usage type ("...-xAZ-Out-Bytes");
// "" when neither carries one.
func (l BillLine) Side() Side {
	for _, side := range []Side{SideIn, SideOut} {
		if strings.HasSuffix(l.Operation, "-"+string(side)) || strings.HasSuffix(l.UsageType, "-"+string(side)+"-Bytes") {
			return side
		}
	}
	return ""
}

// Bill is the daily Cost Explorer bill for [From, To). Unknown is "모름", never $0:
// AccessDenied, DataUnavailable or an empty result.
type Bill struct {
	From     time.Time   `json:"from"` // first UTC day
	To       time.Time   `json:"to"`   // exclusive end (UTC midnight)
	Lines    []BillLine  `json:"lines"`
	NotFinal []time.Time `json:"notFinal,omitempty"` // CE Estimated days, also those with no in-scope line
	Unknown  bool        `json:"unknown,omitempty"`
	Warnings []string    `json:"warnings,omitempty"`
}

// Days is the number of UTC days in the window.
func (b Bill) Days() int { return int(b.To.Sub(b.From) / (24 * time.Hour)) }

// NotFinalDays returns the sorted distinct days in NotFinal or with an Estimated line.
func (b Bill) NotFinalDays() []time.Time {
	var days []time.Time
	for _, d := range b.NotFinal {
		if !slices.ContainsFunc(days, d.Equal) {
			days = append(days, d)
		}
	}
	for _, l := range b.Lines {
		if l.Estimated && !slices.ContainsFunc(days, l.Day.Equal) {
			days = append(days, l.Day)
		}
	}
	slices.SortFunc(days, func(a, b time.Time) int { return a.Compare(b) })
	return days
}

// BillReport is the `crossaz bill` result: the bill and the query that produced it.
type BillReport struct {
	Region   string   `json:"region"`
	Accounts []string `json:"accounts"` // LINKED_ACCOUNT filter; empty = REGION only
	Metric   string   `json:"metric"`   // Cost Explorer cost metric in BillLine.USD
	Bill
}
