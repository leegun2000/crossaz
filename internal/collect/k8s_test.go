package collect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func k8sTestPtr[T any](v T) *T { return &v }

func k8sTestMeta(namespace, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: namespace, Name: name}
}

// k8sTestByName indexes collector output so tests do not depend on fake list order.
func k8sTestByName[T any](t *testing.T, items []T, key func(T) string) map[string]T {
	t.Helper()
	out := make(map[string]T, len(items))
	for _, item := range items {
		out[key(item)] = item
	}
	if len(out) != len(items) {
		t.Fatalf("duplicate keys in %d items", len(items))
	}
	return out
}

func k8sTestFailList(cs *fake.Clientset, resource string) {
	cs.PrependReactor("list", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	})
}

func TestNewK8sNodesMapsZoneRegionAndInstanceID(t *testing.T) {
	// Arrange
	node := func(name string, labels map[string]string, providerID string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
			Spec:       corev1.NodeSpec{ProviderID: providerID},
		}
	}
	cs := fake.NewClientset(
		node("stable", map[string]string{
			corev1.LabelTopologyZone:            "us-west-2a",
			corev1.LabelTopologyRegion:          "us-west-2",
			corev1.LabelFailureDomainBetaZone:   "ignored",
			corev1.LabelFailureDomainBetaRegion: "ignored",
		}, "aws:///us-west-2a/i-0123456789abcdef0"),
		node("beta", map[string]string{
			corev1.LabelFailureDomainBetaZone:   "us-west-2b",
			corev1.LabelFailureDomainBetaRegion: "us-west-2",
		}, "aws:///us-west-2b/i-0beta"),
		node("kind", nil, "kind://docker/kind/kind-control-plane"),
		node("bare", nil, ""),
	)
	want := map[string]model.Node{
		"stable": {Name: "stable", Zone: "us-west-2a", Region: "us-west-2", InstanceID: "i-0123456789abcdef0"},
		"beta":   {Name: "beta", Zone: "us-west-2b", Region: "us-west-2", InstanceID: "i-0beta"},
		"kind":   {Name: "kind"},
		"bare":   {Name: "bare"},
	}

	// Act
	nodes, err := NewK8s(cs).Nodes(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Nodes() error = %v", err)
	}
	got := k8sTestByName(t, nodes, func(n model.Node) string { return n.Name })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Nodes() = %+v, want %+v", got, want)
	}
}

func TestNewK8sServicesMapsSpecAndStatus(t *testing.T) {
	// Arrange
	cs := fake.NewClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "web", Name: "front", Annotations: map[string]string{"a": "b"}},
			Spec: corev1.ServiceSpec{
				Type:                  corev1.ServiceTypeLoadBalancer,
				ClusterIP:             "10.100.0.10",
				LoadBalancerClass:     k8sTestPtr("service.k8s.aws/nlb"),
				TrafficDistribution:   k8sTestPtr("PreferClose"),
				ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
				InternalTrafficPolicy: k8sTestPtr(corev1.ServiceInternalTrafficPolicyCluster),
			},
			Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{
				{Hostname: "k8s-web-front.elb.us-west-2.amazonaws.com"},
				{IP: "203.0.113.10"},
			}}},
		},
		&corev1.Service{
			ObjectMeta: k8sTestMeta("db", "pg"),
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone},
		},
	)
	want := map[string]model.Service{
		"web/front": {
			Namespace: "web", Name: "front", Type: "LoadBalancer", ClusterIP: "10.100.0.10",
			Annotations:           map[string]string{"a": "b"},
			LoadBalancerClass:     "service.k8s.aws/nlb",
			TrafficDistribution:   "PreferClose",
			ExternalTrafficPolicy: "Local",
			InternalTrafficPolicy: "Cluster",
			LBHostnames:           []string{"k8s-web-front.elb.us-west-2.amazonaws.com"},
		},
		"db/pg": {Namespace: "db", Name: "pg", Type: "ClusterIP", ClusterIP: "None"},
	}

	// Act
	services, err := NewK8s(cs).Services(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	got := k8sTestByName(t, services, func(s model.Service) string { return s.Namespace + "/" + s.Name })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Services() = %+v, want %+v", got, want)
	}
}

