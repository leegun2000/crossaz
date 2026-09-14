//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/leegun2000/crossaz/internal/model"
)

// kubeContext is the only context the e2e tests talk to; the current context is never used.
const kubeContext = "kind-crossaz-e2e"

// harnessKubeconfig is where run.sh keeps the kind cluster's kubeconfig (never the user's ~/.kube/config).
const harnessKubeconfig = ".kubeconfig"

const (
	pollInterval = 5 * time.Second
	promTimeout  = 30 * time.Second
)

// crossazBin is built once by TestMain.
var crossazBin string

// promClient bounds every Prometheus query, so a stalled port-forward fails the test (and Cleanup stops it)
// instead of hanging until the go test timeout kills the process and orphans kubectl.
var promClient = &http.Client{Timeout: promTimeout}

func TestMain(m *testing.M) { os.Exit(testMain(m)) }

func testMain(m *testing.M) int {
	// kubectl and crossaz inherit this, so the tests read only the harness kubeconfig.
	kubeconfig, err := filepath.Abs(harnessKubeconfig)
	if err == nil {
		_, err = os.Stat(kubeconfig)
	}
	if err == nil {
		err = os.Setenv("KUBECONFIG", kubeconfig)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: harness kubeconfig (run hack/e2e/run.sh up first): %v\n", err)
		return 1
	}
	dir, err := os.MkdirTemp("", "crossaz-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	crossazBin = filepath.Join(dir, "crossaz")
	build := exec.Command("go", "build", "-o", crossazBin, "./cmd/crossaz")
	build.Dir = filepath.Join("..", "..")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build crossaz: %v\n", err)
		return 1
	}
	return m.Run()
}

// command runs name and returns its stdout. Stderr is logged; a non-zero exit ends the test.
func command(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Logf("%s stderr: %s", filepath.Base(name), s)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", filepath.Base(name), strings.Join(args, " "), err)
	}
	return stdout.Bytes()
}

func kubectl(t *testing.T, args ...string) []byte {
	t.Helper()
	return command(t, "kubectl", append([]string{"--context", kubeContext}, args...)...)
}

func kubectlJSON(t *testing.T, into any, args ...string) {
	t.Helper()
	if err := json.Unmarshal(kubectl(t, append(args, "-o", "json")...), into); err != nil {
		t.Fatalf("decode kubectl %s: %v", strings.Join(args, " "), err)
	}
}

func listNodes(t *testing.T) []corev1.Node {
	t.Helper()
	var l corev1.NodeList
	kubectlJSON(t, &l, "get", "nodes")
	return l.Items
}

func listPods(t *testing.T, args ...string) []corev1.Pod {
	t.Helper()
	var l corev1.PodList
	kubectlJSON(t, &l, append([]string{"get", "pods"}, args...)...)
	return l.Items
}

// runningPod returns the name of the single Running pod matching selector.
func runningPod(t *testing.T, namespace, selector string) string {
	t.Helper()
	pods := listPods(t, "-n", namespace, "-l", selector, "--field-selector", "status.phase=Running")
	if len(pods) != 1 {
		t.Fatalf("%s pods %q: %d running, want 1", namespace, selector, len(pods))
	}
	return pods[0].Name
}

func nodeZones(t *testing.T) map[string]string {
	t.Helper()
	zones := map[string]string{}
	for _, n := range listNodes(t) {
		zones[n.Name] = n.Labels[corev1.LabelTopologyZone]
	}
	return zones
}

// scan runs crossaz scan on the live cluster, L0 only unless extra flags add layers.
func scan(t *testing.T, extra ...string) model.ScanResult {
	t.Helper()
	args := append([]string{"scan", "--context", kubeContext, "--no-aws", "--json", "--threshold", "0"}, extra...)
	return decodeScan(t, command(t, crossazBin, args...))
}

func decodeScan(t *testing.T, data []byte) model.ScanResult {
	t.Helper()
	var r model.ScanResult
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("decode scan JSON: %v", err)
	}
	return r
}

// pathsByKey indexes the paths of every section. Without L1 there are only service paths, one per key.
func pathsByKey(t *testing.T, r model.ScanResult) map[string]model.Path {
	t.Helper()
	out := map[string]model.Path{}
	for _, p := range slices.Concat(r.Paths, r.Excluded, r.Uncertain) {
		if _, dup := out[p.Key()]; dup {
			t.Errorf("path %s appears more than once in the scan result", p.Key())
		}
		out[p.Key()] = p
	}
	return out
}

// poll calls check every interval until it reports true or timeout passes.
func poll(timeout, interval time.Duration, check func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

var forwardingRE = regexp.MustCompile(`^Forwarding from 127\.0\.0\.1:(\d+) -> `)

// portForwardPrometheus forwards a random free local port to prometheus-server until the test ends.
func portForwardPrometheus(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("kubectl", "--context", kubeContext, "-n", "monitoring", "port-forward", "svc/prometheus-server", ":80")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("port-forward stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start port-forward: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ports := make(chan string, 1)
	go func() {
		// Keep reading after the port is known: port-forward blocks once its stdout pipe fills up.
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := forwardingRE.FindStringSubmatch(sc.Text()); m != nil && len(ports) == 0 {
				ports <- m[1]
			}
		}
	}()
	select {
	case port := <-ports:
		return "http://127.0.0.1:" + port
	case <-time.After(30 * time.Second):
		t.Fatal("kubectl port-forward svc/prometheus-server printed no local port within 30s")
		return ""
	}
}

// promSeriesCount returns how many series an instant query returns.
func promSeriesCount(t *testing.T, baseURL, query string) int {
	t.Helper()
	resp, err := promClient.Get(baseURL + "/api/v1/query?" + url.Values{"query": {query}}.Encode())
	if err != nil {
		t.Fatalf("prometheus query %s: %v", query, err)
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("prometheus query %s: HTTP %d: %v", query, resp.StatusCode, err)
	}
	if body.Status != "success" {
		t.Fatalf("prometheus query %s: status %q: %s", query, body.Status, body.Error)
	}
	return len(body.Data.Result)
}
