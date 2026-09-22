# crossaz 설계 노트 (SPEC_1 검토 결과 + 구현 계약)

SPEC_1.md를 검토하며 바꾼 결정과 패키지 간 계약. 과금 사실의 근거는 [billing-rules.md](billing-rules.md),
적용 중인 규칙은 [`rules/rules.yaml`](../rules/rules.yaml)이 원본이다.

---

## A. SPEC 검토 — 바꾼 것과 이유

### A-1. 과금 사실이 틀렸던 곳 (2026-09-14 공식 문서 대조)

| SPEC | 실제 | 반영 |
|---|---|---|
| `vpce-interface-cross-az` 과금 $0.02/GB | 2022-04-01부터 인터페이스 엔드포인트의 AZ 간 전송은 **무료**. PrivateLink 처리료($0.01/GB)만 AZ와 무관하게 발생 | 규칙·수집 제거 (VPCE는 cross-AZ 판정 대상 아님) |
| `rds-cross-az` 등 $0.02/GB | RDS·ElastiCache는 **EC2 쪽만** 과금. MSK도 공식 확인은 클라이언트 쪽만 | `rate_gb: 0.01` |
| `rds-cross-az` = 프라이머리만 | 리플리카 포함 **클라이언트가 붙는 모든 인스턴스** | 인스턴스(또는 Aurora 클러스터 멤버) AZ 분포 |
| `alb-private-same-vpc` 근거 = cross-zone 상시 활성 | 근거는 EC2 요금의 "EC2↔ALB/CLB 사설 IP·동일 VPC 무료". scheme과 무관 | `scheme` 조건 제거, 근거 URL 교체 |
| CLB cross-zone 상시 활성 | CLB cross-zone은 끌 수 있음(API 생성 시 기본 off). 그래도 전송 무료 | 이유 문구만 교정 |
| NLB cross-zone = LB 속성 | **타겟그룹 속성이 우선**(`true`/`false`), `use_load_balancer_configuration`일 때만 LB 값 | TG별 유효값 계산 |
| annotation 없으면 NLB cross-zone off | LBC는 annotation을 지워도 속성을 되돌리지 않음 → **모름** | L1 없이 annotation도 없으면 route `nlb`(unknown), low |
| `alb-ip-target-mode` low | EKS Best Practices가 IP 모드는 전송료 없음으로 명시. 요금 문구의 "EC2 인스턴스" 공백만 남음 | medium, 비과금. 여전히 `--verify` 대조 권장 |
| MSK 전반 | Serverless는 리전 내 AZ 간 요금이 명시되지 않음 | Provisioned만 수집 |
| 모든 리전 요율 동일? | 상용·GovCloud 36개 리전 모두 측당 $0.01. 중국·Local Zone은 다름 | 36개 리전 내장. Local/Wavelength Zone에 닿는 과금 경로는 금액 생략 + 경고 |

### A-2. 설계·수식 문제

| # | SPEC 원문 | 문제 | 결정 |
|---|---|---|---|
| B1 | §8 `바이트 × CrossAZFraction × 2`, `rate_gb: 0.02` | 0.02는 이미 양쪽 합산. ×2면 4배. §9 예시(4.2TB→$56)는 ×2 없이만 맞음 | `월 비용 = recv/1e9 × CrossAZFraction × rate_gb` |
| B2 | `CrossAZFraction() = 1 - 1/n` (엔드포인트 분포) | 엔드포인트가 한 AZ면 0 → 다른 AZ 호출자 트래픽을 무료로 판정(**미탐**). §9의 `cache/redis 1/3 → NOT BILLABLE`이 이 오류 | `CrossAZFraction(callers, dests) = 1 - Σ_z (1/호출자AZ수)×dest_share(z)`. 호출자 = 노드 AZ 균등 가정 |
| B3 | `same-az: az_spread: single` | "엔드포인트가 한 AZ" ≠ "트래픽이 한 AZ" | `single` = 호출자 AZ ∪ 목적지 AZ가 1개 |
| B4 | PreferClose 권고만 | 이미 PreferClose가 걸린 서비스를 다시 과금 | `zone_local` 사실 추가(힌트 기반, §D) → 비과금 medium |
| B5 | 커버리지 무시 | 엔드포인트 없는 AZ의 호출자는 PreferClose로도 넘어감 | `zone_coverage: partial` → fix `topologySpread` |
| B6 | ALB 뒤 서비스 = 무료 | **instance 타겟 + externalTrafficPolicy: Cluster**면 kube-proxy가 다른 AZ pod로 한 번 더 보냄 → 과금 (EKS BP 명시) | `nodeport-hop-cross-az` 규칙: route alb/clb/nlb-zonal/nlb(cross-zone 모름 포함) + instance + Cluster + zone_local=false. cross-zone을 몰라도 홉은 과금이므로 `nlb-cross-zone-unknown`보다 앞 |
| B7 | §8 `increase(...[30d])` by pod | 롤링 배포로 pod 이름이 바뀌면 매칭 실패, 런타임에 따라 컨테이너별 중복 시계열, 30d는 흔한 보존기간 초과 | 현재 엔드포인트 pod의 수신 속도를 window(기본 7d) 중 **pod가 존재한 5분 스텝만** 평균(`avg_over_time(rate[5m] and offset 5m [window:5m])`) × 30일, `max by (namespace,pod,interface)`로 중복 제거. `rate[window]`는 window 전체로 나눠 window보다 어린 pod·짧은 보존기간을 크게 줄이므로 쓰지 않음(e2e에서 14분 pod가 1/700) |
| B8 | CostPerMo float64 | 0과 "모름" 구분 불가 | `*float64` (nil → `?`) |
| B9 | 수집기에 Ingress 없음 | L0에서 ALB 뒤 서비스 식별 불가 → 핵심 오탐 | `Ingresses`, `IngressClasses` 추가 |
| B10 | ENIs(ctx) 전체 | 큰 VPC에서 무거움 | `IPZones(vpcIDs, ips)` — zone 없는 엔드포인트 IP만, 클러스터 VPC의 ENI만 |
| B11 | Exposure NONE = 단일 AZ | 단일 AZ 클러스터라도 다른 AZ RDS/NLB는 과금 | NONE = 노드 AZ ≤1 && 과금(또는 과금 가능 low)·billable unknown 경로 없음 |
| B12 | Confidence 정의(§5) vs 예시(nlb-xz high) | 추정 볼륨이면 high 불가 | §5 정의: 최종 = min(규칙, 근거, 볼륨) |
| B13 | 매칭 필드가 여러 구조체에 분산 | drift | `Path.Facts` 한 곳 |
| B14 | hostNetwork pod = 노드 전체 트래픽 | 큰 과대추정(오탐) | hostNetwork 엔드포인트는 볼륨 "모름" |
| B15 | 한 pod가 여러 Service | 중복 계산 | 소속 서비스 수로 균등 분할 |
| B16 | trafficDistribution 조건 | kube-proxy < 1.31은 annotation 없는 힌트를 무시. headless·IP 타겟은 kube-proxy를 안 거침 | §D `topology_hints` 조건 |
| B17 | `kafka:ListClusters` | Serverless 구분 불가 | `kafka:ListClustersV2` |

