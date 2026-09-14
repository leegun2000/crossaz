package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leegun2000/crossaz/internal/collect"
	"github.com/leegun2000/crossaz/internal/model"
	"github.com/leegun2000/crossaz/rules"
)

var (
	prodFixture = filepath.Join("..", "..", "testdata", "fixtures", "prod-a.json")
	goldenDir   = filepath.Join("..", "..", "testdata", "golden")
)

// noSources fails the test if a command reaches for a live source.
func noSources(t *testing.T) sources {
	return sources{
		kube: func(string, string) (collect.K8sCollector, string, error) {
			t.Error("kube source used")
			return nil, "", errors.New("unexpected")
		},
		aws: func(context.Context, string) (awsSession, error) {
			t.Error("aws source used")
			return awsSession{}, errors.New("unexpected")
		},
		now: time.Now,
	}
}

func runTest(t *testing.T, src sources, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runWith(args, &out, &errOut, src)
	return code, out.String(), errOut.String()
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(goldenDir, name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(data)
}

func TestRunCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string // substring; "" = no check
		wantStderr string // substring; "" = no check
	}{
		{"version prints the build version", []string{"version"}, exitOK, "crossaz dev\n", ""},
		{"help prints usage to stdout", []string{"help"}, exitOK, "crossaz explain <ns/name | kind/ref>", ""},
		{"no command prints usage and fails", nil, exitUsage, "", "Usage:"},
		{"unknown command prints usage and fails", []string{"deploy"}, exitUsage, "", `unknown command "deploy"`},
		{"rules lists the built-in rules", []string{"rules"}, exitOK, "nlb-cross-zone", ""},
		{"rules rejects a missing rules file", []string{"rules", "--rules", "does-not-exist.yaml"}, exitError, "", "read rules"},
		{"rules rejects positional arguments", []string{"rules", "extra"}, exitUsage, "", "want 0 argument(s)"},
		{"scan rejects an unknown flag", []string{"scan", "--bogus"}, exitUsage, "", "flag provided but not defined: -bogus"},
		{"scan -h shows flags and succeeds", []string{"scan", "-h"}, exitOK, "", "-prometheus-url"},
		{"scan rejects a negative threshold", []string{"scan", "--fixture", prodFixture, "--threshold", "-1"}, exitUsage, "", "--threshold must be >= 0"},
		{"scan rejects a negative rate", []string{"scan", "--rate-gb", "-0.1"}, exitUsage, "", "--rate-gb must be >= 0"},
		{"scan rejects a zero timeout", []string{"scan", "--timeout", "0s"}, exitUsage, "", "--timeout must be > 0"},
		{"scan rejects --verify with --no-aws", []string{"scan", "--verify", "--no-aws"}, exitUsage, "", "--verify needs AWS"},
		{"scan rejects positional arguments", []string{"scan", "payments/api"}, exitUsage, "", "want 0 argument(s)"},
		{"scan reports a missing fixture", []string{"scan", "--fixture", "nope.json"}, exitError, "", "open fixture"},
		{"scan reports a bad rules file before collecting", []string{"scan", "--fixture", prodFixture, "--rules", "nope.yaml"}, exitError, "", "read rules"},
		{"scan applies --cluster and --region to a fixture", []string{"scan", "--fixture", prodFixture, "--cluster", "renamed", "--region", "eu-west-1"}, exitOK, "Cluster: renamed   Region: eu-west-1", ""},
		{"scan warns about a region without a built-in rate", []string{"scan", "--fixture", prodFixture, "--region", "cn-north-1"}, exitOK, "리전 cn-north-1 요율이 내장되어 있지 않아", ""},
		{"scan --threshold 0 hides nothing", []string{"scan", "--fixture", prodFixture, "--threshold", "0"}, exitOK, "kube-system/kube-dns", ""},
		{"explain accepts flags after the key", []string{"explain", "payments/api", "--fixture", prodFixture}, exitOK, "rule:        clusterip-cross-az", ""},
		{"explain accepts flags before the key", []string{"explain", "--fixture", prodFixture, "rds/orders-db"}, exitOK, "rds-multiaz-replication", ""},
		{"explain honours --rate-gb", []string{"explain", "payments/api", "--fixture", prodFixture, "--rate-gb", "0.04"}, exitOK, "× $0.04/GB = ~$112", ""},
		{"explain fails for an unknown key", []string{"explain", "nope/missing", "--fixture", prodFixture}, exitError, "", "no path for nope/missing"},
		{"explain requires a key", []string{"explain", "--fixture", prodFixture}, exitUsage, "", "want 1 argument(s), got 0"},
		{"explain rejects two keys", []string{"explain", "a/b", "c/d", "--fixture", prodFixture}, exitUsage, "", "want 1 argument(s), got 2"},
		{"explain has no --json flag", []string{"explain", "a/b", "--json"}, exitUsage, "", "flag provided but not defined: -json"},
		{"explain reports a bad rules file", []string{"explain", "a/b", "--rules", "nope.yaml"}, exitError, "", "read rules"},
		{"explain prints analysis warnings to stderr", []string{"explain", "payments/api", "--fixture", prodFixture, "--region", "cn-north-1"}, exitOK, "", "crossaz: warning: 리전 cn-north-1 요율이 내장되어 있지 않아"},
		{"explain prints warnings even when no path matches", []string{"explain", "nope/missing", "--fixture", prodFixture, "--region", "cn-north-1"}, exitError, "", "crossaz: warning: 리전 cn-north-1 요율이 내장되어 있지 않아"},
		{"explain has no --verify flag", []string{"explain", "a/b", "--verify"}, exitUsage, "", "flag provided but not defined: -verify"},
		{"scan warns that --fixture ignores --prometheus-url", []string{"scan", "--fixture", prodFixture, "--prometheus-url", "http://127.0.0.1:1"}, exitOK, warnFixtureIgnoresCollection, ""},
		{"scan warns that --fixture ignores --verify", []string{"scan", "--fixture", prodFixture, "--verify"}, exitOK, warnFixtureIgnoresCollection, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			code, stdout, stderr := runTest(t, noSources(t), tt.args...)

			// Assert
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, stdout, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout lacks %q:\n%s", tt.wantStdout, stdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr lacks %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestRunScanFixtureMatchesGoldenFiles(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		golden string
	}{
		{"table", []string{"scan", "--fixture", prodFixture}, "prod-a.txt"},
		{"json", []string{"scan", "--json", "--fixture", prodFixture}, "prod-a.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			code, stdout, stderr := runTest(t, noSources(t), tt.args...)

			// Assert
			if code != exitOK || stderr != "" {
				t.Fatalf("exit = %d, stderr = %q", code, stderr)
			}
			if want := readGolden(t, tt.golden); stdout != want {
				t.Errorf("output differs from %s:\n%s", tt.golden, stdout)
			}
		})
	}
}

