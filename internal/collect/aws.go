package collect

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	elbclassic "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/leegun2000/crossaz/internal/model"
)

const (
	awsFilterChunk      = 200 // EC2 filter values per request
	awsCrossZoneAttr    = "load_balancing.cross_zone.enabled"
	awsClassicType      = "classic"
	awsMSKProvisioned   = "PROVISIONED"
	awsCacheMultipleAZs = "Multiple"
)

// awsPager is the shape shared by every SDK paginator.
type awsPager[Out, Opt any] interface {
	HasMorePages() bool
	NextPage(ctx context.Context, optFns ...func(*Opt)) (*Out, error)
}

type awsCollector struct{ c AWSClients }

// NewAWS returns an AWSCollector backed by the given read-only clients.
func NewAWS(c AWSClients) AWSCollector { return awsCollector{c: c} }

// NewAWSFromConfig builds the read-only clients from cfg.
func NewAWSFromConfig(cfg aws.Config) AWSCollector {
	return NewAWS(AWSClients{
		EC2:         ec2.NewFromConfig(cfg),
		ELBv2:       elbv2.NewFromConfig(cfg),
		ELB:         elbclassic.NewFromConfig(cfg),
		RDS:         rds.NewFromConfig(cfg),
		ElastiCache: elasticache.NewFromConfig(cfg),
		Kafka:       kafka.NewFromConfig(cfg),
	})
}

func (a awsCollector) InstanceVPCs(ctx context.Context, instanceIDs []string) ([]string, error) {
	vpcs := map[string]bool{}
	for chunk := range slices.Chunk(instanceIDs, awsFilterChunk) {
		in := &ec2.DescribeInstancesInput{Filters: awsFilter("instance-id", chunk)}
		err := awsEachPage(ctx, "ec2:DescribeInstances", ec2.NewDescribeInstancesPaginator(a.c.EC2, in),
			func(page *ec2.DescribeInstancesOutput) {
				for _, r := range page.Reservations {
					for _, inst := range r.Instances {
						vpcs[aws.ToString(inst.VpcId)] = true
					}
				}
			})
		if err != nil {
			return nil, err
		}
	}
	delete(vpcs, "")
	return slices.Sorted(maps.Keys(vpcs)), nil
}

func (a awsCollector) Subnets(ctx context.Context, vpcIDs []string) (map[string]model.Subnet, error) {
	subnets := map[string]model.Subnet{}
	if len(vpcIDs) == 0 {
		return subnets, nil
	}
	in := &ec2.DescribeSubnetsInput{Filters: awsFilter("vpc-id", vpcIDs)}
	err := awsEachPage(ctx, "ec2:DescribeSubnets", ec2.NewDescribeSubnetsPaginator(a.c.EC2, in),
		func(page *ec2.DescribeSubnetsOutput) {
			for _, s := range page.Subnets {
				subnets[aws.ToString(s.SubnetId)] = model.Subnet{AZ: aws.ToString(s.AvailabilityZone), VPCID: aws.ToString(s.VpcId)}
			}
		})
	if err != nil {
		return nil, err
	}
	return subnets, nil
}

// IPZones only looks at ENIs in vpcIDs; an IP whose ENIs disagree on AZ stays unresolved.
func (a awsCollector) IPZones(ctx context.Context, vpcIDs, ips []string) (map[string]string, error) {
	zones := map[string]string{}
	if len(vpcIDs) == 0 {
		return zones, nil
	}
	wanted := awsSet(ips)
	conflicting := map[string]bool{}
	for chunk := range slices.Chunk(ips, awsFilterChunk) {
		filters := slices.Concat(awsFilter("vpc-id", vpcIDs), awsFilter("addresses.private-ip-address", chunk))
		in := &ec2.DescribeNetworkInterfacesInput{Filters: filters}
		err := awsEachPage(ctx, "ec2:DescribeNetworkInterfaces", ec2.NewDescribeNetworkInterfacesPaginator(a.c.EC2, in),
			func(page *ec2.DescribeNetworkInterfacesOutput) {
				for _, ni := range page.NetworkInterfaces {
					zone := aws.ToString(ni.AvailabilityZone)
					for _, ip := range awsENIAddresses(ni) {
						if !wanted[ip] {
							continue
						}
						if seen, ok := zones[ip]; ok && seen != zone {
							conflicting[ip] = true
						}
						zones[ip] = zone
					}
				}
			})
		if err != nil {
			return nil, err
		}
	}
	maps.DeleteFunc(zones, func(ip, _ string) bool { return conflicting[ip] })
	return zones, nil
}