func TestNewK8sEndpointSlicesExpandsAddresses(t *testing.T) {
	// Arrange
	labeled := func(name, service string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Namespace: "web", Name: name, Labels: map[string]string{discoveryv1.LabelServiceName: service}}
	}
	cs := fake.NewClientset(
		&discoveryv1.EndpointSlice{
			ObjectMeta: labeled("front-abc", "front"),
			Endpoints: []discoveryv1.Endpoint{
				{
					Addresses: []string{"10.0.1.5", "10.0.1.6"},
					TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "front-1"},
					NodeName:  k8sTestPtr("node-a"),
					Zone:      k8sTestPtr("us-west-2a"),
					Hints:     &discoveryv1.EndpointHints{ForZones: []discoveryv1.ForZone{{Name: "us-west-2a"}, {Name: "us-west-2b"}}},
				},
				{
					Addresses:  []string{"10.0.2.7"},
					Conditions: discoveryv1.EndpointConditions{Ready: k8sTestPtr(false)},
					TargetRef:  &corev1.ObjectReference{Kind: "Node", Name: "node-b"},
				},
				{
					Addresses:  []string{"10.0.3.8"},
					Conditions: discoveryv1.EndpointConditions{Ready: k8sTestPtr(true)},
				},
			},
		},
		&discoveryv1.EndpointSlice{ObjectMeta: k8sTestMeta("web", "orphan"), Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.9.9"}}}},
	)
	hints := []string{"us-west-2a", "us-west-2b"}
	want := []EndpointSlice{{
		Namespace:   "web",
		ServiceName: "front",
		Endpoints: []model.Endpoint{
			{IP: "10.0.1.5", PodName: "front-1", NodeName: "node-a", Zone: "us-west-2a", Ready: true, ForZones: hints},
			{IP: "10.0.1.6", PodName: "front-1", NodeName: "node-a", Zone: "us-west-2a", Ready: true, ForZones: hints},
			{IP: "10.0.2.7", Ready: false},
			{IP: "10.0.3.8", Ready: true},
		},
	}}

	// Act
	got, err := NewK8s(cs).EndpointSlices(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("EndpointSlices() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EndpointSlices() = %+v, want %+v", got, want)
	}
	if &got[0].Endpoints[0].ForZones[0] == &got[0].Endpoints[1].ForZones[0] {
		t.Error("endpoints of one address group share a ForZones backing array")
	}
}

func TestNewK8sPodsSkipsFinishedAndPicksControllerOwner(t *testing.T) {
	// Arrange
	pod := func(name string, phase corev1.PodPhase, owners []metav1.OwnerReference) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "web", Name: name, OwnerReferences: owners},
			Spec:       corev1.PodSpec{NodeName: "node-a", HostNetwork: name == "hostnet"},
			Status:     corev1.PodStatus{Phase: phase, PodIP: "10.0.1.5"},
		}
	}
	cs := fake.NewClientset(
		pod("controlled", corev1.PodRunning, []metav1.OwnerReference{
			{Kind: "Other", Name: "first"},
			{Kind: "ReplicaSet", Name: "front-7d9", Controller: k8sTestPtr(true)},
		}),
		pod("hostnet", corev1.PodPending, []metav1.OwnerReference{
			{Kind: "DaemonSet", Name: "agent", Controller: k8sTestPtr(false)},
			{Kind: "Other", Name: "second"},
		}),
		pod("orphan", corev1.PodRunning, nil),
		pod("done", corev1.PodSucceeded, nil),
		pod("crashed", corev1.PodFailed, nil),
	)
	base := model.Pod{Namespace: "web", NodeName: "node-a", IP: "10.0.1.5"}
	with := func(name, kind, owner string, hostNetwork bool) model.Pod {
		p := base
		p.Name, p.OwnerKind, p.OwnerName, p.HostNetwork = name, kind, owner, hostNetwork
		return p
	}
	want := map[string]model.Pod{
		"controlled": with("controlled", "ReplicaSet", "front-7d9", false),
		"hostnet":    with("hostnet", "DaemonSet", "agent", true),
		"orphan":     with("orphan", "", "", false),
	}

	// Act
	pods, err := NewK8s(cs).Pods(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pods() error = %v", err)
	}
	got := k8sTestByName(t, pods, func(p model.Pod) string { return p.Name })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Pods() = %+v, want %+v", got, want)
	}
}

