package classify

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	annIngressClass       = "kubernetes.io/ingress.class"
	annALBTargetType      = "alb.ingress.kubernetes.io/target-type"
	annALBScheme          = "alb.ingress.kubernetes.io/scheme"
	annLBType             = "service.beta.kubernetes.io/aws-load-balancer-type"
	annNLBTargetType      = "service.beta.kubernetes.io/aws-load-balancer-nlb-target-type"
	annLBAttributes       = "service.beta.kubernetes.io/aws-load-balancer-attributes"
	annTGAttributes       = "service.beta.kubernetes.io/aws-load-balancer-target-group-attributes"
	annLBCrossZone        = "service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"
	annLBScheme           = "service.beta.kubernetes.io/aws-load-balancer-scheme"
	annLBInternal         = "service.beta.kubernetes.io/aws-load-balancer-internal"
	annTopologyMode       = "service.kubernetes.io/topology-mode"
	annTopologyAwareHints = "service.kubernetes.io/topology-aware-hints"
	annServiceUpstream    = "nginx.ingress.kubernetes.io/service-upstream"

	attrCrossZone         = "load_balancing.cross_zone.enabled"
	lbcALBController      = "ingress.k8s.aws/alb"
	autoModeALBController = "eks.amazonaws.com/alb"
	autoModeNLBClass      = "eks.amazonaws.com/nlb"
	albClassName          = "alb"
	hintsDisabled         = "disabled"
	serviceUpstreamOn     = "true"

	lbTypeNLB      = "nlb"
	lbTypeNLBIP    = "nlb-ip"
	lbTypeExternal = "external"
	lbKindALB      = "alb"
	lbKindNLB      = "nlb"
	lbKindCLB      = "clb"

	schemeInternal       = "internal"
	schemeInternetFacing = "internet-facing"
	targetTypeInstance   = "instance"
	targetTypeIP         = "ip"
	policyCluster        = "Cluster"
	policyLocal          = "Local"

	serviceTypeLoadBalancer = "LoadBalancer"
	serviceTypeExternalName = "ExternalName"
	clusterIPNone           = "None"

	// kube-proxy uses EndpointSlice hints unconditionally from this minor...
	kubeProxyHintsMinMinor = 31
	// ...and from this minor only when the service opts in via annotation.
	kubeProxyAnnotatedHintsMinMinor = 24
)

var (
	nlbClasses     = []string{"service.k8s.aws/nlb", autoModeNLBClass}
	albControllers = []string{lbcALBController, autoModeALBController}
)

// serviceRoute is how traffic reaches a service, before it becomes Facts.
type serviceRoute struct {
	route      model.RouteKind
	scheme     string
	targetType string
	etp        string
	crossZone  *bool    // NLB cross-zone from annotations (or the in-tree default); nil = unknown
	hostnames  []string // LB DNS names from status, matched against L1
	source     model.Workload
	evidence   string
	sameVPC    bool
}

func servicePaths(s model.Snapshot, callers model.AZDistribution) []model.Path {
	var out []model.Path
	for _, svc := range s.Services {
		if p, ok := servicePath(s, svc, callers); ok {
			out = append(out, p)
		}
	}
	return out
}

func servicePath(s model.Snapshot, svc model.Service, callers model.AZDistribution) (model.Path, bool) {
	ready := readyEndpoints(svc.Endpoints)
	if svc.Type == serviceTypeExternalName || len(ready) == 0 {
		return model.Path{}, false
	}
	sr := withLoadBalancer(decideRoute(s, svc, callers), s.AWS)
	zones := endpointZones(ready, s.AWS)
	dests, unknown := zoneDist(zones)
	hints := kubeProxyOnPath(sr) && kubeProxyUsesHints(s.KubeProxyVersion, svc.Annotations) && allHinted(ready)
	var hintedFor map[string]model.AZDistribution
	if hints {
		hintedFor = hintedForZones(ready, zones)
	}
	nodeLocal := sr.route == model.RouteClusterIP && svc.InternalTrafficPolicy == policyLocal
	// ZoneLocal is judged against node callers: kube-proxy filters on the nodes, whatever the LB zones are.
	facts := model.Facts{
		Route:                 sr.route,
		TargetKind:            model.TargetService,
		Scheme:                sr.scheme,
		TargetType:            sr.targetType,
		ExternalTrafficPolicy: sr.etp,
		Traffic:               trafficClient,
		SameVPC:               sr.sameVPC,
		PrivateIP:             allPrivateIPv4(ready),
		AZSpread:              azSpread(spreadCallers(sr, callers), dests, unknown),
		ZoneCoverage:          zoneCoverage(sr.source.AZDist, dests, unknown),
		TopologyHints:         hints,
		ZoneLocal:             nodeLocal || hints && unknown == 0 && model.HintedCrossAZFraction(callers, dests, hintedFor) == 0,
		Evidence:              sr.evidence,
		HintsConfigured:       hintsConfigured(svc),
		DirectIngressBackend:  directIngressBackend(s, svc),
	}
	return model.Path{
		Source: sr.source,
		Dest:   model.Target{Kind: model.TargetService, Ref: svc.Namespace + "/" + svc.Name, AZDist: dests, HintedFor: hintedFor},
		Route:  sr.route,
		Facts:  facts,
	}, true
}

