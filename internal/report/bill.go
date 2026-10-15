package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const billDayLayout = "2006-01-02"

// billFamilies is the WATERFALL row order of the billed families; xaz-free comes after TOTAL.
var (
	billFamilies = []model.Family{model.FamilyInterZone, model.FamilyPublicIP, model.FamilyLBPublicIP, model.FamilyVPCPeering, model.FamilyNATGWBytes, model.FamilyOther}
	billSides    = []model.Side{model.SideIn, model.SideOut, ""}
)

type billKey struct {
	family model.Family
	side   model.Side
}

type billSum struct{ usd, gb float64 }

// BillTable writes the `crossaz bill` WATERFALL: USD, GB and $/GB per family and side, the billed TOTAL
// (xaz-free excluded), the not-final days and the warnings. An unknown bill prints no amount at all.
func BillTable(w io.Writer, r model.BillReport) error {
	var b bytes.Buffer
	accounts := strings.Join(r.Accounts, ",")
	if accounts == "" {
		accounts = unknownMark + " (filtered by REGION only)"
	}
	fmt.Fprintf(&b, "Region: %s   Accounts: %s   Window: %s..%s (UTC, %s)   Metric: %s\n",
		clean(r.Region), clean(accounts), r.From.Format(billDayLayout), r.To.AddDate(0, 0, -1).Format(billDayLayout), count(r.Days(), "day"), clean(r.Metric))
	if r.Unknown {
		fmt.Fprintln(&b, "\nBill: unknown, not $0. See WARNINGS for why")
	} else if err := writeWaterfall(&b, r.Lines); err != nil {
		return err
	}
	if days := r.NotFinalDays(); len(days) > 0 && !r.Unknown {
		labels := make([]string, len(days))
		for i, d := range days {
			labels[i] = d.Format(billDayLayout)
		}
		fmt.Fprintf(&b, "Not final (Estimated) days: %s; amounts may still change (included in TOTAL)\n", strings.Join(labels, ", "))
	}
	writeWarnings(&b, r.Warnings)
	return flushTo(w, &b)
}

func writeWaterfall(b *bytes.Buffer, lines []model.BillLine) error {
	sums := map[billKey]billSum{}
	for _, l := range lines {
		k := billKey{l.Family, l.Side()}
		sums[k] = billSum{sums[k].usd + l.USD, sums[k].gb + l.GB}
	}
	rows := [][]string{{"FAMILY", "SIDE", "USD", "GB", "$/GB"}}
	var total billSum
	for _, f := range billFamilies {
		for _, side := range billSides {
			if s, ok := sums[billKey{f, side}]; ok {
				rows = append(rows, billRow(string(f), side, s))
				total = billSum{total.usd + s.usd, total.gb + s.gb}
			}
		}
	}
	rows = append(rows, []string{"TOTAL", "", billUSD(total.usd), billGB(total.gb), billRate(total)})
	for _, side := range billSides {
		if s, ok := sums[billKey{model.FamilyXAZFree, side}]; ok {
			rows = append(rows, billRow(string(model.FamilyXAZFree), side, s))
		}
	}
	fmt.Fprint(b, "\nWATERFALL\n")
	if err := writeTable(b, rows); err != nil {
		return err
	}
	fmt.Fprintln(b, "xaz-free is $0 (PrivateLink/TGW/Client VPN) and left out of TOTAL")
	return nil
}

func billRow(family string, side model.Side, s billSum) []string {
	label := string(side)
	if side == "" {
		label = unknownMark
	}
	return []string{family, label, billUSD(s.usd), billGB(s.gb), billRate(s)}
}

// billUSD keeps cents: the waterfall is compared with the Cost Explorer console to the cent.
func billUSD(v float64) string { return fmt.Sprintf("$%.2f", v) }

func billGB(v float64) string { return fmt.Sprintf("%.2f", v) }

func billRate(s billSum) string {
	if s.gb == 0 {
		return unknownMark
	}
	return fmt.Sprintf("%.4f", s.usd/s.gb)
}

// BillJSON writes the bill report as 2-space indented JSON; nil lists are emitted as [].
func BillJSON(w io.Writer, r model.BillReport) error {
	out := r
	out.Accounts = orEmpty(r.Accounts)
	out.Lines = orEmpty(r.Lines)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode bill: %w", err)
	}
	return nil
}
