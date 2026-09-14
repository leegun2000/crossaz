package classify

import (
	"maps"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	lbcNLBClass = "service.k8s.aws/nlb"
	autoModeNLB = "eks.amazonaws.com/nlb"
	autoModeALB = "eks.amazonaws.com/alb"
	lbcALB      = "ingress.k8s.aws/alb"
)

type routeWant struct {
	route      model.RouteKind
	scheme     string
	targetType string
	etp        string
	sourceKind string
	sourceName string
	evidence   string
}

func inCluster(route model.RouteKind) routeWant {
	return routeWant{route: route, sourceKind: "cluster", sourceName: "*", evidence: "k8s"}
}

func viaLB(route model.RouteKind, scheme, targetType, etp, kind, name string) routeWant {
	return routeWant{route, scheme, targetType, etp, kind, name, "annotation"}
}

func lbService(class string, ann map[string]string) model.Service {
	svc := clusterIPService("api", readyEP("10.0.0.1", "a"), readyEP("10.0.1.1", "b"))
	svc.Type, svc.LoadBalancerClass, svc.Annotations = "LoadBalancer", class, ann
	return svc
}

func albIngressFor(name, class string, ann map[string]string) model.Ingress {
	return model.Ingress{Namespace: "app", Name: name, ClassName: class, Annotations: ann, Services: []string{"api"}}
}

func gotRoute(p model.Path) routeWant {
	f := p.Facts
	return routeWant{f.Route, f.Scheme, f.TargetType, f.ExternalTrafficPolicy, p.Source.Kind, p.Source.Name, f.Evidence}
}