// spreadCallers is where traffic toward the endpoints starts. A cross-zone (or possibly cross-zone) NLB node
// forwards from every LB zone, so those zones count too. Other LB legs reach the endpoints from the nodes
// (ALB/CLB to node is free; a zonal NLB node only serves its own zone), so they keep node callers.
func spreadCallers(sr serviceRoute, callers model.AZDistribution) model.AZDistribution {
	if sr.route != model.RouteNLBXZ && sr.route != model.RouteNLB {
		return callers
	}
	out := model.AZDistribution{}
	maps.Copy(out, callers)
	maps.Copy(out, sr.source.AZDist)
	return out
}

func kubeProxyOnPath(sr serviceRoute) bool {
	switch sr.route {
	case model.RouteClusterIP:
		return true
	case model.RouteHeadless, model.RouteExternal:
		return false
	default:
		return sr.targetType == targetTypeInstance && sr.etp != policyLocal
	}
}

func kubeProxyUsesHints(kubeProxyVersion string, ann map[string]string) bool {
	minor, ok := minorVersion(kubeProxyVersion)
	switch {
	case !ok || minor < kubeProxyAnnotatedHintsMinMinor:
		return false
	case minor >= kubeProxyHintsMinMinor:
		return true
	}
	return topologyAnnotationEnabled(ann)
}

func topologyAnnotationEnabled(ann map[string]string) bool {
	mode := cmp.Or(ann[annTopologyMode], ann[annTopologyAwareHints])
	return mode != "" && !strings.EqualFold(mode, hintsDisabled)
}

// hintsConfigured reports whether the Service asks for zone routing, whether or not kube-proxy honors it.
func hintsConfigured(svc model.Service) bool {
	return svc.TrafficDistribution != "" || topologyAnnotationEnabled(svc.Annotations)
}

// directIngressBackend reports whether a non-ALB Ingress routes to the service. Such controllers
// (ingress-nginx by default) dial EndpointSlice pod IPs, bypassing kube-proxy and its zone hints.
func directIngressBackend(s model.Snapshot, svc model.Service) bool {
	return slices.ContainsFunc(s.Ingresses, func(ing model.Ingress) bool {
		return ing.Namespace == svc.Namespace && slices.Contains(ing.Services, svc.Name) &&
			!isALB(ing, s.IngressClasses) && ing.Annotations[annServiceUpstream] != serviceUpstreamOn
	})
}

func decideRoute(s model.Snapshot, svc model.Service, callers model.AZDistribution) serviceRoute {
	if ing, ok := albIngress(s, svc); ok {
		controller, _ := albControllerOf(ing, s.IngressClasses)
		return albServiceRoute(svc, ing, controller, callers)
	}
	switch {
	case svc.Type == serviceTypeLoadBalancer && isNLB(svc):
		return nlbServiceRoute(svc, callers)
	case svc.Type == serviceTypeLoadBalancer && svc.LoadBalancerClass == "":
		return lbServiceRoute(model.RouteCLB, lbKindCLB, svc, callers)
	case svc.Type == serviceTypeLoadBalancer, svc.ClusterIP != clusterIPNone:
		return inClusterRoute(model.RouteClusterIP, callers)
	default:
		return inClusterRoute(model.RouteHeadless, callers)
	}
}

func inClusterRoute(route model.RouteKind, callers model.AZDistribution) serviceRoute {
	return serviceRoute{route: route, source: clusterSource(callers), evidence: model.EvidenceK8s, sameVPC: true}
}

// albIngress returns the name-first ALB Ingress in the service's namespace that routes to it.
func albIngress(s model.Snapshot, svc model.Service) (model.Ingress, bool) {
	var matches []model.Ingress
	for _, ing := range s.Ingresses {
		if ing.Namespace == svc.Namespace && slices.Contains(ing.Services, svc.Name) && isALB(ing, s.IngressClasses) {
			matches = append(matches, ing)
		}
	}
	if len(matches) == 0 {
		return model.Ingress{}, false
	}
	return slices.MinFunc(matches, func(a, b model.Ingress) int { return cmp.Compare(a.Name, b.Name) }), true
}