// LoadBalancers returns every LB it could describe, plus the joined errors of those it could not.
func (a awsCollector) LoadBalancers(ctx context.Context, vpcIDs []string) ([]model.LoadBalancer, error) {
	if len(vpcIDs) == 0 {
		return []model.LoadBalancer{}, nil
	}
	inVPC := awsSet(vpcIDs)
	v2, errs := a.elbv2LoadBalancers(ctx, inVPC)
	classic, err := a.classicLoadBalancers(ctx, inVPC)
	all := slices.Concat(v2, classic)
	slices.SortFunc(all, func(x, y model.LoadBalancer) int {
		return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.Type, y.Type))
	})
	return all, errors.Join(append(errs, err)...)
}

// elbv2LoadBalancers skips an LB whose attributes or target groups fail and reports its error.
func (a awsCollector) elbv2LoadBalancers(ctx context.Context, inVPC map[string]bool) ([]model.LoadBalancer, []error) {
	var kept []elbv2types.LoadBalancer
	err := awsEachPage(ctx, "elasticloadbalancing:DescribeLoadBalancers",
		elbv2.NewDescribeLoadBalancersPaginator(a.c.ELBv2, &elbv2.DescribeLoadBalancersInput{}),
		func(page *elbv2.DescribeLoadBalancersOutput) {
			for _, lb := range page.LoadBalancers {
				if inVPC[aws.ToString(lb.VpcId)] {
					kept = append(kept, lb)
				}
			}
		})
	if err != nil {
		return nil, []error{err}
	}
	out := make([]model.LoadBalancer, 0, len(kept))
	var errs []error
	for _, lb := range kept {
		m, err := a.elbv2LoadBalancer(ctx, lb)
		if err != nil {
			errs = append(errs, fmt.Errorf("load balancer %s: %w", aws.ToString(lb.LoadBalancerName), err))
			continue
		}
		out = append(out, m)
	}
	return out, errs
}

func (a awsCollector) elbv2LoadBalancer(ctx context.Context, lb elbv2types.LoadBalancer) (model.LoadBalancer, error) {
	zones := make([]string, 0, len(lb.AvailabilityZones))
	for _, z := range lb.AvailabilityZones {
		zones = append(zones, aws.ToString(z.ZoneName))
	}
	slices.Sort(zones)
	out := model.LoadBalancer{
		Name:    aws.ToString(lb.LoadBalancerName),
		DNSName: aws.ToString(lb.DNSName),
		Type:    string(lb.Type),
		Scheme:  string(lb.Scheme),
		VPCID:   aws.ToString(lb.VpcId),
		Zones:   zones,
	}
	isNetwork := lb.Type == elbv2types.LoadBalancerTypeEnumNetwork
	if isNetwork || lb.Type == elbv2types.LoadBalancerTypeEnumGateway {
		crossZone, err := a.lbCrossZone(ctx, lb.LoadBalancerArn)
		if err != nil {
			return model.LoadBalancer{}, err
		}
		out.CrossZone = crossZone
	}
	tgs, err := a.targetGroups(ctx, lb.LoadBalancerArn, isNetwork)
	if err != nil {
		return model.LoadBalancer{}, err
	}
	out.TargetGroups = tgs
	return out, nil
}

// lbCrossZone returns nil when the attribute is absent.
func (a awsCollector) lbCrossZone(ctx context.Context, arn *string) (*bool, error) {
	const api = "elasticloadbalancing:DescribeLoadBalancerAttributes"
	out, err := a.c.ELBv2.DescribeLoadBalancerAttributes(ctx, &elbv2.DescribeLoadBalancerAttributesInput{LoadBalancerArn: arn})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", api, err)
	}
	for _, attr := range out.Attributes {
		if aws.ToString(attr.Key) != awsCrossZoneAttr {
			continue
		}
		v, err := strconv.ParseBool(aws.ToString(attr.Value))
		if err != nil {
			return nil, fmt.Errorf("%s: %s: %w", api, awsCrossZoneAttr, err)
		}
		return &v, nil
	}
	return nil, nil
}

