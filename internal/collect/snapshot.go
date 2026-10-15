package collect

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/leegun2000/crossaz/internal/model"
)

// WarnNoVPCs means L1 found none of the node instances: the credentials likely belong to another account or region.
const WarnNoVPCs = "L1: no VPC found for the node instances; skipping AWS lookups"

const (
	snapLayerK8s    = "L0"
	snapLayerAWS    = "L1"
	snapLayerVolume = "L2"
	snapLayerCost   = "L3"

	snapWarnNoInstanceIDs = "L1: nodes have no EC2 instance ID; skipping AWS lookups"
	snapWarnNoRegion      = "L3: region unknown; skipping Cost Explorer"
	snapWarnVolumeNoMatch = "L2: the Prometheus result has no endpoint pod of this cluster (another cluster, missing cAdvisor metrics, or scraping for less than 10 minutes)"
)

// GatherK8s collects the L0 snapshot. Object listing failures are fatal; version lookups only warn.
func GatherK8s(ctx context.Context, k K8sCollector, cluster string) (model.Snapshot, error) {
	s, err := snapListK8s(ctx, k)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("gather kubernetes objects: %w", err)
	}
	s.Cluster = cluster
	s.Region = snapMajorityRegion(s.Nodes)
	if s.ServerVersion, err = k.ServerVersion(ctx); err != nil {
		s.Warnings = append(s.Warnings, snapWarn(snapLayerK8s, "control plane version", err))
	}
	if s.KubeProxyVersion, err = k.KubeProxyVersion(ctx); err != nil {
		s.Warnings = append(s.Warnings, snapWarn(snapLayerK8s, "kube-proxy version", err))
	}
	return s, nil
}

// snapListK8s lists every object kind into a new snapshot, joined and sorted for determinism.
func snapListK8s(ctx context.Context, k K8sCollector) (model.Snapshot, error) {
	nodes, err := k.Nodes(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	services, err := k.Services(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	endpointSlices, err := k.EndpointSlices(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	pods, err := k.Pods(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	ingresses, err := k.Ingresses(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	classes, err := k.IngressClasses(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	return model.Snapshot{
		Nodes:          snapSorted(nodes, snapCompareNode),
		Services:       snapSorted(snapJoinEndpoints(services, endpointSlices), snapCompareService),
		Pods:           snapSorted(pods, snapComparePod),
		Ingresses:      snapSorted(ingresses, snapCompareIngress),
		IngressClasses: snapSorted(classes, snapCompareIngressClass),
	}, nil
}

// WithAWS returns a copy of s with the L1 inventory. Every AWS failure becomes a warning.
func WithAWS(ctx context.Context, s model.Snapshot, a AWSCollector) model.Snapshot {
	out := s
	out.AWS = nil
	ids := snapInstanceIDs(s.Nodes)
	if len(ids) == 0 {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnNoInstanceIDs)
		return out
	}
	vpcs, accounts, err := a.InstanceVPCs(ctx, ids)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerAWS, "node instance VPCs", err))
		return out
	}
	vpcs = snapSortedUnique(vpcs)
	if len(vpcs) == 0 {
		out.Warnings = snapAppendWarnings(s.Warnings, WarnNoVPCs)
		return out
	}
	inv, warnings := snapInventory(ctx, a, vpcs, s.Services)
	inv.Accounts = snapSortedUnique(accounts)
	out.AWS = &inv
	out.Warnings = snapAppendWarnings(s.Warnings, warnings...)
	return out
}

// WithVolume returns a copy of s with L2 per-pod receive volume, or a warning on failure.
// A result that matches none of the snapshot's endpoint pods is dropped, so it cannot pass for "measured 0";
// the warning names selector (the --prom-selector text, "" if none) as another possible cause.
func WithVolume(ctx context.Context, s model.Snapshot, v VolumeCollector, window, selector string) model.Snapshot {
	out := s
	out.Volume = nil
	recv, err := v.PodRecvBytesPerSecond(ctx, window)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerVolume, "pod receive volume", err))
		return out
	}
	if !snapVolumeMatchesEndpoints(s.Services, recv) {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnVolumeNoMatchFor(selector))
		return out
	}
	out.Volume = &model.VolumeData{Window: window, RecvBytesPerS: recv}
	return out
}