### A-3. v1 단순화 (README 한계에 명시)

- 서비스당 경로 1개: ALB Ingress가 가리키면 `alb`, type=LoadBalancer면 NLB/CLB, 아니면 clusterip/headless. ALB Ingress 백엔드와 type=LoadBalancer(NLB/CLB) 서비스 모두 같은 서비스로 들어오는 클러스터 내부 ClusterIP 트래픽을 분리하지 않는다(수신량에는 포함되고 판정은 LB 경로 기준).
- LB 뒤 단일 AZ 노드 그룹: ALB/CLB/zonal NLB 경로의 `az_spread`는 노드 AZ 기준으로 둔다. ALB/CLB→노드는 무료이고 zonal NLB는 타겟 없는 AZ를 DNS에서 빼므로, LB AZ를 합치면 무료 경로에 `nodeport-hop-cross-az`가 붙는다(오탐). LB 노드가 다른 AZ 타겟으로 넘길 수 있는 `nlb-cross-zone`/`nlb` 경로만 LB AZ를 합친다.
- pod IP로 직접 다이얼하는 Ingress 컨트롤러(ingress-nginx 기본값 등)는 kube-proxy 힌트를 우회한다. 그런 Ingress가 가리키는 zone-local 서비스는 비과금으로 확정하지 않고 `low`(`ingress-direct-backend`). 컨트롤러 설정(topology-aware routing 등)은 읽지 않는다.
- 외부 대상(RDS/ElastiCache/MSK)은 클러스터와 실제 통신하는지·볼륨을 모른다 → 과금 규칙이어도 최종 `low`, 금액 `?`.
- 수신 바이트만(§8). 모든 바이트는 누군가 한 번 수신하므로 총합은 맞지만, 응답 바이트는 호출자 서비스로 귀속된다.
- 공인 IP 헤어핀(pod가 internet-facing LB 호스트명 호출)은 AZ와 무관하게 과금되지만 Flow Logs 없이는 탐지 불가.
- kube-proxy 대체(Cilium, EKS Auto Mode) 환경의 힌트 동작은 미검증 → kube-proxy DaemonSet이 없으면 zone-local 인정 안 함. 힌트를 설정한 서비스(`hints_configured`)는 `hints-unverified`(과금, low)로 확정 과금과 분리. 힌트를 쓰는 kube-proxy(`kube_proxy_hints`)로 확인됐는데 엔드포인트에 힌트가 없으면 kube-proxy가 전체로 분산하므로 `hints-withheld`(과금, high).

---

## B. 데이터 흐름

```
collect (I/O)                         pure
K8sCollector ─┐
AWSCollector ─┼─> model.Snapshot ──> classify.BuildPaths ──> classify.Apply(rules)
Volume/Cost ──┘   (= fixture JSON)                                  │
                                                          estimate.Estimate
                                                                    │
                                                          estimate.Summarize -> model.ScanResult
                                                                    │
                                                          report.Table / JSON / Explain / Rules
```

`internal/scan.Analyze`가 pure 구간 전체를 묶는다. 골든 테스트는 여기서 돈다.

---

## C. 패키지 계약

### C1. `rules` (repo root 패키지) — `rules.YAML []byte` (go:embed)

### C2. `internal/classify`

```go
func Load(data []byte) (model.RuleSet, error)   // sigs.k8s.io/yaml.UnmarshalStrict + Validate
func Default() model.RuleSet                     // Load(rules.YAML); panic on error (테스트로 보장)
func Validate(rs model.RuleSet) error            // id 비어있지 않고 유일, confidence ∈ {high,medium,low},
                                                 // 규칙 ≥1, 마지막 규칙 match가 비어있음(catch-all),
                                                 // default_rate_gb > 0, rate_gb ≥ 0, fix command 템플릿 파싱 가능,
                                                 // StringSet match 값이 사실이 가질 수 있는 값 중 하나(아래)
func RateFor(rs model.RuleSet, region string, override float64) (rate float64, builtin bool)
func BuildPaths(s model.Snapshot) []model.Path   // 사실만 채움 (§D)
func Apply(paths []model.Path, rs model.RuleSet, s model.Snapshot, rateOverride float64) ([]model.Path, error)
func Matches(m model.Match, f model.Facts) bool
```

`Apply` (첫 매치 승리, 새 슬라이스 반환):
- `Billable, RuleID, Reason, SourceURL, RuleConfidence`. `Billable`은 `model.Tri`(`true|false|unknown`): YAML bool 또는 `"unknown"`에서 읽고, JSON에는 문자열로 쓴다. 빠지거나 다른 값이면 검증 오류
- `RateGB` (billable이 false가 아닐 때만. unknown은 과금이라면 쓸 요율을 들고 간다): `override>0` → override; `rule.RateGB>0` → 그것; 아니면 `RateFor(rs, s.Region, 0)`
- `Fix`: `text/template` 렌더, `model.FixVars{Namespace, Name, Ref, Kind, TrafficDistribution}`.
  서비스면 Ref `ns/name`을 분리. `TrafficDistribution` = `s.ServerVersion` minor ≥ 34면 `PreferSameZone`, 아니면(모름 포함) `PreferClose`
- `Confidence = MinLevel(rule.Confidence, evidence)` — evidence `k8s`/`aws` → high, `annotation` → medium
- 매칭 규칙이 없으면 에러 (Validate가 catch-all을 보장하므로 발생하면 버그)

