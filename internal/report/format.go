// Package report renders scan results as text tables, JSON, rule explanations and the rule list.
package report

import (
	"bytes"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	unknownMark   = "?"
	notBilledMark = "—"

	byteBase = 1000
	// Values below this (in the chosen unit) get one decimal: "4.2 TB", "$4.20".
	decimalBelow = 10
	// AZ SPREAD only says "even" when the destination spans at least this many zones.
	minEvenZones = 2
	tablePadding = 3
)

var byteUnits = []string{"B", "KB", "MB", "GB", "TB", "PB"}

// Bytes formats a byte count with decimal units: "4.2 TB", "900 GB", "12 MB", "0 B".
func Bytes(b float64) string {
	if b == 0 {
		return "0 B"
	}
	sign := ""
	if b < 0 {
		sign = "-"
	}
	v := math.Abs(b)
	unit := 0
	for unit < len(byteUnits)-1 && math.Round(v) >= byteBase {
		v /= byteBase
		unit++
	}
	digits := 0
	if unit > 0 && math.Round(v*10)/10 < decimalBelow {
		digits = 1
	}
	return sign + strconv.FormatFloat(v, 'f', digits, 64) + " " + byteUnits[unit]
}

// USD formats a monthly cost: "~$56", "~$0.40", "?" (billable but unknown), "—" (not billable).
func USD(v *float64, billable bool) string {
	if !billable {
		return notBilledMark
	}
	if v == nil {
		return unknownMark
	}
	return "~" + dollars(*v)
}

func dollars(v float64) string {
	if math.Round(math.Abs(v)*100)/100 >= decimalBelow {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func placeholder(billable bool) string {
	if billable {
		return unknownMark
	}
	return notBilledMark
}

func confLabel(l model.Level) string {
	switch l {
	case model.High:
		return "high"
	case model.Medium:
		return "med"
	default:
		return "low"
	}
}

func routeLabel(p model.Path) string {
	route := string(p.Route)
	if p.Route == model.RouteNLBXZ {
		route = "nlb-xz"
	}
	// Target type decides whether a kube-proxy hop exists, so show it for ALB/NLB (CLB is always instance).
	switch {
	case p.Route == model.RouteCLB || p.Route == model.RouteClusterIP || p.Route == model.RouteHeadless || p.Route == model.RouteExternal:
		return route
	case p.Facts.TargetType == "ip":
		return route + " (ip)"
	case p.Facts.TargetType == "instance":
		return route + " (inst)"
	default:
		return route
	}
}

func recvLabel(p model.Path) string {
	if p.BytesPerMo == nil {
		return placeholder(p.Billable)
	}
	return Bytes(*p.BytesPerMo)
}

func azSpread(dest model.AZDistribution, callerZones int) string {
	n := dest.ZoneCount()
	s := fmt.Sprintf("%d/%d", n, callerZones)
	if n >= minEvenZones && dest.IsEven() {
		s += " even"
	}
	return s
}

// zoneList renders a distribution as "zone=count" pairs sorted by zone.
func zoneList(d model.AZDistribution) string {
	parts := make([]string, 0, len(d))
	for _, z := range slices.Sorted(maps.Keys(d)) {
		if d[z] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", z, d[z]))
		}
	}
	return orDash(strings.Join(parts, " "))
}

func orDash(s string) string {
	if s == "" {
		return notBilledMark
	}
	return s
}

// clean replaces control characters (ESC, BEL, CR, newline, tab, C1) in cluster-, AWS- or rules-sourced text,
// so a crafted name or annotation cannot drive the terminal or forge report lines.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

func writeTable(b *bytes.Buffer, rows [][]string) error {
	tw := tabwriter.NewWriter(b, 0, 0, tablePadding, ' ', 0)
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = clean(c)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	return nil
}

func flushTo(w io.Writer, b *bytes.Buffer) error {
	if _, err := w.Write(b.Bytes()); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}
