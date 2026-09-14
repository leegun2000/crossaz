package collect

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const promTestSuccessBody = `{"status":"success","data":{"resultType":"vector","result":[
	{"metric":{"namespace":"shop","pod":"api-1"},"value":[1757800000.1,"1500.5"]},
	{"metric":{"namespace":"shop","pod":"api-2"},"value":[1757800000.1,"0"]},
	{"metric":{"namespace":"shop","pod":"nan"},"value":[1757800000.1,"NaN"]},
	{"metric":{"namespace":"shop","pod":"inf"},"value":[1757800000.1,"+Inf"]},
	{"metric":{"namespace":"shop","pod":"neg"},"value":[1757800000.1,"-3"]},
	{"metric":{"pod":"no-namespace"},"value":[1757800000.1,"7"]},
	{"metric":{"namespace":"shop"},"value":[1757800000.1,"7"]}
]}}`

// promTestServer serves body with status and records the request path, query and count.
func promTestServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var lastURL atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastURL.Store(*r.URL)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &lastURL
}

func TestPodRecvBytesPerSecondParsesVectorAndSendsExactQuery(t *testing.T) {
	// Arrange
	srv, calls, lastURL := promTestServer(t, http.StatusOK, promTestSuccessBody)
	// A 5m rate per 5m step, kept only where the series already existed 5m earlier, averaged over the steps that
	// exist: a pod younger than the window (or Prometheus retention shorter than it) is not divided by the window.
	wantQuery := `sum by (namespace, pod) (avg_over_time((max by (namespace, pod, interface) ` +
		`(rate(container_network_receive_bytes_total{pod!=""}[5m]) and container_network_receive_bytes_total{pod!=""} offset 5m))[7d:5m]))`

	// Act
	got, err := NewPrometheus(srv.URL+"/prom/", srv.Client()).PodRecvBytesPerSecond(t.Context(), "7d")

	// Assert
	if err != nil {
		t.Fatalf("PodRecvBytesPerSecond: %v", err)
	}
	want := map[string]float64{"shop/api-1": 1500.5, "shop/api-2": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if calls.Load() != 1 {
		t.Fatalf("requests = %d, want 1", calls.Load())
	}
	u := lastURL.Load().(url.URL)
	if u.Path != "/prom/api/v1/query" {
		t.Errorf("path = %q, want /prom/api/v1/query", u.Path)
	}
	if q := u.Query(); q.Get("query") != wantQuery || len(q) != 1 {
		t.Errorf("query params = %v, want only query=%s", q, wantQuery)
	}
}

func TestPodRecvBytesPerSecondErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{name: "HTTP 500 includes a short body snippet", status: http.StatusInternalServerError,
			body: "overloaded" + strings.Repeat("x", 1000), wantErr: `HTTP 500: "overloadedxxx`},
		{name: "API status error reports errorType and error", status: http.StatusOK,
			body: `{"status":"error","errorType":"bad_data","error":"parse error"}`, wantErr: "bad_data: parse error"},
		{name: "non-vector result is rejected", status: http.StatusOK,
			body: `{"status":"success","data":{"resultType":"matrix","result":[]}}`, wantErr: `result type "matrix"`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"status":`, wantErr: "decode response"},
		{name: "non-string sample value", status: http.StatusOK,
			body:    `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"namespace":"a","pod":"b"},"value":[1,2]}]}}`,
			wantErr: "sample a/b"},
		{name: "unparseable sample value", status: http.StatusOK,
			body:    `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"namespace":"a","pod":"b"},"value":[1,"abc"]}]}}`,
			wantErr: "sample a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			srv, _, _ := promTestServer(t, tt.status, tt.body)

			// Act
			_, err := NewPrometheus(srv.URL, nil).PodRecvBytesPerSecond(t.Context(), "5m")

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if len(err.Error()) > 2*promSnippetBytes {
				t.Errorf("error message is %d bytes, want a short snippet", len(err.Error()))
			}
		})
	}
}

func TestPodRecvBytesPerSecondRejectsBadWindowWithoutRequest(t *testing.T) {
	for _, window := range []string{"", "7", "7days", "1h30m", "5m])) or vector(1", "-1d", "99999999999999999999y"} {
		t.Run(window, func(t *testing.T) {
			// Arrange
			srv, calls, _ := promTestServer(t, http.StatusOK, promTestSuccessBody)

			// Act
			_, err := NewPrometheus(srv.URL, srv.Client()).PodRecvBytesPerSecond(t.Context(), window)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "invalid window") {
				t.Errorf("error = %v, want invalid window", err)
			}
			if calls.Load() != 0 {
				t.Errorf("requests = %d, want 0", calls.Load())
			}
		})
	}
}

func TestPodRecvBytesPerSecondWindowAgainstRateStep(t *testing.T) {
	tests := []struct {
		window  string
		wantErr bool
	}{
		{"1m", true},
		{"299s", true},
		{"299999ms", true},
		{"0y", true},
		{"5m", false},
		{"300s", false},
		{"1h", false},
		{"7d", false},
		{"2w", false},
		{"1y", false},
	}
	for _, tt := range tests {
		t.Run(tt.window, func(t *testing.T) {
			// Arrange
			srv, calls, _ := promTestServer(t, http.StatusOK, promTestSuccessBody)

			// Act
			_, err := NewPrometheus(srv.URL, srv.Client()).PodRecvBytesPerSecond(t.Context(), tt.window)

			// Assert
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "shorter than the 5m rate step") || calls.Load() != 0 {
					t.Errorf("error = %v after %d requests, want a step error and no request", err, calls.Load())
				}
				return
			}
			if err != nil || calls.Load() != 1 {
				t.Errorf("error = %v after %d requests, want the query sent", err, calls.Load())
			}
		})
	}
}

func TestPodRecvBytesPerSecondRejectsBadBaseURL(t *testing.T) {
	for _, base := range []string{"localhost:9090", "ftp://prom", "http://", "http://[::1"} {
		t.Run(base, func(t *testing.T) {
			_, err := NewPrometheus(base, nil).PodRecvBytesPerSecond(t.Context(), "7d")
			if err == nil || !strings.Contains(err.Error(), "base URL") {
				t.Errorf("error = %v, want base URL error", err)
			}
		})
	}
}

func TestPodRecvBytesPerSecondNeverLeaksURLCredentials(t *testing.T) {
	closed, _, _ := promTestServer(t, http.StatusOK, promTestSuccessBody)
	closed.Close()
	withCreds := strings.Replace(closed.URL, "http://", "http://alice:s3cret@", 1)
	tests := []struct {
		name    string
		base    string
		wantErr string
		secrets []string
	}{
		{name: "caret in password fails to parse", base: "http://alice:p^ss{q@prom.internal:9090",
			wantErr: "base URL", secrets: []string{"alice", "p^ss"}},
		{name: "hash in password truncates the URL", base: "http://alice:s3cr#et@prom.internal:9090",
			wantErr: "base URL", secrets: []string{"alice", "s3cr"}},
		{name: "bad percent escape in password", base: "http://alice:p%zz@prom.internal:9090",
			wantErr: "base URL", secrets: []string{"alice", "p%zz"}},
		{name: "non-http scheme with credentials", base: "ftp://alice:s3cret@prom.internal",
			wantErr: "base URL", secrets: []string{"alice", "s3cret", "xxxxx"}},
		{name: "transport error from net/http", base: withCreds,
			wantErr: "prometheus: query", secrets: []string{"alice", "s3cret", "***"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			_, err := NewPrometheus(tt.base, nil).PodRecvBytesPerSecond(t.Context(), "7d")

			// Assert
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			for _, secret := range tt.secrets {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q leaks %q", err, secret)
				}
			}
		})
	}
}

func TestPromRedactURLError(t *testing.T) {
	cause := errors.New("connection refused")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "non-URL error is unchanged", err: cause, want: "connection refused"},
		{name: "userinfo is stripped", err: &url.Error{Op: "Get", URL: "http://alice:***@prom:9090/api", Err: cause},
			want: `Get "http://prom:9090/api": connection refused`},
		{name: "unparseable URL is replaced", err: &url.Error{Op: "Get", URL: "http://alice:p^ss@prom", Err: cause},
			want: `Get "<redacted>": connection refused`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := promRedactURLError(tt.err)

			// Assert
			if got.Error() != tt.want || !errors.Is(got, cause) {
				t.Errorf("promRedactURLError() = %q, want %q wrapping the cause", got, tt.want)
			}
		})
	}
}

func TestPodRecvBytesPerSecondReportsTransportError(t *testing.T) {
	// Arrange
	srv, _, _ := promTestServer(t, http.StatusOK, promTestSuccessBody)
	base := srv.URL
	srv.Close()

	// Act
	_, err := NewPrometheus(base, nil).PodRecvBytesPerSecond(t.Context(), "7d")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "prometheus: query") {
		t.Fatalf("error = %v, want transport error", err)
	}
}
