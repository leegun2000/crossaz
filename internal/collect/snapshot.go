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

const (
	snapLayerK8s    = "L0"
	snapLayerAWS    = "L1"
	snapLayerVolume = "L2"
	snapLayerCost   = "L3"

	snapWarnNoInstanceIDs = "L1: 노드에 EC2 instance ID가 없어 AWS 조회를 건너뜀"
	snapWarnNoVPCs        = "L1: 노드 인스턴스의 VPC를 찾지 못해 AWS 조회를 건너뜀"
	snapWarnNoRegion      = "L3: 리전을 알 수 없어 Cost Explorer 조회를 건너뜀"
	snapWarnVolumeNoMatch = "L2: Prometheus 결과에 이 클러스터의 엔드포인트 pod가 없음 — 다른 클러스터이거나 cAdvisor 메트릭 누락"
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
		s.Warnings = append(s.Warnings, snapWarn(snapLayerK8s, "컨트롤 플레인 버전", err))
	}
	if s.KubeProxyVersion, err = k.KubeProxyVersion(ctx); err != nil {
		s.Warnings = append(s.Warnings, snapWarn(snapLayerK8s, "kube-proxy 버전", err))
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
	vpcs, err := a.InstanceVPCs(ctx, ids)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerAWS, "노드 인스턴스 VPC", err))
		return out
	}
	vpcs = snapSortedUnique(vpcs)
	if len(vpcs) == 0 {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnNoVPCs)
		return out
	}
	inv, warnings := snapInventory(ctx, a, vpcs, s.Services)
	out.AWS = &inv
	out.Warnings = snapAppendWarnings(s.Warnings, warnings...)
	return out
}

// WithVolume returns a copy of s with L2 per-pod receive volume, or a warning on failure.
// A result that matches none of the snapshot's endpoint pods is dropped, so it cannot pass for "measured 0".
func WithVolume(ctx context.Context, s model.Snapshot, v VolumeCollector, window string) model.Snapshot {
	out := s
	out.Volume = nil
	recv, err := v.PodRecvBytesPerSecond(ctx, window)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerVolume, "pod 수신 볼륨", err))
		return out
	}
	if !snapVolumeMatchesEndpoints(s.Services, recv) {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnVolumeNoMatch)
		return out
	}
	out.Volume = &model.VolumeData{Window: window, RecvBytesPerS: recv}
	return out
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

// WithCost returns a copy of s with L3 actual inter-AZ cost for the month, or a warning on failure.
func WithCost(ctx context.Context, s model.Snapshot, c CostCollector, month time.Time) model.Snapshot {
	out := s
	out.Cost = nil
	if s.Region == "" {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarnNoRegion)
		return out
	}
	cost, err := c.InterAZDataTransferUSD(ctx, s.Region, month)
	if err != nil {
		out.Warnings = snapAppendWarnings(s.Warnings, snapWarn(snapLayerCost, "inter-AZ 실제 비용", err))
		return out
	}
	out.Cost = &cost
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
	if subnets, err := a.Subnets(ctx, vpcs); succeeded("서브넷", err) {
		inv.Subnets = subnets
	}
	if lbs, err := a.LoadBalancers(ctx, vpcs); succeeded("로드밸런서", err) || lbs != nil {
		inv.LoadBalancers = lbs
	}
	if rds, err := a.RDSInstances(ctx); succeeded("RDS 인스턴스", err) {
		inv.RDS = snapInVPCs(rds, vpcs, func(r model.RDSInstance) string { return r.VPCID })
	}
	if caches, err := a.ElastiCacheClusters(ctx); succeeded("ElastiCache 클러스터", err) {
		inv.Caches = snapInVPCs(caches, vpcs, func(c model.CacheCluster) string { return c.VPCID })
	}
	if msk, err := a.MSKClusters(ctx, inv.Subnets); succeeded("MSK 클러스터", err) {
		inv.MSK = snapInVPCs(msk, vpcs, func(m model.MSKCluster) string { return m.VPCID })
	}
	if ips := snapUnzonedIPs(services); len(ips) > 0 {
		if zones, err := a.IPZones(ctx, vpcs, ips); succeeded("엔드포인트 IP의 AZ", err) {
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