`Matches`: 모든 비어있지 않은 조건 AND. StringSet은 `Has(fact)`, 빈 fact("")는 어떤 값과도 매치 안 함. *bool(`same_vpc`, `private_ip`, `kube_proxy_hints`, `topology_hints`, `zone_local`, `hints_configured`, `direct_ingress_backend`)은 nil이 아니면 일치해야 함. Route는 `string(f.Route)`.

`Validate`의 match 값 검사 (정확히 일치, 대소문자 구분). 다른 값은 `match.<key>: unknown value "<v>" (allowed: ...)` 에러. 키 목록은 `model.Match`의 json 태그를 reflect로 읽으므로, 허용 목록이 없는 StringSet 필드를 추가하면 모든 값이 거부되어 바로 드러난다.

| 키 | 허용 값 |
|---|---|
| `route` | clusterip, headless, alb, clb, nlb-cross-zone, nlb-zonal, nlb, external |
| `target_kind` | service, rds, elasticache, msk |
| `scheme` | internal, internet-facing |
| `target_type` | ip, instance |
| `external_traffic_policy` | Cluster, Local |
| `traffic` | client, replication |
| `az_spread` | single, multi, unknown |
| `zone_coverage` | full, partial, unknown |
| `evidence` | k8s, aws, annotation |

### C3. `internal/estimate`

```go
const MonthSeconds = 30 * 24 * 3600
func Estimate(paths []model.Path, s model.Snapshot) []model.Path
func Summarize(s model.Snapshot, paths []model.Path, threshold float64) model.ScanResult
func Verify(estimatedUSD *float64, bill model.Bill) model.VerifyResult
```

`Estimate` (경로마다, 새 슬라이스):
- `CrossAZFrac`:
  - route `nlb-cross-zone`, `nlb`, `headless`, `external` → 항상 `model.CrossAZFraction(Source.AZDist, Dest.AZDist)`. 힌트는 LB 노드→타겟 다리를 없애지 못한다
  - route `clusterip`, `alb`, `clb`, `nlb-zonal`(AZ를 넘는 다리가 kube-proxy 홉뿐인 경로)만:
    - `Facts.ZoneLocal` → 0
    - `Facts.TopologyHints` → `model.HintedCrossAZFraction(Source.AZDist, Dest.AZDist, Dest.HintedFor)` — zone z 호출자는 z로 힌트된 엔드포인트만, 없으면 전체로 폴백
    - 그 외 → `model.CrossAZFraction(Source.AZDist, Dest.AZDist)`
- 비과금 → 금액 nil, confidence 그대로. 단 L2가 있고 서비스 경로면 `BytesPerMo`는 채운다("ALB로 6TB가 지나가지만 무료"를 보여주기 위해)
- 과금 + `Dest.Kind != service` → confidence **low**, note "클러스터 외부 대상: 통신 여부와 볼륨을 알 수 없음"
- 과금 + `Dest.AZDist.Total() == 0` → 금액 nil, confidence **low**, note "엔드포인트 AZ를 알 수 없어 cross-AZ 비율 계산 불가". L2 유무와 무관(L0에서도 같은 결과)
- 과금 + `s.Volume == nil` → 금액 nil, confidence 그대로, note "L2 없음: --prometheus-url로 볼륨 추정"
- 과금 + L2:
  - 서비스(`Dest.Ref` = ns/name)의 ready 엔드포인트 중 PodName 있는 것 → key `ns/PodName`
  - pod 볼륨 ÷ (그 pod를 ready 엔드포인트로 가진 서비스 수)
  - hostNetwork pod(`s.Pods`)나 `RecvBytesPerS`에 없는 pod는 미측정
  - 측정 0 → confidence low, 금액 nil, note "엔드포인트 pod 메트릭 없음"
  - 일부 측정 → `합 × 전체/측정`, note "k/n pods measured"
  - `BytesPerMo = recv/s × MonthSeconds`
  - 이미 low(규칙 low 등) → 금액 nil, note "판정 신뢰도 low: 금액 생략"
  - 아니면 `CostPerMo = BytesPerMo/1e9 × CrossAZFrac × RateGB`, confidence = min(현재, medium)

`Summarize`:
- Cluster/Region from snapshot, Zones = 노드 zone 정렬(빈 값 제외, 중복 제거), NodeCount, Layers(`L0` 항상, `L1` AWS≠nil, `L2` Volume≠nil, `L3` Bill≠nil && !Bill.Unknown)
- `Uncertain` = confidence low 또는 billable unknown. `Excluded` = billable false (low 아님). 나머지(billable): 금액 알려짐 && `< threshold` → Hidden++, 아니면 `Paths`
- `UnconfirmedMaxUSD`: Volume≠nil이고 billable unknown 경로가 있으면 Σ `BytesPerMo/1e9 × RateGB`(cross-AZ 비율 1로 본 상한). 그중 하나라도 `BytesPerMo == nil`이면 nil. TotalUSD에는 넣지 않는다
- 정렬: Paths 금액 내림차순(nil 뒤) → Key; Excluded/Uncertain은 Key
- `TotalUSD`: Volume≠nil이면 billable·non-low 경로의 알려진 금액 합(숨김 포함). 아니면 nil
- "추정 강등 과금 경로" = Uncertain 중 billable true && `RuleConfidence != low` && `Dest.Kind == service` (규칙은 확실한데 추정 단계에서 low가 됨: 메트릭 없음, 목적지 AZ 모름, Local Zone)
- Exposure: Paths나 Hidden이 있거나 추정 강등 과금 경로가 있으면 `likely`; 아니면 Zones ≤1이고 Uncertain 중 billable true 없음(unknown은 세지 않음) → `none`; 그 외 `unlikely`
- Warnings = s.Warnings 복사. Volume≠nil이고 `BytesPerMo == nil`인 추정 강등 과금 경로가 N>0개면 뒤에 "추정 총액에 볼륨을 측정하지 못한 과금 경로 N개가 빠져 있음"(TotalUSD는 알려진 합 그대로)
- Verify = Bill≠nil이면 `Verify(TotalUSD, *s.Bill)`

