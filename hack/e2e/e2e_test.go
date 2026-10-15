//go:build e2e

// Package e2e checks crossaz against the kind cluster that hack/e2e/run.sh builds. See hack/e2e/README.md.
package e2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/leegun2000/crossaz/internal/classify"
	"github.com/leegun2000/crossaz/internal/estimate"
	"github.com/leegun2000/crossaz/internal/model"
)

type verdict struct {
	key      string
	ruleID   string
	billable model.Tri
}

// expectedVerdicts are the L0 verdicts for hack/e2e/manifests/scenarios.yaml and traffic.yaml.
var expectedVerdicts = []verdict{
	{"payments/api", "clusterip-cross-az", model.TriTrue},
	{"cache/redis", "clusterip-cross-az-partial", model.TriTrue},
	{"orders/api", "topology-aware-routing", model.TriFalse},
	{"notify/worker", "hints-withheld", model.TriTrue},
	{"events/kafka", "headless-cross-az", model.TriTrue},
	{"ingress-alb/web-ip", "alb-ip-target-mode", model.TriFalse},
	{"ingress-alb/web-instance", "nodeport-hop-cross-az", model.TriTrue},
	{"ingress-alb/web-local", "alb-private-same-vpc", model.TriFalse},
	{"ingress-automode/web", "alb-ip-target-mode", model.TriFalse},
	{"ingress-nginx/web", "ingress-direct-backend", model.TriFalse},
	{"edge/nlb-xz", "nlb-cross-zone", model.TriTrue},
	{"edge/nlb-unknown", "nlb-cross-zone-unknown", model.TriTrue},
	{"traffic/spread-default", "clusterip-cross-az", model.TriTrue},
	{"traffic/spread-close", "topology-aware-routing", model.TriFalse},
	{"traffic/one-zone", "clusterip-cross-az-partial", model.TriTrue},
}

// rulesUnderTest are the rules the scenarios exist for; expectedVerdicts must reach each of them.
// hints-unverified needs an unknown kube-proxy, which kind cannot run: TestUnknownKubeProxyVersion covers it.
var rulesUnderTest = []string{
	"clusterip-cross-az", "clusterip-cross-az-partial", "topology-aware-routing", "hints-withheld",
	"headless-cross-az", "alb-ip-target-mode", "nodeport-hop-cross-az", "alb-private-same-vpc",
	"ingress-direct-backend", "nlb-cross-zone", "nlb-cross-zone-unknown",
}

var kindZones = []string{"ap-northeast-2a", "ap-northeast-2b", "ap-northeast-2c"}

func TestScanVerdicts(t *testing.T) {
	checkExpectedTable(t)

	r := scan(t)
	if !slices.Equal(r.Zones, kindZones) {
		t.Errorf("zones %v, want %v (the zone-less control-plane must not count)", r.Zones, kindZones)
	}
	if !slices.Equal(r.Layers, []string{"L0"}) {
		t.Errorf("layers %v, want [L0]", r.Layers)
	}
	paths := pathsByKey(t, r)
	for _, want := range expectedVerdicts {
		p, ok := paths[want.key]
		if !ok {
			t.Errorf("%s: no path in the scan result", want.key)
			continue
		}
		if p.RuleID != want.ruleID || p.Billable != want.billable {
			t.Errorf("%s: rule %s billable=%s, want %s billable=%s (facts %+v)", want.key, p.RuleID, p.Billable, want.ruleID, want.billable, p.Facts)
		}
	}
	// The control plane is v1.35, so the fix must use the 1.34+ name.
	if fix := paths["payments/api"].Fix; fix == nil || !strings.Contains(fix.Command, `"trafficDistribution":"PreferSameZone"`) {
		t.Errorf("payments/api fix %+v: want a PreferSameZone patch", fix)
	}
}

// checkExpectedTable is the table's own negative check: unknown rule ids, billable flags that contradict
// rules/rules.yaml, or a rule under test that no scenario expects all fail before the cluster is asked.
func checkExpectedTable(t *testing.T) {
	t.Helper()
	billable := map[string]model.Tri{}
	for _, r := range classify.Default().Rules {
		billable[r.ID] = r.Billable
	}
	covered := map[string]bool{}
	for _, v := range expectedVerdicts {
		b, ok := billable[v.ruleID]
		switch {
		case !ok:
			t.Fatalf("expectedVerdicts %s: rule %q is not in rules/rules.yaml", v.key, v.ruleID)
		case b != v.billable:
			t.Fatalf("expectedVerdicts %s: rule %s is billable=%s in rules/rules.yaml, table says %s", v.key, v.ruleID, b, v.billable)
		}
		covered[v.ruleID] = true
	}
	for _, id := range rulesUnderTest {
		if !covered[id] {
			t.Fatalf("expectedVerdicts has no scenario for rule %s", id)
		}
	}
}

