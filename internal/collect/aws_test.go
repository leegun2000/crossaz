package collect

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticache"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/leegun2000/crossaz/internal/model"
)

var awsTestErr = errors.New("access denied")

// awsTestPage serves the page registered under the request's pagination token ("" = first page).
func awsTestPage[Out any](pages map[string]*Out, token *string) (*Out, error) {
	page, ok := pages[aws.ToString(token)]
	if !ok {
		return nil, fmt.Errorf("unexpected page token %q", aws.ToString(token))
	}
	return page, nil
}

// awsTestAssertErr checks err wraps awsTestErr and its message carries the API context in want.
func awsTestAssertErr(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, awsTestErr) || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to wrap %q and contain %q", err, awsTestErr, want)
	}
}

type stubEC2 struct {
	instances func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
	subnets   func(*ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error)
	enis      func(*ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error)
	filters   [][]ec2types.Filter
}

func (s *stubEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	s.filters = append(s.filters, in.Filters)
	return s.instances(in)
}

func (s *stubEC2) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	s.filters = append(s.filters, in.Filters)
	return s.subnets(in)
}

func (s *stubEC2) DescribeNetworkInterfaces(_ context.Context, in *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	s.filters = append(s.filters, in.Filters)
	return s.enis(in)
}

// awsTestFilterSizes checks every recorded call used exactly one filter named name and returns its value counts.
func awsTestFilterSizes(t *testing.T, calls [][]ec2types.Filter, name string) []int {
	t.Helper()
	sizes := []int{}
	for _, filters := range calls {
		if len(filters) != 1 || aws.ToString(filters[0].Name) != name {
			t.Fatalf("filters = %+v, want one %q filter", filters, name)
		}
		sizes = append(sizes, len(filters[0].Values))
	}
	return sizes
}

func awsTestIDs(prefix string, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return ids
}

func TestInstanceVPCs(t *testing.T) {
	chunkVPC := map[string]string{"i-0": "vpc-b", "i-200": "vpc-a", "i-400": "vpc-b"}
	tests := []struct {
		name      string
		ids       []string
		serve     func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
		want      []string
		wantOwner []string
		wantSizes []int
		wantErr   string
	}{
		{
			name: "chunks 450 IDs into filter calls of 200 and returns distinct sorted VPCs and owners",
			ids:  awsTestIDs("i-", 450),
			serve: func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				vpc := chunkVPC[in.Filters[0].Values[0]]
				return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{
					{OwnerId: aws.String("owner-" + vpc), Instances: []ec2types.Instance{{VpcId: aws.String(vpc)}, {VpcId: nil}}},
					{Instances: []ec2types.Instance{{VpcId: aws.String(vpc)}}},
				}}, nil
			},
			want:      []string{"vpc-a", "vpc-b"},
			wantOwner: []string{"owner-vpc-a", "owner-vpc-b"},
			wantSizes: []int{200, 200, 50},
		},
		{
			name: "follows NextToken pages",
			ids:  []string{"i-1", "i-2"},
			serve: func(in *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
				return awsTestPage(map[string]*ec2.DescribeInstancesOutput{
					"":   {NextToken: aws.String("p2"), Reservations: []ec2types.Reservation{{OwnerId: aws.String("222"), Instances: []ec2types.Instance{{VpcId: aws.String("vpc-2")}}}}},
					"p2": {Reservations: []ec2types.Reservation{{OwnerId: aws.String("111"), Instances: []ec2types.Instance{{VpcId: aws.String("vpc-1")}}}}},
				}, in.NextToken)
			},
			want:      []string{"vpc-1", "vpc-2"},
			wantOwner: []string{"111", "222"},
			wantSizes: []int{2, 2},
		},
		{name: "no instance IDs makes no call", wantSizes: []int{}},
		{
			name:      "wraps API errors with the API name",
			ids:       []string{"i-1"},
			serve:     func(*ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) { return nil, awsTestErr },
			wantErr:   "ec2:DescribeInstances: access denied",
			wantSizes: []int{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			stub := &stubEC2{instances: tt.serve}

			// Act
			got, owners, err := NewAWS(AWSClients{EC2: stub}).InstanceVPCs(t.Context(), tt.ids)

			// Assert
			if tt.wantErr != "" {
				awsTestAssertErr(t, err, tt.wantErr)
			} else if err != nil || !slices.Equal(got, tt.want) || !slices.Equal(owners, tt.wantOwner) {
				t.Fatalf("InstanceVPCs = %v, %v, %v; want %v, %v", got, owners, err, tt.want, tt.wantOwner)
			}
			if sizes := awsTestFilterSizes(t, stub.filters, "instance-id"); !reflect.DeepEqual(sizes, tt.wantSizes) {
				t.Errorf("filter value counts = %v, want %v", sizes, tt.wantSizes)
			}
		})
	}
}

