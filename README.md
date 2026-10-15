# crossaz

English | [한국어](README.ko.md)

A read-only CLI that finds the cross-AZ traffic paths in an EKS (VPC CNI) cluster that are **actually billed**, estimates their monthly cost, and tells you how to fix them.

Most tools paint "crossed an AZ" red. That flags services behind an ALB as leaks, even though private-IP, same-VPC traffic from an ALB is free. It also misses services whose endpoints all sit in one AZ, where every caller in the other AZs is billed. What sets crossaz apart is not measurement precision but the **billing verdict**. Every path is classified by a rule in [`rules/rules.yaml`](rules/rules.yaml), and every verdict carries the URL of the AWS documentation it rests on.

---

## Required permissions

**crossaz never calls a write API.** Each SDK client is wrapped in an interface that only has Describe/List/Get methods, so this is enforced at compile time, and a test checks it ([`internal/collect/readonly.go`](internal/collect/readonly.go)).

### AWS IAM (L1, optional)

Without credentials, or with `--no-aws`, crossaz runs without L1. `elasticloadbalancing:DescribeLoadBalancers` covers both ALB/NLB and Classic Load Balancers.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "CrossAZReadOnly",
      "Effect": "Allow",
      "Action": [
        "elasticloadbalancing:DescribeLoadBalancers",
        "elasticloadbalancing:DescribeLoadBalancerAttributes",
        "elasticloadbalancing:DescribeTargetGroups",
        "elasticloadbalancing:DescribeTargetGroupAttributes",
        "ec2:DescribeInstances",
        "ec2:DescribeSubnets",
        "ec2:DescribeNetworkInterfaces",
        "rds:DescribeDBInstances",
        "elasticache:DescribeCacheClusters",
        "elasticache:DescribeCacheSubnetGroups",
        "kafka:ListClustersV2"
      ],
      "Resource": "*"
    }
  ]
}
```

Add the following only for `crossaz bill` and `--verify` (L3). **The Cost Explorer API charges $0.01 per request (page).** Daily results grouped by usage type and operation can span several pages, so a single run can cost more than $0.01.

```json
{
  "Sid": "CrossAZVerify",
  "Effect": "Allow",
  "Action": ["ce:GetCostAndUsage"],
  "Resource": "*"
}
```

### Kubernetes RBAC (L0, required)

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: crossaz-reader
rules:
  - apiGroups: [""]
    resources: [nodes, services, pods]
    verbs: [list]
  - apiGroups: [discovery.k8s.io]
    resources: [endpointslices]
    verbs: [list]
  - apiGroups: [networking.k8s.io]
    resources: [ingresses, ingressclasses]
    verbs: [list]
  - apiGroups: [apps]
    resources: [daemonsets]
    resourceNames: [kube-proxy]
    verbs: [get]
```

Reading the server version (`/version`) is allowed for every authenticated user by default (`system:public-info-viewer`), so it needs no extra permission.
To bind the role to a Kubernetes group of an EKS access entry: `kubectl create clusterrolebinding crossaz-reader --clusterrole=crossaz-reader --group=<group>`.

---

## Install

```sh
# Homebrew
brew install --cask leegun2000/tap/crossaz

# kubectl plugin (once listed in krew-index)
kubectl krew install crossaz

# Go
go install github.com/leegun2000/crossaz/cmd/crossaz@latest
```

---

## Usage

```
crossaz scan    [flags]
crossaz explain <ns/name | kind/ref> [flags]
crossaz bill    [--days N] [flags]
crossaz rules   [--rules file]
crossaz version
```

