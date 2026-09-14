package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	awsIDHexLen = 17 // i-/vpc-/subnet- IDs carry 17 hex digits
	nameHexLen  = 12
	dnsHexLen   = 16

	warningSep   = ": "
	redactedMark = "<redacted>"

	ownerKindNode = "Node" // a mirror (static) pod's controller

	annLBAttributes = "service.beta.kubernetes.io/aws-load-balancer-attributes"
	annTGAttributes = "service.beta.kubernetes.io/aws-load-balancer-target-group-attributes"
	attrCrossZone   = "load_balancing.cross_zone.enabled"
)

var (
	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

	// classifyAnnotations are the only annotation keys classify reads. Fixtures keep nothing else:
	// other annotations (last-applied-configuration, certificate ARNs, bucket names) are noise or sensitive.
	classifyAnnotations = map[string]bool{
		"kubernetes.io/ingress.class":                                                    true,
		"alb.ingress.kubernetes.io/target-type":                                          true,
		"alb.ingress.kubernetes.io/scheme":                                               true,
		"service.beta.kubernetes.io/aws-load-balancer-type":                              true,
		"service.beta.kubernetes.io/aws-load-balancer-nlb-target-type":                   true,
		"service.beta.kubernetes.io/aws-load-balancer-attributes":                        true,
		"service.beta.kubernetes.io/aws-load-balancer-target-group-attributes":           true,
		"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": true,
		"service.beta.kubernetes.io/aws-load-balancer-scheme":                            true,
		"service.beta.kubernetes.io/aws-load-balancer-internal":                          true,
		"service.kubernetes.io/topology-mode":                                            true,
		"service.kubernetes.io/topology-aware-hints":                                     true,
		"nginx.ingress.kubernetes.io/service-upstream":                                   true,
	}
)

// Anonymize returns a copy of s with IPs, node names, instance/VPC/subnet IDs and load balancer
// (and target group) names and DNS names replaced by sha256-derived values. Within one snapshot the same
// input always maps to the same output, so joins survive: endpoint IP = pod IP = IPZones key,
// LB DNS name = Service/Ingress hostname, node name = pod/endpoint NodeName. Namespace, service and pod
// names are kept, except the node name inside a mirror pod's name; annotations classify does not read are dropped;
// warnings are scrubbed of replaced values and cut to their "<layer>: <what>" prefix.
func Anonymize(s model.Snapshot) model.Snapshot {
	a := &anonymizer{byKey: map[string]string{}, taken: map[string]bool{}}
	out := s
	out.Nodes = mapSlice(s.Nodes, a.node)
	a.mirrorPods = a.mirrorPodNames(s.Pods)
	out.Services = mapSlice(s.Services, a.service)
	out.Pods = mapSlice(s.Pods, a.pod)
	out.Ingresses = mapSlice(s.Ingresses, a.ingress)
	out.IngressClasses = slices.Clone(s.IngressClasses)
	if s.AWS != nil {
		inv := a.inventory(*s.AWS)
		out.AWS = &inv
	}
	if s.Volume != nil {
		v := *s.Volume
		v.RecvBytesPerS = mapSorted(s.Volume.RecvBytesPerS, func(key string, bps float64) (string, float64) {
			namespace, pod, ok := strings.Cut(key, "/")
			if !ok {
				return key, bps
			}
			return namespace + "/" + a.podName(namespace, pod), bps
		})
		out.Volume = &v
	}
	if s.Cost != nil {
		c := *s.Cost
		out.Cost = &c
	}
	out.Warnings = mapSlice(a.scrub(s.Warnings), redactErrorText)
	return out
}

// redactErrorText keeps a warning's fixed "<layer>: <what>" prefix and drops the rest: it is raw SDK or API
// error text that can quote account IDs, principal ARNs, session emails and local paths never seen elsewhere.
func redactErrorText(warning string) string {
	parts := strings.SplitN(warning, warningSep, 3)
	if len(parts) < 3 {
		return warning
	}
	return parts[0] + warningSep + parts[1] + warningSep + redactedMark
}

type anonymizer struct {
	byKey map[string]string // kind + NUL + original -> replacement
	taken map[string]bool   // replacements already handed out
	pairs []string          // original, replacement, ... for scrubbing free text
	// mirrorPods maps "namespace/pod" of mirror pods to their names with the node name replaced.
	mirrorPods map[string]string
}

// value returns the replacement of original, derived from sha256(kind, original, salt). The salt only
// grows on a collision, so distinct inputs never share an output.
func (a *anonymizer) value(kind, original string, format func(sum [sha256.Size]byte) string) string {
	if original == "" {
		return ""
	}
	key := kind + "\x00" + original
	if v, ok := a.byKey[key]; ok {
		return v
	}
	for salt := 0; ; salt++ {
		v := format(sha256.Sum256([]byte(key + "\x00" + strconv.Itoa(salt))))
		if a.taken[v] {
			continue
		}
		a.byKey[key], a.taken[v] = v, true
		a.pairs = append(a.pairs, original, v)
		return v
	}
}