func TestSubnets(t *testing.T) {
	pages := map[string]*ec2.DescribeSubnetsOutput{
		"": {NextToken: aws.String("s2"), Subnets: []ec2types.Subnet{
			{SubnetId: aws.String("subnet-a"), AvailabilityZone: aws.String("us-east-1a"), VpcId: aws.String("vpc-1")},
		}},
		"s2": {Subnets: []ec2types.Subnet{
			{SubnetId: aws.String("subnet-b"), AvailabilityZone: aws.String("us-east-1b"), VpcId: aws.String("vpc-1")},
		}},
	}
	tests := []struct {
		name      string
		vpcs      []string
		err       error
		want      map[string]model.Subnet
		wantSizes []int
		wantErr   string
	}{
		{
			name: "maps every subnet across pages",
			vpcs: []string{"vpc-1"},
			want: map[string]model.Subnet{
				"subnet-a": {AZ: "us-east-1a", VPCID: "vpc-1"},
				"subnet-b": {AZ: "us-east-1b", VPCID: "vpc-1"},
			},
			wantSizes: []int{1, 1},
		},
		{name: "no VPCs returns an empty map without calling", want: map[string]model.Subnet{}, wantSizes: []int{}},
		{name: "wraps API errors", vpcs: []string{"vpc-1"}, err: awsTestErr, wantErr: "ec2:DescribeSubnets: access denied", wantSizes: []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			stub := &stubEC2{subnets: func(in *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
				if tt.err != nil {
					return nil, tt.err
				}
				return awsTestPage(pages, in.NextToken)
			}}

			// Act
			got, err := NewAWS(AWSClients{EC2: stub}).Subnets(t.Context(), tt.vpcs)

			// Assert
			if tt.wantErr != "" {
				awsTestAssertErr(t, err, tt.wantErr)
			} else if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Subnets = %v, %v; want %v", got, err, tt.want)
			}
			if sizes := awsTestFilterSizes(t, stub.filters, "vpc-id"); !reflect.DeepEqual(sizes, tt.wantSizes) {
				t.Errorf("filter value counts = %v, want %v", sizes, tt.wantSizes)
			}
		})
	}
}

// awsTestIPZoneFilterSizes checks every call filtered by exactly the cluster VPCs and returns the IP filter value counts.
func awsTestIPZoneFilterSizes(t *testing.T, calls [][]ec2types.Filter, vpcs []string) []int {
	t.Helper()
	sizes := []int{}
	for _, filters := range calls {
		if len(filters) != 2 || aws.ToString(filters[0].Name) != "vpc-id" || !reflect.DeepEqual(filters[0].Values, vpcs) ||
			aws.ToString(filters[1].Name) != "addresses.private-ip-address" {
			t.Fatalf("filters = %+v, want vpc-id %v and addresses.private-ip-address", filters, vpcs)
		}
		sizes = append(sizes, len(filters[1].Values))
	}
	return sizes
}