| Flag | Default | Description |
|---|---|---|
| `--kubeconfig` | `$KUBECONFIG` / `~/.kube/config` | Path to the kubeconfig |
| `--context` | current context | kubeconfig context |
| `--cluster` | the context's cluster (the part after `/` for an EKS ARN) | Cluster name used in the output |
| `--region` | node label `topology.kubernetes.io/region`, then the AWS config | Region |
| `--rules` | built-in [`rules/rules.yaml`](rules/rules.yaml) | **Replace** the rules with this file |
| `--rate-gb` | rule or region rate | Force the USD/GB rate (both sides combined) of every billable path |
| `--days` | `14` | (bill only) Number of closed UTC days to read, 1–90. Exit code 2 outside that range |
| `--threshold` | `1` | (scan) Hide billable paths estimated below this many USD/month and show only their count |
| `--json` | | (scan) the whole `ScanResult`; (bill) the `Bill` with region, accounts and metric, as JSON |
| `--prometheus-url` | | L2 volume estimate (e.g. `http://localhost:9090`). Percent-encode special characters in basic-auth credentials (`https://user:p%40ss@host`). An unparsable URL fails with `prometheus: base URL is invalid (percent-encode credentials)`, and no error or warning ever includes the user name or password |
| `--prom-selector` | | PromQL label matchers appended to every vector selector of the Prometheus queries (e.g. `'cluster="prod-a",job=~"kubelet\|cadvisor"'`, braces optional). Narrows a Prometheus that holds several clusters down to this one. Bad syntax (a missing comma, an unterminated string, a regex that does not compile, …) is reported with its position before any request is sent, with exit code 2 |
| `--prom-window` | `7d` | Period over which volume is averaged (a PromQL duration; an error if shorter than the 5-minute `rate` step). Pods that lived less, or a Prometheus retention that is shorter, are averaged over what exists |
| `--verify` | | (scan only) L3: compare with the Cost Explorer InterZone lines of the last 14 closed UTC days, scaled to 30 days. If L1 (the AWS inventory) is unavailable (no credentials, credentials for another account, …) it is skipped with a warning |
| `--no-aws` | | Disable L1 (Kubernetes only) |
| `--fixture <file>` | | Use a snapshot JSON instead of a cluster. `--prometheus-url` and `--verify` are ignored with a warning |
| `--dump-fixture` | | (scan) Print the collected snapshot, anonymized, as JSON and exit |
| `--timeout` | `2m` | Overall collection timeout |

```sh
crossaz scan                                    # L0 + L1 (when credentials exist)
crossaz scan --prometheus-url http://localhost:9090 --verify
crossaz scan --prometheus-url http://thanos:9090 --prom-selector 'cluster="prod-a"'   # Prometheus shared by several clusters
crossaz explain payments/api                    # why: facts, matched rule, source URL, fix command
crossaz explain rds/orders-db                   # targets outside the cluster use kind/ref
crossaz bill --days 14                          # daily Cost Explorer bill waterfall (account and region)
crossaz rules                                   # table of the rules in effect
```

It works the same as a kubectl plugin. Put flags **after** the plugin name.

```sh
kubectl -n monitoring port-forward svc/prometheus-server 9090:80 &
kubectl crossaz scan --context prod-a --prometheus-url http://localhost:9090
```

### Example output

This is real output for [`testdata/fixtures/prod-a.json`](testdata/fixtures/prod-a.json), a 3-AZ cluster with all of L0 to L3. The same text is tested as the golden file [`testdata/golden/prod-a.txt`](testdata/golden/prod-a.txt).