`Verify`: actual = 창 안 모든 날의 family `interzone` 라인 USD 합 ÷ 날 수(`Days`) × 30(`MonthSeconds`/일). CE는 열린 달의 모든 날을 `Estimated`로 표시하므로 그 날도 합하고, 개수를 `EstimatedDays`에 담아 VERIFY 줄에 `미확정 N일 포함(잠정)`으로 적는다. PublicIP·LB-PublicIP·peering·xAZ·other는 넣지 않는다. ratio = est/actual(둘 다 >0). note 우선순위: Bill.Unknown → "Cost Explorer 청구 모름 — 비교 불가"(ActualUSD nil); est nil → "L2(--prometheus-url) 없이는 비교 불가"; actual==0 → "해당 기간 InterZone 실제 비용 0"; ratio>1.5 → "추정 과대: 제외 규칙 누락 의심"; ratio<0.5 → "클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심"; 그 외 "제외 규칙이 대체로 맞음". Actual은 계정·리전 전체(ELB·공인 IP·다른 서비스 포함) 상한임을 note 뒤에 붙이지 않고 README에 적는다.

### C4. `internal/collect`

인터페이스는 `collect.go`. `AWSCollector.IPZones(ctx, vpcIDs, ips []string)`는 클러스터 VPC를 받는다(VPC가 없으면 호출 없이 빈 맵). 구현:

```go
func NewK8s(cs kubernetes.Interface) K8sCollector
func NewAWS(c AWSClients) AWSCollector            // AWSClients = readonly 인터페이스 묶음 (readonly.go)
func NewAWSFromConfig(cfg aws.Config) AWSCollector
func NewPrometheus(baseURL string, matchers []PromMatcher, hc *http.Client) VolumeCollector
func ParsePromSelector(s string) ([]PromMatcher, error) // --prom-selector 파서, 빈 입력 = nil
func NewCostExplorer(c CostExplorerReadOnly) BillCollector
func NewCostExplorerFromConfig(cfg aws.Config) BillCollector   // 리전을 us-east-1로 강제한 복사본

func GatherK8s(ctx context.Context, k K8sCollector, cluster string) (model.Snapshot, error)
func WithAWS(ctx context.Context, s model.Snapshot, a AWSCollector) model.Snapshot
func WithVolume(ctx context.Context, s model.Snapshot, v VolumeCollector, window, selector string) model.Snapshot // selector는 no-match 경고에만 씀
func WithBill(ctx context.Context, s model.Snapshot, c BillCollector, from, to time.Time) model.Snapshot // accounts = s.AWS.Accounts; bill 경고는 "L3: " 붙여 snapshot 경고로 옮김
```

- `GatherK8s`: Nodes/Services/EndpointSlices/Pods/Ingresses/IngressClasses 실패 = 치명 에러. ServerVersion/KubeProxyVersion 실패 = 경고.
  EndpointSlice를 `(namespace, serviceName)`으로 서비스에 합침(IP 기준 중복 제거, ready 우선). Region = 노드 region 라벨 최빈값.
  `Ingress.Services` = 규칙·defaultBackend의 서비스 이름. 포트 이름이 `use-annotation`인 백엔드(AWS Load Balancer Controller 액션)는 서비스가 아니라 액션 이름이므로 `alb.ingress.kubernetes.io/actions.<name>` JSON의 `forwardConfig.targetGroups[].serviceName`과 레거시 최상위 `serviceName`(키 대소문자 무시)으로 바꾼다. 액션 이름 자체는 일반 백엔드가 같은 이름을 쓸 때만 남는다. annotation이 없거나 깨졌거나 fixed-response·ARN 전용 forward면 아무것도 추가하지 않는다. 가중치·blue/green·Argo Rollouts canary 구성이 여기에 해당. 정렬·중복 제거.
- `WithAWS`(새 Snapshot 반환, 원본 불변): 노드 InstanceID → `InstanceVPCs` → `Subnets(vpcs)` → `LoadBalancers(vpcs)`; `RDSInstances`, `ElastiCacheClusters`, `MSKClusters(subnets)` 결과는 `VPCID ∈ vpcs`만 남김. zone 없는 ready 엔드포인트 IP → `IPZones(vpcs, ips)`. 호출별 실패는 `"L1: <what>: <err>"` 경고로 남기고 계속. VPC를 못 구하면 경고 + AWS=nil.
  - `LoadBalancers`는 부분 결과를 돌려준다: 속성·타겟그룹 조회에 실패한 ELBv2 LB는 빼고(그 경로는 annotation 근거로 폴백), v2 목록과 Classic 목록은 서로 독립적으로 실패한다. 실패마다 경고 한 줄 `L1: 로드밸런서: load balancer <name>: <api>: <err>`(errors.Join을 풀어서, 경고에 개행 없음). 전부 실패하면 빈 목록.
- `WithVolume`: 쿼리 성공 후, PodName이 있는 ready 엔드포인트가 하나 이상인데 결과에 그 `ns/pod` 키가 **하나도** 없으면(빈 결과, 다른 클러스터, cAdvisor 미수집, 수집 10분 미만이라 5분 스텝이 없음) Volume=nil + 경고 `L2: Prometheus 결과에 이 클러스터의 엔드포인트 pod가 없음 — 다른 클러스터, cAdvisor 메트릭 누락, 또는 수집한 지 10분 미만`. 결과는 L2 없는 출력(Layers에 L2 없음, 총액 `?`)으로 폴백. 일부만 겹치면 L2로 인정. 입력의 이전 Volume은 먼저 지운다.
- read-only 강제: `readonly.go`에 SDK 클라이언트별 인터페이스(메서드는 Describe*/List*/Get*만). `aws.go`·`ce.go`는 이 인터페이스 타입만 참조. `readonly_test.go`가 reflect로 모든 인터페이스 메서드 이름 접두어를 검사하고, 실제 SDK 클라이언트 타입이 인터페이스를 만족하는지 컴파일 타임 단언.
- API 사용 (IAM과 1:1):
  - `ec2:DescribeInstances`(InstanceIds) → VpcId, Reservation OwnerId(계정)
  - `ec2:DescribeSubnets`(filter vpc-id) → AZ, VpcId
  - `ec2:DescribeNetworkInterfaces`(filter `vpc-id` = 클러스터 VPC **AND** `addresses.private-ip-address`, 200개씩) → AZ. ENI들이 서로 다른 AZ를 보고하는 IP는 해석하지 않음(zone 모름), 클러스터 VPC 밖 IP도 해석하지 않음
  - `elasticloadbalancing:DescribeLoadBalancers`(v2, 페이지) + `DescribeLoadBalancerAttributes`(network/gateway만) + `DescribeTargetGroups`(LB별) + `DescribeTargetGroupAttributes`(network TG만); Classic `DescribeLoadBalancers`(VPCId, AvailabilityZones, Scheme, DNSName). v2 LB는 VpcId ∈ vpcs만
  - `rds:DescribeDBInstances`(페이지) → AZ, SecondaryAZ, MultiAZ, DBClusterIdentifier, DBSubnetGroup.VpcId
  - `elasticache:DescribeCacheClusters`(ShowCacheNodeInfo=true, 페이지) → CacheNodes[].CustomerAvailabilityZone, ReplicationGroupId; `elasticache:DescribeCacheSubnetGroups` → VpcId
  - `kafka:ListClustersV2`(ClusterTypeFilter=PROVISIONED) → BrokerNodeGroupInfo.ClientSubnets, NumberOfBrokerNodes → 서브넷 AZ에 균등 분배