func TestIPZones(t *testing.T) {
	vpcs := []string{"vpc-1", "vpc-2"}
	eni := func(zone string, ips ...string) ec2types.NetworkInterface {
		ni := ec2types.NetworkInterface{AvailabilityZone: aws.String(zone), PrivateIpAddress: aws.String(ips[0])}
		for _, ip := range ips {
			ni.PrivateIpAddresses = append(ni.PrivateIpAddresses, ec2types.NetworkInterfacePrivateIpAddress{PrivateIpAddress: aws.String(ip)})
		}
		return ni
	}
	tests := []struct {
		name      string
		vpcs      []string
		ips       []string
		serve     func(*ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error)
		want      map[string]string
		wantSizes []int
		wantErr   string
	}{
		{
			name: "chunks 450 IPs into 3 calls and maps only requested primary and secondary IPs",
			vpcs: vpcs,
			ips:  awsTestIDs("10.0.0.", 450),
			serve: func(in *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
				values := in.Filters[1].Values
				return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: []ec2types.NetworkInterface{{
					AvailabilityZone: aws.String("zone-of-" + values[0]),
					PrivateIpAddress: aws.String(values[0]),
					PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
						{PrivateIpAddress: aws.String(values[1])},
						{PrivateIpAddress: aws.String("192.168.9.9")},
					},
				}}}, nil
			},
			want: map[string]string{
				"10.0.0.0": "zone-of-10.0.0.0", "10.0.0.1": "zone-of-10.0.0.0",
				"10.0.0.200": "zone-of-10.0.0.200", "10.0.0.201": "zone-of-10.0.0.200",
				"10.0.0.400": "zone-of-10.0.0.400", "10.0.0.401": "zone-of-10.0.0.400",
			},
			wantSizes: []int{200, 200, 50},
		},
		{
			name: "follows NextToken pages",
			vpcs: vpcs,
			ips:  []string{"10.0.0.1", "10.0.0.2"},
			serve: func(in *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
				return awsTestPage(map[string]*ec2.DescribeNetworkInterfacesOutput{
					"":   {NextToken: aws.String("n2"), NetworkInterfaces: []ec2types.NetworkInterface{{AvailabilityZone: aws.String("us-east-1a"), PrivateIpAddress: aws.String("10.0.0.1")}}},
					"n2": {NetworkInterfaces: []ec2types.NetworkInterface{{AvailabilityZone: aws.String("us-east-1b"), PrivateIpAddress: aws.String("10.0.0.2")}}},
				}, in.NextToken)
			},
			want:      map[string]string{"10.0.0.1": "us-east-1a", "10.0.0.2": "us-east-1b"},
			wantSizes: []int{2, 2},
		},
		{
			name: "leaves an IP unresolved when its ENIs disagree on AZ, regardless of order",
			vpcs: vpcs,
			ips:  []string{"10.0.5.10", "10.0.5.11"},
			serve: func(*ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
				return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: []ec2types.NetworkInterface{
					eni("us-west-2a", "10.0.5.10", "10.0.5.11"),
					eni("us-west-2c", "10.0.5.10"),
					eni("us-west-2a", "10.0.5.10"),
				}}, nil
			},
			want:      map[string]string{"10.0.5.11": "us-west-2a"},
			wantSizes: []int{2},
		},
		{name: "no IPs makes no call", vpcs: vpcs, want: map[string]string{}, wantSizes: []int{}},
		{name: "no VPCs makes no call", ips: []string{"10.0.0.1"}, want: map[string]string{}, wantSizes: []int{}},
		{
			name: "wraps API errors",
			vpcs: vpcs,
			ips:  []string{"10.0.0.1"},
			serve: func(*ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
				return nil, awsTestErr
			},
			wantErr:   "ec2:DescribeNetworkInterfaces: access denied",
			wantSizes: []int{1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			stub := &stubEC2{enis: tt.serve}

			// Act
			got, err := NewAWS(AWSClients{EC2: stub}).IPZones(t.Context(), tt.vpcs, tt.ips)

			// Assert
			if tt.wantErr != "" {
				awsTestAssertErr(t, err, tt.wantErr)
			} else if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("IPZones = %v, %v; want %v", got, err, tt.want)
			}
			if sizes := awsTestIPZoneFilterSizes(t, stub.filters, tt.vpcs); !reflect.DeepEqual(sizes, tt.wantSizes) {
				t.Errorf("filter value counts = %v, want %v", sizes, tt.wantSizes)
			}
		})
	}
}

type stubRDS struct {
	pages map[string]*rds.DescribeDBInstancesOutput
	err   error
}

func (s *stubRDS) DescribeDBInstances(_ context.Context, in *rds.DescribeDBInstancesInput, _ ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return awsTestPage(s.pages, in.Marker)
}

func TestRDSInstances(t *testing.T) {
	// Arrange
	stub := &stubRDS{pages: map[string]*rds.DescribeDBInstancesOutput{
		"": {Marker: aws.String("r2"), DBInstances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("aurora-1"), DBClusterIdentifier: aws.String("aurora"),
			Engine: aws.String("aurora-postgresql"), AvailabilityZone: aws.String("us-east-1a"),
			DBSubnetGroup: &rdstypes.DBSubnetGroup{VpcId: aws.String("vpc-1")},
		}}},
		"r2": {DBInstances: []rdstypes.DBInstance{{
			DBInstanceIdentifier: aws.String("pg"), Engine: aws.String("postgres"),
			AvailabilityZone: aws.String("us-east-1b"), SecondaryAvailabilityZone: aws.String("us-east-1c"),
			MultiAZ: aws.Bool(true),
		}}},
	}}
	want := []model.RDSInstance{
		{ID: "aurora-1", ClusterID: "aurora", Engine: "aurora-postgresql", AZ: "us-east-1a", VPCID: "vpc-1"},
		{ID: "pg", Engine: "postgres", AZ: "us-east-1b", SecondaryAZ: "us-east-1c", MultiAZ: true},
	}

	// Act
	got, err := NewAWS(AWSClients{RDS: stub}).RDSInstances(t.Context())

	// Assert
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("RDSInstances = %+v, %v; want %+v", got, err, want)
	}
}

