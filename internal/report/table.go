package report

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

// Table writes the human-readable scan report (SPEC §9 layout).
func Table(w io.Writer, r model.ScanResult) error {
	var b bytes.Buffer
	if r.Exposure == model.ExposureNone {
		writeNoExposure(&b, r)
		return flushTo(w, &b)
	}
	fmt.Fprintf(&b, "Cluster: %s   Region: %s   AZs: %d   Nodes: %d   Layers: %s\n",
		clean(r.Cluster), clean(r.Region), len(r.Zones), r.NodeCount, clean(strings.Join(r.Layers, "+")))
	fmt.Fprintf(&b, "Exposure: %s\n", strings.ToUpper(r.Exposure))
	if err := writeSections(&b, r); err != nil {
		return err
	}
	writeTotal(&b, r)
	writeVerify(&b, r.Verify)
	writeWarnings(&b, r.Warnings)
	return flushTo(w, &b)
}

func writeNoExposure(b *bytes.Buffer, r model.ScanResult) {
	fmt.Fprintf(b, "Cluster: %s   AZs: %d   Nodes: %d\n", clean(r.Cluster), len(r.Zones), r.NodeCount)
	fmt.Fprintln(b, "Exposure: NONE — 모든 노드가 단일 AZ에 있고 과금 경로가 없습니다.")
	writeWarnings(b, r.Warnings)
}

func writeSections(b *bytes.Buffer, r model.ScanResult) error {
	zones := len(r.Zones)
	if len(r.Paths) > 0 || r.Hidden > 0 {
		fmt.Fprint(b, "\nBILLABLE\n")
		if err := writePathTable(b, r.Paths, zones, "FIX", fixKind); err != nil {
			return err
		}
		if r.Hidden > 0 {
			fmt.Fprintf(b, "(%d paths below $%g/mo hidden — use --threshold 0)\n", r.Hidden, r.Threshold)
		}
	}
	others := []struct {
		title string
		paths []model.Path
	}{
		{"NOT BILLABLE", r.Excluded},
		{"UNCERTAIN", r.Uncertain},
	}
	for _, s := range others {
		if len(s.paths) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n%s\n", s.title)
		if err := writePathTable(b, s.paths, zones, "NOTE", ruleNote); err != nil {
			return err
		}
	}
	return nil
}

func writePathTable(b *bytes.Buffer, paths []model.Path, zones int, lastTitle string, last func(model.Path) string) error {
	if len(paths) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(paths)+1)
	rows = append(rows, []string{"SERVICE", "ROUTE", "RECV/mo", "AZ SPREAD", "EST $/mo", "CONF", lastTitle})
	for _, p := range paths {
		rows = append(rows, []string{
			p.Key(), routeLabel(p), recvLabel(p), azSpread(p.Dest.AZDist, zones),
			USD(p.CostPerMo, p.Billable != model.TriFalse), confLabel(p.Confidence), last(p),
		})
	}
	return writeTable(b, rows)
}

func fixKind(p model.Path) string {
	if p.Fix == nil {
		return notBilledMark
	}
	return orDash(p.Fix.Kind)
}

func ruleNote(p model.Path) string {
	return orDash(p.RuleID)
}

func writeTotal(b *bytes.Buffer, r model.ScanResult) {
	total := "? (L2 없음 — --prometheus-url)"
	if r.TotalUSD != nil {
		total = USD(r.TotalUSD, true) + "/mo"
	}
	fmt.Fprintf(b, "\nEstimated total: %s\n", total)
	writeUnconfirmed(b, r)
	if len(r.Paths) > 0 {
		fmt.Fprintf(b, "Run `crossaz explain %s` for rule details.\n", clean(r.Paths[0].Key()))
	}
}

// writeUnconfirmed prints the unknown-billable paths as a "$0–max" range next to, never inside, the total.
func writeUnconfirmed(b *bytes.Buffer, r model.ScanResult) {
	n := 0
	for _, p := range r.Uncertain {
		if p.Billable == model.TriUnknown {
			n++
		}
	}
	if n == 0 {
		return
	}
	upper := unknownMark
	if r.UnconfirmedMaxUSD != nil {
		upper = dollars(*r.UnconfirmedMaxUSD) + "/mo"
	}
	fmt.Fprintf(b, "미확정: $0–%s (billable 미확정 경로 %d개, 총액 제외)\n", upper, n)
}

func writeVerify(b *bytes.Buffer, v *model.VerifyResult) {
	if v == nil {
		return
	}
	estimated := unknownMark
	if v.EstimatedUSD != nil {
		estimated = USD(v.EstimatedUSD, true) + "/mo"
	}
	ratio := unknownMark
	if v.Ratio != nil {
		ratio = fmt.Sprintf("%.2f", *v.Ratio)
	}
	fmt.Fprint(b, "\nVERIFY\n")
	fmt.Fprintf(b, "  Estimated:  %s\n", estimated)
	usage := v.UsageType
	if usage == "" {
		usage = "DataTransfer-Regional-Bytes"
	}
	actual := unknownMark
	if v.ActualUSD != nil {
		actual = dollars(*v.ActualUSD) + "/mo"
	}
	provisional := ""
	if v.EstimatedDays > 0 {
		provisional = fmt.Sprintf(", 미확정 %d일 포함(잠정)", v.EstimatedDays)
	}
	fmt.Fprintf(b, "  Actual:     %s   (Cost Explorer %s InterZone, %s %d일 기준%s — 계정·리전 전체)\n",
		actual, clean(usage), clean(v.Window), v.Days, provisional)
	fmt.Fprintf(b, "  Ratio:      %s   %s\n\n", ratio, clean(v.Note))
	fmt.Fprintf(b, "  Ratio > %g → 제외 규칙 누락 의심\n", model.VerifyRatioHigh)
	fmt.Fprintf(b, "  Ratio < %g → 클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심\n", model.VerifyRatioLow)
}

func writeWarnings(b *bytes.Buffer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	fmt.Fprint(b, "\nWARNINGS\n")
	for _, w := range warnings {
		fmt.Fprintf(b, "  - %s\n", clean(w))
	}
}