func TestRunRulesLoadsACustomRulesFile(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "rules.yaml")
	custom := strings.Replace(string(rules.YAML), "default_rate_gb: 0.02", "default_rate_gb: 0.05", 1)
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 1\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	code, stdout, _ := runTest(t, noSources(t), "rules", "--rules", path)
	badCode, _, badStderr := runTest(t, noSources(t), "rules", "--rules", bad)

	// Assert
	if code != exitOK || !strings.Contains(stdout, "default rate $0.05/GB") {
		t.Errorf("custom rules: exit %d, stdout:\n%s", code, stdout)
	}
	if badCode != exitError || !strings.Contains(badStderr, "invalid rules") {
		t.Errorf("invalid rules: exit %d, stderr %q", badCode, badStderr)
	}
}

func TestRunScanDumpFixtureAnonymizes(t *testing.T) {
	// Arrange
	fixture, err := readFixture(prodFixture)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	code, stdout, stderr := runTest(t, noSources(t), "scan", "--dump-fixture", "--fixture", prodFixture)

	// Assert
	if code != exitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var dumped model.Snapshot
	if err := json.Unmarshal([]byte(stdout), &dumped); err != nil {
		t.Fatalf("dump is not a snapshot: %v", err)
	}
	if len(dumped.Nodes) != len(fixture.Nodes) || dumped.Cluster != fixture.Cluster {
		t.Errorf("dump lost data: %d nodes, cluster %q", len(dumped.Nodes), dumped.Cluster)
	}
	for _, original := range []string{fixture.Nodes[0].Name, fixture.Nodes[0].InstanceID, fixture.AWS.VPCIDs[0], fixture.AWS.LoadBalancers[0].DNSName, fixture.Pods[0].IP} {
		if strings.Contains(stdout, original) {
			t.Errorf("dump still contains %q", original)
		}
	}
}

func TestRunScanRejectsMalformedFixture(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"cluster":"x","nodez":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	code, _, stderr := runTest(t, noSources(t), "scan", "--fixture", path)

	// Assert
	if code != exitError || !strings.Contains(stderr, `unknown field "nodez"`) {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRunEntryPointUsesLiveSourcesOnlyWithoutFixture(t *testing.T) {
	// Arrange
	var out, errOut bytes.Buffer

	// Act
	code := run([]string{"version"}, &out, &errOut)

	// Assert
	if code != exitOK || out.String() != "crossaz dev\n" {
		t.Errorf("run(version) = %d, %q", code, out.String())
	}
}