func TestNewK8sIngressesCollectsBackendsAndHostnames(t *testing.T) {
	// Arrange
	backend := func(service string) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: service}}
	}
	paths := func(services ...string) *networkingv1.HTTPIngressRuleValue {
		v := &networkingv1.HTTPIngressRuleValue{}
		for _, s := range services {
			v.Paths = append(v.Paths, networkingv1.HTTPIngressPath{Backend: backend(s)})
		}
		return v
	}
	defaultBackend := backend("fallback")
	cs := fake.NewClientset(
		&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{Namespace: "web", Name: "main", Annotations: map[string]string{"alb.ingress.kubernetes.io/scheme": "internal"}},
			Spec: networkingv1.IngressSpec{
				IngressClassName: k8sTestPtr("alb"),
				DefaultBackend:   &defaultBackend,
				Rules: []networkingv1.IngressRule{
					{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: paths("front", "api")}},
					{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: paths("front")}},
					{Host: "no-http.example.com"},
					{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{Backend: networkingv1.IngressBackend{Resource: &corev1.TypedLocalObjectReference{Kind: "Bucket", Name: "static"}}}},
					}}},
				},
			},
			Status: networkingv1.IngressStatus{LoadBalancer: networkingv1.IngressLoadBalancerStatus{Ingress: []networkingv1.IngressLoadBalancerIngress{
				{Hostname: "internal-k8s-web-main.us-west-2.elb.amazonaws.com"},
				{IP: "10.0.0.1"},
			}}},
		},
		&networkingv1.Ingress{ObjectMeta: k8sTestMeta("web", "empty")},
	)
	want := map[string]model.Ingress{
		"main": {
			Namespace: "web", Name: "main", ClassName: "alb",
			Annotations: map[string]string{"alb.ingress.kubernetes.io/scheme": "internal"},
			Services:    []string{"api", "fallback", "front"},
			LBHostnames: []string{"internal-k8s-web-main.us-west-2.elb.amazonaws.com"},
		},
		"empty": {Namespace: "web", Name: "empty", Services: []string{}},
	}

	// Act
	ingresses, err := NewK8s(cs).Ingresses(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Ingresses() error = %v", err)
	}
	got := k8sTestByName(t, ingresses, func(i model.Ingress) string { return i.Name })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ingresses() = %+v, want %+v", got, want)
	}
}

func TestK8sIngressResolvesALBActionBackends(t *testing.T) {
	const action = "alb.ingress.kubernetes.io/actions."
	backend := func(service, portName string) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
			Name: service, Port: networkingv1.ServiceBackendPort{Name: portName},
		}}
	}
	forward := func(services ...string) string {
		groups := make([]string, 0, len(services))
		for _, s := range services {
			groups = append(groups, `{"serviceName":"`+s+`","servicePort":"80","weight":50}`)
		}
		return `{"type":"forward","forwardConfig":{"targetGroups":[` + strings.Join(groups, ",") + `]}}`
	}
	tests := []struct {
		name           string
		annotations    map[string]string
		defaultBackend *networkingv1.IngressBackend
		paths          []networkingv1.IngressBackend
		want           []string
	}{
		{
			name:        "Argo Rollouts root action forwards to stable and canary; action name dropped",
			annotations: map[string]string{action + "app-root": forward("app-stable", "app-canary")},
			paths:       []networkingv1.IngressBackend{backend("app-root", "use-annotation")},
			want:        []string{"app-canary", "app-stable"},
		},
		{
			name:        "action named after the stable service keeps it through the forward config",
			annotations: map[string]string{action + "app-stable": forward("app-stable", "app-canary")},
			paths:       []networkingv1.IngressBackend{backend("app-stable", "use-annotation")},
			want:        []string{"app-canary", "app-stable"},
		},
		{
			name: "legacy capitalized keys and top-level ServiceName decode",
			annotations: map[string]string{
				action + "old":     `{"Type":"forward","ServiceName":"legacy","ServicePort":"80"}`,
				action + "weights": `{"Type":"forward","ForwardConfig":{"TargetGroups":[{"ServiceName":"blue"}]}}`,
			},
			paths: []networkingv1.IngressBackend{backend("old", "use-annotation"), backend("weights", "use-annotation")},
			want:  []string{"blue", "legacy"},
		},
		{
			name:           "default backend action resolves and a plain backend of the same name is kept",
			annotations:    map[string]string{action + "blue-green": forward("blue", "green", "blue")},
			defaultBackend: k8sTestPtr(backend("blue-green", "use-annotation")),
			paths:          []networkingv1.IngressBackend{backend("blue-green", "http")},
			want:           []string{"blue", "blue-green", "green"},
		},
		{
			name:        "malformed action JSON is ignored without dropping other backends",
			annotations: map[string]string{action + "broken": `{"forwardConfig":`},
			paths:       []networkingv1.IngressBackend{backend("front", ""), backend("broken", "use-annotation")},
			want:        []string{"front"},
		},
		{
			name: "missing annotation, fixed-response and ARN-only forwards add nothing",
			annotations: map[string]string{
				action + "fixed": `{"type":"fixed-response","fixedResponseConfig":{"statusCode":"404"}}`,
				action + "arn":   `{"type":"forward","forwardConfig":{"targetGroups":[{"targetGroupARN":"arn:aws:elasticloadbalancing:tg/x"}]}}`,
			},
			paths: []networkingv1.IngressBackend{
				backend("missing", "use-annotation"), backend("fixed", "use-annotation"), backend("arn", "use-annotation"),
			},
			want: []string{},
		},
		{
			name:        "actions annotation is not read for a backend without use-annotation",
			annotations: map[string]string{action + "web": forward("other")},
			paths:       []networkingv1.IngressBackend{backend("web", "http")},
			want:        []string{"web"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			http := &networkingv1.HTTPIngressRuleValue{}
			for _, b := range tt.paths {
				http.Paths = append(http.Paths, networkingv1.HTTPIngressPath{Backend: b})
			}
			ing := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "app", Annotations: tt.annotations},
				Spec: networkingv1.IngressSpec{
					DefaultBackend: tt.defaultBackend,
					Rules:          []networkingv1.IngressRule{{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: http}}},
				},
			}

			// Act
			got, _ := k8sIngress(ing)

			// Assert
			if !reflect.DeepEqual(got.Services, tt.want) {
				t.Errorf("Services = %q, want %q", got.Services, tt.want)
			}
		})
	}
}

