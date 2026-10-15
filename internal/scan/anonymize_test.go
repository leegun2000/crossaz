package scan

import (
	"crypto/sha256"
	"encoding/json"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	origNode     = "ip-10-20-1-37.ap-northeast-2.compute.internal"
	origPodIP    = "10.20.3.44"
	origAPIIP    = "10.20.35.9"
	origInstance = "i-0123456789abcdef0"
	origVPC      = "vpc-0aaaabbbbccccdddd"
	origSubnet   = "subnet-0ffffeeeeddddcccc"
	origLBName   = "k8s-edge-gateway-9f60025a00"
	origLBDNS    = "k8s-edge-gateway-9f60025a00-1a2b3c4d5e6f7a8b.elb.ap-northeast-2.amazonaws.com"
	origTGName   = "k8s-edge-gateway-tg-77aa"
	origAccount  = "123456789012"
)

func sensitiveSnapshot() model.Snapshot {
	crossZone := true
	return model.Snapshot{
		Cluster: "prod-a",
		Region:  "ap-northeast-2",
		Nodes:   []model.Node{{Name: origNode, Zone: "ap-northeast-2a", InstanceID: origInstance}},
		Services: []model.Service{
			{
				Namespace: "edge", Name: "gateway", Type: "LoadBalancer", ClusterIP: "172.20.9.9",
				Annotations: map[string]string{
					"kubectl.kubernetes.io/last-applied-configuration":             `{"kind":"Service"}`,
					"service.beta.kubernetes.io/aws-load-balancer-attributes":      "access_logs.s3.bucket=prod-logs, load_balancing.cross_zone.enabled=true",
					"service.beta.kubernetes.io/aws-load-balancer-nlb-target-type": "ip",
				},
				LBHostnames: []string{strings.ToUpper(origLBDNS)},
				Endpoints:   []model.Endpoint{{IP: origPodIP, PodName: "gateway-0", NodeName: origNode, Zone: "ap-northeast-2a", Ready: true, ForZones: []string{"ap-northeast-2a"}}},
			},
			{
				Namespace: "default", Name: "kubernetes", Type: "ClusterIP", ClusterIP: "None",
				Annotations: map[string]string{"service.beta.kubernetes.io/aws-load-balancer-attributes": "deletion_protection.enabled=true"},
				Endpoints:   []model.Endpoint{{IP: origAPIIP, Ready: true}},
			},
		},
		Pods:           []model.Pod{{Namespace: "edge", Name: "gateway-0", NodeName: origNode, IP: origPodIP}},
		Ingresses:      []model.Ingress{{Namespace: "edge", Name: "web", Services: []string{"gateway"}, LBHostnames: []string{origLBDNS}}},
		IngressClasses: []model.IngressClass{{Name: "alb", Controller: "ingress.k8s.aws/alb"}},
		AWS: &model.AWSInventory{
			VPCIDs:   []string{origVPC},
			Accounts: []string{origAccount},
			Subnets:  map[string]model.Subnet{origSubnet: {AZ: "ap-northeast-2a", VPCID: origVPC}},
			IPZones:  map[string]string{origAPIIP: "ap-northeast-2c"},
			LoadBalancers: []model.LoadBalancer{{
				Name: origLBName, DNSName: origLBDNS, Type: "network", VPCID: origVPC, Zones: []string{"ap-northeast-2a"},
				CrossZone: &crossZone, TargetGroups: []model.TargetGroup{{Name: origTGName, TargetType: "ip", CrossZone: "true"}},
			}},
			RDS:    []model.RDSInstance{{ID: "orders-db", AZ: "ap-northeast-2a", VPCID: origVPC}},
			Caches: []model.CacheCluster{{ID: "sessions-001", NodeZones: []string{"ap-northeast-2b"}, VPCID: origVPC}},
			MSK:    []model.MSKCluster{{Name: "events", BrokerZones: []string{"ap-northeast-2c"}, VPCID: origVPC}},
		},
		Volume:   &model.VolumeData{Window: "7d", RecvBytesPerS: map[string]float64{"edge/gateway-0": 12}},
		Bill:     &model.Bill{Lines: []model.BillLine{{Account: origAccount, UsageType: "APN2-DataTransfer-Regional-Bytes", Operation: "InterZone-In", Family: model.FamilyInterZone, USD: 10}}},
		Warnings: []string{"L1: endpoint IP AZs: DescribeNetworkInterfaces " + origAPIIP + " in " + origVPC + ": throttled"},
	}
}

