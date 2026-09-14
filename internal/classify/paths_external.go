package classify

import (
	"slices"

	"github.com/leegun2000/crossaz/internal/model"
)

// minReplicatedMembers is the smallest RDS cluster that replicates between its own instances.
const minReplicatedMembers = 2

func externalPaths(aws model.AWSInventory, callers model.AZDistribution) []model.Path {
	rds := inVPCs(aws.RDS, aws.VPCIDs, func(r model.RDSInstance) string { return r.VPCID })
	caches := inVPCs(aws.Caches, aws.VPCIDs, func(c model.CacheCluster) string { return c.VPCID })
	msk := inVPCs(aws.MSK, aws.VPCIDs, func(m model.MSKCluster) string { return m.VPCID })
	return slices.Concat(rdsPaths(rds, callers), cachePaths(caches, callers), mskPaths(msk, callers))
}

func rdsPaths(instances []model.RDSInstance, callers model.AZDistribution) []model.Path {
	var out []model.Path
	for _, g := range groupBy(instances, func(i model.RDSInstance) string { return i.ClusterID }) {
		if g.key == "" {
			continue
		}
		var zones []string
		for _, member := range g.items {
			zones = append(zones, member.AZ)
		}
		out = append(out, clientPath(model.TargetRDS, g.key, zones, callers))
		if len(g.items) >= minReplicatedMembers {
			out = append(out, replicationPath(model.TargetRDS, g.key, zones, callers))
		}
	}
	for _, inst := range instances {
		if inst.ClusterID != "" {
			continue
		}
		out = append(out, clientPath(model.TargetRDS, inst.ID, []string{inst.AZ}, callers))
		if inst.MultiAZ {
			out = append(out, replicationPath(model.TargetRDS, inst.ID, []string{inst.AZ, inst.SecondaryAZ}, callers))
		}
	}
	return out
}

func cachePaths(clusters []model.CacheCluster, callers model.AZDistribution) []model.Path {
	var out []model.Path
	for _, g := range groupBy(clusters, func(c model.CacheCluster) string { return c.GroupID }) {
		if g.key == "" {
			continue
		}
		var zones []string
		for _, member := range g.items {
			zones = append(zones, member.NodeZones...)
		}
		out = append(out, clientPath(model.TargetElastiCache, g.key, zones, callers))
	}
	for _, c := range clusters {
		if c.GroupID == "" {
			out = append(out, clientPath(model.TargetElastiCache, c.ID, c.NodeZones, callers))
		}
	}
	return out
}

func mskPaths(clusters []model.MSKCluster, callers model.AZDistribution) []model.Path {
	var out []model.Path
	for _, c := range clusters {
		out = append(out,
			clientPath(model.TargetMSK, c.Name, c.BrokerZones, callers),
			replicationPath(model.TargetMSK, c.Name, c.BrokerZones, callers))
	}
	return out
}

func clientPath(kind, ref string, zones []string, callers model.AZDistribution) model.Path {
	return externalPath(kind, ref, clusterSource(callers), trafficClient, zones, callers)
}

// replicationPath is traffic among the target's own members: source and destination share one distribution.
func replicationPath(kind, ref string, zones []string, callers model.AZDistribution) model.Path {
	members, _ := zoneDist(zones)
	source := model.Workload{Name: ref, Kind: kind, AZDist: members}
	return externalPath(kind, ref, source, trafficReplication, zones, callers)
}

func externalPath(kind, ref string, source model.Workload, traffic string, zones []string, callers model.AZDistribution) model.Path {
	dests, unknown := zoneDist(zones)
	return model.Path{
		Source: source,
		Dest:   model.Target{Kind: kind, Ref: ref, AZDist: dests},
		Route:  model.RouteExternal,
		Facts: model.Facts{
			Route:        model.RouteExternal,
			TargetKind:   kind,
			Traffic:      traffic,
			SameVPC:      true,
			PrivateIP:    true,
			AZSpread:     azSpread(callers, dests, unknown),
			ZoneCoverage: zoneCoverage(callers, dests, unknown),
			Evidence:     model.EvidenceAWS,
		},
	}
}

func inVPCs[T any](items []T, vpcs []string, vpcOf func(T) string) []T {
	var out []T
	for _, it := range items {
		if slices.Contains(vpcs, vpcOf(it)) {
			out = append(out, it)
		}
	}
	return out
}

type group[T any] struct {
	key   string
	items []T
}

// groupBy groups items by key in order of first appearance.
func groupBy[T any](items []T, key func(T) string) []group[T] {
	index := map[string]int{}
	var out []group[T]
	for _, it := range items {
		k := key(it)
		i, ok := index[k]
		if !ok {
			i = len(out)
			index[k] = i
			out = append(out, group[T]{key: k})
		}
		out[i].items = append(out[i].items, it)
	}
	return out
}
