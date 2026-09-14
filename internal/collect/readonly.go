package collect

// Read-only guarantee (SPEC §7): aws.go and ce.go reach AWS only through the interfaces below,
// which declare nothing but Describe*/List*/Get* calls with their exact SDK signatures.
// A mutating call would have to be added here first; readonly_test.go rejects any interface
// method without a read-only prefix and any mutating SDK method name appearing in aws.go/ce.go.

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// EC2ReadOnly is the subset of *ec2.Client crossaz calls.
type EC2ReadOnly interface {
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeSubnets(ctx context.Context, params *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	DescribeNetworkInterfaces(ctx context.Context, params *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error)
}

// ELBv2ReadOnly is the subset of *elasticloadbalancingv2.Client crossaz calls.
type ELBv2ReadOnly interface {
	DescribeLoadBalancers(ctx context.Context, params *elasticloadbalancingv2.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error)
	DescribeLoadBalancerAttributes(ctx context.Context, params *elasticloadbalancingv2.DescribeLoadBalancerAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancerAttributesOutput, error)
	DescribeTargetGroups(ctx context.Context, params *elasticloadbalancingv2.DescribeTargetGroupsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error)
	DescribeTargetGroupAttributes(ctx context.Context, params *elasticloadbalancingv2.DescribeTargetGroupAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupAttributesOutput, error)
}

// ELBClassicReadOnly is the subset of *elasticloadbalancing.Client crossaz calls.
type ELBClassicReadOnly interface {
	DescribeLoadBalancers(ctx context.Context, params *elasticloadbalancing.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancersOutput, error)
}

// RDSReadOnly is the subset of *rds.Client crossaz calls.
type RDSReadOnly interface {
	DescribeDBInstances(ctx context.Context, params *rds.DescribeDBInstancesInput, optFns ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
}

// ElastiCacheReadOnly is the subset of *elasticache.Client crossaz calls.
type ElastiCacheReadOnly interface {
	DescribeCacheClusters(ctx context.Context, params *elasticache.DescribeCacheClustersInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeCacheClustersOutput, error)
	DescribeCacheSubnetGroups(ctx context.Context, params *elasticache.DescribeCacheSubnetGroupsInput, optFns ...func(*elasticache.Options)) (*elasticache.DescribeCacheSubnetGroupsOutput, error)
}

// KafkaReadOnly is the subset of *kafka.Client crossaz calls.
type KafkaReadOnly interface {
	ListClustersV2(ctx context.Context, params *kafka.ListClustersV2Input, optFns ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
}

// CostExplorerReadOnly is the subset of *costexplorer.Client crossaz calls.
type CostExplorerReadOnly interface {
	GetCostAndUsage(ctx context.Context, params *costexplorer.GetCostAndUsageInput, optFns ...func(*costexplorer.Options)) (*costexplorer.GetCostAndUsageOutput, error)
}

// AWSClients bundles the read-only clients the L1 collector needs.
type AWSClients struct {
	EC2         EC2ReadOnly
	ELBv2       ELBv2ReadOnly
	ELB         ELBClassicReadOnly
	RDS         RDSReadOnly
	ElastiCache ElastiCacheReadOnly
	Kafka       KafkaReadOnly
}

// The real SDK clients must keep satisfying the read-only interfaces.
var (
	_ EC2ReadOnly          = (*ec2.Client)(nil)
	_ ELBv2ReadOnly        = (*elasticloadbalancingv2.Client)(nil)
	_ ELBClassicReadOnly   = (*elasticloadbalancing.Client)(nil)
	_ RDSReadOnly          = (*rds.Client)(nil)
	_ ElastiCacheReadOnly  = (*elasticache.Client)(nil)
	_ KafkaReadOnly        = (*kafka.Client)(nil)
	_ CostExplorerReadOnly = (*costexplorer.Client)(nil)
)
