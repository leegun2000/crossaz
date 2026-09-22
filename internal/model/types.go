// Package model holds the plain data types shared by every layer.
// No I/O, no SDK types: collectors convert into these, classify/estimate/report consume them.
package model

// Level is a confidence level.
type Level string

const (
	High   Level = "high"   // rule is clear and volume is measurement-based (or volume irrelevant)
	Medium Level = "medium" // rule is clear, volume or routing is estimated
	Low    Level = "low"    // rule unsettled, traffic existence unknown, or volume unknown
)

// levelRank orders levels for Min.
var levelRank = map[Level]int{Low: 0, Medium: 1, High: 2}

// MinLevel returns the lowest of the given levels. Unknown/empty levels count as Low.
func MinLevel(levels ...Level) Level {
	out := High
	for _, l := range levels {
		r, ok := levelRank[l]
		if !ok {
			return Low
		}
		if r < levelRank[out] {
			out = l
		}
	}
	return out
}

// RouteKind is how traffic reaches the destination.
type RouteKind string

const (
	RouteClusterIP RouteKind = "clusterip"
	RouteALB       RouteKind = "alb"
	RouteCLB       RouteKind = "clb"
	RouteNLBXZ     RouteKind = "nlb-cross-zone"
	RouteNLBZonal  RouteKind = "nlb-zonal"
	RouteNLB       RouteKind = "nlb" // cross-zone setting unknown (no L1 and no explicit annotation)
	RouteHeadless  RouteKind = "headless"
	RouteExternal  RouteKind = "external" // RDS/ElastiCache/MSK
)

// Evidence says where the routing facts of a path came from.
const (
	EvidenceK8s        = "k8s"        // intrinsic to Kubernetes objects (ClusterIP, headless)
	EvidenceAWS        = "aws"        // confirmed by AWS read-only APIs (L1)
	EvidenceAnnotation = "annotation" // inferred from annotations/defaults only (no L1)
)

// Target kinds.
const (
	TargetService     = "service"
	TargetRDS         = "rds"
	TargetElastiCache = "elasticache"
	TargetMSK         = "msk"
)

// AZDistribution maps zone name -> count (endpoints, nodes, broker nodes...).
type AZDistribution map[string]int

// ZoneCount is the number of zones with a positive count.
func (d AZDistribution) ZoneCount() int {
	n := 0
	for _, c := range d {
		if c > 0 {
			n++
		}
	}
	return n
}

// Total is the sum of counts.
func (d AZDistribution) Total() int {
	t := 0
	for _, c := range d {
		if c > 0 {
			t += c
		}
	}
	return t
}

// IsEven reports whether max/min differ by at most 20% of max. A single zone is even.
func (d AZDistribution) IsEven() bool {
	lo, hi := -1, 0
	for _, c := range d {
		if c <= 0 {
			continue
		}
		if lo < 0 || c < lo {
			lo = c
		}
		if c > hi {
			hi = c
		}
	}
	if hi == 0 {
		return false
	}
	return float64(hi-lo) <= 0.2*float64(hi)
}

// CrossAZFraction estimates the share of traffic that crosses an AZ when callers are spread
// UNIFORMLY over the zones of `callers` and each request picks a destination uniformly by count:
//
//	1 - Σ_z (1/|callerZones|) × share_dest(z)
//
// This equals 1 - 1/n when every destination zone is a caller zone, and it is NOT zero when
// all destinations sit in one zone of a multi-zone cluster (callers from other zones cross).
// Returns 0 when either side is empty.
func CrossAZFraction(callers, dests AZDistribution) float64 {
	n := callers.ZoneCount()
	total := dests.Total()
	if n == 0 || total == 0 {
		return 0
	}
	local := 0.0
	for z, c := range callers {
		if c <= 0 {
			continue
		}
		if d := dests[z]; d > 0 {
			local += float64(d) / float64(total)
		}
	}
	return 1 - local/float64(n)
}