func TestDumpFixtureRoundTrip(t *testing.T) {
	live := ruleIDs(t, scan(t))
	dump := command(t, crossazBin, "scan", "--context", kubeContext, "--no-aws", "--dump-fixture")
	replay := ruleIDs(t, scanFixture(t, writeTemp(t, "kind.json", dump)))

	if len(live) < len(expectedVerdicts) {
		t.Fatalf("live scan has %d paths, want at least %d", len(live), len(expectedVerdicts))
	}
	for key, rule := range live {
		if replay[key] != rule {
			t.Errorf("%s: live rule %q, fixture rule %q", key, rule, replay[key])
		}
	}
	for key, rule := range replay {
		if _, ok := live[key]; !ok {
			t.Errorf("%s: only in the fixture scan (rule %q)", key, rule)
		}
	}

	var nodeNames, podIPs []string
	for _, n := range listNodes(t) {
		nodeNames = append(nodeNames, n.Name)
	}
	for _, p := range listPods(t, "--all-namespaces") {
		for _, ip := range p.Status.PodIPs {
			podIPs = append(podIPs, ip.IP)
		}
	}
	// Self-check: the scanner must catch planted values, or a clean result means nothing.
	planted := fmt.Sprintf(`{"pods":[{"name":"etcd-%s","ip":"x %s"}]}`, nodeNames[0], podIPs[0])
	if got := leakedValues(t, []byte(planted), nodeNames, podIPs); len(got) != 2 {
		t.Fatalf("leak scanner found %q in %s, want both values", got, planted)
	}
	if leaks := leakedValues(t, dump, nodeNames, podIPs); len(leaks) > 0 {
		t.Errorf("--dump-fixture leaks live node names / pod IPs: %q", leaks)
	}
}

// TestUnknownKubeProxyVersion replays the live snapshot without kube-proxy's version, as when kube-proxy is
// replaced (Cilium, EKS Auto Mode) or its DaemonSet cannot be read. kind always runs kube-proxy, so this is how
// real EndpointSlices reach hints-unverified.
func TestUnknownKubeProxyVersion(t *testing.T) {
	var s model.Snapshot
	if err := json.Unmarshal(command(t, crossazBin, "scan", "--context", kubeContext, "--no-aws", "--dump-fixture"), &s); err != nil {
		t.Fatalf("decode dump: %v", err)
	}
	if s.KubeProxyVersion == "" {
		t.Fatal("the live dump has no kubeProxyVersion: crossaz did not read kind's kube-proxy DaemonSet")
	}
	s.KubeProxyVersion = ""
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	paths := pathsByKey(t, scanFixture(t, writeTemp(t, "no-kube-proxy.json", data)))
	for key, want := range map[string]string{
		"notify/worker":        "hints-unverified",
		"orders/api":           "hints-unverified",
		"traffic/spread-close": "hints-unverified",
		"payments/api":         "clusterip-cross-az", // asks for no zone routing: billed whatever the proxy
	} {
		if got := paths[key].RuleID; got != want {
			t.Errorf("%s without a kube-proxy version: rule %q, want %s", key, got, want)
		}
	}
}

func scanFixture(t *testing.T, path string) model.ScanResult {
	t.Helper()
	return decodeScan(t, command(t, crossazBin, "scan", "--fixture", path, "--json", "--threshold", "0"))
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func ruleIDs(t *testing.T, r model.ScanResult) map[string]string {
	t.Helper()
	out := map[string]string{}
	for key, p := range pathsByKey(t, r) {
		out[key] = p.RuleID
	}
	return out
}

// leakedValues returns every JSON string (value or object key) that contains a node name, or holds a pod IP as
// an IPv4 token (so "10.244.1.2" does not hit an anonymized "10.244.1.23").
func leakedValues(t *testing.T, data []byte, nodeNames, podIPs []string) []string {
	t.Helper()
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode dump: %v", err)
	}
	isPodIP := map[string]bool{}
	for _, ip := range podIPs {
		isPodIP[ip] = true
	}
	notIPv4 := func(r rune) bool { return r != '.' && (r < '0' || r > '9') }
	leaks := func(s string) bool {
		return slices.ContainsFunc(nodeNames, func(n string) bool { return strings.Contains(s, n) }) ||
			slices.ContainsFunc(strings.FieldsFunc(s, notIPv4), func(tok string) bool { return isPodIP[tok] })
	}
	var found []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			if leaks(v) {
				found = append(found, v)
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			for k, e := range v {
				walk(k)
				walk(e)
			}
		}
	}
	walk(doc)
	return found
}