func TestBuildPathsRouteDecision(t *testing.T) {
	nodePort := clusterIPService("api", readyEP("10.0.0.1", "a"))
	nodePort.Type = "NodePort"
	withETP := func(svc model.Service, etp string) model.Service {
		svc.ExternalTrafficPolicy = etp
		return svc
	}
	headless := clusterIPService("api", readyEP("10.0.0.1", "a"))
	headless.ClusterIP = "None"
	albClass := model.IngressClass{Name: "public", Controller: lbcALB}
	nginxClass := model.IngressClass{Name: "nginx", Controller: "k8s.io/ingress-nginx", IsDefault: true}
	defaultALB := model.IngressClass{Name: "aws", Controller: lbcALB, IsDefault: true}
	autoModeCustom := model.IngressClass{Name: "internet-alb", Controller: autoModeALB}
	autoModeDefault := model.IngressClass{Name: "auto", Controller: autoModeALB, IsDefault: true}
	autoModeNamedALB := model.IngressClass{Name: "alb", Controller: autoModeALB}
	nginxNamedALB := model.IngressClass{Name: "alb", Controller: "k8s.io/ingress-nginx"}
	otherNS := albIngressFor("web", "alb", nil)
	otherNS.Namespace = "other"
	notBackend := albIngressFor("web", "alb", nil)
	notBackend.Services = []string{"frontend"}

	tests := []struct {
		name      string
		svc       model.Service
		ingresses []model.Ingress
		classes   []model.IngressClass
		want      routeWant
	}{
		{"ALB via IngressClass controller", nodePort, []model.Ingress{albIngressFor("web", "public", nil)}, []model.IngressClass{albClass},
			viaLB(model.RouteALB, "internal", "instance", "Cluster", "alb", "app/web")},
		{"ALB via className alb without IngressClass object", nodePort, []model.Ingress{albIngressFor("web", "alb", nil)}, nil,
			viaLB(model.RouteALB, "internal", "instance", "Cluster", "alb", "app/web")},
		{"ALB via legacy ingress.class annotation", nodePort, []model.Ingress{albIngressFor("web", "", map[string]string{annIngressClass: "alb"})}, nil,
			viaLB(model.RouteALB, "internal", "instance", "Cluster", "alb", "app/web")},
		{"ALB via default IngressClass", nodePort, []model.Ingress{albIngressFor("web", "", nil)}, []model.IngressClass{defaultALB},
			viaLB(model.RouteALB, "internal", "instance", "Cluster", "alb", "app/web")},
		{"Auto Mode ALB via custom class name defaults to ip targets", nodePort,
			[]model.Ingress{albIngressFor("web", "internet-alb", nil)}, []model.IngressClass{autoModeCustom},
			viaLB(model.RouteALB, "internal", "ip", "Cluster", "alb", "app/web")},
		{"Auto Mode ALB via default IngressClass defaults to ip targets", nodePort,
			[]model.Ingress{albIngressFor("web", "", nil)}, []model.IngressClass{nginxClassNotDefault(), autoModeDefault},
			viaLB(model.RouteALB, "internal", "ip", "Cluster", "alb", "app/web")},
		{"Auto Mode ALB class literally named alb defaults to ip targets", nodePort,
			[]model.Ingress{albIngressFor("web", "alb", nil)}, []model.IngressClass{autoModeNamedALB},
			viaLB(model.RouteALB, "internal", "ip", "Cluster", "alb", "app/web")},
		{"Auto Mode ALB ingress target-type annotation wins", nodePort,
			[]model.Ingress{albIngressFor("web", "alb", map[string]string{annALBTargetType: "instance"})}, []model.IngressClass{autoModeNamedALB},
			viaLB(model.RouteALB, "internal", "instance", "Cluster", "alb", "app/web")},
		{"class named alb owned by another controller is not ALB", nodePort,
			[]model.Ingress{albIngressFor("web", "alb", nil)}, []model.IngressClass{nginxNamedALB},
			inCluster(model.RouteClusterIP)},
		{"default IngressClass ignored when annotation names another controller", nodePort,
			[]model.Ingress{albIngressFor("web", "", map[string]string{annIngressClass: "nginx"})}, []model.IngressClass{defaultALB},
			inCluster(model.RouteClusterIP)},
		{"non-ALB IngressClass", nodePort, []model.Ingress{albIngressFor("web", "nginx", nil)}, []model.IngressClass{nginxClass, albClass},
			inCluster(model.RouteClusterIP)},
		{"ALB ingress in another namespace", nodePort, []model.Ingress{otherNS}, nil, inCluster(model.RouteClusterIP)},
		{"ALB ingress for another service", nodePort, []model.Ingress{notBackend}, nil, inCluster(model.RouteClusterIP)},
		{"service target-type beats ingress, ingress scheme used",
			func() model.Service {
				s := nodePort
				s.Annotations = map[string]string{annALBTargetType: "ip"}
				return s
			}(),
			[]model.Ingress{albIngressFor("web", "alb", map[string]string{annALBTargetType: "instance", annALBScheme: "internet-facing"})}, nil,
			viaLB(model.RouteALB, "internet-facing", "ip", "Cluster", "alb", "app/web")},
		{"ingress target-type annotation", nodePort, []model.Ingress{albIngressFor("web", "alb", map[string]string{annALBTargetType: "ip"})}, nil,
			viaLB(model.RouteALB, "internal", "ip", "Cluster", "alb", "app/web")},
		{"first ALB ingress by name wins", nodePort,
			[]model.Ingress{albIngressFor("zz", "alb", nil), albIngressFor("aa", "alb", map[string]string{annALBScheme: "internet-facing"})}, nil,
			viaLB(model.RouteALB, "internet-facing", "instance", "Cluster", "alb", "app/aa")},
		{"ALB wins over type LoadBalancer and keeps service policy", withETP(lbService("", nil), "Local"),
			[]model.Ingress{albIngressFor("web", "alb", nil)}, nil,
			viaLB(model.RouteALB, "internal", "instance", "Local", "alb", "app/web")},
		{"CLB by default", lbService("", nil), nil, nil,
			viaLB(model.RouteCLB, "internet-facing", "instance", "Cluster", "clb", "app/api")},
		{"CLB internal annotation", lbService("", map[string]string{annLBInternal: "true"}), nil, nil,
			viaLB(model.RouteCLB, "internal", "instance", "Cluster", "clb", "app/api")},
		{"in-tree internal annotation with legacy CIDR value", lbService("", map[string]string{annLBType: "nlb", annLBInternal: "0.0.0.0/0"}), nil, nil,
			viaLB(model.RouteNLBZonal, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"CLB internal annotation false is internet-facing", lbService("", map[string]string{annLBInternal: "false"}), nil, nil,
			viaLB(model.RouteCLB, "internet-facing", "instance", "Cluster", "clb", "app/api")},
		{"in-tree NLB defaults to zonal and internet-facing", lbService("", map[string]string{annLBType: "nlb"}), nil, nil,
			viaLB(model.RouteNLBZonal, "internet-facing", "instance", "Cluster", "nlb", "app/api")},
		{"in-tree NLB internal with explicit cross-zone", lbService("", map[string]string{annLBType: "nlb", annLBInternal: "true", annLBCrossZone: "true"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"LBC class without cross-zone annotation is unknown", lbService(lbcNLBClass, nil), nil, nil,
			viaLB(model.RouteNLB, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"EKS Auto Mode class defaults to ip targets", lbService(autoModeNLB, nil), nil, nil,
			viaLB(model.RouteNLB, "internal", "ip", "Cluster", "nlb", "app/api")},
		{"EKS Auto Mode class target-type annotation wins", lbService(autoModeNLB, map[string]string{annNLBTargetType: "instance"}), nil, nil,
			viaLB(model.RouteNLB, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"target group cross-zone false beats LB attribute true", lbService(lbcNLBClass, map[string]string{
			annLBAttributes: "load_balancing.cross_zone.enabled=true", annTGAttributes: "preserve_client_ip.enabled=true, load_balancing.cross_zone.enabled=false"}), nil, nil,
			viaLB(model.RouteNLBZonal, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"target group cross-zone true beats LB attribute false", lbService(lbcNLBClass, map[string]string{
			annLBAttributes: "load_balancing.cross_zone.enabled=false", annTGAttributes: "load_balancing.cross_zone.enabled=true"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"target group cross-zone beats in-tree default", lbService("", map[string]string{
			annLBType: "nlb", annTGAttributes: "load_balancing.cross_zone.enabled=true"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internet-facing", "instance", "Cluster", "nlb", "app/api")},
		{"target group attributes without cross-zone fall back to LB attribute", lbService(lbcNLBClass, map[string]string{
			annLBAttributes: "load_balancing.cross_zone.enabled=true", annTGAttributes: "deregistration_delay.timeout_seconds=30"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"external type with cross-zone attribute", lbService("", map[string]string{annLBType: "external", annLBAttributes: "load_balancing.cross_zone.enabled=true"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"nlb-ip with legacy cross-zone false", lbService("", map[string]string{annLBType: "nlb-ip", annLBCrossZone: "false"}), nil, nil,
			viaLB(model.RouteNLBZonal, "internal", "ip", "Cluster", "nlb", "app/api")},
		{"attribute list with spaces and other keys", lbService(lbcNLBClass, map[string]string{
			annLBAttributes: "deletion_protection.enabled=true, load_balancing.cross_zone.enabled = false"}), nil, nil,
			viaLB(model.RouteNLBZonal, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"invalid attribute value falls back to legacy annotation", lbService(lbcNLBClass, map[string]string{
			annLBAttributes: "load_balancing.cross_zone.enabled=maybe", annLBCrossZone: "true"}), nil, nil,
			viaLB(model.RouteNLBXZ, "internal", "instance", "Cluster", "nlb", "app/api")},
		{"nlb target-type annotation and scheme annotation", lbService("", map[string]string{
			annLBType: "external", annNLBTargetType: "ip", annLBScheme: "internet-facing"}), nil, nil,
			viaLB(model.RouteNLB, "internet-facing", "ip", "Cluster", "nlb", "app/api")},
		{"LB service keeps Local policy", withETP(lbService(lbcNLBClass, nil), "Local"), nil, nil,
			viaLB(model.RouteNLB, "internal", "instance", "Local", "nlb", "app/api")},
		{"unknown LoadBalancerClass is treated as clusterip", lbService("tailscale", nil), nil, nil, inCluster(model.RouteClusterIP)},
		{"unknown LoadBalancerClass ignores type annotation", lbService("tailscale", map[string]string{annLBType: "external"}), nil, nil,
			inCluster(model.RouteClusterIP)},
		{"headless", headless, nil, nil, inCluster(model.RouteHeadless)},
		{"NodePort without ingress", nodePort, nil, nil, inCluster(model.RouteClusterIP)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{Nodes: nodesIn("a", "b"), Services: []model.Service{tt.svc}, Ingresses: tt.ingresses, IngressClasses: tt.classes}

			// Act
			p := onlyPath(t, s)

			// Assert
			if got := gotRoute(p); got != tt.want {
				t.Errorf("route = %+v\nwant    %+v", got, tt.want)
			}
			if p.Route != p.Facts.Route || p.Facts.SameVPC != true {
				t.Errorf("Path.Route = %q, Facts.Route = %q, SameVPC = %v", p.Route, p.Facts.Route, p.Facts.SameVPC)
			}
		})
	}
}

func nginxClassNotDefault() model.IngressClass {
	return model.IngressClass{Name: "nginx", Controller: "k8s.io/ingress-nginx"}
}

func TestBuildPathsALBTopologyHints(t *testing.T) {
	tests := []struct {
		name       string
		targetType string
		want       bool
	}{
		{"ALB ip target bypasses kube-proxy", "ip", false},
		{"ALB instance target passes through kube-proxy", "instance", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := clusterIPService("api", readyEP("10.0.0.1", "a", "a"), readyEP("10.0.1.1", "b", "b"))
			svc.Type = "NodePort"
			ing := albIngressFor("web", "alb", map[string]string{annALBTargetType: tt.targetType})
			s := model.Snapshot{KubeProxyVersion: "v1.31.0", Nodes: nodesIn("a", "b"), Services: []model.Service{svc}, Ingresses: []model.Ingress{ing}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.Route != model.RouteALB || p.Facts.TopologyHints != tt.want || p.Facts.ZoneLocal != tt.want {
				t.Errorf("route/hints/zoneLocal = %s/%v/%v, want alb/%v/%v", p.Facts.Route, p.Facts.TopologyHints, p.Facts.ZoneLocal, tt.want, tt.want)
			}
		})
	}
}

func TestBuildPathsMatchesL1LoadBalancer(t *testing.T) {
	nlbSvc := lbService(lbcNLBClass, nil)
	nlbSvc.LBHostnames = []string{"K8S-APP-API-123.ELB.us-east-1.amazonaws.com"}
	albSvc := clusterIPService("api", readyEP("10.0.0.1", "a"))
	albIng := albIngressFor("web", "alb", nil)
	albIng.LBHostnames = []string{"internal-k8s-web-1.us-east-1.elb.amazonaws.com"}
	unmatched := lbService("", nil)
	unmatched.LBHostnames = []string{"other.elb.amazonaws.com"}
	yes := true
	lbs := []model.LoadBalancer{
		{Name: "no-dns", Type: "network"},
		{Name: "k8s-app-api", DNSName: "k8s-app-api-123.elb.us-east-1.amazonaws.com", Type: "network", Scheme: "internet-facing",
			VPCID: "vpc-2", Zones: []string{"a", "b", "c"}, CrossZone: &yes},
		{Name: "k8s-web", DNSName: "internal-k8s-web-1.us-east-1.elb.amazonaws.com", Type: "application", Scheme: "internal", VPCID: "vpc-1"},
	}
	tests := []struct {
		name       string
		svc        model.Service
		ingresses  []model.Ingress
		want       routeWant
		wantVPC    bool
		wantSource model.AZDistribution
	}{
		{"NLB matched case-insensitively overrides scheme, VPC, evidence and source", nlbSvc, nil,
			routeWant{model.RouteNLBXZ, "internet-facing", "instance", "Cluster", "nlb", "k8s-app-api", "aws"},
			false, model.AZDistribution{"a": 1, "b": 1, "c": 1}},
		{"ALB matched without zones keeps caller distribution", albSvc, []model.Ingress{albIng},
			routeWant{model.RouteALB, "internal", "instance", "Cluster", "alb", "k8s-web", "aws"},
			true, model.AZDistribution{"a": 2, "b": 1}},
		{"no matching LB keeps annotation evidence", unmatched, nil,
			viaLB(model.RouteCLB, "internet-facing", "instance", "Cluster", "clb", "app/api"),
			true, model.AZDistribution{"a": 2, "b": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			s := model.Snapshot{
				Nodes:     nodesIn("a", "a", "b"),
				Services:  []model.Service{tt.svc},
				Ingresses: tt.ingresses,
				AWS:       &model.AWSInventory{VPCIDs: []string{"vpc-1"}, LoadBalancers: lbs},
			}

			// Act
			p := onlyPath(t, s)

			// Assert
			if got := gotRoute(p); got != tt.want {
				t.Errorf("route = %+v\nwant    %+v", got, tt.want)
			}
			if p.Facts.SameVPC != tt.wantVPC || p.Source.Namespace != "app" || !maps.Equal(p.Source.AZDist, tt.wantSource) {
				t.Errorf("sameVPC=%v source=%+v, want sameVPC=%v AZDist=%v", p.Facts.SameVPC, p.Source, tt.wantVPC, tt.wantSource)
			}
		})
	}
}

func TestBuildPathsNLBEffectiveCrossZoneAndTargetType(t *testing.T) {
	on, off := true, false
	tg := func(targetType, crossZone string) model.TargetGroup {
		return model.TargetGroup{TargetType: targetType, CrossZone: crossZone}
	}
	tests := []struct {
		name           string
		annotations    map[string]string
		class          string
		lbCrossZone    *bool
		targetGroups   []model.TargetGroup
		wantRoute      model.RouteKind
		wantTargetType string
	}{
		{"any target group true beats LB off", nil, lbcNLBClass, &off,
			[]model.TargetGroup{tg("ip", "false"), tg("ip", "true")}, model.RouteNLBXZ, "ip"},
		{"all target groups false beat LB on", nil, lbcNLBClass, &on,
			[]model.TargetGroup{tg("instance", "false"), tg("instance", "false")}, model.RouteNLBZonal, "instance"},
		{"mixed false and LB configuration uses LB attribute", nil, lbcNLBClass, &on,
			[]model.TargetGroup{tg("ip", "false"), tg("instance", "use_load_balancer_configuration")}, model.RouteNLBXZ, "instance"},
		{"unknown target group attribute uses LB attribute", nil, lbcNLBClass, &off,
			[]model.TargetGroup{tg("lambda", "")}, model.RouteNLBZonal, "instance"},
		{"LB attribute beats annotation", map[string]string{annLBCrossZone: "true"}, lbcNLBClass, &off,
			nil, model.RouteNLBZonal, "instance"},
		{"annotation used when LB attribute unknown", map[string]string{annLBCrossZone: "true"}, lbcNLBClass, nil,
			nil, model.RouteNLBXZ, "instance"},
		{"nothing known stays unknown", nil, lbcNLBClass, nil, nil, model.RouteNLB, "instance"},
		{"in-tree default applies when LB attribute unknown", map[string]string{annLBType: "nlb"}, "", nil,
			nil, model.RouteNLBZonal, "instance"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := lbService(tt.class, tt.annotations)
			svc.LBHostnames = []string{"nlb.example"}
			lb := model.LoadBalancer{Name: "nlb", DNSName: "nlb.example", Type: "network", Scheme: "internal", VPCID: "vpc-1",
				CrossZone: tt.lbCrossZone, TargetGroups: tt.targetGroups}
			s := model.Snapshot{Nodes: nodesIn("a", "b"), Services: []model.Service{svc},
				AWS: &model.AWSInventory{VPCIDs: []string{"vpc-1"}, LoadBalancers: []model.LoadBalancer{lb}}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.Route != tt.wantRoute || p.Facts.TargetType != tt.wantTargetType || p.Facts.Evidence != "aws" {
				t.Errorf("route/targetType/evidence = %s/%s/%s, want %s/%s/aws",
					p.Facts.Route, p.Facts.TargetType, p.Facts.Evidence, tt.wantRoute, tt.wantTargetType)
			}
		})
	}
}

func TestBuildPathsSpreadIncludesCrossZoneNLBZones(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name       string
		svc        model.Service
		ingresses  []model.Ingress
		lb         model.LoadBalancer
		wantSpread string
		wantRule   string
	}{
		{"cross-zone NLB in three zones in front of one-zone nodes",
			withHostname(lbService(lbcNLBClass, map[string]string{annNLBTargetType: "ip"}), "nlb.example"), nil,
			model.LoadBalancer{Name: "nlb", DNSName: "nlb.example", Type: "network", Scheme: "internet-facing", VPCID: "vpc-1",
				Zones: []string{"a", "b", "c"}, CrossZone: &no, TargetGroups: []model.TargetGroup{{TargetType: "ip", CrossZone: "true"}}},
			"multi", "nlb-cross-zone"},
		{"zonal NLB in three zones in front of one-zone nodes stays single",
			withHostname(lbService(lbcNLBClass, nil), "nlb.example"), nil,
			model.LoadBalancer{Name: "nlb", DNSName: "nlb.example", Type: "network", Scheme: "internal", VPCID: "vpc-1",
				Zones: []string{"a", "b", "c"}, CrossZone: &no},
			"single", "same-az"},
		{"ALB instance targets in two zones in front of one-zone nodes stays single",
			clusterIPService("api", readyEP("10.0.0.1", "a")),
			[]model.Ingress{{Namespace: "app", Name: "web", ClassName: "alb", Services: []string{"api"}, LBHostnames: []string{"alb.example"}}},
			model.LoadBalancer{Name: "alb", DNSName: "alb.example", Type: "application", Scheme: "internal", VPCID: "vpc-1",
				Zones: []string{"a", "b"}, CrossZone: &yes, TargetGroups: []model.TargetGroup{{TargetType: "instance"}}},
			"single", "same-az"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := tt.svc
			svc.Endpoints = []model.Endpoint{readyEP("10.0.0.1", "a"), readyEP("10.0.0.2", "a")}
			s := model.Snapshot{Nodes: nodesIn("a", "a"), Services: []model.Service{svc}, Ingresses: tt.ingresses,
				AWS: &model.AWSInventory{VPCIDs: []string{"vpc-1"}, LoadBalancers: []model.LoadBalancer{tt.lb}}}

			// Act
			judged, err := Apply(BuildPaths(s), Default(), s, 0)

			// Assert
			if err != nil || len(judged) != 1 {
				t.Fatalf("Apply(BuildPaths()) = %+v, %v; want one path", judged, err)
			}
			p := judged[0]
			if p.Facts.AZSpread != tt.wantSpread || p.RuleID != tt.wantRule {
				t.Errorf("spread/rule = %s/%s, want %s/%s (facts %+v)", p.Facts.AZSpread, p.RuleID, tt.wantSpread, tt.wantRule, p.Facts)
			}
		})
	}
}

func TestBuildPathsZoneCoverageUsesLoadBalancerZones(t *testing.T) {
	// Arrange
	svc := withHostname(lbService(lbcNLBClass, nil), "nlb.example")
	lb := model.LoadBalancer{Name: "nlb", DNSName: "nlb.example", Type: "network", Scheme: "internal", VPCID: "vpc-1", Zones: []string{"a"}}
	s := model.Snapshot{Nodes: nodesIn("a", "b", "c"), Services: []model.Service{svc},
		AWS: &model.AWSInventory{VPCIDs: []string{"vpc-1"}, LoadBalancers: []model.LoadBalancer{lb}}}

	// Act
	p := onlyPath(t, s)

	// Assert
	if p.Facts.ZoneCoverage != "full" {
		t.Errorf("ZoneCoverage = %s, want full (LB zone a is covered by endpoints in a and b)", p.Facts.ZoneCoverage)
	}
}

func withHostname(svc model.Service, hostname string) model.Service {
	svc.LBHostnames = []string{hostname}
	return svc
}

func TestBuildPathsDirectIngressBackend(t *testing.T) {
	nginx := model.IngressClass{Name: "nginx", Controller: "k8s.io/ingress-nginx"}
	ingress := func(ns, class string, ann map[string]string, services ...string) model.Ingress {
		return model.Ingress{Namespace: ns, Name: "web", ClassName: class, Annotations: ann, Services: services}
	}
	tests := []struct {
		name      string
		ingresses []model.Ingress
		want      bool
	}{
		{"ingress-nginx backend", []model.Ingress{ingress("app", "nginx", nil, "api")}, true},
		{"unknown ingress class backend", []model.Ingress{ingress("app", "traefik", nil, "api")}, true},
		{"ingress without any class", []model.Ingress{ingress("app", "", nil, "api")}, true},
		{"ingress-nginx with service-upstream", []model.Ingress{ingress("app", "nginx", map[string]string{annServiceUpstream: "true"}, "api")}, false},
		{"service-upstream other than true", []model.Ingress{ingress("app", "nginx", map[string]string{annServiceUpstream: "yes"}, "api")}, true},
		{"one direct ingress among upstream ones", []model.Ingress{
			ingress("app", "nginx", map[string]string{annServiceUpstream: "true"}, "api"), ingress("app", "nginx", nil, "api")}, true},
		{"ALB ingress is not a pod-IP dialer here", []model.Ingress{ingress("app", "alb", nil, "api")}, false},
		{"ingress for another service", []model.Ingress{ingress("app", "nginx", nil, "web")}, false},
		{"ingress in another namespace", []model.Ingress{ingress("other", "nginx", nil, "api")}, false},
		{"no ingress", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := clusterIPService("api", readyEP("10.0.0.1", "a", "a"), readyEP("10.0.1.1", "b", "b"))
			s := model.Snapshot{KubeProxyVersion: "v1.33.0", Nodes: nodesIn("a", "b"), Services: []model.Service{svc},
				Ingresses: tt.ingresses, IngressClasses: []model.IngressClass{nginx}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.DirectIngressBackend != tt.want {
				t.Errorf("DirectIngressBackend = %v, want %v", p.Facts.DirectIngressBackend, tt.want)
			}
		})
	}
}

func TestBuildPathsHintsConfigured(t *testing.T) {
	tests := []struct {
		name                string
		trafficDistribution string
		annotations         map[string]string
		want                bool
	}{
		{"nothing set", "", nil, false},
		{"trafficDistribution PreferClose", "PreferClose", nil, true},
		{"trafficDistribution PreferSameNode", "PreferSameNode", nil, true},
		{"topology-mode Auto", "", map[string]string{annTopologyMode: "Auto"}, true},
		{"deprecated topology-aware-hints auto", "", map[string]string{annTopologyAwareHints: "auto"}, true},
		{"topology-mode Disabled", "", map[string]string{annTopologyMode: "Disabled"}, false},
		{"empty topology-mode", "", map[string]string{annTopologyMode: ""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			svc := clusterIPService("api", readyEP("10.0.0.1", "a", "a"), readyEP("10.0.1.1", "b", "b"))
			svc.TrafficDistribution, svc.Annotations = tt.trafficDistribution, tt.annotations
			s := model.Snapshot{Nodes: nodesIn("a", "b"), Services: []model.Service{svc}}

			// Act
			p := onlyPath(t, s)

			// Assert
			if p.Facts.HintsConfigured != tt.want || p.Facts.TopologyHints {
				t.Errorf("HintsConfigured/TopologyHints = %v/%v, want %v/false (kube-proxy unknown)", p.Facts.HintsConfigured, p.Facts.TopologyHints, tt.want)
			}
		})
	}
}
