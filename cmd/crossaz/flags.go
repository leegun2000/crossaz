package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

const (
	defaultThreshold  = 1.0
	defaultPromWindow = "7d"
	defaultTimeout    = 2 * time.Minute
)

type options struct {
	kubeconfig  string
	kubeContext string
	cluster     string
	region      string
	rulesFile   string
	rateGB      float64
	threshold   float64
	json        bool
	promURL     string
	promWindow  string
	verify      bool
	noAWS       bool
	fixture     string
	dumpFixture bool
	timeout     time.Duration
}

func (o options) validate() error {
	switch {
	case o.threshold < 0:
		return errors.New("--threshold must be >= 0")
	case o.rateGB < 0:
		return errors.New("--rate-gb must be >= 0")
	case o.timeout <= 0:
		return errors.New("--timeout must be > 0")
	case o.verify && o.noAWS:
		return errors.New("--verify needs AWS: remove --no-aws")
	}
	return nil
}

func parseScan(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := newFlagSet("scan", "[flags]", stderr)
	bindRules(fs, &o)
	bindCollection(fs, &o)
	fs.Float64Var(&o.threshold, "threshold", defaultThreshold, "hide billable paths estimated below this many USD/month")
	fs.BoolVar(&o.json, "json", false, "print the full scan result as JSON")
	fs.BoolVar(&o.dumpFixture, "dump-fixture", false, "print the collected snapshot, anonymized, as JSON and exit")
	fs.BoolVar(&o.verify, "verify", false, "L3: compare with last month's Cost Explorer inter-AZ cost ($0.01 per request)")
	_, err := parseCommand(fs, args, 0, func() error { return o.validate() })
	return o, err
}

func parseExplain(args []string, stderr io.Writer) (string, options, error) {
	var o options
	fs := newFlagSet("explain", "<ns/name | kind/ref> [flags]", stderr)
	bindRules(fs, &o)
	bindCollection(fs, &o)
	positional, err := parseCommand(fs, args, 1, func() error { return o.validate() })
	if err != nil {
		return "", o, err
	}
	return positional[0], o, nil
}

func parseRules(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := newFlagSet("rules", "[--rules file]", stderr)
	bindRules(fs, &o)
	_, err := parseCommand(fs, args, 0, nil)
	return o, err
}

func newFlagSet(name, synopsis string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: crossaz %s %s\n\nFlags:\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}

func bindRules(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.rulesFile, "rules", "", "rules file replacing the built-in rules")
}

func bindCollection(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.kubeconfig, "kubeconfig", "", "kubeconfig path (default $KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&o.kubeContext, "context", "", "kubeconfig context (default current context)")
	fs.StringVar(&o.cluster, "cluster", "", "cluster name to report (default the context's cluster)")
	fs.StringVar(&o.region, "region", "", "AWS region (default node region label, then AWS config)")
	fs.Float64Var(&o.rateGB, "rate-gb", 0, "force the USD/GB rate (both sides combined) of every billable path")
	fs.StringVar(&o.promURL, "prometheus-url", "", "Prometheus base URL for L2 volume estimates")
	fs.StringVar(&o.promWindow, "prom-window", defaultPromWindow, "PromQL rate window")
	fs.BoolVar(&o.noAWS, "no-aws", false, "skip AWS (L1)")
	fs.StringVar(&o.fixture, "fixture", "", "read a snapshot JSON file instead of the cluster")
	fs.DurationVar(&o.timeout, "timeout", defaultTimeout, "limit for the whole run")
}

// parseCommand parses flags placed before or after positional arguments, then checks the positional
// count and validate. Problems are printed with the usage; flag.ErrHelp is returned for -h.
func parseCommand(fs *flag.FlagSet, args []string, wantArgs int, validate func() error) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
	var problem error
	switch {
	case len(positional) != wantArgs:
		problem = fmt.Errorf("want %d argument(s), got %d %q", wantArgs, len(positional), positional)
	case validate != nil:
		problem = validate()
	}
	if problem != nil {
		fmt.Fprintf(fs.Output(), "crossaz %s: %v\n", fs.Name(), problem)
		fs.Usage()
		return nil, problem
	}
	return positional, nil
}