```
$ crossaz scan --fixture testdata/fixtures/prod-a.json
Cluster: prod-a   Region: ap-northeast-2   AZs: 3   Nodes: 24   Layers: L0+L1+L2+L3
Exposure: LIKELY

BILLABLE
SERVICE           ROUTE         RECV/mo   AZ SPREAD   EST $/mo   CONF   FIX
payments/api      clusterip     4.2 TB    3/3 even    ~$56       med    PreferClose
events/kafka      headless      3.1 TB    3/3 even    ~$41       med    relocate
cache/redis       clusterip     2.1 TB    1/3         ~$28       med    topologySpread
search/indexer    clusterip     1.8 TB    3/3         ~$24       med    PreferClose
shop/storefront   alb (inst)    1.5 TB    3/3 even    ~$20       med    PreferClose
edge/gateway      nlb-xz (ip)   900 GB    3/3 even    ~$12       med    disable-nlb-xz
(5 paths below $1/mo hidden — use --threshold 0)

NOT BILLABLE
SERVICE                 ROUTE              RECV/mo   AZ SPREAD   EST $/mo   CONF   NOTE
api/internal            alb (inst)         2.4 TB    3/3 even    —          high   alb-private-same-vpc
edge/tcp-ingest         nlb-zonal (inst)   700 GB    3/3 even    —          med    nlb-zonal-internet
ingress/web             alb (ip)           6.0 TB    3/3 even    —          med    alb-ip-target-mode
legacy/billing-portal   clb                300 GB    3/3 even    —          high   clb-private-same-vpc
msk/events-msk          external           —         3/3 even    —          high   msk-inter-broker
orders/api              clusterip          3.3 TB    3/3 even    —          med    topology-aware-routing
rds/ledger              external           —         2/3 even    —          high   rds-multiaz-replication
rds/orders-db           external           —         2/3 even    —          high   rds-multiaz-replication

UNCERTAIN
SERVICE                    ROUTE            RECV/mo   AZ SPREAD   EST $/mo   CONF   NOTE
default/kubernetes         clusterip        ?         2/3 even    ?          low    clusterip-cross-az-partial
elasticache/sessions       external         ?         2/3 even    ?          low    elasticache-cross-az
grpc/ledger-api            nlb-zonal (ip)   400 GB    3/3 even    ?          low    nlb-client-cross-az
monitoring/node-exporter   clusterip        ?         3/3 even    ?          low    clusterip-cross-az
msk/events-msk             external         ?         3/3 even    ?          low    msk-client-cross-az
rds/ledger                 external         ?         2/3 even    ?          low    rds-cross-az
rds/orders-db              external         ?         1/3         ?          low    rds-cross-az

Estimated total: ~$183/mo
Run `crossaz explain payments/api` for rule details.

VERIFY
  Estimated:  ~$183/mo
  Actual:     $165/mo   (Cost Explorer APN2-DataTransfer-Regional-Bytes InterZone, 2026-08-18..2026-08-31, 14 days, 1 day provisional (Estimated); whole account and region)
  Ratio:      1.11   exclusion rules look broadly right

  Ratio > 1.5 → suspect a missing exclusion rule
  Ratio < 0.5 → suspect leaks outside the cluster (RDS/MSK/NLB/other workloads)

WARNINGS
  - the estimated total leaves out 2 billable path(s) whose volume could not be measured
```

How to read it:

- `cache/redis`: its endpoints are in only 1 of 3 AZs. Callers in the other two AZs always cross an AZ, so it is **billed**. `trafficDistribution` alone cannot fix that, so the fix is `topologySpread`.
- `shop/storefront`: ALB instance targets with `externalTrafficPolicy: Cluster`. ALB to node is free, but kube-proxy's extra hop to a pod in another AZ is billed.
- `api/internal`: the same ALB instance targets, but with `externalTrafficPolicy: Local` there is no hop, so it is not billed (`high`).
- `ingress/web`: ALB IP target mode is judged not billed (`medium`), but the pricing wording has a gap, so cross-checking with `--verify` is recommended.
- `orders/api`: every endpoint has a hint for its own AZ (`trafficDistribution: PreferClose`), so kube-proxy keeps traffic zone-local. Not billed, but with L2 the received volume is still shown.
- `rds/orders-db`: replication is free; the client path matches a billing rule, but crossaz does not know whether the cluster really talks to it or how much, so it is `low` with amount `?`.
- `monitoring/node-exporter`: a hostNetwork pod captures the whole node's traffic, so it is left unmeasured; with no measured pod it is `low`.
- `grpc/ledger-api`: an internal NLB with cross-zone off. A client that gets another AZ's NLB node from DNS is billed, but that depends on DNS routing, so it is `low`: the received volume is shown and the amount is left blank.
- The warning about "2 billable paths whose volume could not be measured": `default/kubernetes` (endpoints without pods) and `monitoring/node-exporter` match billing rules but their volume could not be measured, so they went to UNCERTAIN and their cost is missing from the total.
- AZ SPREAD = `destination AZs/caller AZs`, with `even` when there are 2 or more destination AZs and endpoints are spread evenly.
- Without L2 the amounts are `?` and the total reads `Estimated total: ? (no L2 — --prometheus-url)` ([`testdata/golden/l0-only.txt`](testdata/golden/l0-only.txt)). That golden also shows: an EKS Auto Mode ALB (`marketing/site`, ip targets by default); a zone-local service that ingress-nginx reaches directly by pod IP (`admin/console`, `ingress-direct-backend`); a service with topology-mode Auto whose controller withholds hints so kube-proxy spreads across all endpoints (`notify/worker`, `hints-withheld` under BILLABLE); a non-private IPv4 ALB target (`partners/webhook`, `lb-exemption-unverified`); and an undecided path (`edge/relay`, `unclassified`). For a cluster whose kube-proxy add-on is still 1.30, `trafficDistribution` without the annotation (`hints-unverified`) and withheld Auto hints (`hints-withheld`) are in [`testdata/golden/stale-proxy.json`](testdata/golden/stale-proxy.json).

