package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	k8sPageSize            = 500
	k8sKubeSystemNamespace = "kube-system"
	k8sKubeProxyName       = "kube-proxy"
	k8sInstanceIDPrefix    = "i-"
	k8sPodKind             = "Pod"
	k8sALBUseAnnotation    = "use-annotation"
	k8sALBActionPrefix     = "alb.ingress.kubernetes.io/actions."
)

type k8sCollector struct {
	cs kubernetes.Interface
}

// NewK8s returns a K8sCollector backed by a Kubernetes clientset. It only issues List/Get calls.
func NewK8s(cs kubernetes.Interface) K8sCollector {
	return k8sCollector{cs: cs}
}

// k8sPage lists one page and returns the converted items and the continue token.
type k8sPage[T any] func(ctx context.Context, opts metav1.ListOptions) ([]T, string, error)

func k8sListAll[T any](ctx context.Context, resource string, page k8sPage[T]) ([]T, error) {
	var out []T
	opts := metav1.ListOptions{Limit: k8sPageSize}
	for {
		items, next, err := page(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", resource, err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		opts.Continue = next
	}
}

// k8sConvert maps items through f, dropping those for which f reports false.
func k8sConvert[S, T any](items []S, f func(*S) (T, bool)) []T {
	out := make([]T, 0, len(items))
	for i := range items {
		if v, ok := f(&items[i]); ok {
			out = append(out, v)
		}
	}
	return out
}

func (k k8sCollector) Nodes(ctx context.Context) ([]model.Node, error) {
	return k8sListAll(ctx, "nodes", func(ctx context.Context, opts metav1.ListOptions) ([]model.Node, string, error) {
		l, err := k.cs.CoreV1().Nodes().List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sNode), l.Continue, nil
	})
}

func (k k8sCollector) Services(ctx context.Context) ([]model.Service, error) {
	return k8sListAll(ctx, "services", func(ctx context.Context, opts metav1.ListOptions) ([]model.Service, string, error) {
		l, err := k.cs.CoreV1().Services(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sService), l.Continue, nil
	})
}

func (k k8sCollector) EndpointSlices(ctx context.Context) ([]EndpointSlice, error) {
	return k8sListAll(ctx, "endpointslices", func(ctx context.Context, opts metav1.ListOptions) ([]EndpointSlice, string, error) {
		l, err := k.cs.DiscoveryV1().EndpointSlices(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sEndpointSlice), l.Continue, nil
	})
}

func (k k8sCollector) Pods(ctx context.Context) ([]model.Pod, error) {
	return k8sListAll(ctx, "pods", func(ctx context.Context, opts metav1.ListOptions) ([]model.Pod, string, error) {
		l, err := k.cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sPod), l.Continue, nil
	})
}

func (k k8sCollector) Ingresses(ctx context.Context) ([]model.Ingress, error) {
	return k8sListAll(ctx, "ingresses", func(ctx context.Context, opts metav1.ListOptions) ([]model.Ingress, string, error) {
		l, err := k.cs.NetworkingV1().Ingresses(metav1.NamespaceAll).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sIngress), l.Continue, nil
	})
}

func (k k8sCollector) IngressClasses(ctx context.Context) ([]model.IngressClass, error) {
	return k8sListAll(ctx, "ingressclasses", func(ctx context.Context, opts metav1.ListOptions) ([]model.IngressClass, string, error) {
		l, err := k.cs.NetworkingV1().IngressClasses().List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return k8sConvert(l.Items, k8sIngressClass), l.Continue, nil
	})
}

func (k k8sCollector) ServerVersion(ctx context.Context) (string, error) {
	info, err := discovery.ToServerVersionInterfaceWithContext(k.cs.Discovery()).ServerVersionWithContext(ctx)
	if err != nil {
		return "", fmt.Errorf("discover server version: %w", err)
	}
	return info.GitVersion, nil
}