func TestRDSInstancesWrapsAPIError(t *testing.T) {
	_, err := NewAWS(AWSClients{RDS: &stubRDS{err: awsTestErr}}).RDSInstances(t.Context())
	awsTestAssertErr(t, err, "rds:DescribeDBInstances: access denied")
}

type stubElastiCache struct {
	groupPages   map[string]*elasticache.DescribeCacheSubnetGroupsOutput
	clusterPages map[string]*elasticache.DescribeCacheClustersOutput
	groupErr     error
	clusterErr   error
	clusterCalls []*elasticache.DescribeCacheClustersInput
}

func (s *stubElastiCache) DescribeCacheClusters(_ context.Context, in *elasticache.DescribeCacheClustersInput, _ ...func(*elasticache.Options)) (*elasticache.DescribeCacheClustersOutput, error) {
	s.clusterCalls = append(s.clusterCalls, in)
	if s.clusterErr != nil {
		return nil, s.clusterErr
	}
	return awsTestPage(s.clusterPages, in.Marker)
}

func (s *stubElastiCache) DescribeCacheSubnetGroups(_ context.Context, in *elasticache.DescribeCacheSubnetGroupsInput, _ ...func(*elasticache.Options)) (*elasticache.DescribeCacheSubnetGroupsOutput, error) {
	if s.groupErr != nil {
		return nil, s.groupErr
	}
	return awsTestPage(s.groupPages, in.Marker)
}

func awsTestCacheStub() *stubElastiCache {
	node := func(zone string) elasticachetypes.CacheNode {
		return elasticachetypes.CacheNode{CustomerAvailabilityZone: aws.String(zone)}
	}
	return &stubElastiCache{
		groupPages: map[string]*elasticache.DescribeCacheSubnetGroupsOutput{
			"":   {Marker: aws.String("g2"), CacheSubnetGroups: []elasticachetypes.CacheSubnetGroup{{CacheSubnetGroupName: aws.String("sg-1"), VpcId: aws.String("vpc-1")}}},
			"g2": {CacheSubnetGroups: []elasticachetypes.CacheSubnetGroup{{CacheSubnetGroupName: aws.String("sg-2"), VpcId: aws.String("vpc-2")}}},
		},
		clusterPages: map[string]*elasticache.DescribeCacheClustersOutput{
			"": {Marker: aws.String("c2"), CacheClusters: []elasticachetypes.CacheCluster{{
				CacheClusterId: aws.String("redis-001"), Engine: aws.String("redis"), ReplicationGroupId: aws.String("redis"),
				CacheSubnetGroupName: aws.String("sg-1"), CacheNodes: []elasticachetypes.CacheNode{node("us-east-1a")},
			}}},
			"c2": {CacheClusters: []elasticachetypes.CacheCluster{
				{CacheClusterId: aws.String("memcached"), Engine: aws.String("memcached"), CacheSubnetGroupName: aws.String("sg-2"),
					PreferredAvailabilityZone: aws.String("Multiple"), CacheNodes: []elasticachetypes.CacheNode{node("us-east-1a"), node("us-east-1b")}},
				{CacheClusterId: aws.String("no-nodes"), Engine: aws.String("redis"), CacheSubnetGroupName: aws.String("sg-1"),
					PreferredAvailabilityZone: aws.String("us-east-1c")},
				{CacheClusterId: aws.String("multiple"), Engine: aws.String("memcached"), CacheSubnetGroupName: aws.String("sg-unknown"),
					PreferredAvailabilityZone: aws.String("Multiple")},
			}},
		},
	}
}

func TestElastiCacheClusters(t *testing.T) {
	// Arrange
	stub := awsTestCacheStub()
	want := []model.CacheCluster{
		{ID: "redis-001", Engine: "redis", GroupID: "redis", NodeZones: []string{"us-east-1a"}, VPCID: "vpc-1"},
		{ID: "memcached", Engine: "memcached", NodeZones: []string{"us-east-1a", "us-east-1b"}, VPCID: "vpc-2"},
		{ID: "no-nodes", Engine: "redis", NodeZones: []string{"us-east-1c"}, VPCID: "vpc-1"},
		{ID: "multiple", Engine: "memcached", NodeZones: []string{}},
	}

	// Act
	got, err := NewAWS(AWSClients{ElastiCache: stub}).ElastiCacheClusters(t.Context())

	// Assert
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ElastiCacheClusters = %+v, %v; want %+v", got, err, want)
	}
	for _, in := range stub.clusterCalls {
		if !aws.ToBool(in.ShowCacheNodeInfo) {
			t.Errorf("DescribeCacheClusters called without ShowCacheNodeInfo=true")
		}
	}
}

func TestElastiCacheClustersWrapsAPIErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*stubElastiCache)
		wantErr string
	}{
		{name: "subnet groups", mutate: func(s *stubElastiCache) { s.groupErr = awsTestErr }, wantErr: "elasticache:DescribeCacheSubnetGroups: access denied"},
		{name: "cache clusters", mutate: func(s *stubElastiCache) { s.clusterErr = awsTestErr }, wantErr: "elasticache:DescribeCacheClusters: access denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := awsTestCacheStub()
			tt.mutate(stub)
			_, err := NewAWS(AWSClients{ElastiCache: stub}).ElastiCacheClusters(t.Context())
			awsTestAssertErr(t, err, tt.wantErr)
		})
	}
}

type stubKafka struct {
	pages map[string]*kafka.ListClustersV2Output
	err   error
	calls []*kafka.ListClustersV2Input
}

func (s *stubKafka) ListClustersV2(_ context.Context, in *kafka.ListClustersV2Input, _ ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	return awsTestPage(s.pages, in.NextToken)
}

func TestMSKClusters(t *testing.T) {
	// Arrange
	provisioned := func(name string, brokers int32, subnets ...string) kafkatypes.Cluster {
		return kafkatypes.Cluster{ClusterName: aws.String(name), Provisioned: &kafkatypes.Provisioned{
			NumberOfBrokerNodes: aws.Int32(brokers),
			BrokerNodeGroupInfo: &kafkatypes.BrokerNodeGroupInfo{ClientSubnets: subnets},
		}}
	}
	stub := &stubKafka{pages: map[string]*kafka.ListClustersV2Output{
		"": {NextToken: aws.String("k2"), ClusterInfoList: []kafkatypes.Cluster{provisioned("main", 5, "s-a", "s-b", "s-c")}},
		"k2": {ClusterInfoList: []kafkatypes.Cluster{
			provisioned("foreign", 2, "s-a", "s-other-vpc"),
			provisioned("no-subnets", 3),
			{ClusterName: aws.String("serverless")},
		}},
	}}
	subnets := map[string]model.Subnet{
		"s-a": {AZ: "us-east-1a", VPCID: "vpc-1"},
		"s-b": {AZ: "us-east-1b", VPCID: "vpc-1"},
		"s-c": {AZ: "us-east-1c", VPCID: "vpc-1"},
	}
	want := []model.MSKCluster{
		{Name: "main", BrokerZones: []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-east-1a", "us-east-1b"}, VPCID: "vpc-1"},
	}

	// Act
	got, err := NewAWS(AWSClients{Kafka: stub}).MSKClusters(t.Context(), subnets)

	// Assert
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MSKClusters = %+v, %v; want %+v", got, err, want)
	}
	if len(stub.calls) != 2 {
		t.Fatalf("ListClustersV2 calls = %d, want 2", len(stub.calls))
	}
	for _, in := range stub.calls {
		if aws.ToString(in.ClusterTypeFilter) != "PROVISIONED" {
			t.Errorf("ClusterTypeFilter = %q, want PROVISIONED", aws.ToString(in.ClusterTypeFilter))
		}
	}
}

func TestMSKClustersWrapsAPIError(t *testing.T) {
	_, err := NewAWS(AWSClients{Kafka: &stubKafka{err: awsTestErr}}).MSKClusters(t.Context(), nil)
	awsTestAssertErr(t, err, "kafka:ListClustersV2: access denied")
}

func TestNewAWSFromConfigBuildsSDKClients(t *testing.T) {
	// Act
	got, ok := NewAWSFromConfig(aws.Config{Region: "us-west-2"}).(awsCollector)

	// Assert
	if !ok {
		t.Fatal("NewAWSFromConfig did not return an awsCollector")
	}
	if _, ok := got.c.EC2.(*ec2.Client); !ok {
		t.Errorf("EC2 client = %T", got.c.EC2)
	}
	if _, ok := got.c.ELB.(*elasticloadbalancing.Client); !ok {
		t.Errorf("classic ELB client = %T", got.c.ELB)
	}
	if got.c.ELBv2 == nil || got.c.RDS == nil || got.c.ElastiCache == nil || got.c.Kafka == nil {
		t.Errorf("missing clients: %+v", got.c)
	}
}