func TestAnonymizePreservesJoins(t *testing.T) {
	// Arrange
	s := sensitiveSnapshot()

	// Act
	got := Anonymize(s)

	// Assert
	svc, api := got.Services[0], got.Services[1]
	ep := svc.Endpoints[0]
	checks := []struct {
		name     string
		ok       bool
		detail   any
		replaced string
		original string
	}{
		{"endpoint IP equals pod IP", ep.IP == got.Pods[0].IP, ep.IP, ep.IP, origPodIP},
		{"zone-less endpoint IP is an IPZones key", got.AWS.IPZones[api.Endpoints[0].IP] == "ap-northeast-2c", got.AWS.IPZones, api.Endpoints[0].IP, origAPIIP},
		{"node name equals pod and endpoint NodeName", got.Nodes[0].Name == got.Pods[0].NodeName && ep.NodeName == got.Nodes[0].Name, got.Nodes[0].Name, got.Nodes[0].Name, origNode},
		{"LB DNS equals service hostname regardless of case", got.AWS.LoadBalancers[0].DNSName == svc.LBHostnames[0], svc.LBHostnames, svc.LBHostnames[0], origLBDNS},
		{"LB DNS equals ingress hostname", got.AWS.LoadBalancers[0].DNSName == got.Ingresses[0].LBHostnames[0], got.Ingresses[0].LBHostnames, got.Ingresses[0].LBHostnames[0], origLBDNS},
		{"VPC IDs agree across inventory", got.AWS.VPCIDs[0] == got.AWS.LoadBalancers[0].VPCID && got.AWS.VPCIDs[0] == got.AWS.RDS[0].VPCID &&
			got.AWS.VPCIDs[0] == got.AWS.Caches[0].VPCID && got.AWS.VPCIDs[0] == got.AWS.MSK[0].VPCID, got.AWS.VPCIDs, got.AWS.VPCIDs[0], origVPC},
		{"bill account equals inventory account", got.Bill.Lines[0].Account == got.AWS.Accounts[0], got.Bill.Lines, got.AWS.Accounts[0], origAccount},
		{"subnet VPC is the replaced VPC", got.AWS.Subnets[firstKey(got.AWS.Subnets)].VPCID == got.AWS.VPCIDs[0], got.AWS.Subnets, firstKey(got.AWS.Subnets), origSubnet},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: %v", c.name, c.detail)
		}
		if c.replaced == c.original || c.replaced == "" {
			t.Errorf("%s: value %q was not replaced", c.name, c.replaced)
		}
	}
	if got.Services[0].ClusterIP == "172.20.9.9" || api.ClusterIP != "None" {
		t.Errorf("ClusterIPs = %q, %q; want the IP replaced and None kept", svc.ClusterIP, api.ClusterIP)
	}
}

func firstKey[V any](m map[string]V) string {
	for k := range m {
		return k
	}
	return ""
}

