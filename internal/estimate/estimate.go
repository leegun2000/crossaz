// Package estimate turns classified paths into monthly volume/cost estimates and a scan summary.
package estimate

import (
	"fmt"

	"github.com/leegun2000/crossaz/internal/model"
)

// MonthSeconds is the length of a 30-day month.
const MonthSeconds = 30 * 24 * 3600

const bytesPerGB = 1e9

const (
	noteExternal    = "target outside the cluster: traffic and volume unknown"
	noteNoVolume    = "no L2: estimate volume with --prometheus-url"
	noteNoMetrics   = "no metrics for the endpoint pods"
	noteNoZones     = "endpoint AZs unknown: cannot compute the cross-AZ fraction"
	noteLowNoAmount = "low verdict confidence: amount omitted"
)

// Estimate fills CrossAZFrac, BytesPerMo, CostPerMo and adjusts Confidence for classified paths.
// It returns a new slice; inputs are not modified.
func Estimate(paths []model.Path, s model.Snapshot) []model.Path {
	idx := newPodIndex(s)
	out := make([]model.Path, len(paths))
	for i, p := range paths {
		out[i] = estimatePath(p, s.Volume, idx)
	}
	return out
}

func estimatePath(p model.Path, vol *model.VolumeData, idx podIndex) model.Path {
	p.CrossAZFrac = crossAZFrac(p)
	p.BytesPerMo = nil
	p.CostPerMo = nil
	switch {
	case p.Billable != model.TriTrue:
		return withObservedBytes(p, vol, idx)
	case p.Dest.Kind != model.TargetService:
		p.Confidence = model.Low
		p.ConfidenceNote = noteExternal
		return p
	case p.Dest.AZDist.Total() == 0:
		p.Confidence = model.Low
		p.ConfidenceNote = noteNoZones
		return p
	case vol == nil:
		p.ConfidenceNote = noteNoVolume
		return p
	}

	recv, measured, total := idx.recvBytesPerS(p.Dest.Ref, vol.RecvBytesPerS)
	if measured == 0 {
		p.Confidence = model.Low
		p.ConfidenceNote = noteNoMetrics
		return p
	}
	if measured < total {
		recv = recv * float64(total) / float64(measured)
		p.ConfidenceNote = fmt.Sprintf("%d/%d pods measured", measured, total)
	}
	bytes := recv * MonthSeconds
	p.BytesPerMo = &bytes
	// "When in doubt, mark it Low and leave the amount blank": a low-confidence verdict never carries a number.
	if p.Confidence == model.Low {
		p.ConfidenceNote = noteLowNoAmount
		return p
	}
	cost := bytes / bytesPerGB * p.CrossAZFrac * p.RateGB
	p.CostPerMo = &cost
	p.Confidence = model.MinLevel(p.Confidence, model.Medium)
	return p
}

// withObservedBytes shows how much a non-billable or unknown-billable service path carries ("6 TB through the
// ALB, free"). Confidence and cost are untouched: the verdict does not depend on volume.
func withObservedBytes(p model.Path, vol *model.VolumeData, idx podIndex) model.Path {
	if vol == nil || p.Dest.Kind != model.TargetService {
		return p
	}
	recv, measured, total := idx.recvBytesPerS(p.Dest.Ref, vol.RecvBytesPerS)
	if measured == 0 {
		return p
	}
	bytes := recv * float64(total) / float64(measured) * MonthSeconds
	p.BytesPerMo = &bytes
	return p
}

// hopOnlyRoutes are the routes whose only cross-AZ leg is the kube-proxy hop, the one topology hints and
// zone_local describe. On nlb-cross-zone/nlb the LB-to-node leg crosses whatever kube-proxy picks next.
var hopOnlyRoutes = map[model.RouteKind]bool{
	model.RouteClusterIP: true, model.RouteALB: true, model.RouteCLB: true, model.RouteNLBZonal: true,
}

func crossAZFrac(p model.Path) float64 {
	switch {
	case !hopOnlyRoutes[p.Route]:
		return model.CrossAZFraction(p.Source.AZDist, p.Dest.AZDist)
	case p.Facts.ZoneLocal:
		return 0
	case p.Facts.TopologyHints:
		return model.HintedCrossAZFraction(p.Source.AZDist, p.Dest.AZDist, p.Dest.HintedFor)
	default:
		return model.CrossAZFraction(p.Source.AZDist, p.Dest.AZDist)
	}
}

// podIndex joins services, their ready endpoint pods and pod attributes.
type podIndex struct {
	servicePods map[string][]string // "ns/svc" -> distinct "ns/pod" of ready endpoints, in endpoint order
	podServices map[string]int      // "ns/pod" -> number of services with it as a ready endpoint
	hostNetwork map[string]bool     // "ns/pod" -> true for hostNetwork pods
}

func newPodIndex(s model.Snapshot) podIndex {
	idx := podIndex{
		servicePods: make(map[string][]string, len(s.Services)),
		podServices: map[string]int{},
		hostNetwork: map[string]bool{},
	}
	for _, svc := range s.Services {
		keys := readyPodKeys(svc)
		idx.servicePods[joinKey(svc.Namespace, svc.Name)] = keys
		for _, k := range keys {
			idx.podServices[k]++
		}
	}
	for _, pod := range s.Pods {
		if pod.HostNetwork {
			idx.hostNetwork[joinKey(pod.Namespace, pod.Name)] = true
		}
	}
	return idx
}

func readyPodKeys(svc model.Service) []string {
	seen := map[string]bool{}
	var keys []string
	for _, ep := range svc.Endpoints {
		if !ep.Ready || ep.PodName == "" {
			continue
		}
		k := joinKey(svc.Namespace, ep.PodName)
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys
}

// recvBytesPerS sums the measured receive rate of a service's endpoint pods, each pod split
// evenly across the services it backs. hostNetwork pods and pods without metrics are unmeasured.
func (idx podIndex) recvBytesPerS(ref string, recv map[string]float64) (sum float64, measured, total int) {
	pods := idx.servicePods[ref]
	for _, k := range pods {
		v, ok := recv[k]
		if !ok || idx.hostNetwork[k] {
			continue
		}
		sum += v / float64(idx.podServices[k])
		measured++
	}
	return sum, measured, len(pods)
}

func joinKey(namespace, name string) string {
	return namespace + "/" + name
}