type stubELBv2 struct {
	lbPages   map[string]*elbv2.DescribeLoadBalancersOutput
	lbAttrs   map[string][]elbv2types.LoadBalancerAttribute           // by LB ARN
	tgPages   map[string]map[string]*elbv2.DescribeTargetGroupsOutput // by LB ARN, then marker
	tgAttrs   map[string][]elbv2types.TargetGroupAttribute            // by TG ARN
	errs      map[string]error                                        // by method name
	attrCalls []string                                                // ARNs, both attribute APIs
}

func (s *stubELBv2) DescribeLoadBalancers(_ context.Context, in *elbv2.DescribeLoadBalancersInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancersOutput, error) {
	if err := s.errs["DescribeLoadBalancers"]; err != nil {
		return nil, err
	}
	return awsTestPage(s.lbPages, in.Marker)
}

func (s *stubELBv2) DescribeLoadBalancerAttributes(_ context.Context, in *elbv2.DescribeLoadBalancerAttributesInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeLoadBalancerAttributesOutput, error) {
	s.attrCalls = append(s.attrCalls, aws.ToString(in.LoadBalancerArn))
	if err := s.errs["DescribeLoadBalancerAttributes"]; err != nil {
		return nil, err
	}
	return &elbv2.DescribeLoadBalancerAttributesOutput{Attributes: s.lbAttrs[aws.ToString(in.LoadBalancerArn)]}, nil
}

func (s *stubELBv2) DescribeTargetGroups(_ context.Context, in *elbv2.DescribeTargetGroupsInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeTargetGroupsOutput, error) {
	if err := s.errs["DescribeTargetGroups"]; err != nil {
		return nil, err
	}
	return awsTestPage(s.tgPages[aws.ToString(in.LoadBalancerArn)], in.Marker)
}

func (s *stubELBv2) DescribeTargetGroupAttributes(_ context.Context, in *elbv2.DescribeTargetGroupAttributesInput, _ ...func(*elbv2.Options)) (*elbv2.DescribeTargetGroupAttributesOutput, error) {
	s.attrCalls = append(s.attrCalls, aws.ToString(in.TargetGroupArn))
	if err := s.errs["DescribeTargetGroupAttributes"]; err != nil {
		return nil, err
	}
	return &elbv2.DescribeTargetGroupAttributesOutput{Attributes: s.tgAttrs[aws.ToString(in.TargetGroupArn)]}, nil
}

type stubELB struct {
	pages map[string]*elasticloadbalancing.DescribeLoadBalancersOutput
	err   error
}

func (s *stubELB) DescribeLoadBalancers(_ context.Context, in *elasticloadbalancing.DescribeLoadBalancersInput, _ ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancersOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return awsTestPage(s.pages, in.Marker)
}

func awsTestV2LB(name string, typ elbv2types.LoadBalancerTypeEnum, vpc string, zones ...string) elbv2types.LoadBalancer {
	lb := elbv2types.LoadBalancer{
		LoadBalancerName: aws.String(name), LoadBalancerArn: aws.String("arn:" + name), DNSName: aws.String(name + ".elb"),
		Type: typ, Scheme: elbv2types.LoadBalancerSchemeEnumInternal, VpcId: aws.String(vpc),
	}
	for _, z := range zones {
		lb.AvailabilityZones = append(lb.AvailabilityZones, elbv2types.AvailabilityZone{ZoneName: aws.String(z)})
	}
	return lb
}

func awsTestTG(name string, typ elbv2types.TargetTypeEnum) elbv2types.TargetGroup {
	return elbv2types.TargetGroup{TargetGroupName: aws.String(name), TargetGroupArn: aws.String("arn:" + name), TargetType: typ}
}