Use `explain` to see why. Collection and analysis warnings go to stderr first as `crossaz: warning:` (the explanation goes to stdout).

```
$ crossaz explain cache/redis --fixture testdata/fixtures/prod-a.json
crossaz: warning: the estimated total leaves out 2 billable path(s) whose volume could not be measured
cache/redis  (service)
  route:       clusterip  (evidence: k8s)
  callers:     cluster/*  ap-northeast-2a=8 ap-northeast-2b=8 ap-northeast-2c=8
  destination: ap-northeast-2a=3
  facts:       az_spread=multi zone_coverage=partial kube_proxy_hints=true topology_hints=false hints_configured=false zone_local=false direct_ingress_backend=false same_vpc=true private_ip=true traffic=client
  rule:        clusterip-cross-az-partial  (billable: true, rule confidence: high)
  reason:      Callers in an AZ without an endpoint always cross to another AZ (trafficDistribution cannot fix this)
  source:      https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html  (verified 2026-09-14)
  estimate:    recv 2.1 TB/mo × cross-AZ 0.667 × $0.02/GB = ~$28
  confidence:  medium
  fix:         topologySpread — Add topologySpreadConstraints (topology.kubernetes.io/zone) so every AZ gets an endpoint, then set trafficDistribution
```

A single-AZ cluster with no billable or undecided path ends in two lines:

```
Cluster: dev   AZs: 1   Nodes: 3
Exposure: NONE — all nodes are in a single AZ and there is no billable path.
```

`bill` shows the daily Cost Explorer bill by family and direction, narrowed to the accounts that own the node instances (L1, the OwnerId from `ec2:DescribeInstances`) and the region ([`testdata/golden/bill.txt`](testdata/golden/bill.txt)).

```
$ crossaz bill
Region: us-east-1   Accounts: 111122223333   Window: 2026-08-31..2026-09-13 (UTC, 14 days)   Metric: UnblendedCost

WATERFALL
FAMILY      SIDE   USD      GB        $/GB
interzone   In     $14.00   1400.00   0.0100
interzone   Out    $14.00   1400.00   0.0100
publicip    Out    $7.00    700.00    0.0100
other       ?      $0.25    25.00     0.0100
TOTAL              $35.25   3525.00   0.0100
xaz-free    In     $0.00    420.00    0.0000
xaz-free is $0 (PrivateLink/TGW/Client VPN) and left out of TOTAL
Not final (Estimated) days: 2026-09-13; amounts may still change (included in TOTAL)
```

- Scope: lines whose usage type ends in `DataTransfer-Regional-Bytes`, split by operation into `interzone`, `publicip` and `lb-publicip` (an unknown operation goes to `other` with a warning); `VpcPeering-In/Out-Bytes` (`vpcpeering`); and the $0 `DataTransfer-xAZ-In/Out-Bytes` (`xaz-free`, left out of TOTAL).
- Cost Explorer accepts at most two GroupBy keys (USAGE_TYPE, OPERATION), so lines are not split by account. A line's `account` is filled only when there is a single account.
- Without L1, the query is filtered by region only, with a warning. But if L1 runs and finds no VPC for the nodes (credentials for another account or region), crossaz does not query and exits with code 1. `AccessDenied`, `DataUnavailable` and an empty result show as "bill: unknown", never `$0`.

---

## Layers

Each layer works on its own and gives a result without the layers above it.

| Layer | Needs | Produces | When |
|---|---|---|---|
| **L0 topology** | Kubernetes API (the ClusterRole above) | Node AZ spread, service routes (ALB Ingress, LB annotations, ClusterIP, headless), EndpointSlice AZ spread and hints, billing verdicts | Always |
| **L1 AWS check** | AWS credentials + the IAM policy above | Actual LB scheme, cross-zone, target type and AZs; AZ of endpoint IPs without a zone (ENIs in the cluster VPC only); RDS/ElastiCache/MSK paths | When credentials exist and `--no-aws` is not set. If some LB lookups fail, only those LBs are dropped (falling back to annotations) with a warning |
| **L2 volume estimate** | Prometheus (cAdvisor `container_network_receive_bytes_total`) | Monthly received bytes → cost | `--prometheus-url` (plus optional `--prom-selector`). If the result contains no endpoint pod of this cluster (another cluster, missing cAdvisor, Prometheus scraping for under 10 minutes, a `--prom-selector` that matches nothing), L2 is treated as absent with a warning |
| **L3 bill** | `ce:GetCostAndUsage` | Daily bill waterfall (`bill`), estimate vs. actual ratio (`--verify`) | `bill`, or `--verify` with L1 |
| L4 measured | VPC Flow Logs | Exact cost | Out of scope for v1 |