func TestNewK8sIngressClassesDetectsDefault(t *testing.T) {
	// Arrange
	class := func(name, controller, isDefault string) *networkingv1.IngressClass {
		return &networkingv1.IngressClass{
			ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: map[string]string{networkingv1.AnnotationIsDefaultIngressClass: isDefault}},
			Spec:       networkingv1.IngressClassSpec{Controller: controller},
		}
	}
	cs := fake.NewClientset(
		class("alb", "ingress.k8s.aws/alb", "true"),
		class("nginx", "k8s.io/ingress-nginx", "false"),
		class("other", "example.com/other", ""),
	)
	want := map[string]model.IngressClass{
		"alb":   {Name: "alb", Controller: "ingress.k8s.aws/alb", IsDefault: true},
		"nginx": {Name: "nginx", Controller: "k8s.io/ingress-nginx"},
		"other": {Name: "other", Controller: "example.com/other"},
	}

	// Act
	classes, err := NewK8s(cs).IngressClasses(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("IngressClasses() error = %v", err)
	}
	got := k8sTestByName(t, classes, func(c model.IngressClass) string { return c.Name })
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IngressClasses() = %+v, want %+v", got, want)
	}
}

func TestNewK8sListFollowsContinueTokens(t *testing.T) {
	// Arrange
	cs := fake.NewClientset()
	var seen []metav1.ListOptions
	cs.PrependReactor("list", "services", func(action k8stesting.Action) (bool, runtime.Object, error) {
		opts := action.(interface{ GetListOptions() metav1.ListOptions }).GetListOptions()
		seen = append(seen, opts)
		if opts.Continue == "" {
			return true, &corev1.ServiceList{
				ListMeta: metav1.ListMeta{Continue: "page-2"},
				Items:    []corev1.Service{{ObjectMeta: k8sTestMeta("a", "one")}},
			}, nil
		}
		return true, &corev1.ServiceList{Items: []corev1.Service{{ObjectMeta: k8sTestMeta("b", "two")}}}, nil
	})

	// Act
	services, err := NewK8s(cs).Services(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Services() error = %v", err)
	}
	if len(services) != 2 || services[0].Name != "one" || services[1].Name != "two" {
		t.Errorf("Services() = %+v, want one then two", services)
	}
	wantOpts := []metav1.ListOptions{{Limit: k8sPageSize}, {Limit: k8sPageSize, Continue: "page-2"}}
	if !reflect.DeepEqual(seen, wantOpts) {
		t.Errorf("list options = %+v, want %+v", seen, wantOpts)
	}
}