func (k k8sCollector) KubeProxyVersion(ctx context.Context) (string, error) {
	ds, err := k.cs.AppsV1().DaemonSets(k8sKubeSystemNamespace).Get(ctx, k8sKubeProxyName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get daemonset %s/%s: %w", k8sKubeSystemNamespace, k8sKubeProxyName, err)
	}
	return k8sImageTag(k8sKubeProxyImage(ds.Spec.Template.Spec.Containers)), nil
}

func k8sKubeProxyImage(containers []corev1.Container) string {
	if len(containers) == 0 {
		return ""
	}
	for i := range containers {
		if containers[i].Name == k8sKubeProxyName {
			return containers[i].Image
		}
	}
	return containers[0].Image
}

// k8sImageTag returns the tag of an image reference, ignoring any digest and registry port.
func k8sImageTag(image string) string {
	if at := strings.Index(image, "@"); at >= 0 {
		image = image[:at]
	}
	name := image[strings.LastIndex(image, "/")+1:]
	colon := strings.LastIndex(name, ":")
	if colon < 0 {
		return ""
	}
	return name[colon+1:]
}

func k8sNode(n *corev1.Node) (model.Node, bool) {
	return model.Node{
		Name:       n.Name,
		Zone:       k8sFirstLabel(n.Labels, corev1.LabelTopologyZone, corev1.LabelFailureDomainBetaZone),
		Region:     k8sFirstLabel(n.Labels, corev1.LabelTopologyRegion, corev1.LabelFailureDomainBetaRegion),
		InstanceID: k8sInstanceID(n.Spec.ProviderID),
	}, true
}

func k8sFirstLabel(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

// k8sInstanceID extracts i-xxx from a providerID like aws:///us-west-2a/i-0abc.
func k8sInstanceID(providerID string) string {
	id := providerID[strings.LastIndex(providerID, "/")+1:]
	if !strings.HasPrefix(id, k8sInstanceIDPrefix) {
		return ""
	}
	return id
}

func k8sService(s *corev1.Service) (model.Service, bool) {
	return model.Service{
		Namespace:             s.Namespace,
		Name:                  s.Name,
		Type:                  string(s.Spec.Type),
		ClusterIP:             s.Spec.ClusterIP,
		Annotations:           maps.Clone(s.Annotations),
		LoadBalancerClass:     k8sDeref(s.Spec.LoadBalancerClass),
		TrafficDistribution:   k8sDeref(s.Spec.TrafficDistribution),
		ExternalTrafficPolicy: string(s.Spec.ExternalTrafficPolicy),
		InternalTrafficPolicy: string(k8sDeref(s.Spec.InternalTrafficPolicy)),
		LBHostnames: k8sHostnames(s.Status.LoadBalancer.Ingress,
			func(i corev1.LoadBalancerIngress) string { return i.Hostname }),
	}, true
}

func k8sEndpointSlice(es *discoveryv1.EndpointSlice) (EndpointSlice, bool) {
	svc := es.Labels[discoveryv1.LabelServiceName]
	if svc == "" {
		return EndpointSlice{}, false
	}
	var eps []model.Endpoint
	for i := range es.Endpoints {
		eps = append(eps, k8sEndpoints(&es.Endpoints[i])...)
	}
	return EndpointSlice{Namespace: es.Namespace, ServiceName: svc, Endpoints: eps}, true
}

// k8sEndpoints expands one EndpointSlice endpoint into one model.Endpoint per address.
func k8sEndpoints(ep *discoveryv1.Endpoint) []model.Endpoint {
	podName := ""
	if ep.TargetRef != nil && ep.TargetRef.Kind == k8sPodKind {
		podName = ep.TargetRef.Name
	}
	var forZones []string
	if ep.Hints != nil {
		for _, z := range ep.Hints.ForZones {
			forZones = append(forZones, z.Name)
		}
	}
	out := make([]model.Endpoint, 0, len(ep.Addresses))
	for _, ip := range ep.Addresses {
		out = append(out, model.Endpoint{
			IP:       ip,
			PodName:  podName,
			NodeName: k8sDeref(ep.NodeName),
			Zone:     k8sDeref(ep.Zone),
			Ready:    ep.Conditions.Ready == nil || *ep.Conditions.Ready,
			ForZones: slices.Clone(forZones),
		})
	}
	return out
}

func k8sPod(p *corev1.Pod) (model.Pod, bool) {
	if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
		return model.Pod{}, false
	}
	pod := model.Pod{
		Namespace:   p.Namespace,
		Name:        p.Name,
		NodeName:    p.Spec.NodeName,
		IP:          p.Status.PodIP,
		HostNetwork: p.Spec.HostNetwork,
	}
	owner := metav1.GetControllerOfNoCopy(p)
	if owner == nil && len(p.OwnerReferences) > 0 {
		owner = &p.OwnerReferences[0]
	}
	if owner != nil {
		pod.OwnerKind, pod.OwnerName = owner.Kind, owner.Name
	}
	return pod, true
}