- Prometheus: `GET <base>/api/v1/query?query=` 아래 쿼리, `status != "success"`면 에러, 결과 vector → `"ns/pod"` → float. 기본으로는 cluster 라벨로 범위를 좁히지 않는다. `--prom-selector`(예: `cluster="prod-a",job=~"kubelet|cadvisor"`, 중괄호 있어도 됨)를 주면 그 매처를 **모든 쿼리의 모든 vector selector**에 `pod!=""` 뒤로 덧붙인다(아래 SEL).
  `--prom-selector`는 플래그 검증 단계에서 stdlib 파서(`collect.ParsePromSelector`)로 엄격히 파싱한다: 라벨 이름 `[a-zA-Z_][a-zA-Z0-9_]*`, 연산자 `=` `!=` `=~` `!~`, 값은 큰따옴표 Go 문자열 리터럴(`strconv.Unquote`), 정규식 값은 Prometheus처럼 `^(?:re)$`로 감싸 RE2로 컴파일돼야 한다. 쉼표 누락, 뒤따르는 문자, 닫히지 않은 문자열·중괄호는 `--prom-selector: offset N: want ..., got "..."`로 거부하고 종료 코드 2(HTTP 요청 없음). 빈 값은 no-op. 쿼리에는 파싱한 매처를 `,name op strconv.Quote(value)`로 다시 렌더링해 넣으므로 입력이 selector 밖으로 나갈 수 없다. selector를 준 채 결과가 엔드포인트 pod와 하나도 안 맞으면 아래 no-match 경고 뒤에 ` (--prom-selector "<입력>" 적용됨)`을 붙인다.
  base URL의 basic-auth 자격증명은 퍼센트 인코딩해야 한다. `url.Parse` 실패는 고정 메시지 `prometheus: base URL is invalid (percent-encode credentials)`, 스킴 오류 메시지와 `http.NewRequest`/`hc.Do`의 `*url.Error`는 userinfo를 지운 URL(파싱 불가면 `<redacted>`)로 다시 만든다(`errors.Is` 체인 유지). 사용자 이름·비밀번호가 경고·`--json`·`--dump-fixture`에 남지 않는다.
  ```promql
  sum by (namespace, pod) (avg_over_time((max by (namespace, pod, interface) (rate(container_network_receive_bytes_total{pod!=""SEL}[5m]) and container_network_receive_bytes_total{pod!=""SEL} offset 5m))[WINDOW:5m]))
  ```
  SEL은 `--prom-selector`가 없으면 빈 문자열, 있으면 `,cluster="prod-a",job=~"kubelet|cadvisor"` 꼴.
  WINDOW는 `^[0-9]+(ms|s|m|h|d|w|y)$`이고 5m 이상이어야 한다(아니면 요청 없이 에러 `prometheus: invalid window` / `window "<w>" is shorter than the 5m rate step`). 5분 전에도 샘플이 있던 스텝만 평균하므로 5~10분 안 된 pod는 결과에 없다(미측정 → 외삽).
- CE (`BillCollector.DailyBill(ctx, region, accounts, from, to) (model.Bill, error)`): `GetCostAndUsage`, `TimePeriod` = [from, to) 닫힌 UTC 일(End 배타), DAILY, Metrics `UnblendedCost`+`UsageQuantity`(GB), GroupBy `USAGE_TYPE`+`OPERATION`(CE 최대 2개), Filter REGION, 계정을 알면 `And[REGION, LINKED_ACCOUNT]`(모르면 REGION만 + 경고). NextPageToken을 비거나 같은 토큰이 나올 때까지 순회(페이지당 $0.01).
  - 범위(접미사, 접두어 표 하드코딩 없음): `DataTransfer-Regional-Bytes`로 끝나는 usage type은 operation으로 family를 정한다(`InterZone-In/Out` interzone, `PublicIP-In/Out` publicip, `LB-PublicIP-In/Out`·`LoadBalancing-PublicIP-In/Out`(둘 다 미확인) lb-publicip, 그 외 other + operation마다 경고 한 줄). `VpcPeering-In/Out-Bytes`는 vpcpeering(2025-04-11부터 별도 usage type), `NatGateway-Bytes`는 natgw-bytes(NAT 처리 요금, AZ 간 아님: WATERFALL TOTAL엔 넣고 verify엔 안 넣음), `DataTransfer-xAZ-In/Out-Bytes`는 xaz-free($0, 합계 제외). 나머지는 버린다. 방향(`BillLine.Side()`)은 operation의 `-In/-Out`, 없으면 usage type의 `-In/-Out-Bytes`.
  - `Estimated` = `ResultByTime.Estimated`. 날 단위 표시이므로 범위 안 라인이 없는 날도 `Bill.NotFinal`에 넣는다(`NotFinalDays()` = NotFinal ∪ Estimated 라인의 날). API 오류 코드 `AccessDenied`/`AccessDeniedException`/`DataUnavailableException`과 그룹이 하나도 없는 결과는 `Bill{Unknown: true}` + 경고, nil 에러("모름", $0 아님). 그 외 오류는 에러.
  - `BillLine.Account`는 계정 필터가 정확히 하나일 때만 채운다(LINKED_ACCOUNT로는 더 묶을 수 없음). 계정은 L1 `ec2:DescribeInstances`의 Reservation `OwnerId`(`AWSInventory.Accounts`).

### C5. `internal/report`