### CONF and sections

- **CONF = min(rule, evidence, volume)**
  - Rule: the matched rule's `confidence`.
  - Evidence: `k8s` (ClusterIP, headless) and `aws` (confirmed by L1) → high; `annotation` (inferred from annotations or defaults without L1) → medium.
  - Volume: at most medium once the L2 estimate is used. With no metrics for any endpoint pod: low, amount `?`.
  - A billable path to a target outside the cluster (RDS/ElastiCache/MSK) is low, amount `?`.
  - A billable path whose endpoint AZs are all unknown is low with amount `?`, with or without L2.
  - A billable path that touches a Local or Wavelength Zone is low with amount `?` because the rate differs (received volume is still shown).
- **Sections**
  - `UNCERTAIN`: paths whose CONF is low, or whose billing is undecided (`billable: unknown`).
  - `NOT BILLABLE`: paths that are not billed and not low.
  - `BILLABLE`: the rest. Paths with a known amount below `--threshold` are hidden and only counted (they stay in the total).
  - Sorted by amount, descending (unknown amounts last); the other sections are sorted by key. Empty sections are omitted.
- **Exposure**
  - `LIKELY`: there is a BILLABLE path (hidden ones included), or a service path whose rule is certain but which dropped to UNCERTAIN during estimation (no metrics, unknown AZ, Local Zone).
  - `NONE`: at most one node AZ, and nothing billable or undecided in UNCERTAIN either (there can still be destinations in other AZs, such as the EKS control-plane ENIs).
  - `UNLIKELY`: everything else.
