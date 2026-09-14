package classify

import (
	"cmp"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/leegun2000/crossaz/internal/model"
)

// Fact values shared by service and external paths.
const (
	spreadSingle    = "single"
	spreadMulti     = "multi"
	factUnknown     = "unknown"
	coverageFull    = "full"
	coveragePartial = "partial"

	trafficClient      = "client"
	trafficReplication = "replication"

	sourceAll         = "*"
	sourceKindCluster = "cluster"
)

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// BuildPaths fills the facts of every path: one per service with ready endpoints, plus paths to managed
// AWS targets in the cluster's VPCs. No rule is applied. Sorted by Key, then traffic.
func BuildPaths(s model.Snapshot) []model.Path {
	callers, _ := zoneDist(nodeZones(s.Nodes))
	paths := servicePaths(s, callers)
	if s.AWS != nil {
		paths = append(paths, externalPaths(*s.AWS, callers)...)
	}
	slices.SortStableFunc(paths, func(a, b model.Path) int {
		return cmp.Or(cmp.Compare(a.Key(), b.Key()), cmp.Compare(a.Facts.Traffic, b.Facts.Traffic))
	})
	return paths
}

func nodeZones(nodes []model.Node) []string {
	zones := make([]string, 0, len(nodes))
	for _, n := range nodes {
		zones = append(zones, n.Zone)
	}
	return zones
}

// zoneDist counts known zones; the second result is the number of unknown ("") entries.
func zoneDist(zones []string) (model.AZDistribution, int) {
	dist := model.AZDistribution{}
	unknown := 0
	for _, z := range zones {
		if z == "" {
			unknown++
			continue
		}
		dist[z]++
	}
	return dist, unknown
}

func clusterSource(callers model.AZDistribution) model.Workload {
	return model.Workload{Name: sourceAll, Kind: sourceKindCluster, AZDist: maps.Clone(callers)}
}

func azSpread(callers, dests model.AZDistribution, unknownDests int) string {
	if dests.ZoneCount() == 0 && unknownDests > 0 {
		return factUnknown // callers alone say nothing about where the traffic lands
	}
	zones := map[string]bool{}
	for _, d := range []model.AZDistribution{callers, dests} {
		for z, c := range d {
			if c > 0 {
				zones[z] = true
			}
		}
	}
	switch {
	case len(zones) >= 2:
		return spreadMulti
	case unknownDests > 0:
		return factUnknown
	default:
		return spreadSingle
	}
}

func zoneCoverage(callers, dests model.AZDistribution, unknownDests int) string {
	switch {
	case covers(dests, callers):
		return coverageFull
	case unknownDests > 0:
		return factUnknown
	default:
		return coveragePartial
	}
}

// covers reports whether every zone present in need is present in have.
func covers(have, need model.AZDistribution) bool {
	for z, c := range need {
		if c > 0 && have[z] <= 0 {
			return false
		}
	}
	return true
}

func readyEndpoints(eps []model.Endpoint) []model.Endpoint {
	var out []model.Endpoint
	for _, ep := range eps {
		if ep.Ready {
			out = append(out, ep)
		}
	}
	return out
}

// endpointZones resolves each endpoint's zone from its EndpointSlice label, then L1 IPZones; "" = unknown.
func endpointZones(eps []model.Endpoint, aws *model.AWSInventory) []string {
	zones := make([]string, 0, len(eps))
	for _, ep := range eps {
		zone := ep.Zone
		if zone == "" && aws != nil {
			zone = aws.IPZones[ep.IP]
		}
		zones = append(zones, zone)
	}
	return zones
}

// hintedForZones maps each hinted zone to the zones of the endpoints hinted for it.
// zones[i] is eps[i]'s resolved zone; endpoints with an unknown zone are left out.
func hintedForZones(eps []model.Endpoint, zones []string) map[string]model.AZDistribution {
	out := map[string]model.AZDistribution{}
	for i, ep := range eps {
		if zones[i] == "" {
			continue
		}
		for _, fz := range ep.ForZones {
			if out[fz] == nil {
				out[fz] = model.AZDistribution{}
			}
			out[fz][zones[i]]++
		}
	}
	return out
}

func allHinted(eps []model.Endpoint) bool {
	return len(eps) > 0 && !slices.ContainsFunc(eps, func(ep model.Endpoint) bool { return len(ep.ForZones) == 0 })
}

func allPrivateIPv4(eps []model.Endpoint) bool {
	return !slices.ContainsFunc(eps, func(ep model.Endpoint) bool { return !isPrivateIPv4(ep.IP) })
}

func isPrivateIPv4(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	return err == nil && addr.Is4() && (addr.IsPrivate() || cgnatPrefix.Contains(addr))
}

// minorVersion extracts the minor from "v1.33.4-eks-abc", "v1.31.2-eksbuild.3" or "1.30".
func minorVersion(v string) (int, bool) {
	major, rest, ok := strings.Cut(strings.TrimPrefix(v, "v"), ".")
	if !ok {
		return 0, false
	}
	if _, err := strconv.ParseUint(major, 10, 32); err != nil {
		return 0, false
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	minor, err := strconv.ParseUint(rest[:end], 10, 32)
	if err != nil {
		return 0, false
	}
	return int(minor), true
}