func TestAnonymizeLeavesNoOriginalIdentifier(t *testing.T) {
	// Arrange
	s := sensitiveSnapshot()

	// Act
	data, err := json.Marshal(Anonymize(s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Assert
	out := strings.ToLower(string(data))
	for _, orig := range []string{origNode, origPodIP, origAPIIP, "172.20.9.9", origInstance, origVPC, origSubnet, origLBName, origLBDNS, origTGName, origAccount, "prod-logs", "last-applied", "deletion_protection"} {
		if strings.Contains(out, strings.ToLower(orig)) {
			t.Errorf("anonymized JSON still contains %q", orig)
		}
	}
	for _, kept := range []string{"edge", "gateway-0", "orders-db", "sessions-001", "prod-a", "load_balancing.cross_zone.enabled=true", "nlb-target-type"} {
		if !strings.Contains(out, kept) {
			t.Errorf("anonymized JSON lost %q", kept)
		}
	}
}

func TestAnonymizeReplacesNodeNameInMirrorPods(t *testing.T) {
	// Arrange: the kubelet names a static pod's mirror "<pod>-<node>" and makes the Node its owner
	// (seen on kind: kube-system/etcd-crossaz-e2e-control-plane, ownerName crossaz-e2e-control-plane).
	mirror := "etcd-" + origNode
	s := sensitiveSnapshot()
	s.Pods = append(s.Pods, model.Pod{Namespace: "kube-system", Name: mirror, NodeName: origNode, IP: "10.20.1.37", HostNetwork: true, OwnerKind: "Node", OwnerName: origNode})
	s.Services = append(s.Services, model.Service{Namespace: "kube-system", Name: "etcd", Type: "ClusterIP", ClusterIP: "None",
		Endpoints: []model.Endpoint{{IP: "10.20.1.37", PodName: mirror, NodeName: origNode, Zone: "ap-northeast-2a", Ready: true}}})
	s.Volume.RecvBytesPerS["kube-system/"+mirror] = 3

	// Act
	got := Anonymize(s)

	// Assert
	node, pod, ep := got.Nodes[0].Name, got.Pods[1], got.Services[2].Endpoints[0]
	if want := "etcd-" + node; pod.Name != want || pod.OwnerName != node || pod.OwnerKind != "Node" {
		t.Errorf("mirror pod = %s owner %s/%s, want %s owner Node/%s", pod.Name, pod.OwnerKind, pod.OwnerName, want, node)
	}
	if ep.PodName != pod.Name {
		t.Errorf("endpoint PodName %q, want the renamed pod %q", ep.PodName, pod.Name)
	}
	if v, ok := got.Volume.RecvBytesPerS["kube-system/"+pod.Name]; !ok || v != 3 {
		t.Errorf("volume keys %v: want kube-system/%s = 3", got.Volume.RecvBytesPerS, pod.Name)
	}
	if got.Pods[0].Name != "gateway-0" {
		t.Errorf("non-mirror pod renamed to %q", got.Pods[0].Name)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(strings.ToLower(string(data)), strings.ToLower(origNode)) {
		t.Errorf("anonymized JSON still contains node name %q", origNode)
	}
}

func TestKeepAnnotationsKeepsOnlyWhatClassifyReads(t *testing.T) {
	const (
		lbAttrs = "service.beta.kubernetes.io/aws-load-balancer-attributes"
		tgAttrs = "service.beta.kubernetes.io/aws-load-balancer-target-group-attributes"
		upstrm  = "nginx.ingress.kubernetes.io/service-upstream"
	)
	tests := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{"target group attributes keep only the cross-zone pair",
			map[string]string{tgAttrs: "deregistration_delay.timeout_seconds=30, load_balancing.cross_zone.enabled=false"},
			map[string]string{tgAttrs: "load_balancing.cross_zone.enabled=false"}},
		{"target group attributes without cross-zone are dropped",
			map[string]string{tgAttrs: "stickiness.enabled=true"}, nil},
		{"LB attributes keep only the cross-zone pair",
			map[string]string{lbAttrs: "access_logs.s3.bucket=logs,load_balancing.cross_zone.enabled=true"},
			map[string]string{lbAttrs: "load_balancing.cross_zone.enabled=true"}},
		{"ingress-nginx service-upstream is kept", map[string]string{upstrm: "true"}, map[string]string{upstrm: "true"}},
		{"annotations classify ignores are dropped", map[string]string{"nginx.ingress.kubernetes.io/auth-url": "https://auth.internal"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got := keepAnnotations(tt.in)

			// Assert
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("keepAnnotations(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAnonymizeIsDeterministicAndDoesNotMutateInput(t *testing.T) {
	// Arrange
	s := sensitiveSnapshot()
	pristine := sensitiveSnapshot()

	// Act
	first := Anonymize(s)
	second := Anonymize(s)

	// Assert
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two runs differ:\n%+v\n%+v", first, second)
	}
	if !reflect.DeepEqual(s, pristine) {
		t.Errorf("input mutated:\n%+v", s)
	}
	*first.AWS.LoadBalancers[0].CrossZone = false
	first.Services[0].Endpoints[0].ForZones[0] = "changed"
	first.Volume.RecvBytesPerS["edge/gateway-0"] = 0
	if !reflect.DeepEqual(s, pristine) {
		t.Errorf("output aliases input: %+v", s)
	}
}

func TestAnonymizeKeepsEmptySnapshotEmpty(t *testing.T) {
	// Arrange
	empty := model.Snapshot{Cluster: "c"}

	// Act
	got := Anonymize(empty)

	// Assert
	if !reflect.DeepEqual(got, empty) {
		t.Errorf("Anonymize(empty) = %+v, want unchanged", got)
	}
}

func TestAnonymizeIPKeepsFamilyAndPrivacyClass(t *testing.T) {
	tests := []struct {
		name  string
		ip    string
		check func(string) bool
	}{
		{"private IPv4 becomes 10/8", "192.168.4.20", inPrefix("10.0.0.0/8")},
		{"CGNAT IPv4 stays private in 10/8", "100.64.3.2", inPrefix("10.0.0.0/8")},
		{"public IPv4 stays public in 198.18/15", "54.180.1.2", inPrefix("198.18.0.0/15")},
		{"IPv6 becomes ULA fd00::/8", "2406:da12::1", inPrefix("fd00::/8")},
		{"unparseable value becomes a token", "not-an-ip", func(v string) bool { return strings.HasPrefix(v, "ip-") }},
		{"empty stays empty", "", func(v string) bool { return v == "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			a := &anonymizer{byKey: map[string]string{}, taken: map[string]bool{}}

			// Act
			got := a.ip(tt.ip)

			// Assert
			if !tt.check(got) {
				t.Errorf("ip(%q) = %q", tt.ip, got)
			}
			if tt.ip != "" && a.ip(tt.ip) != got {
				t.Errorf("ip(%q) is not stable", tt.ip)
			}
		})
	}
}

func inPrefix(prefix string) func(string) bool {
	p := netip.MustParsePrefix(prefix)
	return func(v string) bool {
		addr, err := netip.ParseAddr(v)
		return err == nil && p.Contains(addr)
	}
}

func TestAnonymizerResolvesCollisionsWithSalt(t *testing.T) {
	// Arrange: a format with only two outputs forces the second value onto a salted hash.
	a := &anonymizer{byKey: map[string]string{}, taken: map[string]bool{}}
	tiny := func(sum [sha256.Size]byte) string { return strconv.Itoa(int(sum[0] % 2)) }

	// Act
	first := a.value("k", "one", tiny)
	second := a.value("k", "two", tiny)

	// Assert
	if first == second {
		t.Errorf("distinct inputs share replacement %q", first)
	}
	if again := a.value("k", "one", tiny); again != first {
		t.Errorf("repeat lookup = %q, want %q", again, first)
	}
}

func TestAnonymizeRedactsWarningErrorText(t *testing.T) {
	const (
		principal = "arn:aws:sts::123456789012:assumed-role/AWSReservedSSO_ReadOnly_abc/jane.doe@example.com"
		homePath  = "/Users/jane.doe/.aws/sso/cache/ee0b.json"
	)
	tests := []struct {
		name    string
		warning string
		want    func(anon model.Snapshot) string
	}{
		{
			name:    "layer and subject are kept and AWS error text quoting replaced values is redacted",
			warning: "L1: endpoint IP AZs: DescribeNetworkInterfaces " + origAPIIP + " in " + origVPC + ": throttled",
			want:    func(model.Snapshot) string { return "L1: endpoint IP AZs: <redacted>" },
		},
		{
			name:    "access denied with account ID, principal ARN, email and LB name is redacted",
			warning: "L1: load balancers: load balancer k8s-payments-secretapi-0123abcd: operation error: AccessDenied: User: " + principal + " is not authorized",
			want:    func(model.Snapshot) string { return "L1: load balancers: <redacted>" },
		},
		{
			name:    "credential error with a home directory path is redacted",
			warning: "L1: no AWS credentials found; running L0 only: failed to read cached SSO token file, open " + homePath + ": no such file or directory",
			want:    func(model.Snapshot) string { return "L1: no AWS credentials found; running L0 only: <redacted>" },
		},
		{
			name:    "two-segment warning is kept with replaced values scrubbed",
			warning: "L1: node " + origInstance + " has no instance ID",
			want: func(anon model.Snapshot) string {
				return "L1: node " + anon.Nodes[0].InstanceID + " has no instance ID"
			},
		},
		{
			name:    "warning without separator is kept",
			warning: "--fixture in use",
			want:    func(model.Snapshot) string { return "--fixture in use" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := sensitiveSnapshot()
			s.Warnings = []string{tt.warning}

			// Act
			got := Anonymize(s)

			// Assert
			if want := tt.want(got); len(got.Warnings) != 1 || got.Warnings[0] != want {
				t.Errorf("warnings = %q, want [%q]", got.Warnings, want)
			}
			for _, leak := range []string{"123456789012", "jane.doe", "/Users/", "k8s-payments-secretapi", origAPIIP, origVPC, origInstance} {
				if strings.Contains(strings.Join(got.Warnings, "\n"), leak) {
					t.Errorf("warning still contains %q: %q", leak, got.Warnings)
				}
			}
		})
	}
}