func snapWarnVolumeNoMatchFor(selector string) string {
	if selector == "" {
		return snapWarnVolumeNoMatch
	}
	return fmt.Sprintf("%s (--prom-selector %q applied)", snapWarnVolumeNoMatch, selector)
}

// snapVolumeMatchesEndpoints reports whether recv has any ready endpoint pod, or there is no such pod to match.
func snapVolumeMatchesEndpoints(services []model.Service, recv map[string]float64) bool {
	hasPodEndpoints := false
	for _, svc := range services {
		for _, ep := range svc.Endpoints {
			if !ep.Ready || ep.PodName == "" {
				continue
			}
			if _, ok := recv[svc.Namespace+"/"+ep.PodName]; ok {
				return true
			}
			hasPodEndpoints = true
		}
	}
	return !hasPodEndpoints
}

// WithBill returns a copy of s with the L3 daily bill for [from, to), filtered to the L1 accounts when known.
// The bill's warnings move to the snapshot's; an unknown bill is kept (it is "unknown", not missing), a failure only warns.
func WithBill(ctx context.Context, s model.Snapshot, c BillCollector, from, to time.Time) model.Snapshot {
	out := s
	out.Bill = nil
	if s.Region == "" {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnNoRegion)
		return out
	}
	var accounts []string
	if s.AWS != nil {
		accounts = s.AWS.Accounts
	}
	bill, err := c.DailyBill(ctx, s.Region, accounts, from, to)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerCost, "daily bill", err))
		return out
	}
	warnings := make([]string, 0, len(bill.Warnings))
	for _, w := range bill.Warnings {
		warnings = append(warnings, snapLayerCost+": "+w)
	}
	stored := bill
	stored.Warnings = nil
	out.Bill = &stored
	out.Warnings = snapAppendWarnings(s.Warnings, warnings...)
	return out
}

// snapInventory queries everything after the VPCs are known; each failed call leaves its part empty.
func snapInventory(ctx context.Context, a AWSCollector, vpcs []string, services []model.Service) (model.AWSInventory, []string) {
	var warnings []string
	succeeded := func(what string, err error) bool {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				warnings = append(warnings, snapWarn(snapLayerAWS, what, e))
			}
		} else if err != nil {
			warnings = append(warnings, snapWarn(snapLayerAWS, what, err))
		}
		return err == nil
	}
	inv := model.AWSInventory{
		VPCIDs:        vpcs,
		Subnets:       map[string]model.Subnet{},
		LoadBalancers: []model.LoadBalancer{},
		RDS:           []model.RDSInstance{},
		Caches:        []model.CacheCluster{},
		MSK:           []model.MSKCluster{},
	}
	if subnets, err := a.Subnets(ctx, vpcs); succeeded("subnets", err) {
		inv.Subnets = subnets
	}
	if lbs, err := a.LoadBalancers(ctx, vpcs); succeeded("load balancers", err) || lbs != nil {
		inv.LoadBalancers = lbs
	}
	if rds, err := a.RDSInstances(ctx); succeeded("RDS instances", err) {
		inv.RDS = snapInVPCs(rds, vpcs, func(r model.RDSInstance) string { return r.VPCID })
	}
	if caches, err := a.ElastiCacheClusters(ctx); succeeded("ElastiCache clusters", err) {
		inv.Caches = snapInVPCs(caches, vpcs, func(c model.CacheCluster) string { return c.VPCID })
	}
	if msk, err := a.MSKClusters(ctx, inv.Subnets); succeeded("MSK clusters", err) {
		inv.MSK = snapInVPCs(msk, vpcs, func(m model.MSKCluster) string { return m.VPCID })
	}
	if ips := snapUnzonedIPs(services); len(ips) > 0 {
		if zones, err := a.IPZones(ctx, vpcs, ips); succeeded("endpoint IP AZs", err) {
			inv.IPZones = zones
		}
	}
	return inv, warnings
}