// HintedCrossAZFraction is CrossAZFraction when kube-proxy filters endpoints by topology hints:
// callers in zone z pick only endpoints hinted for z, and a zone with no hinted endpoint falls
// back to every destination. hintedFor maps caller zone -> zone distribution of endpoints hinted
// for it. Hints may point at other zones (topology-mode Auto rebalancing), which still crosses.
func HintedCrossAZFraction(callers, dests AZDistribution, hintedFor map[string]AZDistribution) float64 {
	n := callers.ZoneCount()
	if n == 0 {
		return 0
	}
	sum := 0.0
	for z, c := range callers {
		if c <= 0 {
			continue
		}
		pool := hintedFor[z]
		if pool.Total() == 0 {
			pool = dests
		}
		if t := pool.Total(); t > 0 {
			sum += 1 - float64(max(pool[z], 0))/float64(t)
		}
	}
	return sum / float64(n)
}

// Workload is the calling side of a path. For L0-L2 the real callers are unknown, so it is
// usually the whole cluster (Kind "cluster") or a load balancer (Kind "alb"/"nlb"/"clb").
type Workload struct {
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	AZDist    AZDistribution `json:"azDist"`
}

// Target is the destination side of a path.
type Target struct {
	Kind   string         `json:"kind"` // service|rds|elasticache|msk
	Ref    string         `json:"ref"`  // ns/name, or AWS identifier (never a full ARN in output)
	AZDist AZDistribution `json:"azDist"`
	// HintedFor maps caller zone -> zone distribution of ready endpoints hinted for it.
	// Set only when Facts.TopologyHints is true.
	HintedFor map[string]AZDistribution `json:"hintedFor,omitempty"`
}

// Facts are every attribute a rule may match on. Filled by classify.BuildPaths.
// Empty string means "unknown/not applicable"; a rule that requires a value never matches "".
type Facts struct {
	Route                 RouteKind `json:"route"`
	TargetKind            string    `json:"targetKind"`
	Scheme                string    `json:"scheme,omitempty"`                // internal|internet-facing
	TargetType            string    `json:"targetType,omitempty"`            // ip|instance
	ExternalTrafficPolicy string    `json:"externalTrafficPolicy,omitempty"` // Cluster|Local
	Traffic               string    `json:"traffic"`                         // client|replication
	SameVPC               bool      `json:"sameVpc"`
	PrivateIP             bool      `json:"privateIp"`      // every destination IP is private IPv4 (IPv6 exemptions are unverified)
	AZSpread              string    `json:"azSpread"`       // single (callers ∪ dests within one zone) | multi | unknown
	ZoneCoverage          string    `json:"zoneCoverage"`   // full (dest zones ⊇ caller zones) | partial | unknown
	KubeProxyHints        bool      `json:"kubeProxyHints"` // kube-proxy is on the path and its version is known to honor hints for this Service
	TopologyHints         bool      `json:"topologyHints"`  // KubeProxyHints AND every ready endpoint has hints.forZones (kube-proxy filters by zone)
	ZoneLocal             bool      `json:"zoneLocal"`      // internalTrafficPolicy Local, or TopologyHints with every caller zone served only by same-zone endpoints
	Evidence              string    `json:"evidence"`       // k8s|aws|annotation
	// HintsConfigured: the Service asks for zone routing (trafficDistribution, or a topology-mode /
	// topology-aware-hints annotation that is not "disabled"), whether or not kube-proxy honors it.
	HintsConfigured bool `json:"hintsConfigured"`
	// DirectIngressBackend: the Service backs a non-ALB Ingress that dials pod IPs (no service-upstream opt-in),
	// so that traffic bypasses kube-proxy and its zone hints.
	DirectIngressBackend bool `json:"directIngressBackend"`
}