func (a awsCollector) targetGroups(ctx context.Context, lbARN *string, withCrossZone bool) ([]model.TargetGroup, error) {
	var raw []elbv2types.TargetGroup
	err := awsEachPage(ctx, "elasticloadbalancing:DescribeTargetGroups",
		elbv2.NewDescribeTargetGroupsPaginator(a.c.ELBv2, &elbv2.DescribeTargetGroupsInput{LoadBalancerArn: lbARN}),
		func(page *elbv2.DescribeTargetGroupsOutput) { raw = append(raw, page.TargetGroups...) })
	if err != nil {
		return nil, err
	}
	out := make([]model.TargetGroup, 0, len(raw))
	for _, tg := range raw {
		m := model.TargetGroup{Name: aws.ToString(tg.TargetGroupName), TargetType: string(tg.TargetType)}
		if withCrossZone {
			if m.CrossZone, err = a.tgCrossZone(ctx, tg.TargetGroupArn); err != nil {
				return nil, fmt.Errorf("target group %s: %w", m.Name, err)
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func (a awsCollector) tgCrossZone(ctx context.Context, arn *string) (string, error) {
	out, err := a.c.ELBv2.DescribeTargetGroupAttributes(ctx, &elbv2.DescribeTargetGroupAttributesInput{TargetGroupArn: arn})
	if err != nil {
		return "", fmt.Errorf("elasticloadbalancing:DescribeTargetGroupAttributes: %w", err)
	}
	for _, attr := range out.Attributes {
		if aws.ToString(attr.Key) == awsCrossZoneAttr {
			return aws.ToString(attr.Value), nil
		}
	}
	return "", nil
}

func (a awsCollector) classicLoadBalancers(ctx context.Context, inVPC map[string]bool) ([]model.LoadBalancer, error) {
	var out []model.LoadBalancer
	err := awsEachPage(ctx, "elasticloadbalancing:DescribeLoadBalancers (classic)",
		elbclassic.NewDescribeLoadBalancersPaginator(a.c.ELB, &elbclassic.DescribeLoadBalancersInput{}),
		func(page *elbclassic.DescribeLoadBalancersOutput) {
			for _, d := range page.LoadBalancerDescriptions {
				if !inVPC[aws.ToString(d.VPCId)] {
					continue
				}
				out = append(out, model.LoadBalancer{
					Name:    aws.ToString(d.LoadBalancerName),
					DNSName: aws.ToString(d.DNSName),
					Type:    awsClassicType,
					Scheme:  aws.ToString(d.Scheme),
					VPCID:   aws.ToString(d.VPCId),
					Zones:   slices.Sorted(slices.Values(d.AvailabilityZones)),
				})
			}
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a awsCollector) RDSInstances(ctx context.Context) ([]model.RDSInstance, error) {
	out := []model.RDSInstance{}
	err := awsEachPage(ctx, "rds:DescribeDBInstances",
		rds.NewDescribeDBInstancesPaginator(a.c.RDS, &rds.DescribeDBInstancesInput{}),
		func(page *rds.DescribeDBInstancesOutput) {
			for _, db := range page.DBInstances {
				out = append(out, awsRDSInstance(db))
			}
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func awsRDSInstance(db rdstypes.DBInstance) model.RDSInstance {
	vpc := ""
	if db.DBSubnetGroup != nil {
		vpc = aws.ToString(db.DBSubnetGroup.VpcId)
	}
	return model.RDSInstance{
		ID:          aws.ToString(db.DBInstanceIdentifier),
		ClusterID:   aws.ToString(db.DBClusterIdentifier),
		Engine:      aws.ToString(db.Engine),
		AZ:          aws.ToString(db.AvailabilityZone),
		SecondaryAZ: aws.ToString(db.SecondaryAvailabilityZone),
		MultiAZ:     aws.ToBool(db.MultiAZ),
		VPCID:       vpc,
	}
}

func (a awsCollector) ElastiCacheClusters(ctx context.Context) ([]model.CacheCluster, error) {
	groupVPC := map[string]string{}
	err := awsEachPage(ctx, "elasticache:DescribeCacheSubnetGroups",
		elasticache.NewDescribeCacheSubnetGroupsPaginator(a.c.ElastiCache, &elasticache.DescribeCacheSubnetGroupsInput{}),
		func(page *elasticache.DescribeCacheSubnetGroupsOutput) {
			for _, g := range page.CacheSubnetGroups {
				groupVPC[aws.ToString(g.CacheSubnetGroupName)] = aws.ToString(g.VpcId)
			}
		})
	if err != nil {
		return nil, err
	}
	out := []model.CacheCluster{}
	in := &elasticache.DescribeCacheClustersInput{ShowCacheNodeInfo: aws.Bool(true)}
	err = awsEachPage(ctx, "elasticache:DescribeCacheClusters",
		elasticache.NewDescribeCacheClustersPaginator(a.c.ElastiCache, in),
		func(page *elasticache.DescribeCacheClustersOutput) {
			for _, cc := range page.CacheClusters {
				out = append(out, model.CacheCluster{
					ID:        aws.ToString(cc.CacheClusterId),
					Engine:    aws.ToString(cc.Engine),
					GroupID:   aws.ToString(cc.ReplicationGroupId),
					NodeZones: awsCacheNodeZones(cc.CacheNodes, aws.ToString(cc.PreferredAvailabilityZone)),
					VPCID:     groupVPC[aws.ToString(cc.CacheSubnetGroupName)],
				})
			}
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a awsCollector) MSKClusters(ctx context.Context, subnets map[string]model.Subnet) ([]model.MSKCluster, error) {
	out := []model.MSKCluster{}
	in := &kafka.ListClustersV2Input{ClusterTypeFilter: aws.String(awsMSKProvisioned)}
	err := awsEachPage(ctx, "kafka:ListClustersV2", kafka.NewListClustersV2Paginator(a.c.Kafka, in),
		func(page *kafka.ListClustersV2Output) {
			for _, c := range page.ClusterInfoList {
				if m, ok := awsMSKCluster(c, subnets); ok {
					out = append(out, m)
				}
			}
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// awsMSKCluster spreads brokers round-robin over the client subnets (MSK places them evenly per AZ).
// ok is false for non-provisioned clusters and clusters with a subnet outside the known VPCs.
func awsMSKCluster(c kafkatypes.Cluster, subnets map[string]model.Subnet) (model.MSKCluster, bool) {
	if c.Provisioned == nil || c.Provisioned.BrokerNodeGroupInfo == nil {
		return model.MSKCluster{}, false
	}
	clientSubnets := c.Provisioned.BrokerNodeGroupInfo.ClientSubnets
	if len(clientSubnets) == 0 {
		return model.MSKCluster{}, false
	}
	subnetZones := make([]string, len(clientSubnets))
	for i, id := range clientSubnets {
		s, ok := subnets[id]
		if !ok {
			return model.MSKCluster{}, false
		}
		subnetZones[i] = s.AZ
	}
	brokers := make([]string, max(int(aws.ToInt32(c.Provisioned.NumberOfBrokerNodes)), 0))
	for i := range brokers {
		brokers[i] = subnetZones[i%len(subnetZones)]
	}
	return model.MSKCluster{
		Name:        aws.ToString(c.ClusterName),
		BrokerZones: brokers,
		VPCID:       subnets[clientSubnets[0]].VPCID,
	}, true
}

func awsEachPage[Out, Opt any](ctx context.Context, api string, p awsPager[Out, Opt], visit func(*Out)) error {
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", api, err)
		}
		visit(page)
	}
	return nil
}

func awsFilter(name string, values []string) []ec2types.Filter {
	return []ec2types.Filter{{Name: aws.String(name), Values: slices.Clone(values)}}
}

func awsSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[v] = true
	}
	return set
}

func awsENIAddresses(ni ec2types.NetworkInterface) []string {
	ips := []string{aws.ToString(ni.PrivateIpAddress)}
	for _, addr := range ni.PrivateIpAddresses {
		ips = append(ips, aws.ToString(addr.PrivateIpAddress))
	}
	return ips
}

// awsCacheNodeZones falls back to the preferred AZ when node info is missing; "Multiple" says nothing.
func awsCacheNodeZones(nodes []elasticachetypes.CacheNode, preferred string) []string {
	zones := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if z := aws.ToString(n.CustomerAvailabilityZone); z != "" {
			zones = append(zones, z)
		}
	}
	if len(zones) == 0 && preferred != "" && preferred != awsCacheMultipleAZs {
		return []string{preferred}
	}
	return zones
}
