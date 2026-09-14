package model

// Snapshot is everything collected, with no judgment applied. It is also the fixture format
// (`crossaz scan --dump-fixture`, `--fixture file.json`), so keep it JSON-stable.
type Snapshot struct {
	Cluster string `json:"cluster"`
	Region  string `json:"region"`

	// L0 — Kubernetes
	ServerVersion    string         `json:"serverVersion,omitempty"`    // control plane, e.g. "v1.33.4-eks-..."; "" unknown
	KubeProxyVersion string         `json:"kubeProxyVersion,omitempty"` // image tag of kube-system/kube-proxy DaemonSet; "" = not found (replaced or unknown)
	Nodes            []Node         `json:"nodes"`
	Services         []Service      `json:"services"` // endpoints already joined from EndpointSlices
	Pods             []Pod          `json:"pods"`
	Ingresses        []Ingress      `json:"ingresses"`
	IngressClasses   []IngressClass `json:"ingressClasses"`

	// L1 — AWS read-only. nil = layer not available.
	AWS *AWSInventory `json:"aws,omitempty"`

	// L2 — Prometheus. nil = layer not available. Key "namespace/pod".
	Volume *VolumeData `json:"volume,omitempty"`

	// L3 — Cost Explorer. nil = not requested/available.
	Cost *CostData `json:"cost,omitempty"`

	// Non-fatal collection problems (missing permissions, partial data). Shown to the user.
	Warnings []string `json:"warnings,omitempty"`
}

// Node is a Kubernetes node.
type Node struct {
	Name       string `json:"name"`
	Zone       string `json:"zone"`                 // topology.kubernetes.io/zone ("" if missing)
	Region     string `json:"region,omitempty"`     // topology.kubernetes.io/region
	InstanceID string `json:"instanceId,omitempty"` // from spec.providerID aws:///zone/i-xxx
}

// Pod is a Kubernetes pod (only fields we need).
type Pod struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	NodeName    string `json:"nodeName"`
	IP          string `json:"ip"`
	HostNetwork bool   `json:"hostNetwork,omitempty"`
	OwnerKind   string `json:"ownerKind,omitempty"` // ReplicaSet|StatefulSet|DaemonSet|Job|...
	OwnerName   string `json:"ownerName,omitempty"`
}

// Service is a Kubernetes Service with its EndpointSlice endpoints merged in.
type Service struct {
	Namespace             string            `json:"namespace"`
	Name                  string            `json:"name"`
	Type                  string            `json:"type"`      // ClusterIP|NodePort|LoadBalancer|ExternalName
	ClusterIP             string            `json:"clusterIP"` // "None" = headless
	Annotations           map[string]string `json:"annotations,omitempty"`
	LoadBalancerClass     string            `json:"loadBalancerClass,omitempty"`
	TrafficDistribution   string            `json:"trafficDistribution,omitempty"`
	ExternalTrafficPolicy string            `json:"externalTrafficPolicy,omitempty"` // Cluster|Local
	InternalTrafficPolicy string            `json:"internalTrafficPolicy,omitempty"` // Cluster|Local
	LBHostnames           []string          `json:"lbHostnames,omitempty"`           // status.loadBalancer.ingress[].hostname
	Endpoints             []Endpoint        `json:"endpoints"`
}

// Endpoint is one EndpointSlice endpoint (one address).
type Endpoint struct {
	IP       string   `json:"ip"`
	PodName  string   `json:"podName,omitempty"` // targetRef.name when targetRef.kind == Pod
	NodeName string   `json:"nodeName,omitempty"`
	Zone     string   `json:"zone,omitempty"`
	Ready    bool     `json:"ready"`
	ForZones []string `json:"forZones,omitempty"` // hints.forZones[].name
}