func awsTestLBStubs() (*stubELBv2, *stubELB) {
	xz := func(v string) *string { return aws.String(v) }
	v2 := &stubELBv2{
		lbPages: map[string]*elbv2.DescribeLoadBalancersOutput{
			"": {NextMarker: aws.String("m2"), LoadBalancers: []elbv2types.LoadBalancer{
				awsTestV2LB("nlb-a", elbv2types.LoadBalancerTypeEnumNetwork, "vpc-1", "us-east-1b", "us-east-1a"),
				awsTestV2LB("alb-other-vpc", elbv2types.LoadBalancerTypeEnumApplication, "vpc-9", "us-east-1a"),
			}},
			"m2": {LoadBalancers: []elbv2types.LoadBalancer{
				awsTestV2LB("alb-b", elbv2types.LoadBalancerTypeEnumApplication, "vpc-1", "us-east-1a"),
				awsTestV2LB("gwlb-c", elbv2types.LoadBalancerTypeEnumGateway, "vpc-1", "us-east-1c"),
			}},
		},
		lbAttrs: map[string][]elbv2types.LoadBalancerAttribute{
			"arn:nlb-a":  {{Key: aws.String("deletion_protection.enabled"), Value: xz("true")}, {Key: aws.String(awsCrossZoneAttr), Value: xz("false")}},
			"arn:gwlb-c": {{Key: aws.String(awsCrossZoneAttr), Value: xz("true")}},
		},
		tgPages: map[string]map[string]*elbv2.DescribeTargetGroupsOutput{
			"arn:nlb-a": {
				"":   {NextMarker: aws.String("t2"), TargetGroups: []elbv2types.TargetGroup{awsTestTG("tg-1", elbv2types.TargetTypeEnumIp)}},
				"t2": {TargetGroups: []elbv2types.TargetGroup{awsTestTG("tg-2", elbv2types.TargetTypeEnumInstance)}},
			},
			"arn:alb-b":  {"": {TargetGroups: []elbv2types.TargetGroup{awsTestTG("tg-3", elbv2types.TargetTypeEnumInstance)}}},
			"arn:gwlb-c": {"": {TargetGroups: []elbv2types.TargetGroup{awsTestTG("tg-4", elbv2types.TargetTypeEnumIp)}}},
		},
		tgAttrs: map[string][]elbv2types.TargetGroupAttribute{
			"arn:tg-1": {{Key: aws.String(awsCrossZoneAttr), Value: xz("true")}},
			"arn:tg-2": {{Key: aws.String(awsCrossZoneAttr), Value: xz("use_load_balancer_configuration")}},
		},
	}
	classic := &stubELB{pages: map[string]*elasticloadbalancing.DescribeLoadBalancersOutput{
		"": {NextMarker: aws.String("c2"), LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{
			LoadBalancerName: aws.String("clb-z"), DNSName: aws.String("clb-z.elb"), Scheme: aws.String("internet-facing"),
			VPCId: aws.String("vpc-1"), AvailabilityZones: []string{"us-east-1b", "us-east-1a"},
		}}},
		"c2": {LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{LoadBalancerName: aws.String("clb-other"), VPCId: aws.String("vpc-2")}}},
	}}
	return v2, classic
}

func TestLoadBalancersMapsELBv2AndClassicInVPC(t *testing.T) {
	// Arrange
	v2, classic := awsTestLBStubs()
	isTrue, isFalse := true, false
	want := []model.LoadBalancer{
		{Name: "alb-b", DNSName: "alb-b.elb", Type: "application", Scheme: "internal", VPCID: "vpc-1", Zones: []string{"us-east-1a"},
			TargetGroups: []model.TargetGroup{{Name: "tg-3", TargetType: "instance"}}},
		{Name: "clb-z", DNSName: "clb-z.elb", Type: "classic", Scheme: "internet-facing", VPCID: "vpc-1", Zones: []string{"us-east-1a", "us-east-1b"}},
		{Name: "gwlb-c", DNSName: "gwlb-c.elb", Type: "gateway", Scheme: "internal", VPCID: "vpc-1", Zones: []string{"us-east-1c"}, CrossZone: &isTrue,
			TargetGroups: []model.TargetGroup{{Name: "tg-4", TargetType: "ip"}}},
		{Name: "nlb-a", DNSName: "nlb-a.elb", Type: "network", Scheme: "internal", VPCID: "vpc-1", Zones: []string{"us-east-1a", "us-east-1b"}, CrossZone: &isFalse,
			TargetGroups: []model.TargetGroup{{Name: "tg-1", TargetType: "ip", CrossZone: "true"}, {Name: "tg-2", TargetType: "instance", CrossZone: "use_load_balancer_configuration"}}},
	}

	// Act
	got, err := NewAWS(AWSClients{ELBv2: v2, ELB: classic}).LoadBalancers(t.Context(), []string{"vpc-1"})

	// Assert
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadBalancers =\n%+v, %v\nwant\n%+v", got, err, want)
	}
	wantAttrCalls := []string{"arn:nlb-a", "arn:tg-1", "arn:tg-2", "arn:gwlb-c"}
	if !reflect.DeepEqual(v2.attrCalls, wantAttrCalls) {
		t.Errorf("attribute calls = %v, want %v (network/gateway LBs and network TGs only)", v2.attrCalls, wantAttrCalls)
	}
}

