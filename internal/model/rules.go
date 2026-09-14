package model

import (
	"encoding/json"
	"fmt"
)

// RuleSet is rules/rules.yaml. Keys are snake_case (YAML is read via sigs.k8s.io/yaml -> json tags).
type RuleSet struct {
	Version       int                   `json:"version"`
	DefaultRateGB float64               `json:"default_rate_gb"`
	Regions       map[string]RegionRate `json:"regions,omitempty"`
	Rules         []Rule                `json:"rules"`
}

// RegionRate overrides the inter-AZ rate for a region.
type RegionRate struct {
	RateGB float64 `json:"rate_gb"`
}

// Rule is one billing rule. Rules are evaluated in declaration order; first match wins.
type Rule struct {
	ID         string       `json:"id"`
	Match      Match        `json:"match"`
	Billable   bool         `json:"billable"`
	RateGB     float64      `json:"rate_gb,omitempty"` // 0 = use region/default rate
	Confidence Level        `json:"confidence"`
	Reason     string       `json:"reason"`
	Source     string       `json:"source,omitempty"`
	Verified   string       `json:"verified,omitempty"` // YYYY-MM-DD the source was last checked; "" = unverified
	Fix        *FixTemplate `json:"fix,omitempty"`
}

// Match lists conditions; all non-empty conditions must hold (AND). A StringSet matches if the
// fact equals any listed value (OR). An empty Match matches everything (catch-all).
type Match struct {
	Route                 StringSet `json:"route,omitempty"`
	TargetKind            StringSet `json:"target_kind,omitempty"`
	Scheme                StringSet `json:"scheme,omitempty"`
	TargetType            StringSet `json:"target_type,omitempty"`
	ExternalTrafficPolicy StringSet `json:"external_traffic_policy,omitempty"`
	Traffic               StringSet `json:"traffic,omitempty"`
	AZSpread              StringSet `json:"az_spread,omitempty"`
	ZoneCoverage          StringSet `json:"zone_coverage,omitempty"`
	Evidence              StringSet `json:"evidence,omitempty"`
	SameVPC               *bool     `json:"same_vpc,omitempty"`
	PrivateIP             *bool     `json:"private_ip,omitempty"`
	TopologyHints         *bool     `json:"topology_hints,omitempty"`
	ZoneLocal             *bool     `json:"zone_local,omitempty"`
	HintsConfigured       *bool     `json:"hints_configured,omitempty"`
	DirectIngressBackend  *bool     `json:"direct_ingress_backend,omitempty"`
}

// FixTemplate is rendered with text/template against FixVars.
type FixTemplate struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Command     string `json:"command,omitempty"`
}

// FixVars are the fields available to fix command templates.
type FixVars struct {
	Namespace string
	Name      string
	Ref       string
	Kind      string
	// TrafficDistribution is "PreferSameZone" when the control plane is >= 1.34, else "PreferClose" (same semantics).
	TrafficDistribution string
}

// StringSet accepts either a scalar or a list in YAML/JSON.
type StringSet []string

// UnmarshalJSON implements json.Unmarshaler for scalar-or-list.
func (s *StringSet) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = StringSet{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected string or list of strings: %w", err)
	}
	*s = many
	return nil
}

// Has reports whether v is in the set.
func (s StringSet) Has(v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
