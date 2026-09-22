// Command crossaz finds billed cross-AZ traffic paths in an EKS cluster. It only reads.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/leegun2000/crossaz/internal/classify"
	"github.com/leegun2000/crossaz/internal/collect"
	"github.com/leegun2000/crossaz/internal/model"
	"github.com/leegun2000/crossaz/internal/report"
	"github.com/leegun2000/crossaz/internal/scan"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// warnV1FixtureCost: a v1 fixture's monthly "cost" has no daily lines to verify against.
const warnV1FixtureCost = "v1 fixture의 월별 cost는 무시됨 — --verify로 다시 수집"

const usageText = `crossaz finds cross-AZ traffic paths that are actually billed in an EKS cluster (read-only).

Usage:
  crossaz scan    [flags]
  crossaz explain <ns/name | kind/ref> [flags]
  crossaz bill    [--days N] [flags]
  crossaz rules   [--rules file]
  crossaz version

Run "crossaz <command> -h" for the flags of a command.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runWith(args, stdout, stderr, liveSources())
}

func runWith(args []string, stdout, stderr io.Writer, src sources) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "scan":
		o, err := parseScan(rest, stderr)
		if err != nil {
			return parseExit(err)
		}
		return exit(stderr, scanCommand(o, stdout, src))
	case "explain":
		key, o, err := parseExplain(rest, stderr)
		if err != nil {
			return parseExit(err)
		}
		return exit(stderr, explainCommand(key, o, stdout, stderr, src))
	case "bill":
		o, err := parseBill(rest, stderr)
		if err != nil {
			return parseExit(err)
		}
		return exit(stderr, billCommand(o, stdout, src))
	case "rules":
		o, err := parseRules(rest, stderr)
		if err != nil {
			return parseExit(err)
		}
		return exit(stderr, rulesCommand(o, stdout))
	case "version":
		fmt.Fprintf(stdout, "crossaz %s\n", version)
		return exitOK
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(stderr, "crossaz: unknown command %q\n\n%s", cmd, usageText)
		return exitUsage
	}
}

func scanCommand(o options, stdout io.Writer, src sources) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	if o.dumpFixture {
		s, err := loadSnapshot(ctx, o, src)
		if err != nil {
			return err
		}
		return writeFixture(stdout, scan.Anonymize(s))
	}
	// Rules first: a bad --rules file should fail before a slow collection.
	rs, err := loadRules(o.rulesFile)
	if err != nil {
		return err
	}
	s, err := loadSnapshot(ctx, o, src)
	if err != nil {
		return err
	}
	r, _, err := scan.Analyze(s, rs, scan.Options{Threshold: o.threshold, RateGB: o.rateGB})
	if err != nil {
		return err
	}
	if o.json {
		return report.JSON(stdout, r)
	}
	return report.Table(stdout, r)
}

func explainCommand(key string, o options, stdout, stderr io.Writer, src sources) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	rs, err := loadRules(o.rulesFile)
	if err != nil {
		return err
	}
	s, err := loadSnapshot(ctx, o, src)
	if err != nil {
		return err
	}
	r, paths, err := scan.Analyze(s, rs, scan.Options{RateGB: o.rateGB})
	if err != nil {
		return err
	}
	// Warnings go out first so a failed layer is visible even when Explain finds no path.
	for _, w := range r.Warnings {
		fmt.Fprintf(stderr, "crossaz: warning: %s\n", w)
	}
	return report.Explain(stdout, key, paths, rs)
}

// billCommand prints the daily Cost Explorer bill of the cluster's region, filtered to the accounts that
// own its nodes (L1); without them it queries the region only and says so, unless L1 found none of the nodes.
func billCommand(o options, stdout io.Writer, src sources) error {
	ctx, cancel := context.WithTimeout(context.Background(), o.timeout)
	defer cancel()
	s, sess, err := collectL0L1(ctx, o, src)
	if err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("bill needs AWS credentials: %s", strings.Join(s.Warnings, "; "))
	}
	// A region-only query would bill and report an account that may not own the cluster at all.
	if s.AWS == nil && slices.Contains(s.Warnings, collect.WarnNoVPCs) {
		return errors.New("bill: L1이 클러스터 노드를 찾지 못함(다른 계정/리전 자격증명?) — AWS_PROFILE/--region 확인")
	}
	if s.Region == "" {
		return errors.New("bill: region unknown, use --region")
	}
	var accounts []string
	if s.AWS != nil {
		accounts = s.AWS.Accounts
	}
	from, to := closedDays(src.now(), o.days)
	bill, err := sess.bill.DailyBill(ctx, s.Region, accounts, from, to)
	if err != nil {
		return err
	}
	bill.Warnings = append(slices.Clone(s.Warnings), bill.Warnings...)
	r := model.BillReport{Region: s.Region, Accounts: accounts, Metric: collect.BillMetric, Bill: bill}
	if o.json {
		return report.BillJSON(stdout, r)
	}
	return report.BillTable(stdout, r)
}

func rulesCommand(o options, stdout io.Writer) error {
	rs, err := loadRules(o.rulesFile)
	if err != nil {
		return err
	}
	return report.Rules(stdout, rs)
}

func loadRules(path string) (model.RuleSet, error) {
	if path == "" {
		return classify.Default(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return model.RuleSet{}, fmt.Errorf("read rules: %w", err)
	}
	rs, err := classify.Load(data)
	if err != nil {
		return model.RuleSet{}, fmt.Errorf("rules %s: %w", path, err)
	}
	return rs, nil
}

func readFixture(path string) (model.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("open fixture: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	// v1 fixtures carry a monthly "cost" instead of the daily "bill"; read them and say it is dropped.
	var v struct {
		model.Snapshot
		Cost json.RawMessage `json:"cost"`
	}
	if err := dec.Decode(&v); err != nil {
		return model.Snapshot{}, fmt.Errorf("parse fixture %s: %w", path, err)
	}
	s := v.Snapshot
	if len(v.Cost) > 0 {
		s.Warnings = append(slices.Clone(s.Warnings), warnV1FixtureCost)
	}
	return s, nil
}

func writeFixture(w io.Writer, s model.Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}
	return nil
}

func exit(stderr io.Writer, err error) int {
	if err == nil {
		return exitOK
	}
	fmt.Fprintf(stderr, "crossaz: %v\n", err)
	return exitError
}

// parseExit maps a flag parsing failure (already printed) to an exit code; -h is not a failure.
func parseExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	return exitUsage
}