```go
func Table(w io.Writer, r model.ScanResult) error
func JSON(w io.Writer, r model.ScanResult) error                       // 2-space indent + 개행
func Explain(w io.Writer, key string, paths []model.Path, rs model.RuleSet) error // Key()가 같은 경로 전부, 없으면 에러
func Rules(w io.Writer, rs model.RuleSet) error    // MATCH 열은 model.Match 필드를 reflect로 나열
func Bytes(b float64) string        // 십진: "4.2 TB", "900 GB", "12 MB", "0 B"
func USD(v *float64, billable bool) string // "~$56", "~$0.40"(<$10은 소수 둘째), "?"(billable·unknown이고 nil), "—"(비과금)
```

`Table` 형식은 SPEC §9를 따른다. 섹션 `BILLABLE`/`NOT BILLABLE`/`UNCERTAIN`, 비어있는 섹션은 생략.
열: `SERVICE  ROUTE  RECV/mo  AZ SPREAD  EST $/mo  CONF  FIX|NOTE`.
- SERVICE = `Key()`, ROUTE = route (alb에 target_type=ip면 `alb (ip)`, `nlb-cross-zone`은 `nlb-xz`), RECV/mo = Bytes 또는 `?`/`—`
- AZ SPREAD = `목적지zone수/호출자zone수` + (목적지 zone ≥2 && IsEven이면 ` even`)
- BILLABLE의 마지막 열 = Fix.Kind(없으면 `—`), 나머지 섹션 = RuleID
- Hidden>0이면 BILLABLE 표 아래 `(N paths below $T/mo hidden — use --threshold 0)`
- `Exposure: NONE`이면 2줄만: `Cluster: X   AZs: 1   Nodes: N` / `Exposure: NONE — 모든 노드가 단일 AZ에 있고 과금 경로가 없습니다.`
- 헤더 `Cluster: X   Region: R   AZs: n   Nodes: n   Layers: L0+L1`, `Exposure: LIKELY`
- 푸터 `Estimated total: ~$133/mo` 또는 `Estimated total: ? (L2 없음 — --prometheus-url)`, billable unknown 경로가 있으면 총액 바로 아래 `미확정: $0–$3.00/mo (billable 미확정 경로 N개, 총액 제외)`(상한을 모르면 `$0–?`), `Run \`crossaz explain <첫 BILLABLE 키>\` for rule details.`(BILLABLE 있을 때), VERIFY 블록(SPEC §9), `WARNINGS` 목록
- `Explain` facts 줄: `az_spread zone_coverage kube_proxy_hints topology_hints hints_configured zone_local direct_ingress_backend scheme target_type external_traffic_policy same_vpc private_ip traffic` (빈 문자열 사실은 생략, bool은 항상)
- 터미널 제어 문자 차단: Table·Explain·Rules는 클러스터·AWS·규칙 파일에서 온 문자열(표 셀, 헤더, 클러스터 이름, 경고, VERIFY 값, explain 값·fix 명령 줄, SOURCES id·URL)의 `unicode.IsControl` 문자(ESC, BEL, CR, LF, TAB, C1 포함)를 `?`로 바꾼다. JSON은 `encoding/json` 이스케이프 그대로

### C6. `internal/scan`

```go
type Options struct{ Threshold, RateGB float64 }
func Analyze(s model.Snapshot, rs model.RuleSet, o Options) (model.ScanResult, []model.Path, error) // result, 전체 경로(explain용)
func Anonymize(s model.Snapshot) model.Snapshot
```

- `Analyze`: BuildPaths → Apply → Estimate → Local Zone 금액 생략 → Summarize. 추가 경고: 리전 요율 미내장(`RateFor` builtin=false && RateGB==0), Local/Wavelength Zone 노드(zone이 `<region><문자>` 형식이 아님) → `Local/Wavelength Zone 노드 발견(<zones>): 요율이 달라 해당 경로 금액 생략`.
  - Local Zone 금액 생략: billable 또는 unknown이고 아직 low가 아닌 경로 중 Source 또는 Dest AZDist에 그 zone이 있으면 `CostPerMo=nil`, confidence low, note `Local/Wavelength Zone 포함: 요율이 달라 금액 생략`, `BytesPerMo`는 유지 → UNCERTAIN, 총액 제외. unknown 경로는 이 note 때문에 미확정 상한이 `?`가 된다(부모 리전 요율로 상한을 낮게 잡지 않음). 새 슬라이스를 만든다. 비과금·이미 low인 경로는 그대로.
- `Anonymize`: sha256 기반 결정적 치환(같은 입력 → 같은 출력, 조인 보존) — IP(사설 IPv4는 `10.x.y.z`, 그 외 IPv4는 198.18.0.0/15, IPv6는 fd00::/8), 노드 이름(EKS 노드 이름에 IP 포함), InstanceID, VPC/서브넷 ID, LB·타겟그룹 이름·DNS·Service/Ingress LBHostnames, IPZones 키.
  - mirror pod(`OwnerKind == Node`, 이름이 `<pod>-<NodeName>`): 이름 끝의 노드 이름과 `OwnerName`을 노드 이름 치환값으로 바꾸고, 같은 이름을 쓰는 엔드포인트 `PodName`·Volume 키(`ns/pod`)도 함께 바꾼다(kubelet이 static pod를 노드 이름으로 명명하므로).
  - annotation은 classify가 읽는 키만 남긴다. `aws-load-balancer-attributes`와 `aws-load-balancer-target-group-attributes`는 `load_balancing.cross_zone.enabled` 쌍만 남긴다. `nginx.ingress.kubernetes.io/service-upstream`, `trafficDistribution` 필드도 유지(판정 입력).
  - 경고: 치환한 값을 먼저 문자열 치환한 뒤, `": "` 구분 조각이 3개 이상이면 `<조각1>: <조각2>: <redacted>`로 자른다(AWS/Kubernetes/HTTP 에러 원문의 계정 ID, principal ARN, 이메일, 홈 경로, LB 이름 제거). 조각 2개 이하는 그대로.
  - 그대로 남는 것: 클러스터 이름, 네임스페이스·서비스·pod·Ingress·owner 이름(mirror pod 제외), RDS/ElastiCache/MSK 식별자, IngressClass, Bill 라인(금액·usage type·operation; 계정은 치환), Volume 키(`ns/pod`, mirror pod 제외).

### C7. CLI (`cmd/crossaz`)