const (
	// minRequests is REQUESTS in manifests/traffic.yaml: per client (one per zone), per service.
	minRequests = 400
	// shareSigmas bounds a zone's measured cross-zone share around the prediction, in binomial standard
	// deviations of its sample. Where kube-proxy has no choice (a share of 0 or 1) the bound is exact.
	shareSigmas = 4
	// overallTolerance bounds the service-wide share against crossaz's crossAzFraction (about 3.7σ at 2/3 over
	// 1200 requests), so a real share of 3/4 (a fourth caller zone) or 0.6 fails.
	overallTolerance = 0.05
)

type tally struct{ total, cross, errors int }

func (a tally) plus(b tally) tally {
	return tally{total: a.total + b.total, cross: a.cross + b.cross, errors: a.errors + b.errors}
}

func (a tally) share() float64 { return float64(a.cross) / float64(a.total-a.errors) }

func TestKubeProxyCrossZoneFraction(t *testing.T) {
	kubectl(t, "-n", "traffic", "wait", "--for=condition=complete", "job", "-l", "e2e.crossaz.io/role=client", "--timeout=10m")
	zoneOf := nodeZones(t)
	pods := listPods(t, "-n", "traffic")
	podZone := map[string]string{}
	placement := map[string]model.AZDistribution{}
	for _, p := range pods {
		zone := zoneOf[p.Spec.NodeName]
		podZone[p.Name] = zone
		if app := p.Labels["app"]; app != "client" {
			if placement[app] == nil {
				placement[app] = model.AZDistribution{}
			}
			placement[app][zone]++
		}
	}
	// The model fractions assume this placement; anything else is a harness problem, not a crossaz one.
	for app, want := range map[string]model.AZDistribution{
		"spread":   {kindZones[0]: 2, kindZones[1]: 2, kindZones[2]: 2},
		"one-zone": {kindZones[0]: 2},
	} {
		if !maps.Equal(placement[app], want) {
			t.Fatalf("traffic/%s pods by zone %v, want %v", app, placement[app], want)
		}
	}

	tallies := tallyClients(t, pods, podZone)
	paths := pathsByKey(t, scan(t))
	for _, c := range []struct {
		service, ruleID string
		model           float64
	}{
		{"spread-default", "clusterip-cross-az", 2.0 / 3},
		{"one-zone", "clusterip-cross-az-partial", 2.0 / 3}, // callers in b and c always cross (SPEC B2)
		{"spread-close", "topology-aware-routing", 0},
	} {
		p, ok := paths["traffic/"+c.service]
		if !ok {
			t.Errorf("traffic/%s: no path in the scan result", c.service)
			continue
		}
		if p.RuleID != c.ruleID || math.Abs(p.CrossAZFrac-c.model) > 0.01 {
			t.Errorf("traffic/%s: rule %s crossAzFraction %.3f, want %s %.3f", c.service, p.RuleID, p.CrossAZFrac, c.ruleID, c.model)
		}
		var all tally
		ok = true
		for _, zone := range kindZones {
			tl := tallies[c.service][zone]
			all = all.plus(tl)
			// crossaz's own prediction for callers in this zone, from the endpoints and hints it collected.
			want := model.HintedCrossAZFraction(model.AZDistribution{zone: 1}, p.Dest.AZDist, p.Dest.HintedFor)
			ok = checkZoneShare(t, "traffic/"+c.service, zone, tl, want) && ok
		}
		if !ok {
			continue
		}
		t.Logf("traffic/%s: kube-proxy cross-zone %.3f (%d/%d, %d errors); crossaz crossAzFraction %.3f",
			c.service, all.share(), all.cross, all.total-all.errors, all.errors, p.CrossAZFrac)
		if math.Abs(all.share()-p.CrossAZFrac) > overallTolerance {
			t.Errorf("traffic/%s: kube-proxy cross-zone share %.3f, crossaz crossAzFraction %.3f (want within %.2f)",
				c.service, all.share(), p.CrossAZFrac, overallTolerance)
		}
	}
}