// snapJoinEndpoints attaches EndpointSlice endpoints to their services by (namespace, name).
func snapJoinEndpoints(services []model.Service, endpointSlices []EndpointSlice) []model.Service {
	byService := make(map[string][]model.Endpoint, len(endpointSlices))
	for _, es := range endpointSlices {
		key := es.Namespace + "/" + es.ServiceName
		byService[key] = append(byService[key], es.Endpoints...)
	}
	out := make([]model.Service, 0, len(services))
	for _, svc := range services {
		svc.Endpoints = snapDedupeEndpoints(byService[svc.Namespace+"/"+svc.Name])
		out = append(out, svc)
	}
	return out
}

// snapDedupeEndpoints keeps one endpoint per IP (ready first, then one naming its pod), sorted by IP.
func snapDedupeEndpoints(eps []model.Endpoint) []model.Endpoint {
	byIP := make(map[string]model.Endpoint, len(eps))
	for _, ep := range eps {
		if cur, ok := byIP[ep.IP]; !ok || snapPreferEndpoint(ep, cur) {
			byIP[ep.IP] = ep
		}
	}
	out := make([]model.Endpoint, 0, len(byIP))
	for _, ep := range byIP {
		out = append(out, ep)
	}
	slices.SortFunc(out, func(a, b model.Endpoint) int { return strings.Compare(a.IP, b.IP) })
	return out
}

func snapPreferEndpoint(candidate, current model.Endpoint) bool {
	if candidate.Ready != current.Ready {
		return candidate.Ready
	}
	return candidate.PodName != "" && current.PodName == ""
}

// snapMajorityRegion returns the most common node region; ties go to the lexicographically smallest.
func snapMajorityRegion(nodes []model.Node) string {
	counts := map[string]int{}
	for _, n := range nodes {
		if n.Region != "" {
			counts[n.Region]++
		}
	}
	best := ""
	for region, c := range counts {
		if c > counts[best] || (c == counts[best] && region < best) {
			best = region
		}
	}
	return best
}

func snapInstanceIDs(nodes []model.Node) []string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.InstanceID)
	}
	return snapSortedUnique(ids)
}

// snapUnzonedIPs returns the sorted unique IPs of ready endpoints that have no zone.
func snapUnzonedIPs(services []model.Service) []string {
	var ips []string
	for _, svc := range services {
		for _, ep := range svc.Endpoints {
			if ep.Ready && ep.Zone == "" {
				ips = append(ips, ep.IP)
			}
		}
	}
	return snapSortedUnique(ips)
}

func snapInVPCs[T any](items []T, vpcs []string, vpcOf func(T) string) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		if slices.Contains(vpcs, vpcOf(item)) {
			out = append(out, item)
		}
	}
	return out
}

// snapSortedUnique returns the sorted distinct non-empty values as a new slice.
func snapSortedUnique(values []string) []string {
	out := slices.DeleteFunc(slices.Clone(values), func(v string) bool { return v == "" })
	slices.Sort(out)
	return slices.Compact(out)
}

// snapSorted returns a sorted copy; never nil so fixtures serialize empty lists as [].
func snapSorted[T any](items []T, compare func(a, b T) int) []T {
	out := append(make([]T, 0, len(items)), items...)
	slices.SortFunc(out, compare)
	return out
}

func snapCompareNode(a, b model.Node) int { return strings.Compare(a.Name, b.Name) }

func snapCompareIngressClass(a, b model.IngressClass) int { return strings.Compare(a.Name, b.Name) }

func snapCompareService(a, b model.Service) int {
	return snapCompareNamespaced(a.Namespace, a.Name, b.Namespace, b.Name)
}

func snapComparePod(a, b model.Pod) int {
	return snapCompareNamespaced(a.Namespace, a.Name, b.Namespace, b.Name)
}

func snapCompareIngress(a, b model.Ingress) int {
	return snapCompareNamespaced(a.Namespace, a.Name, b.Namespace, b.Name)
}

func snapCompareNamespaced(nsA, nameA, nsB, nameB string) int {
	return cmp.Or(strings.Compare(nsA, nsB), strings.Compare(nameA, nameB))
}

func snapWarn(layer, what string, err error) string {
	return fmt.Sprintf("%s: %s: %v", layer, what, err)
}

func snapAppendWarnings(base []string, extra ...string) []string {
	return append(slices.Clone(base), extra...)
}