```
crossaz scan    [flags]
crossaz explain <ns/name | kind/ref> [flags]
crossaz bill    [--days N] [flags]
crossaz rules   [--rules file]
crossaz version
```

플래그: `--kubeconfig`, `--context`, `--cluster`, `--region`, `--rules`, `--rate-gb`, `--prometheus-url`, `--prom-selector`, `--prom-window`(7d), `--no-aws`, `--fixture <file>`, `--timeout`(2m)는 scan·explain 공통. `--threshold`(1), `--json`, `--dump-fixture`, `--verify`는 **scan 전용**(explain에 주면 `flag provided but not defined`, 종료 코드 2). 표준 `flag`만.
흐름: fixture 있으면 파일 로드(v1 fixture의 `cost` 키는 읽고 버리며 경고 `v1 fixture의 월별 cost는 무시됨 — --verify로 다시 수집`; `--prometheus-url`/`--verify`를 함께 주면 무시하고 경고 `--fixture 사용 중: --prometheus-url/--verify는 무시됨 (fixture에 포함된 레이어만 사용)` — `--dump-fixture`면 이 경고도 덤프에 들어감); 없으면 GatherK8s → (no-aws 아니면) AWS 설정 로드·`Credentials.Retrieve` 성공 시 WithAWS, 실패 시 경고 → prometheus-url 있으면 WithVolume → verify면 WithBill(최근 14개 닫힌 UTC 일). WithBill은 L1이 AWS 인벤토리를 만든 경우(`snapshot.AWS != nil`)에만 부른다. 자격증명 없음, 노드 instance ID 없음, 노드 VPC 조회 실패 또는 0개(예: 다른 계정 자격증명)면 Cost Explorer를 호출하지 않고 경고 `L3: L1(AWS 인벤토리)을 얻지 못해 --verify 건너뜀`. `--dump-fixture`면 Anonymize 후 JSON 출력하고 종료.
explain: 수집·분석 경고(`ScanResult.Warnings`)를 한 줄씩 `crossaz: warning: <text>`로 stderr에 먼저 쓰고 설명을 stdout에 쓴다. 키가 없어 종료 코드 1(`no path for <key>`)일 때도 경고는 나온다.
bill: `--kubeconfig`, `--context`, `--region`, `--timeout`, `--json`, `--days`(14, 1–90 밖이면 종료 코드 2). GatherK8s → AWS 로드·WithAWS(계정) → `DailyBill(region, AWSInventory.Accounts, today-N .. today 00:00 UTC)`. 자격증명이 없으면 종료 코드 1. L1이 노드 VPC를 하나도 찾지 못하면(다른 계정/리전 자격증명) 조회 없이 종료 코드 1, 그 외 L1이 없으면(instance ID 없음, 조회 실패) REGION만 + 경고. 출력은 `report.BillTable`(WATERFALL: family×side의 USD·GB·$/GB, xaz-free 제외 TOTAL, xaz-free 줄, 미확정 날, WARNINGS; Unknown이면 금액 없이 "청구: 모름") 또는 `report.BillJSON`(`model.BillReport` = region·accounts·metric + Bill).
리전: `--region` > snapshot.Region(노드 라벨) > AWS 설정. 클러스터 이름: `--cluster` > kubeconfig 컨텍스트의 cluster(EKS ARN이면 `/` 뒤).

---

## D. BuildPaths 알고리즘

공통
- `callers` = 노드 zone별 개수(빈 zone 제외)
- 엔드포인트 zone = `ep.Zone` → `AWS.IPZones[ep.IP]` → unknown. **ready 엔드포인트만** 사용
- `az_spread`: 알려진 목적지 zone이 없고 unknown 엔드포인트가 있으면 호출자와 무관하게 `unknown`; 아니면 `spreadCallers ∪ destZones` ≥2 → `multi`; 아니면 unknown 있으면 `unknown`; 아니면 `single`
  - `spreadCallers`: route `nlb-cross-zone`/`nlb`는 노드 zone ∪ `Source.AZDist`(L1이면 LB zone), 그 외 모든 route는 노드 zone(§A-3)
- `zone_coverage`: `destZones ⊇ Source.AZDist zones` → `full`; 아니면 unknown 있으면 `unknown`; 아니면 `partial`. Source.AZDist는 clusterip/headless·LB 미매칭이면 노드 zone, L1 보정 뒤에는 LB zone. `zone_local`과 힌트 판정은 노드 callers 그대로
- `private_ip`: 모든 ready 엔드포인트 IP가 IPv4 사설 대역(10/8, 172.16/12, 192.168/16, 100.64/10)
- `kube_proxy_hints` = 아래 1 && 2 (엔드포인트 힌트와 무관: `hints-unverified`/`hints-withheld` 구분용)
- `topology_hints` (전부 만족):
  1. kube-proxy가 경로에 있음: route `clusterip`, 또는 LB 라우트(alb/clb/nlb-*)이면서 `target_type=instance` && `external_traffic_policy != Local`
  2. kube-proxy가 힌트를 씀: `KubeProxyVersion` minor ≥ 31, 또는 (minor ≥ 24 && 서비스 annotation `service.kubernetes.io/topology-mode` 또는 `service.kubernetes.io/topology-aware-hints`가 비어있지 않고 `disabled`가 아님). 버전 모름 → false
  3. ready 엔드포인트 ≥1개, **모두** `ForZones` 비어있지 않음
- `Dest.HintedFor` (topology_hints일 때만): 호출자 zone → 그 zone으로 힌트된 ready 엔드포인트의 zone 분포. `topology-mode: Auto`는 균형을 위해 **다른 zone 엔드포인트를 힌트**할 수 있다
- `zone_local`: (`topology_hints` && unknown 엔드포인트 없음 && `model.HintedCrossAZFraction(callers, dests, HintedFor) == 0` — 모든 호출자 zone이 같은 zone 엔드포인트로만 서비스됨) 또는 (route `clusterip` && `InternalTrafficPolicy == Local`)
- `hints_configured` (서비스만): `spec.trafficDistribution != ""` 또는 `topology-mode`/`topology-aware-hints` annotation이 비어있지 않고 `disabled`가 아님. kube-proxy가 실제로 쓰는지와 무관(`hints-unverified`·`hints-withheld` 규칙용)
- `direct_ingress_backend` (서비스만): 같은 네임스페이스의 **ALB가 아닌** Ingress가 이 서비스를 가리키고 그 Ingress에 `nginx.ingress.kubernetes.io/service-upstream: "true"`가 없음. 그런 컨트롤러는 EndpointSlice pod IP로 직접 연결해 kube-proxy 힌트를 우회한다(billing-rules §6.4 조건 6, §6.7)