// checkZoneShare compares what kube-proxy did for one zone's client with crossaz's prediction for that zone.
func checkZoneShare(t *testing.T, key, zone string, tl tally, want float64) bool {
	t.Helper()
	switch {
	case tl.total < minRequests:
		t.Errorf("%s from %s: %d requests, want >= %d (one client per zone)", key, zone, tl.total, minRequests)
		return false
	case tl.errors*100 > tl.total:
		t.Errorf("%s from %s: %d/%d requests failed (> 1%%)", key, zone, tl.errors, tl.total)
		return false
	}
	tolerance := shareSigmas * math.Sqrt(want*(1-want)/float64(tl.total-tl.errors))
	t.Logf("%s from %s: kube-proxy cross-zone %.3f (%d/%d), crossaz predicts %.3f ± %.3f",
		key, zone, tl.share(), tl.cross, tl.total-tl.errors, want, tolerance)
	if math.Abs(tl.share()-want) > tolerance {
		t.Errorf("%s from %s: kube-proxy cross-zone share %.3f, crossaz predicts %.3f ± %.3f", key, zone, tl.share(), want, tolerance)
		return false
	}
	return true
}

// tallyClients reads every client pod's "<service> <server pod>" lines and counts answers per service and
// client zone.
func tallyClients(t *testing.T, pods []corev1.Pod, podZone map[string]string) map[string]map[string]tally {
	t.Helper()
	tallies := map[string]map[string]tally{}
	for _, p := range pods {
		if p.Labels["app"] != "client" {
			continue
		}
		clientZone := podZone[p.Name]
		for _, line := range strings.Split(strings.TrimSpace(string(kubectl(t, "-n", "traffic", "logs", p.Name))), "\n") {
			service, server, ok := strings.Cut(line, " ")
			if !ok {
				t.Fatalf("client %s: malformed line %q", p.Name, line)
			}
			if tallies[service] == nil {
				tallies[service] = map[string]tally{}
			}
			tl := tallies[service][clientZone]
			tl.total++
			serverZone, known := podZone[server]
			switch {
			case server == "ERR":
				tl.errors++
			case !known:
				t.Fatalf("client %s: %s answered from unknown pod %q", p.Name, service, server)
			case serverZone != clientZone:
				tl.cross++
			}
			tallies[service][clientZone] = tl
		}
	}
	return tallies
}

const (
	// volumeTolerance: on kind the estimate reads 0.95-1.01x the generator (kubelet cAdvisor stamps samples up to
	// ~30 s late, so each 5m rate under-extrapolates a little). 15% leaves about 3x that bias.
	volumeTolerance = 0.15
	// volumeWait bounds how long crossaz may leave the sink unmeasured: it counts a 5m step only once the series
	// is older than the step, so a new sink (and a new Prometheus) is measured 5-10 minutes after it starts.
	volumeWait    = 12 * time.Minute
	volumePoll    = 20 * time.Second
	seriesWait    = 2 * time.Minute
	regionRateGB  = 0.02 // ap-northeast-2, both sides
	minGeneratorS = 60
)

