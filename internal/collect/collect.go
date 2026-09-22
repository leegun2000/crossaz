// Package collect does all I/O. Every source sits behind an interface so tests use fakes.
// Collectors return model types only; no SDK types escape this package.
package collect

import (
	"context"
	"time"

	"github.com/leegun2000/crossaz/internal/model"
)

// EndpointSlice is a discovery.k8s.io/v1 EndpointSlice reduced to what we need.
type EndpointSlice struct {
	Namespace   string           `json:"namespace"`
	ServiceName string           `json:"serviceName"` // label kubernetes.io/service-name
	Endpoints   []model.Endpoint `json:"endpoints"`
}

// K8sCollector reads the Kubernetes API (L0). List/Get only.
type K8sCollector interface {
	Nodes(ctx context.Context) ([]model.Node, error)
	Services(ctx context.Context) ([]model.Service, error) // Endpoints left empty
	EndpointSlices(ctx context.Context) ([]EndpointSlice, error)
	Pods(ctx context.Context) ([]model.Pod, error)
	Ingresses(ctx context.Context) ([]model.Ingress, error)
	IngressClasses(ctx context.Context) ([]model.IngressClass, error)
	// ServerVersion is the control plane git version (discovery).
	ServerVersion(ctx context.Context) (string, error)
	// KubeProxyVersion is the image tag of DaemonSet kube-system/kube-proxy; "" with nil error when absent.
	KubeProxyVersion(ctx context.Context) (string, error)
}

// AWSCollector reads AWS (L1). Implementations may only call Describe*/List*/Get* APIs,
// enforced by the read-only client interfaces in readonly.go.
type AWSCollector interface {
	// InstanceVPCs returns the distinct sorted VPC IDs and owning accounts (reservation OwnerId) of the given EC2 instances.
	InstanceVPCs(ctx context.Context, instanceIDs []string) (vpcs, accounts []string, err error)
	// Subnets returns subnet ID -> subnet for the given VPCs.
	Subnets(ctx context.Context, vpcIDs []string) (map[string]model.Subnet, error)
	// IPZones resolves private IPs to AZs via network interfaces in the given VPCs. Only called for IPs lacking a zone.
	// IPs whose interfaces disagree on AZ are left out.
	IPZones(ctx context.Context, vpcIDs, ips []string) (map[string]string, error)
	// LoadBalancers returns ELBv2 and Classic LBs in the given VPCs, with cross-zone and target group attributes.
	// On error it still returns the LBs it could describe; the error joins every failure.
	LoadBalancers(ctx context.Context, vpcIDs []string) ([]model.LoadBalancer, error)
	RDSInstances(ctx context.Context) ([]model.RDSInstance, error)
	ElastiCacheClusters(ctx context.Context) ([]model.CacheCluster, error)
	// MSKClusters returns PROVISIONED clusters only; needs subnets to resolve broker AZs and VPC.
	MSKClusters(ctx context.Context, subnets map[string]model.Subnet) ([]model.MSKCluster, error)
}

// VolumeCollector reads per-pod network volume (L2).
type VolumeCollector interface {
	// PodRecvBytesPerSecond returns "namespace/pod" -> average receive bytes/s over window (PromQL duration, e.g. "7d").
	PodRecvBytesPerSecond(ctx context.Context, window string) (map[string]float64, error)
}

// BillCollector reads the daily bill (L3). Implementations may only call ce:GetCostAndUsage.
type BillCollector interface {
	// DailyBill returns the in-scope data transfer lines of region for the UTC days [from, to), filtered to
	// accounts (region only, with a warning, when empty). A bill Cost Explorer cannot tell is Unknown with a
	// warning and a nil error, never $0; other failures are errors.
	DailyBill(ctx context.Context, region string, accounts []string, from, to time.Time) (model.Bill, error)
}