### 서비스 (ExternalName, ready 엔드포인트 0개 제외)

라우트 결정:
1. **ALB**: 같은 네임스페이스 Ingress 중 `Services`에 이 서비스가 있고 ALB인 것(여럿이면 이름순 첫째).
   ALB 컨트롤러 = {`ingress.k8s.aws/alb`(셀프 관리 LBC), `eks.amazonaws.com/alb`(EKS Auto Mode)}. ALB 판정:
   - `ClassName` 있음: 그 이름의 IngressClass 객체가 있으면 controller ∈ ALB 컨트롤러. 객체가 없을 때만 ClassName == `alb`면 ALB(LBC 관례). 그래서 nginx가 소유한 `alb`라는 이름의 클래스는 ALB가 아니다
   - `ClassName` 없고 annotation `kubernetes.io/ingress.class` 있음: 값 == `alb`면 ALB(LBC만 이 annotation을 읽음)
   - 둘 다 없음: controller ∈ ALB 컨트롤러인 default IngressClass가 있으면 ALB
   `target_type` = 서비스 annotation `alb.ingress.kubernetes.io/target-type` > Ingress annotation > (controller `eks.amazonaws.com/alb`면 `ip`, 아니면 `instance`). `scheme` = Ingress annotation `alb.ingress.kubernetes.io/scheme` > `internal`.
2. **type=LoadBalancer**:
   - NLB: `LoadBalancerClass` ∈ {`service.k8s.aws/nlb`, `eks.amazonaws.com/nlb`} || annotation `service.beta.kubernetes.io/aws-load-balancer-type` ∈ {`nlb`, `nlb-ip`, `external`}
   - CLB: LoadBalancerClass 비었고 NLB 아님. 그 외 LoadBalancerClass → 4번(clusterip)
   - target_type: NLB는 annotation `service.beta.kubernetes.io/aws-load-balancer-nlb-target-type` > (`aws-load-balancer-type: nlb-ip` 또는 LoadBalancerClass `eks.amazonaws.com/nlb`(Auto Mode 기본)면 `ip`) > `instance`. CLB는 `instance`
   - NLB cross-zone (annotation), 앞에서부터 첫 `true|false`: `service.beta.kubernetes.io/aws-load-balancer-target-group-attributes`의 `load_balancing.cross_zone.enabled` → `service.beta.kubernetes.io/aws-load-balancer-attributes`의 같은 키 → `service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled`. **모두 없으면 unknown** → route `nlb`. 단 in-tree(`aws-load-balancer-type: nlb` && LoadBalancerClass 비었음)는 컨트롤러가 annotation 없으면 false로 설정 → `nlb-zonal`(TG 속성 annotation이 있으면 그 값이 이 기본값보다 우선)
   - scheme: `service.beta.kubernetes.io/aws-load-balancer-scheme` 값(검증하지 않음: 모르는 값이면 scheme 규칙이 매치되지 않음); 없으면 LBC 관리(class 지정 또는 type `external`/`nlb-ip`)는 `internal`; in-tree(nlb/CLB)는 `service.beta.kubernetes.io/aws-load-balancer-internal`이 비어있지 않고 `false`가 아니면(레거시 `0.0.0.0/0` 포함) `internal`, 아니면 `internet-facing`
   - `external_traffic_policy` = 서비스 값, 비었으면 `Cluster`
3. `ClusterIP == "None"` → headless
4. 그 외 → clusterip (NodePort 포함)

L1 보정 (LB 라우트): `AWS.LoadBalancers` 중 `DNSName`이 Ingress/Service `LBHostnames`에 있는 LB(대소문자 무시)
- `scheme = lb.Scheme`, `same_vpc = lb.VPCID ∈ AWS.VPCIDs`, `evidence = aws`, Source.AZDist = LB zones(각 1), Source.Name = lb.Name
- NLB 유효 cross-zone: TG 중 `CrossZone=="true"`가 하나라도 있으면 true; 모든 TG가 `"false"`면 false; 그 외 `lb.CrossZone`(nil이면 annotation 값, 그것도 없으면 unknown) → route `nlb-cross-zone`/`nlb-zonal`/`nlb`
- target_type: TG들의 TargetType(instance/ip)이 모두 같으면 그 값
- LB를 못 찾으면 evidence `annotation`, Source.AZDist = callers, Source.Name = `ns/ingress` 또는 `ns/svc`

Facts 기본: `route`, `target_kind=service`, `traffic=client`, `same_vpc=true`, `private_ip`(위 계산), `hints_configured`, `direct_ingress_backend`, evidence는 clusterip/headless → `k8s`.
Source: clusterip/headless → `{Name:"*", Kind:"cluster", AZDist: callers}`; LB → `{Kind:"alb"|"nlb"|"clb", Namespace, Name}`.
Dest: `{Kind:"service", Ref:"ns/name", AZDist: 알려진 zone 분포}`.

### 외부 대상 (AWS≠nil, `VPCID ∈ AWS.VPCIDs`만)

공통: Source = `{Name:"*", Kind:"cluster", AZDist: callers}`, `route=external`, `evidence=aws`, `same_vpc=true`, `private_ip=true`, `hints_configured=false`, `direct_ingress_backend=false`, az_spread/zone_coverage는 위 공식(호출자 = 노드 zone).
- RDS: `ClusterID` 있으면 클러스터 단위(Ref=ClusterID, 멤버 AZ 분포), 없으면 인스턴스 단위(Ref=ID, `{AZ:1}`) → `traffic=client`.
  복제 경로: 클러스터(멤버 ≥2) 또는 `MultiAZ` 인스턴스 → `traffic=replication`, Source = `{Kind:"rds", Name:Ref, AZDist: 멤버 AZ 또는 {AZ,SecondaryAZ}}`, Dest AZDist 동일
- ElastiCache: `GroupID` 있으면 그룹 단위(노드 zone 합산), 없으면 클러스터 단위 → client
- MSK: broker zone 분포 → client, 그리고 replication(Source=Dest=broker 분포)

정렬: `Key()` → `Facts.Traffic`.