func isALB(ing model.Ingress, classes []model.IngressClass) bool {
	_, ok := albControllerOf(ing, classes)
	return ok
}

// albControllerOf returns the ALB controller serving the Ingress; ok is false when no ALB controller does.
func albControllerOf(ing model.Ingress, classes []model.IngressClass) (string, bool) {
	annotation := ing.Annotations[annIngressClass]
	pick := func(c model.IngressClass) bool { return c.Name == ing.ClassName }
	switch {
	case ing.ClassName == "" && annotation != "":
		// Only the self-managed controller honors the legacy annotation.
		return lbcALBController, annotation == albClassName
	case ing.ClassName == "":
		// The default class applies only when the Ingress names no class at all.
		pick = func(c model.IngressClass) bool { return c.IsDefault && slices.Contains(albControllers, c.Controller) }
	}
	i := slices.IndexFunc(classes, pick)
	if i < 0 {
		// Without an IngressClass object, class "alb" is the self-managed controller's convention.
		return lbcALBController, ing.ClassName == albClassName
	}
	return classes[i].Controller, slices.Contains(albControllers, classes[i].Controller)
}

func albServiceRoute(svc model.Service, ing model.Ingress, controller string, callers model.AZDistribution) serviceRoute {
	defaultTargetType := targetTypeInstance
	if controller == autoModeALBController {
		defaultTargetType = targetTypeIP // EKS Auto Mode default
	}
	return serviceRoute{
		route:      model.RouteALB,
		scheme:     cmp.Or(ing.Annotations[annALBScheme], schemeInternal),
		targetType: cmp.Or(svc.Annotations[annALBTargetType], ing.Annotations[annALBTargetType], defaultTargetType),
		etp:        cmp.Or(svc.ExternalTrafficPolicy, policyCluster),
		hostnames:  ing.LBHostnames,
		source:     model.Workload{Namespace: ing.Namespace, Name: ing.Namespace + "/" + ing.Name, Kind: lbKindALB, AZDist: maps.Clone(callers)},
		evidence:   model.EvidenceAnnotation,
		sameVPC:    true,
	}
}

func isNLB(svc model.Service) bool {
	if slices.Contains(nlbClasses, svc.LoadBalancerClass) {
		return true
	}
	switch svc.Annotations[annLBType] {
	case lbTypeNLB, lbTypeNLBIP, lbTypeExternal:
		return svc.LoadBalancerClass == ""
	}
	return false
}

func nlbServiceRoute(svc model.Service, callers model.AZDistribution) serviceRoute {
	lbType := svc.Annotations[annLBType]
	crossZone := crossZoneAnnotation(svc.Annotations)
	if crossZone == nil && lbType == lbTypeNLB && svc.LoadBalancerClass == "" {
		// The in-tree controller sets cross-zone off when the annotation is absent.
		crossZone = boolPtr(false)
	}
	defaultTargetType := targetTypeInstance
	if lbType == lbTypeNLBIP || svc.LoadBalancerClass == autoModeNLBClass {
		defaultTargetType = targetTypeIP // EKS Auto Mode defaults to ip targets
	}
	sr := lbServiceRoute(routeForCrossZone(crossZone), lbKindNLB, svc, callers)
	sr.crossZone = crossZone
	sr.targetType = cmp.Or(svc.Annotations[annNLBTargetType], defaultTargetType)
	return sr
}

// lbServiceRoute is a type=LoadBalancer route as seen from annotations only (instance targets).
func lbServiceRoute(route model.RouteKind, kind string, svc model.Service, callers model.AZDistribution) serviceRoute {
	return serviceRoute{
		route:      route,
		scheme:     lbScheme(svc),
		targetType: targetTypeInstance,
		etp:        cmp.Or(svc.ExternalTrafficPolicy, policyCluster),
		hostnames:  svc.LBHostnames,
		source:     model.Workload{Namespace: svc.Namespace, Name: svc.Namespace + "/" + svc.Name, Kind: kind, AZDist: maps.Clone(callers)},
		evidence:   model.EvidenceAnnotation,
		sameVPC:    true,
	}
}