func TestLoadBalancersWithoutVPCsMakesNoCall(t *testing.T) {
	got, err := NewAWS(AWSClients{}).LoadBalancers(t.Context(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("LoadBalancers = %v, %v; want empty", got, err)
	}
}

func awsTestLBNames(lbs []model.LoadBalancer) []string {
	names := []string{}
	for _, lb := range lbs {
		names = append(names, lb.Name)
	}
	return names
}

func TestLoadBalancersReturnsPartialResultsWithErrors(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*stubELBv2, *stubELB)
		wantErrs  []string
		wantNames []string
	}{
		{name: "describe load balancers keeps classic", mutate: func(s *stubELBv2, _ *stubELB) { s.errs = map[string]error{"DescribeLoadBalancers": awsTestErr} },
			wantErrs: []string{"elasticloadbalancing:DescribeLoadBalancers: access denied"}, wantNames: []string{"clb-z"}},
		{name: "load balancer attributes drop each failing LB only", mutate: func(s *stubELBv2, _ *stubELB) {
			s.errs = map[string]error{"DescribeLoadBalancerAttributes": awsTestErr}
		},
			wantErrs: []string{
				"load balancer nlb-a: elasticloadbalancing:DescribeLoadBalancerAttributes: access denied",
				"load balancer gwlb-c: elasticloadbalancing:DescribeLoadBalancerAttributes: access denied",
			},
			wantNames: []string{"alb-b", "clb-z"}},
		{name: "target groups", mutate: func(s *stubELBv2, _ *stubELB) { s.errs = map[string]error{"DescribeTargetGroups": awsTestErr} },
			wantErrs: []string{"load balancer alb-b: elasticloadbalancing:DescribeTargetGroups: access denied"}, wantNames: []string{"clb-z"}},
		{name: "target group attributes", mutate: func(s *stubELBv2, _ *stubELB) { s.errs = map[string]error{"DescribeTargetGroupAttributes": awsTestErr} },
			wantErrs:  []string{"load balancer nlb-a: target group tg-1: elasticloadbalancing:DescribeTargetGroupAttributes: access denied"},
			wantNames: []string{"alb-b", "clb-z", "gwlb-c"}},
		{name: "classic load balancers keeps ELBv2", mutate: func(_ *stubELBv2, c *stubELB) { c.err = awsTestErr },
			wantErrs: []string{"DescribeLoadBalancers (classic): access denied"}, wantNames: []string{"alb-b", "gwlb-c", "nlb-a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			v2, classic := awsTestLBStubs()
			tt.mutate(v2, classic)

			// Act
			got, err := NewAWS(AWSClients{ELBv2: v2, ELB: classic}).LoadBalancers(t.Context(), []string{"vpc-1"})

			// Assert
			for _, want := range tt.wantErrs {
				awsTestAssertErr(t, err, want)
			}
			if names := awsTestLBNames(got); !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("load balancers = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestLoadBalancersRejectsNonBooleanCrossZone(t *testing.T) {
	// Arrange
	v2, classic := awsTestLBStubs()
	v2.lbAttrs["arn:nlb-a"] = []elbv2types.LoadBalancerAttribute{{Key: aws.String(awsCrossZoneAttr), Value: aws.String("maybe")}}

	// Act
	got, err := NewAWS(AWSClients{ELBv2: v2, ELB: classic}).LoadBalancers(t.Context(), []string{"vpc-1"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "load balancer nlb-a") || !strings.Contains(err.Error(), awsCrossZoneAttr) {
		t.Fatalf("error = %v, want cross-zone parse error for nlb-a", err)
	}
	if names := awsTestLBNames(got); !reflect.DeepEqual(names, []string{"alb-b", "clb-z", "gwlb-c"}) {
		t.Errorf("load balancers = %v, want nlb-a dropped and the rest kept", names)
	}
}

func TestLoadBalancersLeavesMissingCrossZoneAttributesUnknown(t *testing.T) {
	// Arrange
	v2, classic := awsTestLBStubs()
	v2.lbAttrs = nil
	v2.tgAttrs = nil

	// Act
	got, err := NewAWS(AWSClients{ELBv2: v2, ELB: classic}).LoadBalancers(t.Context(), []string{"vpc-1"})

	// Assert
	if err != nil {
		t.Fatalf("LoadBalancers: %v", err)
	}
	nlb := got[len(got)-1]
	if nlb.Name != "nlb-a" || nlb.CrossZone != nil || nlb.TargetGroups[0].CrossZone != "" || nlb.TargetGroups[1].CrossZone != "" {
		t.Errorf("nlb-a = %+v, want nil LB cross-zone and empty TG cross-zone", nlb)
	}
}