// Path is the unit of judgment.
type Path struct {
	Source Workload  `json:"source"`
	Dest   Target    `json:"dest"`
	Route  RouteKind `json:"route"`
	Facts  Facts     `json:"facts"`

	// classify result
	Billable       Tri     `json:"billable"`
	RuleID         string  `json:"ruleId"`
	Reason         string  `json:"reason"`
	SourceURL      string  `json:"sourceUrl,omitempty"`
	RateGB         float64 `json:"rateGb"`         // USD per GB exchanged (both directions' charges combined)
	RuleConfidence Level   `json:"ruleConfidence"` // as declared by the matched rule

	// estimate result. nil = unknown (never print a number for unknown).
	BytesPerMo     *float64 `json:"bytesPerMo"` // bytes received by destination pods per month
	CrossAZFrac    float64  `json:"crossAzFraction"`
	CostPerMo      *float64 `json:"costPerMo"`
	Confidence     Level    `json:"confidence"` // final: min(rule, evidence, volume)
	ConfidenceNote string   `json:"confidenceNote,omitempty"`

	Fix *Fix `json:"fix,omitempty"`
}

// NoteLocalZone is the ConfidenceNote of a path touching a Local/Wavelength Zone: the parent region's RateGB
// does not apply there (billing-rules.md §1.2), so no amount or upper bound is computed from it.
const NoteLocalZone = "Local/Wavelength Zone 포함: 요율이 달라 금액 생략"

// Key is "namespace/name" for services, "kind/ref" otherwise. Used by `crossaz explain`.
func (p Path) Key() string {
	if p.Dest.Kind == TargetService {
		return p.Dest.Ref
	}
	return p.Dest.Kind + "/" + p.Dest.Ref
}

// Fix is a suggested remediation.
type Fix struct {
	Kind        string `json:"kind"` // PreferClose|topologySpread|disable-nlb-xz|zonal-dns|relocate
	Description string `json:"description"`
	Command     string `json:"command,omitempty"` // rendered kubectl/aws command
}

// Exposure values.
const (
	ExposureNone     = "none"
	ExposureUnlikely = "unlikely"
	ExposureLikely   = "likely"
)

// ScanResult is the final output of a scan.
type ScanResult struct {
	Cluster   string   `json:"cluster"`
	Region    string   `json:"region"`
	Zones     []string `json:"zones"`
	NodeCount int      `json:"nodeCount"`
	Layers    []string `json:"layers"` // e.g. ["L0","L1","L2"]
	Exposure  string   `json:"exposure"`
	Paths     []Path   `json:"paths"`     // Billable true && Confidence != low && not hidden, cost desc
	Excluded  []Path   `json:"excluded"`  // Billable false && Confidence != low
	Uncertain []Path   `json:"uncertain"` // Confidence == low, or Billable unknown
	Hidden    int      `json:"hidden"`    // billable paths with known cost below threshold
	Threshold float64  `json:"thresholdUsd"`
	TotalUSD  *float64 `json:"estimatedTotalUsd"` // sum of known costs in Paths + hidden; nil if no volume layer
	// UnconfirmedMaxUSD is what the Billable=unknown paths (in Uncertain) would cost if billable, never part of TotalUSD.
	// nil if there are none or any of them has no measured volume.
	UnconfirmedMaxUSD *float64      `json:"unconfirmedMaxUsd,omitempty"`
	Warnings          []string      `json:"warnings,omitempty"`
	Verify            *VerifyResult `json:"verify,omitempty"`
}

// Verify ratio bounds: outside them the estimate and the bill disagree enough to suspect the rules.
const (
	VerifyRatioHigh = 1.5
	VerifyRatioLow  = 0.5
)

// VerifyResult compares the estimate against the Cost Explorer daily bill (L3).
type VerifyResult struct {
	Window        string   `json:"window"`                  // closed UTC days of the bill, "YYYY-MM-DD..YYYY-MM-DD"
	Days          int      `json:"days"`                    // days summed into ActualUSD
	EstimatedDays int      `json:"estimatedDays,omitempty"` // of Days, not final (CE Estimated): ActualUSD is provisional
	UsageType     string   `json:"usageType"`               // usage types of the InterZone lines summed into ActualUSD
	EstimatedUSD  *float64 `json:"estimatedUsd"`
	ActualUSD     *float64 `json:"actualUsd"` // InterZone lines per 30-day month, whole account+region; nil = unknown
	Ratio         *float64 `json:"ratio"`     // estimated/actual; nil when either side unusable
	Note          string   `json:"note"`
}