func hexPrefix(prefix string, n int) func([sha256.Size]byte) string {
	return func(sum [sha256.Size]byte) string { return prefix + hex.EncodeToString(sum[:])[:n] }
}

// ip keeps the address family and whether classify sees it as private IPv4:
// private → 10.x.y.z, other IPv4 → 198.18.0.0/15, IPv6 → fd00::/8.
func (a *anonymizer) ip(original string) string {
	addr, err := netip.ParseAddr(original)
	switch {
	case err != nil:
		return a.value("ip", original, hexPrefix("ip-", nameHexLen))
	case addr.Is4() && (addr.IsPrivate() || cgnatPrefix.Contains(addr)):
		return a.value("ip", original, func(sum [sha256.Size]byte) string {
			return netip.AddrFrom4([4]byte{10, sum[0], sum[1], sum[2]}).String()
		})
	case addr.Is4():
		return a.value("ip", original, func(sum [sha256.Size]byte) string {
			return netip.AddrFrom4([4]byte{198, 18 | sum[0]&1, sum[1], sum[2]}).String()
		})
	default:
		return a.value("ip", original, func(sum [sha256.Size]byte) string {
			b := [16]byte{0xfd}
			copy(b[1:], sum[:15])
			return netip.AddrFrom16(b).String()
		})
	}
}

func (a *anonymizer) nodeName(v string) string {
	return a.value("node", v, hexPrefix("node-", nameHexLen))
}

func (a *anonymizer) vpcID(v string) string {
	return a.value("vpc", v, hexPrefix("vpc-", awsIDHexLen))
}

// dns is case-insensitive, like the LB match in classify.
func (a *anonymizer) dns(v string) string {
	return a.value("dns", strings.ToLower(v), func(sum [sha256.Size]byte) string {
		return hex.EncodeToString(sum[:])[:dnsHexLen] + ".elb.amazonaws.com"
	})
}

func (a *anonymizer) node(n model.Node) model.Node {
	out := n
	out.Name = a.nodeName(n.Name)
	out.InstanceID = a.value("instance", n.InstanceID, hexPrefix("i-", awsIDHexLen))
	return out
}

// mirrorPodNames renames mirror pods: the kubelet names a static pod's mirror "<pod>-<node name>" and makes
// the Node its owner, so the name would carry the node name (an IP on EKS) past nodeName.
func (a *anonymizer) mirrorPodNames(pods []model.Pod) map[string]string {
	out := map[string]string{}
	for _, p := range pods {
		base, ok := strings.CutSuffix(p.Name, "-"+p.NodeName)
		if p.OwnerKind != ownerKindNode || p.NodeName == "" || !ok {
			continue
		}
		out[p.Namespace+"/"+p.Name] = base + "-" + a.nodeName(p.NodeName)
	}
	return out
}

// podName is the pod's name in the output: mirror pods are renamed, every other pod keeps its name.
func (a *anonymizer) podName(namespace, name string) string {
	if renamed, ok := a.mirrorPods[namespace+"/"+name]; ok {
		return renamed
	}
	return name
}

func (a *anonymizer) service(svc model.Service) model.Service {
	out := svc
	if svc.ClusterIP != "None" {
		out.ClusterIP = a.ip(svc.ClusterIP)
	}
	out.Annotations = keepAnnotations(svc.Annotations)
	out.LBHostnames = mapSlice(svc.LBHostnames, a.dns)
	out.Endpoints = mapSlice(svc.Endpoints, func(ep model.Endpoint) model.Endpoint { return a.endpoint(svc.Namespace, ep) })
	return out
}

// endpoint takes the service namespace: EndpointSlice target pods live in it.
func (a *anonymizer) endpoint(namespace string, ep model.Endpoint) model.Endpoint {
	out := ep
	out.IP = a.ip(ep.IP)
	out.PodName = a.podName(namespace, ep.PodName)
	out.NodeName = a.nodeName(ep.NodeName)
	out.ForZones = slices.Clone(ep.ForZones)
	return out
}

func (a *anonymizer) pod(p model.Pod) model.Pod {
	out := p
	out.Name = a.podName(p.Namespace, p.Name)
	out.IP = a.ip(p.IP)
	out.NodeName = a.nodeName(p.NodeName)
	if p.OwnerKind == ownerKindNode {
		out.OwnerName = a.nodeName(p.OwnerName)
	}
	return out
}