func k8sIngress(ing *networkingv1.Ingress) (model.Ingress, bool) {
	return model.Ingress{
		Namespace:   ing.Namespace,
		Name:        ing.Name,
		ClassName:   k8sDeref(ing.Spec.IngressClassName),
		Annotations: maps.Clone(ing.Annotations),
		Services:    k8sIngressServices(&ing.Spec, ing.Annotations),
		LBHostnames: k8sHostnames(ing.Status.LoadBalancer.Ingress,
			func(i networkingv1.IngressLoadBalancerIngress) string { return i.Hostname }),
	}, true
}

// k8sIngressServices returns the sorted unique backend service names of an Ingress.
// An ALB "use-annotation" backend names an action, not a Service: its services come from the action's JSON.
func k8sIngressServices(spec *networkingv1.IngressSpec, annotations map[string]string) []string {
	names := []string{}
	for _, b := range k8sIngressBackends(spec) {
		if b.Port.Name != k8sALBUseAnnotation {
			names = append(names, b.Name)
			continue
		}
		names = append(names, k8sALBActionServices(annotations[k8sALBActionPrefix+b.Name])...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func k8sIngressBackends(spec *networkingv1.IngressSpec) []*networkingv1.IngressServiceBackend {
	var out []*networkingv1.IngressServiceBackend
	if b := spec.DefaultBackend; b != nil && b.Service != nil {
		out = append(out, b.Service)
	}
	for _, rule := range spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			if p.Backend.Service != nil {
				out = append(out, p.Backend.Service)
			}
		}
	}
	return out
}

// k8sALBAction is the part of an AWS Load Balancer Controller action that names Services.
// encoding/json matches keys case-insensitively, so the legacy capitalized form decodes too.
type k8sALBAction struct {
	ServiceName   string
	ForwardConfig *struct {
		TargetGroups []struct{ ServiceName string }
	}
}

// k8sALBActionServices returns the services an action forwards to. A missing or malformed
// annotation yields none: the controller rejects it, so the ALB sends that action nothing.
func k8sALBActionServices(raw string) []string {
	var action k8sALBAction
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		return nil
	}
	names := []string{action.ServiceName}
	if action.ForwardConfig != nil {
		for _, tg := range action.ForwardConfig.TargetGroups {
			names = append(names, tg.ServiceName)
		}
	}
	return slices.DeleteFunc(names, func(n string) bool { return n == "" })
}

func k8sIngressClass(ic *networkingv1.IngressClass) (model.IngressClass, bool) {
	return model.IngressClass{
		Name:       ic.Name,
		Controller: ic.Spec.Controller,
		IsDefault:  ic.Annotations[networkingv1.AnnotationIsDefaultIngressClass] == "true",
	}, true
}

func k8sHostnames[T any](ingress []T, hostname func(T) string) []string {
	var out []string
	for _, i := range ingress {
		if h := hostname(i); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func k8sDeref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