func lbScheme(svc model.Service) string {
	if v := svc.Annotations[annLBScheme]; v != "" {
		return v
	}
	lbType := svc.Annotations[annLBType]
	if svc.LoadBalancerClass != "" || lbType == lbTypeExternal || lbType == lbTypeNLBIP {
		return schemeInternal // AWS Load Balancer Controller default
	}
	// The in-tree provider treats any value but "false" as internal (older docs used "0.0.0.0/0").
	if v := svc.Annotations[annLBInternal]; v != "" && v != "false" {
		return schemeInternal
	}
	return schemeInternetFacing
}

// crossZoneAnnotation mirrors effectiveCrossZone: the target group attribute wins over the LB attributes.
func crossZoneAnnotation(ann map[string]string) *bool {
	return cmp.Or(crossZoneAttribute(ann[annTGAttributes]), crossZoneAttribute(ann[annLBAttributes]), parseBool(ann[annLBCrossZone]))
}

// crossZoneAttribute reads load_balancing.cross_zone.enabled from a comma-separated key=value list.
func crossZoneAttribute(attrs string) *bool {
	for _, attr := range strings.Split(attrs, ",") {
		key, value, ok := strings.Cut(attr, "=")
		if !ok || strings.TrimSpace(key) != attrCrossZone {
			continue
		}
		if b := parseBool(strings.TrimSpace(value)); b != nil {
			return b
		}
	}
	return nil
}

func parseBool(s string) *bool {
	switch s {
	case "true":
		return boolPtr(true)
	case "false":
		return boolPtr(false)
	}
	return nil
}

func boolPtr(b bool) *bool { return &b }

func routeForCrossZone(crossZone *bool) model.RouteKind {
	switch {
	case crossZone == nil:
		return model.RouteNLB
	case *crossZone:
		return model.RouteNLBXZ
	default:
		return model.RouteNLBZonal
	}
}

// withLoadBalancer corrects an LB route with the matching L1 load balancer, when there is one.
func withLoadBalancer(sr serviceRoute, aws *model.AWSInventory) serviceRoute {
	if sr.source.Kind == sourceKindCluster || aws == nil {
		return sr
	}
	lb, ok := findLoadBalancer(aws.LoadBalancers, sr.hostnames)
	if !ok {
		return sr
	}
	out := sr
	out.scheme = cmp.Or(lb.Scheme, sr.scheme)
	out.sameVPC = slices.Contains(aws.VPCIDs, lb.VPCID)
	out.evidence = model.EvidenceAWS
	out.source = model.Workload{Namespace: sr.source.Namespace, Name: lb.Name, Kind: sr.source.Kind, AZDist: lbZoneDist(lb.Zones, sr.source.AZDist)}
	if tt, ok := commonTargetType(lb.TargetGroups); ok {
		out.targetType = tt
	}
	if sr.source.Kind == lbKindNLB {
		out.route = routeForCrossZone(effectiveCrossZone(lb, sr.crossZone))
	}
	return out
}

func findLoadBalancer(lbs []model.LoadBalancer, hostnames []string) (model.LoadBalancer, bool) {
	for _, lb := range lbs {
		if lb.DNSName == "" {
			continue
		}
		if slices.ContainsFunc(hostnames, func(h string) bool { return strings.EqualFold(h, lb.DNSName) }) {
			return lb, true
		}
	}
	return model.LoadBalancer{}, false
}

// lbZoneDist gives each LB zone weight 1; with no zones it keeps the fallback distribution.
func lbZoneDist(zones []string, fallback model.AZDistribution) model.AZDistribution {
	dist := model.AZDistribution{}
	for _, z := range zones {
		if z != "" {
			dist[z] = 1
		}
	}
	if len(dist) == 0 {
		return maps.Clone(fallback)
	}
	return dist
}

func commonTargetType(tgs []model.TargetGroup) (string, bool) {
	if len(tgs) == 0 {
		return "", false
	}
	first := tgs[0].TargetType
	if first != targetTypeInstance && first != targetTypeIP {
		return "", false
	}
	for _, tg := range tgs[1:] {
		if tg.TargetType != first {
			return "", false
		}
	}
	return first, true
}

// effectiveCrossZone: any target group "true" wins; all "false" means off; otherwise the LB attribute,
// then the annotation-derived value.
func effectiveCrossZone(lb model.LoadBalancer, annotation *bool) *bool {
	allFalse := len(lb.TargetGroups) > 0
	for _, tg := range lb.TargetGroups {
		if tg.CrossZone == "true" {
			return boolPtr(true)
		}
		allFalse = allFalse && tg.CrossZone == "false"
	}
	switch {
	case allFalse:
		return boolPtr(false)
	case lb.CrossZone != nil:
		return boolPtr(*lb.CrossZone)
	default:
		return annotation
	}
}