- **Estimated total**: with L2, the sum of known amounts of BILLABLE paths (hidden ones included). If a service path dropped as above had no measurable volume, `WARNINGS` says that N billable paths are missing from the total.
- **Undecided**: `billable: unknown` paths are counted neither as $0 (not billed) nor in the total. They get their own line under `Estimated total`, `Unconfirmed: $0–$X/mo`, where X is the upper bound if they were billed (all received volume × the path's rate), or `?` if any of them has no measured volume. In JSON this is `unconfirmedMaxUsd`.

---

## Rules

The billing facts and their sources are in [`docs/billing-rules.md`](docs/billing-rules.md) (Korean); the rules in effect are in [`rules/rules.yaml`](rules/rules.yaml).

Rules are **evaluated in order, and the first match wins.** The last rule must be a catch-all with an empty match.

| Rule ID | Verdict | Rule CONF | Rate | Fix |
|---|---|---|---|---|
| `same-az` | not billed | high | region | |
| `zone-unknown` | undecided | low | region | |
| `nlb-cross-zone` | **billed** | high | region | `disable-nlb-xz` |
| `nodeport-hop-cross-az` (ALB/CLB/NLB instance targets + `externalTrafficPolicy: Cluster`, not zone-local) | **billed** | medium | region | `PreferClose` |
| `nlb-cross-zone-unknown` | **billed** | low | region | |
| `ingress-direct-backend` (a zone-local ClusterIP that a non-ALB Ingress reaches directly by pod IP) | not billed | low | region | |
| `topology-aware-routing` (clusterip/alb/clb) | not billed | medium | region | |
| `alb-ip-target-mode` | not billed | medium | region | |
| `alb-private-same-vpc`, `clb-private-same-vpc` | not billed | high | region | |
| `lb-exemption-unverified` (IPv6 or non-private IPv4 targets, another VPC) | **billed** | low | region | |
| `nlb-client-cross-az` | **billed** | low | region | `zonal-dns` |
| `nlb-zonal-internet` | not billed | medium | region | |
| `clusterip-cross-az-partial` | **billed** | high | region | `topologySpread` |
| `hints-unverified` (trafficDistribution/topology-mode is set, but it is unconfirmed whether kube-proxy uses this service's hints: unknown version, a replacement proxy, or 1.30 or older without the annotation. Only when endpoints are in every AZ; if an AZ has none, the rule above already decides "billed") | **billed** | low | region | |
| `hints-withheld` (a hint-aware kube-proxy, but the EndpointSlice has no hints, e.g. topology-mode Auto withheld them for too few endpoints or overload, so traffic spreads to all endpoints) | **billed** | high | region | `PreferClose` (after removing the annotation) |
| `clusterip-cross-az` | **billed** | high | region | `PreferClose` |
| `headless-cross-az` | **billed** | high | region | `relocate` |
| `rds-multiaz-replication`, `msk-inter-broker` | not billed | high | region | |
| `rds-cross-az`, `elasticache-cross-az` (EC2 side only) | **billed** | high (final low) | $0.01 | `relocate` |
| `msk-client-cross-az` (only the officially confirmed client side, a lower bound) | **billed** | medium (final low) | $0.01 | `relocate` |
| `unclassified` (catch-all) | undecided | low | region | |

- Rate `region` means the rule has no `rate_gb`, so the region rate is used (all 36 built-in regions are $0.02/GB, both sides combined). It does not apply to rules that are not billed.
- `billable` takes three values: `true`, `false` and `unknown`. In JSON output they are strings (`"true"`, `"false"`, `"unknown"`).
- EKS Auto Mode: an ALB whose IngressClass controller is `eks.amazonaws.com/alb`, and an NLB with `loadBalancerClass: eks.amazonaws.com/nlb`, are treated as ip targets when there is no target-type annotation. ALB detection recognizes both the self-managed LBC (`ingress.k8s.aws/alb`) and the Auto Mode controller, and goes by the controller, not the IngressClass name (a class named `alb` counts as ALB only when no such IngressClass object exists).
- The `PreferClose` fix command is rendered as the equivalent `PreferSameZone` when the control plane is 1.34 or later.
- Cross-AZ traffic to VPC interface endpoints has been free since 2022-04-01, so it is not evaluated.

### Changing the rules

```sh
cp rules/rules.yaml my-rules.yaml               # start from the original
crossaz rules --rules my-rules.yaml             # validate and print the rule table
crossaz scan --rules my-rules.yaml
```

A `--rules` file **replaces** the built-in rules entirely; it is not merged. It is validated on load: unique ids, `confidence` ∈ {high, medium, low}, a `billable` value of true, false or "unknown", a catch-all last rule, `default_rate_gb > 0`, fix command templates that parse, and match values that the facts can actually take (listed in the header comment of [`rules.yaml`](rules/rules.yaml), case-sensitive). Abbreviations used only for display, such as `nlb-xz`, are rejected.

---

## How the estimate works

```
recv_bytes/mo = Σ (endpoint pod received bytes/s ÷ number of services that pod belongs to)
                × (all pods / measured pods) × 2,592,000        # 30 days
monthly cost  = recv_bytes/mo / 1e9 × CrossAZFraction × rate_gb
```

**There is no `× 2`.** `rate_gb: 0.02` already combines both sides ($0.01/GB each). The inter-AZ rate is $0.01 per side in all 36 commercial and GovCloud regions.

```
CrossAZFraction = 1 − Σ_z (1 / caller AZ count) × destination share(z)
```

- Callers in 3 AZs, endpoints spread evenly across 3 AZs → `1 − 1/3 = 0.667`.
- Callers in 3 AZs, endpoints in a single AZ → also `0.667` (not 0: callers in the other two AZs always cross).
- `zone_local` (hints cover every caller AZ, or `internalTrafficPolicy: Local`) → `0`. This applies only to `clusterip`, `alb`, `clb` and `nlb-zonal` paths, where the kube-proxy hop is the only leg that can cross an AZ.
- Hints exist but do not cover some caller AZs → `caller AZs without an endpoint / caller AZs` (same paths only).
- `nlb-cross-zone`, `nlb`, headless and external targets use the base formula regardless of hints; hints cannot remove the LB-node-to-target leg.
- Caller spread: node AZs for ClusterIP/headless; for LB routes, the LB's AZs (L1) or the node AZs.

Prometheus query (`WINDOW` = `--prom-window`, default `7d`):

```promql
sum by (namespace, pod) (avg_over_time((max by (namespace, pod, interface) (
  rate(container_network_receive_bytes_total{pod!=""}[5m])
    and container_network_receive_bytes_total{pod!=""} offset 5m
))[WINDOW:5m]))
```

- A 5-minute `rate` is taken every 5 minutes and averaged **only over the time the pod existed**. `rate(x[7d])` divides the increase by 7 days, which shrank a 14-minute-old pod by about 700× (every path dropped below `--threshold` right after a rolling deploy). For the same reason, a retention shorter than `WINDOW` does not drag the average down.
- `and … offset 5m` keeps only steps that already had a sample 5 minutes earlier, so the half-step of a newly started pod does not pull the average down. Pods younger than 5–10 minutes are therefore unmeasured and extrapolated from the measured pods of the same service (`k/n pods measured`).
- `max by (…, interface)` removes series duplicated per container by some runtimes. Only currently ready endpoint pods match, so traffic of pods removed by a rolling deploy is not counted.
- Because it is a subquery, the Prometheus load is proportional to the number of 5-minute steps in `WINDOW` (2016 for 7d). If a very large number of pods hits `query.max-samples` or a timeout, shorten `--prom-window`. `rate[5m]` needs a scrape interval of 2m30s or less.

Assumptions:

1. Callers are spread **evenly** across node (or LB) AZs, and requests are distributed in proportion to the number of endpoints.
2. Only received bytes are counted. Every byte is received by someone exactly once, so the total is right, but response bytes are attributed to the calling service.
3. hostNetwork pods (which capture the whole node's traffic) and pods without metrics are left unmeasured and extrapolated from the share of measured pods (`k/n pods measured`).
4. Received bytes include traffic from outside the cluster. Where that is free, such as behind an ALB, the rules filter it out as not billed.

The goal is the order of magnitude: telling $0.06 from $800 is enough.

---

## Limitations

- **These are estimates, not measurements.** Cross-checking with `--verify` is recommended.
- Traffic that starts outside the cluster (other workloads, EC2, Lambda, …) is out of scope. For RDS/ElastiCache/MSK, crossaz sees only the path and the AZ spread; it does not know whether the cluster really talks to them or how much, so they are `low` with amount `?`.
- It assumes **AWS VPC CNI + kube-proxy**. Hint behavior under Cilium's kube-proxy replacement and EKS Auto Mode is unverified, so without the kube-proxy DaemonSet (or without permission to `get daemonsets/kube-proxy`) zone-local routing is not credited. Services with `trafficDistribution`/`topology-mode` set become `hints-unverified` (possibly billed, low); services without them are judged billed. If kube-proxy is confirmed to be a hint-aware version but the EndpointSlice has no hints (e.g. topology-mode Auto withheld them), kube-proxy spreads across all endpoints, so the verdict is `hints-withheld` (billed, high). For measured numbers, [kubezonnet](https://github.com/polarsignals/kubezonnet) is recommended.
- Ingress controllers that connect directly to pod IPs (the ingress-nginx default, for example) bypass kube-proxy hints. A zone-local service behind such an Ingress is left as `ingress-direct-backend` (low), and the controller's own zone-aware settings are not read. With `nginx.ingress.kubernetes.io/service-upstream: "true"` traffic goes through the ClusterIP, so zone-local is credited.
- ALB IP target mode is judged not billed (`medium`) based on the EKS Best Practices guide, but the EC2 pricing wording names only "EC2 instances", so a `--verify` cross-check is still worthwhile.
- Caller spread across AZs is assumed even. For workloads whose callers are concentrated in one AZ, the error grows.
- Rates are built in only for the 36 commercial and GovCloud regions. Elsewhere (China regions, for example) a default is used with a warning, so set `--rate-gb`. If there are Local or Wavelength Zone nodes, crossaz warns, and billable paths touching those zones have a different rate, so their amount is left blank and they go to UNCERTAIN (left out of the total, received volume shown).
- Public-IP hairpins (a pod calling an internet-facing LB hostname) are billed regardless of AZ, but cannot be detected without Flow Logs.
- One path per service. A service behind an ALB Ingress is judged only as `alb`, and a type=LoadBalancer service only as its NLB/CLB path; in-cluster ClusterIP traffic to the same service is not separated (it is included in the received volume).
- ALB Ingress backends are recognized both as plain service backends and through `use-annotation` actions (`forwardConfig.targetGroups[].serviceName` in `alb.ingress.kubernetes.io/actions.<name>`, or the legacy `serviceName`). Weighted forwards, blue/green and Argo Rollouts canaries fall in this case. If the action JSON is broken or has only target group ARNs, the service cannot be linked.
- If nodes are in a single AZ but an ALB, CLB or cross-zone-off NLB spans several AZs, the path is still `same-az`: ALB/CLB to node is free, and a zonal NLB removes AZs without targets from DNS. Only an NLB with cross-zone on or unknown adds the LB AZs confirmed by L1 to the callers (without L1 the LB AZs are unknown, so node AZs are used).
- `--verify`'s Actual covers the **whole account and region**: the `InterZone-In/Out` operation lines among `DataTransfer-Regional-Bytes` (PublicIP, LB-PublicIP, VPC peering and the $0 xAZ lines are left out). `Estimated` days are included and scaled to 30 days, and their count is shown as "N days provisional (Estimated)", because Cost Explorer keeps every day Estimated until the month closes. Inter-AZ traffic of other clusters, EC2 and ELB is all included, so this is an upper bound and a ratio below 1 is natural. Without L1 (no credentials, credentials for another account that cannot see the nodes, …) L3 is skipped so that another account's ratio is never shown.
- Without `--prom-selector`, the Prometheus queries are not scoped to a cluster label. In a Prometheus that holds several clusters (Thanos, AMP, …) the same `namespace/pod` names can mix with other clusters' values. crossaz warns and drops L2 only when **none** of this cluster's pods are in the result, so a partial mix is not caught. Name this cluster with `--prom-selector 'cluster="…"'`.
- Response bytes are attributed to the calling service as received bytes.
- With VPC CNI prefix delegation, the AZ of an endpoint IP without a zone may not be found through the ENI lookup (the pod IP lies inside a delegated prefix rather than being a separate ENI IP, so the `addresses.private-ip-address` filter does not match). Endpoints whose EndpointSlice has a zone (most of them) are unaffected. ENI lookups are limited to the cluster VPC, and an IP for which ENIs report different AZs is left with an unknown AZ.

---

## Development

```sh
go test -race ./...
go test ./internal/scan -update                          # refresh golden files (testdata/golden)
crossaz scan --dump-fixture > testdata/fixtures/x.json   # anonymized snapshot
crossaz scan --fixture testdata/fixtures/x.json          # reproduce without a cluster
```

`--dump-fixture` replaces IPs, node names, instance IDs, VPC/subnet IDs, LB and target group names/DNS, and account IDs deterministically using sha256 (joins are preserved, as is the private/public IPv4/IPv6 distinction). Annotations not used for verdicts are dropped (LB/TG attribute annotations keep only the cross-zone value). Warnings have the replaced values substituted and are cut to `<layer>: <target>: <redacted>`, which removes raw AWS/Kubernetes/HTTP error text (account IDs, principal ARNs, emails, home paths, …). For static-pod mirror pods (owner is a Node), the node name at the end of the pod name and the owner name get the same replacement. **Cluster names, namespace/service/pod/Ingress/owner names (otherwise), RDS/ElastiCache/MSK identifiers, and Cost Explorer bill lines (amounts, usage types, operations) are kept as is**, so review a fixture before committing it.

Real-cluster verification lives in [`hack/e2e`](hack/e2e/README.md). It brings up scenarios on kind (3 AZ labels, 6 nodes) and checks the L0 verdicts, the share of traffic kube-proxy actually sends across AZs, and the Prometheus (cAdvisor) volume estimate (`hack/e2e/run.sh`, about 10 minutes, no AWS needed).

Design and package contracts: [`docs/design.md`](docs/design.md). Billing rule sources: [`docs/billing-rules.md`](docs/billing-rules.md). Both are in Korean.

### Releases

Pushing a `v*` tag makes [`.github/workflows/release.yml`](.github/workflows/release.yml) run `go vet` and `go test -race`, then GoReleaser builds linux/darwin/windows × amd64/arm64 binaries, `checksums.txt` and a GitHub Release.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

| Channel | Needs | Without it |
|---|---|---|
| GitHub Release | nothing (the default `GITHUB_TOKEN`) | — |
| Homebrew cask | a `leegun2000/homebrew-tap` repository, plus a token with write access to it stored as the secret `HOMEBREW_TAP_GITHUB_TOKEN` | only the cask push is skipped |
| krew | submit the first version to [krew-index](https://github.com/kubernetes-sigs/krew-index) by hand, then set the repository variable `KREW_RELEASE=true` | the krew step is skipped |

While the repository is private, release assets cannot be downloaded without authentication, so Homebrew and krew installs work only after it goes public. To check locally: `goreleaser release --snapshot --clean`.

## License

[MIT](LICENSE)
