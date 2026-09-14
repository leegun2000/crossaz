package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	promQueryFormat   = `sum by (namespace, pod) (max by (namespace, pod, interface) (rate(container_network_receive_bytes_total{pod!=""}[%s])))`
	promSnippetBytes  = 200
	promStatusSuccess = "success"
	promResultVector  = "vector"

	promErrInvalidBaseURL = "prometheus: base URL is invalid (percent-encode credentials)"
	promRedactedURL       = "<redacted>"
)

// promWindowRE guards the only user value interpolated into PromQL.
var promWindowRE = regexp.MustCompile(`^[0-9]+(ms|s|m|h|d|w|y)$`)

type promCollector struct {
	baseURL string
	hc      *http.Client
}

type promResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string       `json:"resultType"`
		Result     []promSample `json:"result"`
	} `json:"data"`
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"` // [unix time, "value"]
}

// NewPrometheus returns a VolumeCollector querying the Prometheus HTTP API at baseURL. nil hc uses http.DefaultClient.
func NewPrometheus(baseURL string, hc *http.Client) VolumeCollector {
	if hc == nil {
		hc = http.DefaultClient
	}
	return promCollector{baseURL: baseURL, hc: hc}
}

func (p promCollector) PodRecvBytesPerSecond(ctx context.Context, window string) (map[string]float64, error) {
	if !promWindowRE.MatchString(window) {
		return nil, fmt.Errorf("prometheus: invalid window %q: want a PromQL duration such as 7d", window)
	}
	endpoint, err := p.queryURL(fmt.Sprintf(promQueryFormat, window))
	if err != nil {
		return nil, err
	}
	resp, err := p.query(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	return promVector(resp)
}

func (p promCollector) queryURL(query string) (string, error) {
	base, err := url.Parse(p.baseURL)
	if err != nil {
		// url.Error text and its inner error both quote parts of the raw URL, password included.
		return "", errors.New(promErrInvalidBaseURL)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", fmt.Errorf("prometheus: base URL %q must be http(s)://host[/path]", promWithoutUserinfo(*base))
	}
	u := base.JoinPath("api", "v1", "query")
	u.RawQuery = url.Values{"query": {query}}.Encode()
	return u.String(), nil
}

func (p promCollector) query(ctx context.Context, endpoint string) (promResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return promResponse{}, fmt.Errorf("prometheus: build request: %w", promRedactURLError(err))
	}
	res, err := p.hc.Do(req)
	if err != nil {
		return promResponse{}, fmt.Errorf("prometheus: query: %w", promRedactURLError(err))
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, promSnippetBytes))
		return promResponse{}, fmt.Errorf("prometheus: query: HTTP %d: %q", res.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out promResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return promResponse{}, fmt.Errorf("prometheus: decode response: %w", err)
	}
	return out, nil
}

// promRedactURLError strips userinfo from a *url.Error's URL; net/http masks only the password.
func promRedactURLError(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	redacted := promRedactedURL
	if u, parseErr := url.Parse(ue.URL); parseErr == nil {
		redacted = promWithoutUserinfo(*u)
	}
	return &url.Error{Op: ue.Op, URL: redacted, Err: ue.Err}
}

// promWithoutUserinfo takes a copy so the caller's URL keeps its credentials.
func promWithoutUserinfo(u url.URL) string {
	u.User = nil
	return u.String()
}

// promVector maps "namespace/pod" to the sample value, skipping unlabeled or non-finite/negative samples.
func promVector(r promResponse) (map[string]float64, error) {
	if r.Status != promStatusSuccess {
		return nil, fmt.Errorf("prometheus: query status %q: %s: %s", r.Status, r.ErrorType, r.Error)
	}
	if r.Data.ResultType != promResultVector {
		return nil, fmt.Errorf("prometheus: result type %q, want %q", r.Data.ResultType, promResultVector)
	}
	out := make(map[string]float64, len(r.Data.Result))
	for _, s := range r.Data.Result {
		ns, pod := s.Metric["namespace"], s.Metric["pod"]
		if ns == "" || pod == "" {
			continue
		}
		raw, ok := s.Value[1].(string)
		if !ok {
			return nil, fmt.Errorf("prometheus: sample %s/%s: value %v is not a string", ns, pod, s.Value[1])
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("prometheus: sample %s/%s: %w", ns, pod, err)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		out[ns+"/"+pod] = v
	}
	return out, nil
}