func TestPrometheusVolumeEstimate(t *testing.T) {
	if os.Getenv("E2E_L2") != "1" {
		t.Skip("E2E_L2 is not 1: L2 needs Prometheus and the volume generator (hack/e2e/run.sh l2; run.sh test then sets E2E_L2=1)")
	}
	promURL := portForwardPrometheus(t)
	sink := runningPod(t, "volume", "app=sink")
	generator := runningPod(t, "volume", "app=generator")
	requireSinkSeries(t, promURL, sink)

	// Default --prom-window (7d): far longer than the sink has lived and than the chart's 1d retention, so
	// crossaz must average over the samples that exist instead of dividing by the window. Until Prometheus holds
	// a full 5m step, crossaz reports no L2 at all (fresh install) or leaves the sink unmeasured.
	var r model.ScanResult
	var p model.Path
	if !poll(volumeWait, volumePoll, func() bool {
		r = scan(t, "--prometheus-url", promURL)
		p = pathsByKey(t, r)["volume/sink"]
		return slices.Contains(r.Layers, "L2") && p.BytesPerMo != nil
	}) {
		t.Fatalf("volume/sink: no bytesPerMo after %s (layers %v, warnings %v, rule %q, confidence %s: %s)",
			volumeWait, r.Layers, r.Warnings, p.RuleID, p.Confidence, p.ConfidenceNote)
	}

	newest := newestGeneratorSample(t, generator)
	if newest.elapsed < minGeneratorS {
		t.Fatalf("generator %s has run %ds, want >= %ds", generator, newest.elapsed, minGeneratorS)
	}
	rate := float64(newest.sent) / float64(newest.elapsed)
	want := rate * estimate.MonthSeconds
	ratio := *p.BytesPerMo / want
	t.Logf("volume/sink: generator %.0f B/s over %ds -> %.3g B/mo; crossaz bytesPerMo %.3g (ratio %.3f), rule %s, crossAzFraction %.3f",
		rate, newest.elapsed, want, *p.BytesPerMo, ratio, p.RuleID, p.CrossAZFrac)
	if math.Abs(ratio-1) > volumeTolerance {
		t.Errorf("volume/sink: bytesPerMo %.3g is %.0f%% of the generator's %.3g (want within ±%.0f%%)",
			*p.BytesPerMo, ratio*100, want, volumeTolerance*100)
	}
	// One replica in zone a, callers in three zones: 2/3 crosses at the region rate.
	if p.RuleID != "clusterip-cross-az-partial" || math.Abs(p.CrossAZFrac-2.0/3) > 0.01 || p.RateGB != regionRateGB || p.Confidence != model.Medium {
		t.Errorf("volume/sink: rule %s crossAzFraction %.3f rateGb %v confidence %s, want clusterip-cross-az-partial 0.667 %v medium",
			p.RuleID, p.CrossAZFrac, p.RateGB, p.Confidence, regionRateGB)
	}
	if p.CostPerMo == nil {
		t.Fatal("volume/sink: no costPerMo")
	}
	wantCost := *p.BytesPerMo / 1e9 * p.CrossAZFrac * p.RateGB
	t.Logf("volume/sink: costPerMo %.2f", *p.CostPerMo)
	if math.Abs(*p.CostPerMo-wantCost) > 1e-9*wantCost {
		t.Errorf("volume/sink: costPerMo %.4f, want bytesPerMo/1e9 × crossAzFraction × rateGb = %.4f", *p.CostPerMo, wantCost)
	}
}

// requireSinkSeries waits for the sink's cAdvisor series and otherwise says which link of the L2 chain is missing.
func requireSinkSeries(t *testing.T, promURL, sink string) {
	t.Helper()
	series := fmt.Sprintf(`container_network_receive_bytes_total{namespace="volume",pod=%q}`, sink)
	if poll(seriesWait, pollInterval, func() bool { return promSeriesCount(t, promURL, series) > 0 }) {
		return
	}
	switch {
	case promSeriesCount(t, promURL, `up{job="kubernetes-nodes-cadvisor"} == 1`) == 0:
		t.Fatal("Prometheus has no healthy kubernetes-nodes-cadvisor target: the chart's cAdvisor scrape job is missing or failing (nodes/proxy RBAC, kubelet TLS)")
	case promSeriesCount(t, promURL, `container_network_receive_bytes_total{pod!=""}`) == 0:
		t.Fatal("kubelet cAdvisor exports no container_network_receive_bytes_total with a pod label: crossaz's L2 query matches nothing on this kubelet")
	default:
		t.Fatalf("Prometheus has pod network series but none for %s", series)
	}
}

type generatorSample struct{ sent, elapsed int64 }

// newestGeneratorSample parses the generator's "sent_bytes=<n> elapsed_s=<s>" lines and returns the newest. Other
// lines (nc complaining while the sink starts) are skipped.
func newestGeneratorSample(t *testing.T, pod string) generatorSample {
	t.Helper()
	var newest generatorSample
	found := false
	for _, line := range strings.Split(string(kubectl(t, "-n", "volume", "logs", pod)), "\n") {
		if !strings.HasPrefix(line, "sent_bytes=") {
			continue
		}
		if _, err := fmt.Sscanf(line, "sent_bytes=%d elapsed_s=%d", &newest.sent, &newest.elapsed); err != nil {
			t.Fatalf("generator %s: malformed line %q: %v", pod, line, err)
		}
		found = true
	}
	if !found {
		t.Fatalf("generator %s logged no samples", pod)
	}
	return newest
}