func TestNewK8sListErrorsNameTheResource(t *testing.T) {
	tests := []struct {
		resource string
		list     func(K8sCollector) error
	}{
		{"nodes", func(k K8sCollector) error { _, err := k.Nodes(context.Background()); return err }},
		{"services", func(k K8sCollector) error { _, err := k.Services(context.Background()); return err }},
		{"endpointslices", func(k K8sCollector) error { _, err := k.EndpointSlices(context.Background()); return err }},
		{"pods", func(k K8sCollector) error { _, err := k.Pods(context.Background()); return err }},
		{"ingresses", func(k K8sCollector) error { _, err := k.Ingresses(context.Background()); return err }},
		{"ingressclasses", func(k K8sCollector) error { _, err := k.IngressClasses(context.Background()); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			// Arrange
			cs := fake.NewClientset()
			k8sTestFailList(cs, tt.resource)

			// Act
			err := tt.list(NewK8s(cs))

			// Assert
			if err == nil || !strings.Contains(err.Error(), "list "+tt.resource) {
				t.Errorf("error = %v, want one naming %q", err, tt.resource)
			}
		})
	}
}

func TestNewK8sServerVersion(t *testing.T) {
	t.Run("returns git version", func(t *testing.T) {
		// Arrange
		cs := fake.NewClientset()
		cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.33.4-eks-a1b2c3"}

		// Act
		got, err := NewK8s(cs).ServerVersion(context.Background())

		// Assert
		if err != nil || got != "v1.33.4-eks-a1b2c3" {
			t.Errorf("ServerVersion() = %q, %v; want v1.33.4-eks-a1b2c3, nil", got, err)
		}
	})
	t.Run("wraps discovery error", func(t *testing.T) {
		// Arrange
		cs := fake.NewClientset()
		cs.PrependReactor("get", "version", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("forbidden")
		})

		// Act
		_, err := NewK8s(cs).ServerVersion(context.Background())

		// Assert
		if err == nil || !strings.Contains(err.Error(), "server version") {
			t.Errorf("ServerVersion() error = %v, want wrapped discovery error", err)
		}
	})
}

func TestNewK8sKubeProxyVersion(t *testing.T) {
	daemonSet := func(containers ...corev1.Container) *appsv1.DaemonSet {
		return &appsv1.DaemonSet{
			ObjectMeta: k8sTestMeta("kube-system", "kube-proxy"),
			Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: containers},
			}},
		}
	}
	proxy := func(image string) corev1.Container { return corev1.Container{Name: "kube-proxy", Image: image} }
	tests := []struct {
		name    string
		objects []runtime.Object
		want    string
	}{
		{"plain tag", []runtime.Object{daemonSet(proxy("registry.k8s.io/kube-proxy:v1.31.0"))}, "v1.31.0"},
		{"tag with digest", []runtime.Object{daemonSet(proxy("registry.k8s.io/kube-proxy:v1.32.1@sha256:0123abcd"))}, "v1.32.1"},
		{"digest only", []runtime.Object{daemonSet(proxy("registry.k8s.io/kube-proxy@sha256:0123abcd"))}, ""},
		{
			"registry with port",
			[]runtime.Object{daemonSet(proxy("602401143452.dkr.ecr.us-west-2.amazonaws.com:443/eks/kube-proxy:v1.31.2-eksbuild.3"))},
			"v1.31.2-eksbuild.3",
		},
		{"registry with port and no tag", []runtime.Object{daemonSet(proxy("localhost:5000/kube-proxy"))}, ""},
		{
			"named container preferred",
			[]runtime.Object{daemonSet(corev1.Container{Name: "sidecar", Image: "busybox:1.36"}, proxy("kube-proxy:v1.30.0"))},
			"v1.30.0",
		},
		{"first container fallback", []runtime.Object{daemonSet(corev1.Container{Name: "proxy", Image: "kube-proxy:v1.29.3"})}, "v1.29.3"},
		{"no containers", []runtime.Object{daemonSet()}, ""},
		{"missing daemonset", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			cs := fake.NewClientset(tt.objects...)

			// Act
			got, err := NewK8s(cs).KubeProxyVersion(context.Background())

			// Assert
			if err != nil || got != tt.want {
				t.Errorf("KubeProxyVersion() = %q, %v; want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestNewK8sKubeProxyVersionWrapsGetError(t *testing.T) {
	// Arrange
	cs := fake.NewClientset()
	cs.PrependReactor("get", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})

	// Act
	_, err := NewK8s(cs).KubeProxyVersion(context.Background())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "kube-system/kube-proxy") {
		t.Errorf("KubeProxyVersion() error = %v, want wrapped get error", err)
	}
}