// Ingress is a networking.k8s.io/v1 Ingress (only fields we need).
type Ingress struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	ClassName   string            `json:"className,omitempty"` // spec.ingressClassName
	Annotations map[string]string `json:"annotations,omitempty"`
	Services    []string          `json:"services"` // backend service names (same namespace), defaultBackend included
	LBHostnames []string          `json:"lbHostnames,omitempty"`
}

// IngressClass maps a class name to its controller (e.g. ingress.k8s.aws/alb).
type IngressClass struct {
	Name       string `json:"name"`
	Controller string `json:"controller"`
	IsDefault  bool   `json:"isDefault,omitempty"`
}

// AWSInventory is the L1 read-only AWS view of the cluster's region.
type AWSInventory struct {
	VPCIDs        []string          `json:"vpcIds"`            // VPCs of the cluster's node instances
	Subnets       map[string]Subnet `json:"subnets"`           // subnet ID -> subnet
	IPZones       map[string]string `json:"ipZones,omitempty"` // private IP -> AZ, only for endpoint IPs lacking a zone
	LoadBalancers []LoadBalancer    `json:"loadBalancers"`
	RDS           []RDSInstance     `json:"rds"`
	Caches        []CacheCluster    `json:"caches"`
	MSK           []MSKCluster      `json:"msk"`
}

// Subnet is an EC2 subnet.
type Subnet struct {
	AZ    string `json:"az"`
	VPCID string `json:"vpcId"`
}

// LoadBalancer is an ELB (v2 or classic).
type LoadBalancer struct {
	Name         string        `json:"name"`
	DNSName      string        `json:"dnsName"`
	Type         string        `json:"type"`   // application|network|gateway|classic
	Scheme       string        `json:"scheme"` // internal|internet-facing
	VPCID        string        `json:"vpcId"`
	Zones        []string      `json:"zones"`
	CrossZone    *bool         `json:"crossZone,omitempty"` // LB attribute load_balancing.cross_zone.enabled; nil = unknown
	TargetGroups []TargetGroup `json:"targetGroups,omitempty"`
}

// TargetGroup is an ELBv2 target group attached to a load balancer.
type TargetGroup struct {
	Name       string `json:"name"`
	TargetType string `json:"targetType"`          // instance|ip|lambda|alb
	CrossZone  string `json:"crossZone,omitempty"` // "true"|"false"|"use_load_balancer_configuration"|""
}

// RDSInstance is an RDS DB instance (Aurora members carry ClusterID).
type RDSInstance struct {
	ID          string `json:"id"`
	ClusterID   string `json:"clusterId,omitempty"`
	Engine      string `json:"engine"`
	AZ          string `json:"az"`
	SecondaryAZ string `json:"secondaryAz,omitempty"` // Multi-AZ standby
	MultiAZ     bool   `json:"multiAz"`
	VPCID       string `json:"vpcId"`
}

// CacheCluster is an ElastiCache cache cluster.
type CacheCluster struct {
	ID        string   `json:"id"`
	Engine    string   `json:"engine"`
	GroupID   string   `json:"replicationGroupId,omitempty"`
	NodeZones []string `json:"nodeZones"` // one entry per cache node
	VPCID     string   `json:"vpcId"`
}

// MSKCluster is a PROVISIONED MSK cluster (serverless is skipped: no documented cross-AZ client charge).
type MSKCluster struct {
	Name        string   `json:"name"`
	BrokerZones []string `json:"brokerZones"` // one entry per broker; brokers are spread evenly over client subnets
	VPCID       string   `json:"vpcId"`
}

// VolumeData is L2 per-pod network volume.
type VolumeData struct {
	Window        string             `json:"window"`        // PromQL range used, e.g. "7d"
	RecvBytesPerS map[string]float64 `json:"recvBytesPerS"` // "namespace/pod" -> avg receive bytes/s
}

// CostData is L3 actual cost.
type CostData struct {
	Month     string  `json:"month"` // YYYY-MM
	ActualUSD float64 `json:"actualUsd"`
	UsageType string  `json:"usageType"`
}
