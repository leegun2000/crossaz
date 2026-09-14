package report

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const catchAllMark = "*"

// Rules writes the active rule set as a table followed by the rule sources.
func Rules(w io.Writer, rs model.RuleSet) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "rules v%d  default rate $%g/GB  (%d regions built in)\n\n",
		rs.Version, rs.DefaultRateGB, len(rs.Regions))

	rows := make([][]string, 0, len(rs.Rules)+1)
	rows = append(rows, []string{"ID", "BILLABLE", "CONF", "RATE", "MATCH", "VERIFIED"})
	for _, r := range rs.Rules {
		rows = append(rows, []string{
			r.ID, strconv.FormatBool(r.Billable), confLabel(r.Confidence),
			ruleRate(r), matchSummary(r.Match), orUnverified(r.Verified),
		})
	}
	if err := writeTable(&b, rows); err != nil {
		return err
	}

	b.WriteString("\nSOURCES\n")
	for _, r := range rs.Rules {
		if r.Source != "" {
			fmt.Fprintf(&b, "  %s: %s\n", clean(r.ID), clean(r.Source))
		}
	}
	return flushTo(w, &b)
}

func ruleRate(r model.Rule) string {
	if r.RateGB > 0 {
		return fmt.Sprintf("$%g", r.RateGB)
	}
	return "region"
}

func orUnverified(s string) string {
	if s == "" {
		return unverifiedMark
	}
	return s
}

// matchSummary renders non-empty match conditions as "yaml_key=value" pairs in declaration order.
// Reflection keeps it in sync with model.Match without a hand-maintained field list.
func matchSummary(m model.Match) string {
	v := reflect.ValueOf(m)
	t := v.Type()
	parts := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		switch f := v.Field(i).Interface().(type) {
		case model.StringSet:
			if len(f) > 0 {
				parts = append(parts, name+"="+strings.Join(f, ","))
			}
		case *bool:
			if f != nil {
				parts = append(parts, name+"="+strconv.FormatBool(*f))
			}
		}
	}
	if len(parts) == 0 {
		return catchAllMark
	}
	return strings.Join(parts, " ")
}