func (a *anonymizer) ingress(ing model.Ingress) model.Ingress {
	out := ing
	out.Annotations = keepAnnotations(ing.Annotations)
	out.Services = slices.Clone(ing.Services)
	out.LBHostnames = mapSlice(ing.LBHostnames, a.dns)
	return out
}

func (a *anonymizer) inventory(inv model.AWSInventory) model.AWSInventory {
	out := inv
	out.VPCIDs = mapSlice(inv.VPCIDs, a.vpcID)
	out.Subnets = mapSorted(inv.Subnets, func(id string, sn model.Subnet) (string, model.Subnet) {
		return a.value("subnet", id, hexPrefix("subnet-", awsIDHexLen)), model.Subnet{AZ: sn.AZ, VPCID: a.vpcID(sn.VPCID)}
	})
	out.IPZones = mapSorted(inv.IPZones, func(ip, zone string) (string, string) { return a.ip(ip), zone })
	out.LoadBalancers = mapSlice(inv.LoadBalancers, a.loadBalancer)
	out.RDS = mapSlice(inv.RDS, func(r model.RDSInstance) model.RDSInstance {
		r.VPCID = a.vpcID(r.VPCID)
		return r
	})
	out.Caches = mapSlice(inv.Caches, func(c model.CacheCluster) model.CacheCluster {
		c.NodeZones, c.VPCID = slices.Clone(c.NodeZones), a.vpcID(c.VPCID)
		return c
	})
	out.MSK = mapSlice(inv.MSK, func(m model.MSKCluster) model.MSKCluster {
		m.BrokerZones, m.VPCID = slices.Clone(m.BrokerZones), a.vpcID(m.VPCID)
		return m
	})
	return out
}

func (a *anonymizer) loadBalancer(lb model.LoadBalancer) model.LoadBalancer {
	out := lb
	out.Name = a.value("lb", lb.Name, hexPrefix("lb-", nameHexLen))
	out.DNSName = a.dns(lb.DNSName)
	out.VPCID = a.vpcID(lb.VPCID)
	out.Zones = slices.Clone(lb.Zones)
	if lb.CrossZone != nil {
		v := *lb.CrossZone
		out.CrossZone = &v
	}
	out.TargetGroups = mapSlice(lb.TargetGroups, func(tg model.TargetGroup) model.TargetGroup {
		tg.Name = a.value("tg", tg.Name, hexPrefix("tg-", nameHexLen))
		return tg
	})
	return out
}

// scrub replaces every original value seen so far inside free text (AWS error messages may quote IDs).
func (a *anonymizer) scrub(texts []string) []string {
	if texts == nil {
		return nil
	}
	pairs := make([][2]string, 0, len(a.pairs)/2)
	for i := 0; i < len(a.pairs); i += 2 {
		pairs = append(pairs, [2]string{a.pairs[i], a.pairs[i+1]})
	}
	// Longer originals first, so "10.0.0.12" is not rewritten through its prefix "10.0.0.1".
	slices.SortStableFunc(pairs, func(x, y [2]string) int { return len(y[0]) - len(x[0]) })
	args := make([]string, 0, len(a.pairs))
	for _, p := range pairs {
		args = append(args, p[0], p[1])
	}
	r := strings.NewReplacer(args...)
	return mapSlice(texts, r.Replace)
}

func keepAnnotations(ann map[string]string) map[string]string {
	var out map[string]string
	for _, k := range slices.Sorted(maps.Keys(ann)) {
		v := ann[k]
		if k == annLBAttributes || k == annTGAttributes {
			v = crossZoneAttribute(v)
		}
		if !classifyAnnotations[k] || v == "" {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[k] = v
	}
	return out
}

// crossZoneAttribute keeps only the cross-zone pair of an LB/TG attributes annotation (others may name S3 buckets).
func crossZoneAttribute(v string) string {
	var kept []string
	for _, attr := range strings.Split(v, ",") {
		if key, _, ok := strings.Cut(attr, "="); ok && strings.TrimSpace(key) == attrCrossZone {
			kept = append(kept, strings.TrimSpace(attr))
		}
	}
	return strings.Join(kept, ",")
}

// mapSlice returns f applied to each element; nil stays nil so JSON keeps omitempty/null as it was.
func mapSlice[T, U any](in []T, f func(T) U) []U {
	if in == nil {
		return nil
	}
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// mapSorted rebuilds a map visiting keys in order, so replacements (and collision salts) are deterministic.
func mapSorted[V any](in map[string]V, f func(string, V) (string, V)) map[string]V {
	if in == nil {
		return nil
	}
	out := make(map[string]V, len(in))
	for _, k := range slices.Sorted(maps.Keys(in)) {
		nk, nv := f(k, in[k])
		out[nk] = nv
	}
	return out
}
