package classify

import (
	"maps"
	"slices"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

type externalWant struct {
	key        string
	traffic    string
	dest       model.AZDistribution
	sourceKind string
	sourceName string
	source     model.AZDistribution
	spread     string
	coverage   string
}

func externalPathsFor(t *testing.T, aws model.AWSInventory) []model.Path {
	t.Helper()
	aws.VPCIDs = []string{"vpc-1"}
	return BuildPaths(model.Snapshot{Nodes: nodesIn("a", "b"), AWS: &aws})
}

func assertExternalPaths(t *testing.T, got []model.Path, want []externalWant) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		p := got[i]
		if p.Key() != w.key || p.Facts.Traffic != w.traffic || !maps.Equal(p.Dest.AZDist, w.dest) {
			t.Errorf("[%d] key/traffic/dest = %s/%s/%v, want %s/%s/%v", i, p.Key(), p.Facts.Traffic, p.Dest.AZDist, w.key, w.traffic, w.dest)
		}
		if p.Source.Kind != w.sourceKind || p.Source.Name != w.sourceName || !maps.Equal(p.Source.AZDist, w.source) {
			t.Errorf("[%d] source = %+v, want %s/%s %v", i, p.Source, w.sourceKind, w.sourceName, w.source)
		}
		if p.Facts.AZSpread != w.spread || p.Facts.ZoneCoverage != w.coverage {
			t.Errorf("[%d] spread/coverage = %s/%s, want %s/%s", i, p.Facts.AZSpread, p.Facts.ZoneCoverage, w.spread, w.coverage)
		}
		wantFacts := model.Facts{Route: model.RouteExternal, TargetKind: p.Dest.Kind, Traffic: w.traffic, SameVPC: true, PrivateIP: true,
			AZSpread: w.spread, ZoneCoverage: w.coverage, Evidence: "aws"}
		if p.Facts != wantFacts || p.Route != model.RouteExternal {
			t.Errorf("[%d] facts = %+v, want %+v", i, p.Facts, wantFacts)
		}
	}
}

var clusterCallers = model.AZDistribution{"a": 1, "b": 1}

func TestBuildPathsRDS(t *testing.T) {
	// Arrange
	aws := model.AWSInventory{RDS: []model.RDSInstance{
		{ID: "aurora-1", ClusterID: "aurora", Engine: "aurora-postgresql", AZ: "a", VPCID: "vpc-1"},
		{ID: "solo", Engine: "postgres", AZ: "c", VPCID: "vpc-1"},
		{ID: "aurora-2", ClusterID: "aurora", Engine: "aurora-postgresql", AZ: "c", VPCID: "vpc-1"},
		{ID: "ha", Engine: "mysql", AZ: "a", SecondaryAZ: "b", MultiAZ: true, VPCID: "vpc-1"},
		{ID: "lonely-1", ClusterID: "lonely", Engine: "aurora-mysql", AZ: "b", VPCID: "vpc-1"},
	}}

	// Act
	paths := externalPathsFor(t, aws)

	// Assert
	assertExternalPaths(t, paths, []externalWant{
		{"rds/aurora", "client", model.AZDistribution{"a": 1, "c": 1}, "cluster", "*", clusterCallers, "multi", "partial"},
		{"rds/aurora", "replication", model.AZDistribution{"a": 1, "c": 1}, "rds", "aurora", model.AZDistribution{"a": 1, "c": 1}, "multi", "partial"},
		{"rds/ha", "client", model.AZDistribution{"a": 1}, "cluster", "*", clusterCallers, "multi", "partial"},
		{"rds/ha", "replication", model.AZDistribution{"a": 1, "b": 1}, "rds", "ha", model.AZDistribution{"a": 1, "b": 1}, "multi", "full"},
		{"rds/lonely", "client", model.AZDistribution{"b": 1}, "cluster", "*", clusterCallers, "multi", "partial"},
		{"rds/solo", "client", model.AZDistribution{"c": 1}, "cluster", "*", clusterCallers, "multi", "partial"},
	})
}

func TestBuildPathsElastiCacheAndMSK(t *testing.T) {
	// Arrange
	aws := model.AWSInventory{
		Caches: []model.CacheCluster{
			{ID: "sessions-001", GroupID: "sessions", Engine: "redis", NodeZones: []string{"a"}, VPCID: "vpc-1"},
			{ID: "sessions-002", GroupID: "sessions", Engine: "redis", NodeZones: []string{"b"}, VPCID: "vpc-1"},
			{ID: "memcache", Engine: "memcached", NodeZones: []string{"a", "a", ""}, VPCID: "vpc-1"},
		},
		MSK: []model.MSKCluster{{Name: "events", BrokerZones: []string{"a", "b", "c"}, VPCID: "vpc-1"}},
	}

	// Act
	paths := externalPathsFor(t, aws)

	// Assert
	brokers := model.AZDistribution{"a": 1, "b": 1, "c": 1}
	assertExternalPaths(t, paths, []externalWant{
		{"elasticache/memcache", "client", model.AZDistribution{"a": 2}, "cluster", "*", clusterCallers, "multi", "unknown"},
		{"elasticache/sessions", "client", model.AZDistribution{"a": 1, "b": 1}, "cluster", "*", clusterCallers, "multi", "full"},
		{"msk/events", "client", brokers, "cluster", "*", clusterCallers, "multi", "full"},
		{"msk/events", "replication", brokers, "msk", "events", brokers, "multi", "full"},
	})
}

func TestBuildPathsExternalVPCFilter(t *testing.T) {
	// Arrange
	aws := model.AWSInventory{
		RDS:    []model.RDSInstance{{ID: "mine", AZ: "a", VPCID: "vpc-1"}, {ID: "theirs", AZ: "a", VPCID: "vpc-9"}},
		Caches: []model.CacheCluster{{ID: "foreign", NodeZones: []string{"a"}, VPCID: "vpc-9"}},
		MSK:    []model.MSKCluster{{Name: "foreign", BrokerZones: []string{"a"}}},
	}

	// Act
	paths := externalPathsFor(t, aws)

	// Assert
	var keys []string
	for _, p := range paths {
		keys = append(keys, p.Key())
	}
	if !slices.Equal(keys, []string{"rds/mine"}) {
		t.Errorf("keys = %v, want [rds/mine]", keys)
	}
}

func TestBuildPathsWithoutAWSHasNoExternalTargets(t *testing.T) {
	// Arrange
	s := model.Snapshot{Nodes: nodesIn("a", "b")}

	// Act
	paths := BuildPaths(s)

	// Assert
	if len(paths) != 0 {
		t.Errorf("BuildPaths() = %+v, want none", paths)
	}
}

func TestBuildPathsRDSReplicationWithUnknownStandbyZone(t *testing.T) {
	// Arrange
	aws := &model.AWSInventory{VPCIDs: []string{"vpc-1"}, RDS: []model.RDSInstance{{ID: "ha", AZ: "a", MultiAZ: true, VPCID: "vpc-1"}}}

	// Act
	paths := BuildPaths(model.Snapshot{Nodes: nodesIn("a"), AWS: aws})

	// Assert
	if len(paths) != 2 {
		t.Fatalf("got %d paths, want 2", len(paths))
	}
	if got := paths[1]; got.Facts.Traffic != "replication" || got.Facts.AZSpread != "unknown" || got.Facts.ZoneCoverage != "full" {
		t.Errorf("replication facts = %+v, want unknown spread and full coverage", got.Facts)
	}
}
