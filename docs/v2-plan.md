# crossaz v2 최종 계획 (옵션 C, 2026-09-15)

출발점은 점수가 가장 높은 **bill-first** 초안입니다(두 심사 합계 37.5점, consume-and-diagnose 36점, flow-first 32.5점). 여기에 심사위원이 뽑은 좋은 아이디어를 붙였습니다. 초안끼리 부딪히는 부분은 3.1절에서 하나씩 결정했습니다. 사용자가 고른 옵션 C(A 먼저, B는 조건부)와 팀별 비용 배분 요구는 §1과 §9에 반영했습니다.

출처 표기 원칙
- 기본은 선행 조사에서 조회한 자료입니다.
- 이번 종합에서 새로 조회한 것은 Istio ambient traffic-distribution 문서 하나입니다.
- 팀 배분과 벤더 비교는 docs/team-allocation-landscape.md(2026-09-15 조회)를 따릅니다.
- 조회하지 못한 자료는 **[미조회]**로 표시했습니다. 출처는 조회했지만 실제 테넌트에서 확인해야 하는 연동 동작은 **[미검증]**으로 표시했습니다.

---

## 1. 한 줄 방향

**옵션 C.** A를 먼저 냅니다. A는 설치 없는 읽기 전용 CLI로, 청구서(CE/CUR)를 분모로 삼아 워터폴, 규칙 진단, 수정 명령, 노드-일/AZ-일 청구 대조 아래의 모델 가중치 배분, 팀별 롤업(owner)을 냅니다. B는 조건부입니다. 청구 잔차 트리거나 팀 정밀도 트리거(§9.6)가 켜진 사용자에게만, 이미 있는 텔레메트리(Istio ztunnel, OBI/Beyla, Coroot, VPC Flow Logs)로 트리거된 노드의 가중치를 실측 pair로 바꿉니다. 텔레메트리가 없으면 기존 에이전트(OBI 등)를 Fix 텍스트로 안내만 하고 crossaz는 설치하지 않습니다.

- **해석:** 옵션 C의 "노드/AZ 단위 배분"은 청구 대조 키가 노드-일(T1) 또는 AZ-일(Tz)이라는 뜻이고, 그 아래 경로 분할은 M3의 모델 가중치입니다(§9.0).
- v1 규칙 엔진은 배분 가중치와 "왜 과금되나/어떻게 고치나"를 맡습니다. 설명하지 못한 달러는 이름 붙은 버킷으로 보여 줍니다.

### 선행 프로젝트에서 배운 것

1. **사설 CIDR을 기본으로 무료 처리하면 교차 AZ 비용이 조용히 $0이 됩니다.**
   - Kubecost 차트는 develop과 v3.2.4 모두 in-zone 기본값에 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16을 넣습니다. https://github.com/kubecost/kubecost/blob/develop/kubecost/values.yaml
   - 그 결과 EKS multi-AZ 트래픽이 in-zone으로 잡혔습니다. https://github.com/kubecost/kubecost/issues/820
   - 반대 방향 오류도 있습니다. egressd는 zone을 모르면 `cross_zone=false`로 둡니다 (https://github.com/castai/egressd/blob/main/exporter/sinks/prom.go). OBI는 zone이 비어 있어도 inter-zone으로 셉니다 (https://github.com/open-telemetry/opentelemetry-ebpf-instrumentation/blob/HEAD/pkg/export/prom/prom_net.go).
   - → crossaz에서 unknown은 $0도, internet도 아닌 **별도 버킷**입니다.
2. **한쪽만, 전역 단가 하나로 가격을 매기면 AWS의 양측 과금이 틀어집니다.**
   - OpenCost는 egress GiB × 클러스터 단일 단가만 계산합니다. ingress에는 코드 주석대로 "no prometheus cost at the moment for Ingress"입니다. https://github.com/opencost/opencost/blob/develop/pkg/costmodel/networkinsight.go
   - Coroot는 반대로 AWS 요율을 0.01/0.01로 하드코딩하고 RDS 쪽에도 가격을 붙여 2배가 됩니다. https://github.com/coroot/cloud-pricing/blob/main/update.go, https://github.com/coroot/coroot/blob/main/constructor/containers.go
   - → 과금 측(`billed_sides`)을 **규칙 데이터**로 둡니다.
3. **청구 배분은 합계는 맞지만 가중치 오류와 잔차를 숨깁니다.**
   - Kubecost는 라벨 이름이 바뀌어 분류가 깨졌을 때 큰 Adjustment가 이를 흡수했습니다. https://github.com/kubecost/features-bugs/issues/33
   - Finout은 pod 총 바이트로 가중하며 스스로 'best-effort weighting'이라고 부릅니다. https://docs.finout.io/kubernetes-integrations/kubernetes/how-finout-calculates-kubernetes-costs
   - Datadog은 "according to network traffic volume usage"로 배분하고, AWS에서 설명 못 한 몫은 이름 없는 n/a 버킷에 넣습니다. https://github.com/DataDog/documentation/blob/HEAD/hugo/content/en/cloud_cost_management/allocation/container_cost_allocation.mdoc.md
   - → 가중치는 규칙이 정한 교차 AZ 바이트로 둡니다. k에 상한을 두고, 잔차에는 이름을 붙입니다.
4. **수집기를 직접 소유하면 유지비가 제품을 잡아먹습니다.**
   - CAST AI의 egressd와 Kvisor 저장소는 모두 archived입니다. Kvisor는 castai-kvisor 차트(1.165.3, 2026-09-07)로 배포가 계속되며, 소스가 비공개로 바뀌었는지는 **[미조회]**입니다. https://github.com/castai/egressd, https://github.com/castai/kvisor
   - Kubecost network-costs 소스는 비공개입니다(https://github.com/kubecost/network-costs-rs 404, **[미조회]**). 차트 3.2.2/3.2.3은 RBAC 누락으로 깨졌습니다. https://github.com/kubecost/kubecost/issues/4760
   - NFM은 노드당 top 500으로 잘립니다. https://docs.aws.amazon.com/eks/latest/userguide/network-observability.html
   - → 클러스터에 아무것도 설치하지 않고, 이미 있는 데이터만 소비합니다.
5. **경로별 과금 측·무료 경로 규칙과 수정 명령을 청구 달러에 붙이는 도구가 없습니다.**
   - Vantage는 cross-AZ를 in_region에 합칩니다. https://docs.vantage.sh/kubernetes_network_costs
   - AWS NFM 블로그는 요율을 인용만 하고 관측량에 곱하지 않습니다. https://aws.amazon.com/blogs/containers/track-inter-az-and-nat-gateway-traffic-with-eks-container-network-observability/
   - SCAD는 CPU/메모리만 나눕니다. https://docs.aws.amazon.com/cur/latest/userguide/split-cost-allocation-data.html
   - → 이 빈자리가 crossaz의 자리입니다.
6. **팀 매핑과 공유비용 분할은 범용 기능이지만, 과금 측·무료 경로 규칙과 노드-일 대조 잔차를 팀 금액까지 가져가는 도구는 없습니다.**
   - CloudZero, Finout, Vantage, Harness CACM, IBM Cloudability, Kubecost Collections, AWS Cost Categories가 팀 매핑·공유비 분할을 합니다. 교차 AZ 달러를 팀까지 청구 배분하는 Datadog CCM도 가중치가 비공개입니다(docs/team-allocation-landscape.md §0, §2).
   - → 팀 금액은 기존 Row의 그룹핑이고, 거버넌스·재배분·UI는 플랫폼에 맡깁니다(§9).

---

## 2. 선행 프로젝트 비교

선행 조사와 docs/team-allocation-landscape.md 조사(2026-09-15)에서 실제로 읽은 것만 넣었습니다.

| 프로젝트 | 측정 지점 | zone 판정 | 비용 산출 | 청구 대조 | 배울 점 | 피할 점 |
|---|---|---|---|---|---|---|
| Kubecost network-costs + reconciliation | 노드 host netns conntrack, 5초 폴링(3.x values·문서에서는 **[미조회]**) (privileged DaemonSet) | 노드 zone/region 라벨 + CIDR override. 기본 in-zone에 RFC1918 포함 | 흐름 가격: egress GiB × 클러스터 단일 단가(AWS 기본 0.01) | CUR(Athena)로 노드별 Network asset → `networkCostAdjustment`(비공개 코드). 1.x 문서상 `line_item_resource_id` 키. 3.x 문서는 cloud integration 시 청구에서 온 노드 네트워크 비용을 pod `container_network_transmit_bytes_total` 비율로 나눈다고 적음. network-cost 페이지에는 CUR 조정 설명 없음(문서 간 불일치) | direct-classification 형태의 override, estimate/adjustment 분리, 약 48h 미확정 표시. 팀 경로: `aggregate=label:team` 집계, Collections(겹치면 최고 우선순위 collection에 청구), Teams는 RBAC만 | RFC1918 in-zone 기본값, 설정 파싱 실패 시 조용한 기본값 복귀(#1630), adjustment가 버그 흡수 |
| OpenCost | 자체 수집 없음. Kubecost 이미지 메트릭 소비, cAdvisor는 총량만 | 상류 boolean 라벨을 그대로 신뢰 | 흐름 가격: egress GiB × 전역 단가, ingress $0 | 네트워크 대조 미구현(adjustment를 채우는 코드 없음) | CUR `Bytes` 접미사 분류(athenaquerier.go), NetworkInsight 행 모양 | 닫힌 컴포넌트 라벨에 결합, 전역 단가 |
| CAST AI egressd (archived) | conntrack 폴링(netlink/Cilium CT) | 노드 라벨. unknown → `cross_zone=false`. RFC1918만 private | 없음(SaaS 비공개) | 없음 | reply-tuple로 DNAT 해석, 송신 tx만 합산 | 호스트 sysctl 쓰기, kube-system/hostNetwork 기본 제외 |
| CAST AI Kvisor (repo archived, 그러나 castai-kvisor 차트 1.165.3(2026-09-07) 배포 계속) | eBPF 요약 + 서비스 CIDR만 conntrack | 우선순위 CIDR 인덱스(static > VPC/subnet/peering/TGW > public), AZ ID 옵션 | 흐름 가격: allocation group별 interZone ingress/egress 달러(provider 가격) | 없음 | 우선순위 CIDR 인덱스 + connectivityMethod 라벨 | 기본값이 AZ 이름 |
| Coroot | eBPF syscall, 아웃바운드 TCP payload | IMDS AZ 이름 + `actual_destination` 매칭 | 흐름 가격: 목록가, AWS 0.01/0.01 하드코딩, 1e9 | 없음 | 클라이언트 1회 관측으로 양 앱에 배정 | 매칭 안 된 사설 목적지는 조용히 무료, RDS 2배, UDP 없음 |
| aws-samples inter-az-traffic-visibility | VPC Flow Logs egress(`pkt-*`) | 목적지 AZ를 상대의 egress 레코드에서 학습 | 없음(바이트만) | 없음 | egress-only 1회 계수, owner 롤업 | 스냅샷 IP CSV, now 기준 창, INSERT INTO, INNER JOIN 누락 |
| EKS Container Network Observability / NFM | pod TCP 소켓(bpf_sock_ops), 노드당 top 500/30s | AWS 백엔드(비공개) 카테고리 | 없음 | 없음(흐름 표를 다시 보는 방식) | 카테고리 UX, 공유 IP일 때 포트로 pod 선택 | top-K 합을 총량으로 사용, UNCLASSIFIED 혼합 |
| WA Labs CUR 쿼리 | 청구(CUR) | operation 라벨만 | 청구 그 자체 | 청구 그 자체 | InterZone/PublicIP/VpcPeering operation 분리, 인스턴스 In/Out 불균형 | 반올림한 비용끼리 나눗셈, 20x/$1000 고정값 |
| Datadog CCM (+CNM) | agent eBPF 소켓 바이트(유료 add-on) | `intra_availability_zone` true/false/unknown | 청구 배분: 노드 CUR DT $ ÷ 트래픽 양 | 구성상 합계 일치. AWS 미모니터 호스트는 n/a | usage/not_monitored 지출 유형, AZ 이름+ID | 가중치 정의 비공개, AWS 쪽 n/a 버킷에 이름 없음 |
| Vantage K8s Network Cost | conntrack collector(`nf_conntrack_acct` 필요) | 서브넷 맵/EKS discovery, unknown 버킷, S3 경로 인식 | 흐름 가격: CUR 파생 blended 요율, egress | 노드 단위 true-up 없음, 이중계산 경고 | unknown ≠ internet, 경로 테이블 + prefix list로 S3 경로, 헬스 지표 | cross-AZ를 in_region에 합침, egress만 |
| Vantage Network Flow Reports | VPC Flow Logs(S3) | 피어 메타데이터, unknown 카테고리 | 흐름 가격: CUR 파생 요율 | 없음 | 누락 컬럼 이름을 알려 주는 preflight, 샘플링률 공시 | 자동 샘플링, pod 정보 없음 |
| Finout | pod 총 bytes(Prometheus/Datadog) | 없음. CUR `DataTransfer-Regional-Bytes`로만 판단 | 청구 배분: 노드-시간 $ ÷ pod in+out | 합계 일치, 나머지는 node idle | hostNetwork를 가중치에서 제외 | 총 바이트 가중 |
| OBI/Beyla · Retina · Pixie | 패킷 / tc / 소켓 | 노드 zone 라벨. OBI는 빈 zone도 inter-zone. Retina는 remote context만. Pixie는 없음 | 없음 | 없음 | 저카디널리티 inter-zone 시리즈 분리 | 빈 zone 처리, Retina Standard 폐지 예정, Pixie 24h 메모리 보관 |
| nudgebee (BSL) · flowlogs-enricher | Flow Logs(CloudWatch / Firehose) | 현재 인벤토리 / 인스턴스 AZ 이름 | 없음 | 없음 | LogFormat을 파싱, 미해결 IP를 명시적 노드로 | top-2000 절단, 포트로 방향 제거, AZ 이름과 ID 혼동 |
| Spot Ocean (Flexera) | eBPF network client DaemonSet(privileged, hostNetwork, hostPID, NET_ADMIN+SYS_ADMIN) | Internet / Inter-Region / Inter-AZ / Intra-AZ 분류 | 흐름 가격: provider 목록가. Inter-AZ를 **양측 부과**(출발·도착 워크로드 모두) | 없음 | 양측 부과를 문서화한 유일한 도구, 워크로드·라벨별 분해 | 목록가, privileged DS, Ocean 관리 노드만 |
| Red Hat Lightspeed cost management (Koku) | 자체 네트워크 수집 없음(OpenShift Metrics Operator 사용량 + CUR) | 없음 | 청구 배분: 노드 resource_id가 일치하는 EC2 Data Transfer 행 → "Network unattributed" → 클러스터-일 pod CPU 또는 메모리 사용량 가중 | 노드 매칭분만 합계 일치, 마크업 가산 | 미배분을 이름 있는 프로젝트로 표시, 분배 on/off 스위치 | 네트워크를 CPU/메모리로 가중, OpenShift만 |
| Umbrella (구 Anodot Cost) | cAdvisor rx+tx 바이트 | 없음 | 청구 배분: pod 바이트 비율 | 풀을 이루는 DT 라인·노드 범위 미문서 | hostNetwork pod 제외(2026-05-14, compute로 이동) | 총 바이트 가중, 라인 범위 미문서 |
| IBM Cloudability | IBM FinOps Agent(kubelet stats/summary, pod rx/tx) | 없음 | 청구 배분: 2025-02-10부터 EKS DT 라인을 Cluster/Namespace/Workload에 배분, rx/tx 가중 | 라인 범위·AZ 미문서 | Cost Sharing 텔레메트리(Date, Category, Value) | 방법 비공개, DT를 줄로 청구하지 않는 벤더는 인스턴스 비용의 5% |
| CloudZero · nOps · Grafana Cloud | cAdvisor / SCAD / Kubecost·OpenCost 기반 | 미공개 또는 상속 | 네트워크 배분 방법 없음(CloudZero·nOps), OpenCost 목록가 상속(Grafana Cloud) | 미공개 | SCAD 스타일 키로 내보내기 | 방법론 불투명 |

팀 차원 비교(팀 매핑, 공유비 분할, 교차 AZ 달러의 팀 귀속, 외부 배분 데이터 가져오기)는 docs/team-allocation-landscape.md §2에 있습니다.

---

## 3. crossaz v2 아키텍처

### 3.1 초안 간 충돌 해결

| 쟁점 | bill-first | flow-first | consume | **결정** | 이유 |
|---|---|---|---|---|---|
| 달러의 기준 | 청구 배분 | 흐름 가격 + 청구 배분 | 흐름 가격만 | **둘 다 출력하되 합산하지 않음.** CUR T1이면 청구 배분이 주, 없으면 흐름 가격 | SRE는 InterZone 줄을 설명해야 합니다. consume의 우려는 라벨로 반영: 배분 달러는 "청구 배분(모델 가중치)"이며 billed가 아닙니다 |
| Flow Logs 시점 | 조건부 M5 | 코어 | 게이트 M5 | **조건부 M6** (트리거 + PoC 게이트) | 사용자 쪽 쓰기 작업, G1·G7 미확인. 두 심사 모두 flow-first 공수를 2–2.5점으로 봄 |
| Athena | opt-in | 필수 | 게이트 | **CSV가 기본(M2), Athena는 opt-in(M4)**. flow-first의 SQL 안전장치 채택 | Describe/List/Get 불변식 유지 |
| 어댑터 프레임워크 | 없음 | M5에 PairSource | M0에 FlowCollector | **코어에는 없음.** 비용 없는 것(mesh fact, Kubecost doctor, `--prom-selector`)만 M1으로 앞당김 | 구현이 하나뿐인 인터페이스는 금지. 외부 스키마 drift 부담 |
| 미확정 판정 | $0 버킷 제거 | tri-state | tri-state | **M0에서 tri-state `billable`** | 모든 마일스톤의 전제 |
| k 상한 | K_CAP=2 | 부호 있는 잔차 | 잔차 줄 | **K_CAP = 1/`model.VerifyRatioLow` = 2, GB로 비교, 초과분은 `model-residual`** | 기존 상수 재사용, Adjustment식 흡수 차단 |
| DRIFT | 없음 | 없음 | M1 | **pair 소스가 생긴 뒤(M6)** | 실측 없이 drift를 판정할 수 없음 |
| "billed(보정됨)" 라벨 | 명시 없음 | G0–G5 통과 시 | 사용 안 함 | **G0–G5를 통과한 실측 흐름에만** | v2-eni-sufficiency.md 최종 규칙과 동일 |
| 팀 귀속 정책 | — | — | — | **owner 기본, consumption은 M6, initiator 안 함** | A에서 계산 가능한 유일한 정책이고, 금액과 Fix가 같은 팀에 도착(§9.3) |
| 팀 연동 형식 | — | — | — | **비가산 비율 CSV 하나(stdout). FOCUS Allocated*·가산 import 제외** | 이중계산, 제3자 적합성 불명(§9.6) |

### 3.2 실행하는 것과 소비하는 것

- **실행:** 로컬 바이너리 하나(`crossaz scan | explain | rules | bill`). DaemonSet, Lambda, 스케줄러 모두 없음. 주기 실행은 사용자 cron에 맡깁니다.
- **소비(모두 사용자가 이미 만든 것, 읽기만):**
  - **L0 K8s API (List/Get):**
    - v1이 읽는 것 전부.
    - namespace 라벨 `istio.io/dataplane-mode`, pod annotation `sidecar.istio.io/status`, Service annotation `networking.istio.io/traffic-distribution`.
    - (선택) Kubecost `network-costs-config` ConfigMap.
    - pod·namespace의 `--team-label` 키 값 하나(기본 `team`). 선택 `--team-map` 로컬 파일(§9.2).
  - **L1 AWS Describe:**
    - v1 인벤토리. 기존 DescribeInstances 응답의 OwnerId도 사용합니다.
    - M5에서 추가: DescribeAvailabilityZones, DescribeVpcs, DescribeNatGateways, DescribeRouteTables, DescribeManagedPrefixLists, DescribeVpcPeeringConnections, DescribeVpcEndpoints.
  - **L2 Prometheus (GET query / query_range만):**
    - cAdvisor rx(v1)와 tx.
    - KSM `kube_node_info{provider_id}`, `kube_pod_info{pod_ip,node,host_network,created_by_*}`, `kube_replicaset_owner`. provider_id 라벨은 M3에서 KSM 문서로 확인해야 합니다([미조회]).
  - **L3 청구:** CE `GetCostAndUsage` DAILY. CUR 2.0은 사용자가 뽑은 CSV로 받거나(M2), opt-in Athena SELECT로 읽습니다(M4).
  - **M6(조건부):** 기존 VPC Flow Logs Athena 테이블, 기존 Istio ztunnel / OBI / Coroot 메트릭.

```
Snapshot(L0,L1) → classify.BuildPaths → classify.Apply(rules + billed_sides) ─┐
History(L2 range) ────────────────────────────────────────────────────────────┤→ allocate.ModelGB  (path×instance×day×side)
Bill(CE daily) · CURSet(CSV|Athena) ──────────────────────────────────────────┘→ allocate.Allocate (tier, k, cap, buckets, 보존 불변식)
  → allocate.RollupTeams(policy)  (classify.ResolveTeams 결과로 Row·Bucket 그룹핑, §9)
  → report: WATERFALL / BY WORKLOAD / TEAMS / CALIBRATION / UNALLOCATED / WARNINGS (table·JSON·CSV·team-csv)
(M6) PairSource(prom-mesh | flowlogs) → 동일 classify → 트리거된 노드만 ModelGB를 실측으로 교체
```

### 3.3 읽기 전용 경계

- **AWS:** Describe/List/Get + `ce:GetCostAndUsage`만 씁니다. `readonly.go`와 `readonly_test.go`가 강제합니다.
- **유일한 예외(M4, 명시 플래그 필요):** `athena:StartQueryExecution/GetQueryExecution/GetQueryResults` 세 개를 정확히 허용 목록에 넣습니다.
  - SQL은 embed된 템플릿만 씁니다.
  - 렌더링된 SQL이 단일 SELECT/WITH가 아니거나 DDL/DML 키워드를 포함하면 테스트가 실패합니다.
  - managed-results workgroup이 아니면 실행을 거부하고 Fix만 출력합니다. 그래서 `s3:PutObject` 권한이 필요 없습니다.
- **K8s:** List/Get만. 권한이 없으면 경고만 냅니다.
- **Prometheus:** `--prom-selector`로 받은 매처를 모든 쿼리에 주입합니다(v1 cAdvisor 쿼리 포함). 문법이 틀리면 HTTP 요청 전에 exit 2.
- **로컬 쓰기:** `--out dir`의 일별 JSON만.
- **외부 SaaS에 쓰지 않습니다.** 팀 CSV는 stdout으로만 냅니다(`--team-csv`). 플랫폼으로 올리는 일은 사용자 cron에 맡깁니다.
- **절대 만들지 않는 것:** CUR export, flow log, Glue 테이블, NFM Monitor, ServiceMonitor, sysctl. 필요하면 `Fix.Command` 텍스트로만 안내합니다.

### 3.4 데이터 계약

```go
// internal/model/bill.go
type Side string // "In" | "Out"
type Tri string  // "true" | "false" | "unknown"  (Rule.Billable, Path.Billable)

type Family string // interzone | publicip | lb-publicip | vpcpeering | natgw-bytes | xaz-free | other
type BillLine struct {
	Day                           time.Time // UTC, 닫힌 날
	Account, UsageType, Operation string
	Family                        Family // 접미사로 분류, 모르는 값은 other + 경고
	USD, GB                       float64
	Estimated                     bool // CE ResultByTime.Estimated
}
type Bill struct {
	Lines    []BillLine
	Unknown  bool // AccessDenied/DataUnavailable/빈 결과 = "모름", $0 아님
	Warnings []string
}
type CURRow struct {
	Day                                                        time.Time
	Account, ResourceID, ProductCode, UsageType, Operation, AZ string
	GB, USD                                                    float64
}
type CURSet struct {
	Rows       []CURRow
	CostColumn string  // "net_unblended" | "unblended" (출력에 표시)
	G0Share    float64 // AmazonEC2 InterZone GB 중 resource_id "i-" 비율
	AZFill     float64 // line_item_availability_zone 채움 비율
}
type NodeDay struct{ Node string; Day time.Time }
type NodeInterval struct{ Node, InstanceID, AZID string; From, To time.Time }
type PodInterval struct {
	Namespace, Pod, UID, Node, OwnerKind, OwnerName string // ReplicaSet→Deployment 해소 후
	HostNetwork                                     bool
	From, To                                        time.Time
}
type PodDayBytes struct{ Namespace, Pod string; Day time.Time; Rx, Tx float64 }
type History struct {
	Nodes []NodeInterval
	Pods  []PodInterval
	Bytes []PodDayBytes
	Gaps  []NodeDay // 스크레이프 공백 → not-measured
}

// internal/collect/collect.go  (CostCollector.InterAZDataTransferUSD 대체)
type BillCollector interface {
	DailyBill(ctx context.Context, region string, accounts []string, from, to time.Time) (model.Bill, error)
}
type CURReader interface { // csvCUR(M2), athenaCUR(M4); 테스트는 fake
	NodeDay(ctx context.Context, region string, from, to time.Time) (model.CURSet, error)
}
type HistoryCollector interface {
	History(ctx context.Context, from, to time.Time) (model.History, error)
}

// internal/allocate  (순수, I/O 없음, golden 테스트)
type Tier string // "T1-node-day" | "Tz-az-day" | "T0-account-region-day"
func ChooseTier(cur *model.CURSet) Tier // nil이거나 G0Share<0.95 → AZFill 확인 후 Tz 또는 T0
func ModelGB(paths []model.Path, h model.History, from, to time.Time) []Weight
func Allocate(t Tier, w []Weight, bill model.Bill, cur *model.CURSet, h model.History) (Result, error)

type Weight struct {
	Day                                  time.Time
	InstanceID, AZID                     string
	Side                                 model.Side
	SideRole                             string // "endpoint" | "caller-modeled" (M3), "pair" (M6)
	PathKey, RuleID, Namespace, Workload string // Namespace/Workload = 소유(엔드포인트) 워크로드. caller-modeled 행도 같음
	GB                                   float64
	Billable                             model.Tri
	// M6: Consumer WorkloadRef (그 노드 측에서 과금되는 pod의 워크로드, 빈 값 = 모름)
}
type Label string // "allocated(modeled-weights)" | "priced(modeled)" | "billed(calibrated)"(M6에서 G0–G5 통과 시만)
type Row struct {
	Weight
	K, AllocatedUSD, PricedUSD float64
	Confidence                 model.Confidence
	Label                      Label
}
type BucketKind string // unknown-rule | node-unexplained | not-measured | model-residual | not-in-cluster | non-instance | family (M6: node-only, ambiguous-ip)
type Bucket struct {
	Kind     BucketKind
	Key      string // node / product_code / family
	Day      time.Time
	Side     model.Side
	USD, GB  float64
	Suspects []string
}
type Result struct {
	Tier         Tier
	Rows         []Row
	Buckets      []Bucket
	NotFinalDays []time.Time
	Warnings     []string
}
// 불변식(테스트): Σ Row + Σ Bucket == Σ 범위 내 청구 라인 (정수 센트, (i,d,s)마다 최대 잔여법)
```

- **"범위 내 청구 라인" 정의**
  - 조건: 사용자가 준 계정과 리전에서 usage type이 `DataTransfer-Regional-Bytes`로 끝나는 모든 라인입니다. product code와 operation은 가리지 않습니다.
  - InterZone 행은 배분하고, 나머지는 `family[...]`와 `non-instance` 버킷으로 갑니다.
  - 실제로 적용한 (usage type, operation) 집합을 JSON `scope`에 기록합니다.
- **센트 반올림:** 행과 버킷은 (i,d,s)마다 최대 잔여법으로 센트 반올림하고(동률은 PathKey 사전순), 합은 B 센트와 같습니다.
- 팀 뷰 타입과 불변식은 §9.4에 있습니다.

- `model.Rule`에 `billed_sides: [caller|endpoint|lb]`를 추가합니다. `billable`이 true이면 Validate에서 필수입니다.
  - 기본값은 billing-rules.md §1.3을 따릅니다: EC2↔EC2는 caller+endpoint, RDS/ElastiCache/MSK는 caller.
- PairSource(M6)는 **구현이 두 번째로 생길 때만** 만듭니다. 시그니처는 `Pairs(ctx, from, to) (model.PairSet, error)`입니다.
  - PairSet 필드: ObsPoint(eni/host/proxy/socket), ByteLayer, Truncated, Dedupe, Pairs{Src, Dst Endpoint{IP, Pod, Workload, InstanceID, AZID, Kind}, Bytes, Flags}.
  - Truncated=true인 소스는 순위 매기기에만 쓰고 가격을 매기지 않습니다.

### 3.5 비용 산출: 청구 배분과 흐름 가격을 결합하는 방법

**모델 GB (측별):**
- endpoint pod 노드: rx × CrossAZFrac는 In, tx × CrossAZFrac는 Out.
- caller 노드: v1의 균등 caller zone 모델을 씁니다. zone z 몫 = (1/nZones)(1 − destShare(z))이고, 그날 z의 노드들에 균등하게 나눕니다. confidence는 낮춥니다.
- 서비스 경로가 여럿이면 v1 B15 방식대로 균등 분할합니다.
- caller-modeled 행은 경로의 엔드포인트 워크로드에 귀속합니다. 엔드포인트 워크로드가 여럿이면 그날 endpoint 측 GB 비율로 나눕니다(§9.4).
- `billed_sides`에 없는 측은 0입니다. 예: RDS 경로는 요청·응답 양방향이 모두 caller 노드에만 붙습니다.
- hostNetwork pod는 가중치에서 빼고 용의자 목록에만 올립니다(Finout 방식).

**T1 노드-일-측 청구 배분 (주 수치):**

```
B  = Σ CUR USD  (AmazonEC2, *DataTransfer-Regional-Bytes, operation=InterZone-<s>, resource_id=instance(i), day d)
Gb = Σ CUR GB ,  Gm = Σ Weight.GB (i,d,s)
k  = Gb / Gm                                   // GB로 비교해 할인 왜곡 제거
배분 USD(path) = Weight.GB × min(k, K_CAP) × B / Gb     // K_CAP = 1/model.VerifyRatioLow = 2
  Billable=true    → Row(attributed)
  Billable=unknown → Bucket unknown-rule
model-residual   = B − Σ배분                   // k > K_CAP일 때만 > 0
Gm=0, B>0        → node-unexplained (용의자: 그날 tx 상위 pod, hostNetwork pod, 같은 zone의 rds/elasticache/msk 경로)
History.Gaps     → not-measured
k < VerifyRatioLow → 배분은 하되 over-modeled 플래그 (규칙 재검토 신호)
```

- `i-`인데 해당 기간 `kube_node_info`에 없으면 `not-in-cluster`.
- resource ID가 `i-`가 아니거나 비어 있으면 `non-instance[product_code / 접두사]`.
- PublicIP, LB-PublicIP, VpcPeering, NatGateway-Bytes, 모르는 operation은 `family[...]` 버킷으로 갑니다. pod에 바이트 비율로 흩뿌리지 않습니다.
- **Tz(AZ-일):** 같은 식을 instance 대신 AZ 키로 적용합니다.
- **T0(계정-리전-일):** 클러스터 k = CE InterZone GB ÷ 모델 GB 합만 출력합니다. 워크로드 배분은 하지 않습니다. 나머지는 전부 `not-in-cluster-or-unmodeled` 한 줄입니다.

**흐름 가격 (보조, 항상 나란히 출력, 합산 금지):**
- `모델 GB × 측별 단가`.
- 단가는 CUR이 있으면 기간 내 Σcost/ΣGB(usage_type × operation)입니다. 없으면 billing-rules.md §1.2 리전 측별 요율을 씁니다.
- `bytesPerGB = 1e9`는 이름 있는 상수이고 출력에 표시합니다.
- CUR이 없거나 아직 청구가 안 된 날의 추정치로 쓰이며, v1 CostPerMo와 이어집니다.

**미배분 표시와 라벨 규칙:**
- UNALLOCATED 섹션: 버킷 | USD | GB | 용의자. 헤더에 tier, G0 비율, 비용 컬럼 종류를 적습니다.
- CE `Estimated=true`인 날과 CUR 재작성 창 안의 날은 `not-final`로 표시하고 k 통계에서 뺍니다.
- **G6 규칙:** unknown-rule + node-unexplained + model-residual USD가 1위 워크로드 배분액보다 크면 워크로드 순위를 숨기고 노드·AZ 쌍 단위로만 보여 줍니다. 팀 단위는 §9.6 G6-팀을 따릅니다.
- 라벨: "청구 배분(모델 가중치)" / "모델 가격". "billed(보정됨)"은 M6에서 G0–G5를 통과한 실측 흐름에만 붙입니다.

---

## 4. 마일스톤

**A = M0–M5(팀 포함), B = M6(조건부).** 팀 기능의 마일스톤별 범위는 §9.7에서 가져와 각 절에 넣었습니다.

| M | 내용 | 주 |
|---|---|---|
| M0 | 청구 워터폴 + tri-state | 1 |
| M1 | 규칙 보강 + doctor + **팀 해석, TEAM 열** | 2 |
| M2 | G0 판정 + CSV (+팀 fixture 필드) | 1 |
| M3 | 노드-일 배분 + **TEAMS, `--team`, `--team-csv`, 팀 신뢰도, G6-팀** | 4 |
| M4 | Athena + `--out` (+일별 팀 고정) | 1 |
| M5 | 비인스턴스 계열 규칙 (+owner 파생 열거 테스트) | 1.5 |
| M6 | (조건부) 실측 pair + DRIFT + **consumption, GT3, T-team 소스 안내** | 3.5 (+PoC 2) |

테스트 구분:
- **[F]** fixture/golden
- **[K]** kind e2e
- **[R]** 실계정 읽기 실행: 사용자가 이미 가진 CE/CUR로 돌리며 PoC가 아님
- **[P]** 실계정 PoC: v2-eni-sufficiency.md §5의 쓰기 작업이 필요

사전 안내: M0 착수일에 사용자에게 CUR 2.0 export(U1, HOURLY + INCLUDE_RESOURCES) 생성을 권합니다. M2에서 실데이터가 도착해 있게 하기 위해서입니다. crossaz가 대신 만들지는 않습니다.

### M0. 청구 워터폴 + 정직성 수정 (1주), PoC 없이 가치 있음

- **범위**
  - `ce.go`를 MONTHLY에서 **DAILY**로 바꿉니다. GroupBy는 USAGE_TYPE + OPERATION(CE 최대 2개), Filter는 REGION과 LINKED_ACCOUNT(OwnerId, L1이 없으면 REGION만 + 경고)입니다.
  - 접미사 기반 Family 분류와 unknown-operation 경고.
  - `Estimated` 플래그. AccessDenied, DataUnavailable, 빈 결과는 unknown으로 처리합니다.
  - `crossaz bill --days 14`: WATERFALL 출력. InterZone-In/Out을 나누고 PublicIP, LB-PublicIP, VpcPeering, xAZ($0)는 별도 줄로.
  - `scan --verify`를 일별 라인 위에서 다시 구현합니다. 비율 노트는 유지합니다.
  - **tri-state `billable`:** `zone-unknown`, `unclassified`를 unknown으로 바꿉니다. 요약 합계에서 $0로 섞이지 않고 "미확정 $0–max" 줄로 분리됩니다.
  - `--prom-selector`.
- **산출물:** `model.Bill`, `bill` 서브커맨드, 수정된 goldens.
- **종료 조건**
  - [F] 녹화한 CE JSON goldens: 다중 페이지, APN2- 접두어와 us-east-1 무접두어, xAZ 제외, 모르는 operation, Estimated=true, AccessDenied → unknown.
  - [F] 기존 goldens(prod-a, dev-b, l0-only, stale-proxy)는 tri-state 의도 변경분 외에 동일.
  - [F] 잘못된 `--prom-selector`는 HTTP 요청 없이 exit 2.
  - [R](선택) 실계정 워터폴 합계가 같은 필터의 CE 콘솔 합계와 ±$0.01.
  - `go vet`, `go test -race` 녹색.

### M1. 규칙 데이터 보강 + 기존 도구 진단 + 팀 해석 (2주), AWS 불필요

- **범위**
  - `billed_sides`를 rules.yaml과 Validate에 추가합니다.
  - **mesh_dataplane fact** (none / ambient / sidecar).
    - ambient는 zone-local 인정을 kube-proxy 버전 대신 `trafficDistribution` 또는 Istio annotation 기준으로 판정합니다. Istio 문서: "ztunnel categorizes endpoints by locality and routes to the closest healthy ones". spec 필드가 annotation보다 우선합니다. https://istio.io/latest/docs/ambient/usage/traffic-distribution/ (이번에 조회).
    - sidecar는 kind 검증 전까지 `hints-unverified`(low).
  - **Kubecost doctor:** `network-costs-config`의 in-zone 목록이 VPC 또는 pod CIDR을 덮는지, 노드에 zone/region 라벨이 없는지 경고합니다. 비용이 egress-only GiB라는 점도 노트로 남깁니다.
  - cAdvisor tx 쿼리(M3 준비).
  - **팀(§9.2, §9.7):** pod·namespace의 라벨 키 값 복사, `--team-label`, `--team-map` strict 파싱(exit 2), 팀 해석(`classify/teams.go`), `scan`/`explain`의 TEAM 열과 `team: <값> (source: …)` 한 줄. 팀별 합계는 내지 않습니다.
- **산출물:** 규칙·fact·doctor·팀 해석, 회귀 goldens.
- **종료 조건**
  - [F] Coroot RDS 2배 사례: RDS client 경로가 절대 양측으로 가격이 매겨지지 않음.
  - [F] OpenCost egress-only가 절반만 세는 비교 golden.
  - [F] Kubecost 기본 values 기반 ConfigMap이면 경고, 없으면 무경고.
  - [K] (`-tags e2e_istio`, 버전 고정) ambient + PreferClose, 모든 zone에 엔드포인트가 있을 때 판정은 비과금이고 cAdvisor 기준 zone 간 수신 비율 ≤5%. sidecar는 low.
  - [F] 팀: 파싱 오류 줄 번호, 과반/동률/gone/invalid goldens, map-라벨 불일치 경고, Namespaces List가 Forbidden이면 경고만.

### M2. G0 판정: CUR 탐침 + CSV 수집 (1주)

- **범위**
  - `crossaz bill --print-sql`: Q0(product_code × resource_id 접두사별 GB, AZ 채움률)와 노드-일 쿼리를 CUR 2.0과 legacy 컬럼명으로 각각 출력.
  - `--cur-csv`: 누락 컬럼 이름을 적고 exit 2. G0Share, AZFill, 선택된 tier를 출력.
  - fixture에 Bill/CUR 필드 추가. 구 fixture는 누락 필드로 fallback. Anonymize는 v1 InstanceID 매핑으로 resource_id도 치환.
  - **팀(§9.7):** fixture에 `Pod.Labels`(설정 키만), `Namespaces`, `TeamLabel`. Anonymize는 팀 값을 namespace 이름처럼 유지합니다.
- **종료 조건**
  - [F] CSV goldens: G0 97% → T1, 60%+AZFill 높음 → Tz, 둘 다 낮음 → T0, 컬럼 누락 → exit 2.
  - [R] 실제 CUR 2.0 export 한 개 이상에서 Q0 실행. G0 결과를 docs/v2-eni-sufficiency.md에 기록해 CUR 사전과 WA Labs 사이의 충돌을 확정하거나 좁힙니다.
  - 실데이터가 없으면 M3는 T0/Tz를 우선 구현하고 T1은 [F]/[K]로만 검증했다고 명시합니다.

### M3. 노드-일 배분 엔진 + 팀 롤업 (4주)

- **범위**
  - HistoryCollector range 쿼리.
  - `internal/allocate`: ModelGB, Allocate, K_CAP, 버킷, 보존 불변식, G6 표시 규칙, 흐름 가격 병기.
  - **팀(§9.3–§9.6):** `Weight.SideRole`, caller-modeled 소유 워크로드 고정, 센트 최대 잔여법, `allocate.RollupTeams`(owner), unconfirmed-rule, 팀 지표와 신뢰도, TEAMS 섹션·`--team`·JSON `teams[]`·`--team-csv`, G6-팀, GT0/GT1, T-team 계산. 공수가 넘치면 `--team-csv`를 M4로 먼저 미룹니다.
  - report 섹션 BY WORKLOAD, CALIBRATION(AZ×측 k, 노드 이상치), UNALLOCATED.
  - JSON/CSV는 SCAD 스타일 키(cluster, namespace, workload name/type, node).
  - hack/e2e에 합성 CUR CSV 생성기: zone 라벨이 붙은 노드 사이 iperf 알려진 바이트 → InterZone 행.
- **종료 조건:** [F] 표 기반 goldens
  1. 2-zone pod↔pod, 청구 = 모델 → k=1, 100% attributed.
  2. hostNetwork만 있는 노드 → node-unexplained에 용의자 표시.
  3. 청구가 모델의 3배 → 2배로 cap, residual 표시.
  4. `i-`가 kube_node_info에 없음 → not-in-cluster.
  5. RDS 경로 → caller 노드만.
  6. 기간 중간 Deployment 롤아웃 → 옛 ReplicaSet pod도 같은 Service로 매핑.
  7. 모든 fixture에서 센트 단위 보존.
  8. 미배분 > 1위 워크로드 → 순위 숨김.
  - [K] 합성 InterZone 달러의 ≥95%가 generator 워크로드로 배분(실제 KSM 타임라인 조인 포함). AWS 과금 진실은 검증하지 않는다고 명시.
  - [R] 사용자 CSV로 실 클러스터 1회: 모든 InterZone 달러가 attributed 또는 이름 있는 버킷에 들어가고 AZ×측 k가 출력됨.
  - [F] 팀: §9.5 두 예시가 센트 단위로 일치, 모든 fixture에서 팀 보존식(GT0), CSV(0 이하 행 없음, (date, team, cluster) 유일, 날짜 합 = 범위 청구, not-final 제외, T0이면 exit 2).
  - [R] 팀: 실 클러스터의 unmapped 비중과 상위 namespace 출력.

### M4. 선택형 Athena 실행기 + 일별 이력 보존 (1주)

- **범위**
  - `--cur-athena db.table --athena-workgroup`, `AthenaQueryOnly` 인터페이스(메서드 3개).
  - 템플릿 SQL 검사 테스트, managed-results preflight, 스캔 바이트와 쿼리 비용 출력.
  - `--out dir`: 닫힌 날마다 불변 JSON. 재작성 창 안의 날만 덮어씁니다. Prometheus 15일 보존을 넘어 월 단위 뷰를 유지하기 위해서입니다.
  - **팀(§9.2):** 일별 JSON에 그날의 `workload→team`과 `teamsResolvedAt`을 저장하고, 닫힌 날은 다시 매핑하지 않습니다.
- **종료 조건**
  - [F] SQL goldens(CUR 2.0 / legacy). 다른 비 Describe/List/Get 메서드가 추가되거나 DDL/DML이 렌더링되면 `readonly_test` 실패.
  - [R] 같은 날에 대해 Athena 경로와 CSV 경로의 배분 JSON이 바이트 단위로 동일.
  - IAM 문서(billing-rules.md 부록 B)가 실제 호출과 1:1.

### M5. 비인스턴스 청구 계열 규칙 + AZ ID CIDR 인덱스 (1.5주)

- **범위**
  - Kvisor 아이디어로 CIDR 인덱스를 재구현합니다. 키는 AZ ID이고 우선순위는 static > 정확한 ENI IP > subnet 포함(모든 VPC CIDR 연결, 100.64/10, /28 prefix delegation) > peering CIDR > unknown.
  - `--peer-cidrs` strict YAML. 오류 시 줄 번호와 함께 exit 2.
  - `IPZones`의 Ipv4Prefixes 포함 판정.
  - 규칙은 billing-rules.md에 행과 출처를 **먼저** 추가한 뒤 코드에 넣습니다:
    - `nat-gateway-cross-az` (regional NAT는 금액 ?)
    - `vpc-peering-cross-az`
    - `public-ipv4-in-region` (청구 신호 + internet-facing LB 용의자)
    - `vpce-interface`(무료), `tgw`(무료)
    - S3 gateway endpoint 경로 검사(route table + prefix list)
    - `eks-control-plane-xeni` (low, ENI 설명 매처는 EKS 문서로 먼저 검증)
- **종료 조건**
  - [F] `bill`이 낼 수 있는 모든 family가 규칙 ID나 이름 있는 버킷에 매핑됨을 열거 테스트로 확인.
  - [F] 다른 AZ NAT 경유 fixture → NAT 게이트웨이를 명시한 Fix. S3 gateway route 없음 → endpoint Fix.
  - [F] zone 이름은 같고 AZ ID가 다르면 cross로 판정. 100.64/10과 /28 해석. 잘못된 overrides는 exit 2.
  - [R] unknown-rule + family[other] 비율 출력(G4 대리 지표).
  - [F] 팀(§9.3): 새 family 규칙은 버킷으로만 가므로 팀 영향이 없습니다. 열거 테스트로 Row를 만드는 모든 과금 규칙이 owner 파생 표에 들어감을 확인합니다.

### M6. (조건부) 실측 pair로 가중치 교체 + DRIFT + consumption 정책 (3.5주, 경로 B는 PoC 2주 별도)

- **트리거(`bill`이 계산해 출력):** 닫힌 14일 동안 (node-unexplained + model-residual)이 InterZone USD의 10%를 넘거나, AZ×측 k가 [0.5, 2] 밖. 또는 §9.6 T-team, T-consumption.
- **경로 A: 이미 있는 Prometheus pair 메트릭**
  - Istio ztunnel `istio_tcp_sent_bytes_total` (zone 라벨은 문서화 안 됨 → 스키마 탐침).
  - OBI `obi_network_flow_bytes_total` / Beyla `beyla_network_flow_bytes_total` (빈 zone 필터 필수).
  - Coroot `container_net_tcp_bytes_sent_total{actual_destination}`.
  - 어댑터마다 조건: 실사용자 요청 + 고정 버전 녹화 응답 + 2일 예산. 넘으면 "요청 시"로 미룹니다.
  - confidence는 medium까지. 경로당 가격을 매기는 어댑터는 하나뿐입니다.
- **경로 B: 기존 VPC Flow Logs**
  - PoC 선행.
  - DescribeFlowLogs preflight로 누락 필드 이름 표시.
  - `flow_direction='egress'` 고정 쿼리 빌더, 닫힌 UTC 일 창 + 지연 knob.
  - kube_pod_info 구간 조인(±60초, ambiguousIP), 행 예산 초과분은 보존용 remainder 행.
- **공통**
  - pair 데이터는 트리거된 노드의 가중치만 바꿉니다. 분모는 계속 청구서입니다.
  - DRIFT 추가: LEAK는 모델보다 실측이 +0.10 이상이면서 10GB/mo 이상, OVER는 모델의 50% 미만. 원인은 기존 facts에서 제시합니다.
  - SOURCES 섹션: 관측 지점, 바이트 층위, 커버리지, 스키마 경고.
  - **팀(§9.3, §9.6):** `Weight.Consumer`, `--team-policy consumption`, `__crossaz_policy-unresolved__`, GT3, B 소스 안내(기존 ztunnel·OBI/Beyla·Coroot 메트릭이나 Flow Logs, 없으면 Fix 텍스트로 기존 에이전트 안내).
- **종료 조건**
  - [F] aws-samples 결함 fixture 전부 올바른 버킷: AZ 간 IP 재사용, 단방향 UDP, 한 노드 hostNetwork 2개, 지연 도착.
  - [F] 어댑터 녹화 응답 goldens. 라벨 이름이 바뀐 응답은 경고만 내고 가격은 0.
  - [K] OBI 또는 Istio로 generator 바이트 ±15%.
  - [P] G1·G3·G4·G5 통과 시에만 "billed(보정됨)". 미설명 + 잔차 <10%. 실패하면 "모델" 라벨 유지.
  - [F] 팀: §9.5 tier B golden(owner와 consumption 두 표).
  - [K] 팀: Istio 또는 OBI generator로 consumption 팀이 generator 달러의 ≥95%.

**합계:** 코어 M0–M5는 10.5주입니다(팀 기능 +1.5주). M3가 넘치면 `--team-csv`를 M4로 옮깁니다. CUR 지연으로 1–2주 밀릴 수 있습니다. M6은 조건부로 3.5주 + PoC 2주입니다.

---

## 5. v1 코드: 유지 / 변경 / 삭제

- **유지**
  - `internal/classify`: BuildPaths, Apply, Matches, first-match 엔진, Validate.
  - rules.yaml 형식과 출처·검증일 규율.
  - `model.CrossAZFraction` / `HintedCrossAZFraction`: caller 측 zone 몫 계산에도 재사용합니다.
  - low confidence에는 금액을 내지 않는 정책, `*float64`로 표현하는 unknown 금액.
  - `collect/k8s.go`, `aws.go` 인벤토리, `scan.Analyze` 순수 진입점, `anonymize.go`(확장).
  - report 스타일과 제어문자 정리.
  - `readonly.go` 접두사 가드.
  - v1 cAdvisor rx 쿼리(`scan` fallback).
  - hack/e2e kind 하니스(확장).
  - 표준 라이브러리 flag 사용.
  - rules.yaml, classify 규칙 엔진, IAM은 팀 기능 때문에 바뀌지 않습니다.
- **변경**
  - `collect/ce.go`: MONTHLY 합계를 DAILY × USAGE_TYPE × OPERATION + LINKED_ACCOUNT로 바꾸고 `model.Bill`을 반환합니다. 접미사 매칭 방식은 유지.
  - `collect/collect.go`: CostCollector를 BillCollector로 교체. CURReader, HistoryCollector 추가.
  - `collect/prom.go`: selector 주입, tx 쿼리, KSM range 쿼리.
  - `collect/k8s.go`: mesh 라벨·annotation, 선택적 ConfigMap get. `NewK8s(cs, labelKeys ...string)`로 pod·namespace의 설정 키 값만 복사(가변 인자라 기존 호출부 그대로 컴파일).
  - `collect/aws.go`: OwnerId 반환. M5에서 Describe 호출 추가, IPZones가 prefix와 AZ ID를 처리.
  - `readonly.go`/`readonly_test.go`: M5 메서드, M4 `AthenaQueryOnly` 정확 허용 목록 + SQL 검사.
  - `model`: Rule.Billable을 Tri로, `billed_sides` 추가, `Facts.MeshDataplane`, Snapshot.Bill/History, target_kind 확장.
  - `model/snapshot.go`: `Pod.Labels map[string]string`(설정 키만), `Snapshot.Namespaces []Namespace{Name, Labels}`(M1 istio 키와 공유), `Snapshot.TeamLabel`.
  - `report`: TEAM 열, TEAMS 섹션, JSON `teams[]`, `teamcsv.go`(`encoding/csv`).
  - `estimate/verify.go`: 월 비율을 일별 BillCheck로 바꿉니다. `VerifyRatioHigh/Low`는 over-modeled 플래그와 K_CAP로 재사용.
  - `estimate.go`: 측별 모델 GB 추가. `bytesPerGB`는 출력에도 표시.
  - rules.yaml: `billed_sides`, zone-unknown/unclassified를 unknown으로, mesh-aware hint 규칙, M5 규칙.
  - `cmd/crossaz`: `bill` 서브커맨드와 플래그 `--days`, `--print-sql`, `--cur-csv`, `--cur-athena`, `--athena-workgroup`, `--out`, `--prom-selector`, `--peer-cidrs`. 팀 플래그 `--team-label`, `--team-map`(M1), `--team`, `--team-csv`(M3), `--team-policy`(M6).
  - 문서
    - design.md: A-3의 "Prometheus 쿼리에 cluster 라벨 범위가 없다" 한계 삭제, C4 계약 갱신, allocate 절 추가, C 계약에 teams 절 추가.
    - README: 팀 라벨 요구(`--team-label`, `--team-map`)와 fixture에 남는 값(팀 이름).
    - billing-rules.md 부록 B IAM을 1:1로 갱신.
    - measurement-alternatives.md 100–101행의 로그 비용 서술 정정(v2-eni-sufficiency §4에서 지적).
- **신규:** `internal/allocate`, report bill 섹션, hack/e2e 합성 CUR 생성기, `internal/diagnose`(doctor는 M1, drift는 M6), `internal/classify/teams.go`(M1, `LoadTeamMap`, `ResolveTeams`), `internal/allocate/teams.go`(M3, `RollupTeams`). PairSource는 M6에서만.
- **삭제**
  - `CostCollector.InterAZDataTransferUSD` 월 경로와 `model.CostData`: 일별 경로가 `--verify`를 대신한 뒤 삭제하고, 구 fixture는 fallback으로 읽습니다.
  - 그 외 코어는 삭제하지 않습니다.

---

## 6. 리스크와 완화, GO/NO-GO 게이트 배치

### 게이트를 어느 마일스톤에 거는가 (docs/v2-eni-sufficiency.md §5)

| 게이트 | 거는 곳 | 통과 시 | 실패 시 |
|---|---|---|---|
| G0 CUR 키 (`i-` ≥95%) | **M2 판정 → M3 tier 결정** | T1 노드-일 배분 | AZFill이 확인되면 Tz, 아니면 T0. v2 중단은 아님 |
| G1 bytes↔청구 GB | **M6 경로 B 전제.** M3에서는 k(GB)를 진단용으로만 출력 | flow 바이트를 청구 GB로 환산 허용 | billed 주장 금지, flow는 순위·경로 탐지만 |
| G2 NLB | **M6** (NLB 비중이 큰 클러스터). M5까지 NLB LB측 달러는 non-instance | NLB hop별 가격 | NLB 경로는 unknown 유지 |
| G3 노드-일 ±10% | M3에서는 k 분포만 출력(종료 조건 아님). **M6 billed 라벨 조건** | "billed(보정됨)" | NO-GO, "모델" 라벨 유지 |
| G4 분류 공백 ≤5% | **M5 종료 시 [R]로 측정**, M6 조건 | 규칙 커버리지 충분 | 규칙 추가를 우선, billed 보류 |
| G5 손실(SKIPDATA) | **M6 경로 B만** | flow 신뢰 | flow 기반 달러 보류 |
| G6 귀속 | **M3 표시 규칙**(순위 숨김), M6 | 워크로드 금액·순위 | 노드·AZ 쌍 단위만 |
| G7 관측비 | **M6 경로 B 운영 모드** | 상시 | 10–70%면 주 1일 샘플링(배율 표시), 70% 초과면 경로 B 불가 |
| GT0 팀 보존(날마다 정수 센트, CSV 날짜 합 포함) | **M3 [F] 전 fixture** | 팀 출력 | 빌드 실패 |
| G6-팀 | **M3 표시 규칙** | 팀 순위 표시 | 이름순 + 경고. CSV는 버킷 원소 포함해 출력 |
| GT1 unmapped > 범위 청구의 10% | **M3** | — | 경고 + 상위 namespace와 라벨 명령 |
| GT2 가져오기 일치: 실제 테넌트의 닫힌 하루에서 팀 비율 ±1%p, 버킷 원소가 이름 그대로 보이고, §9.6의 [미검증] 항목 확인 | **M3 이후 [R], 선택, 플랫폼별** | README "검증됨(날짜)" | "형식 호환(미검증)" 유지 |
| GT3 policy-unresolved ≤ 10% | **M6** | consumption 금액과 CSV | exit 2, owner 안내 |
| T-team | **M3 계산, M6 착수 판단** | — | B 권장 |

### 리스크

| 리스크 | 완화 |
|---|---|
| G0 실패. CUR 2.0 사전은 data transfer의 resource ID가 비어 있다고 적지만 WA Labs, Kubecost, Datadog 방식은 i-가 채워진다고 가정합니다 | M2에서 실데이터로 먼저 판정. 매 실행마다 G0를 출력. tier 강등 경로를 구현 |
| 공유 계정 T0: 비 EKS 워크로드가 섞임 | 잔차를 "클러스터 밖이거나 모델 밖"으로 표기, 클러스터 전용 계정 권장 |
| caller 측 가중치는 모델(균등 zone 가정) | AZ×측 k로 노출. 트리거되면 M6 |
| endpoint이면서 caller인 pod가 자기 Service에 과가중 | k cap으로 피해 한정, 해당 경로 confidence 하향 |
| 현재 스냅샷 토폴로지를 과거 날짜에 적용 | `--out` 일별 스냅샷(M4). Service/EndpointSlice generation이 기간 시작 이후면 경고 |
| Prometheus 15일 보존 vs CUR 지연·최대 2주 재작성 | `--out` 일별 보존, 재작성 창 안의 날은 not-final |
| RDS/ElastiCache/MSK는 볼륨 신호가 없음 | node-unexplained의 용의자로 제시. 확정 배분인 척하지 않음 |
| 접근 마찰: CUR은 payer 계정, 멤버 계정은 CE 거부 가능 | CSV 경로, AccessDenied는 unknown, `--print-sql` |
| Athena 예외가 읽기 전용 약속을 약화 | opt-in 플래그, 정확한 3개 허용 목록, SQL 검사, managed results, CSV 대안 |
| 할인 의미 차이(unblended / net / amortized) | k는 GB로 비교, 비용 컬럼 종류를 출력에 표시 |
| 외부 스키마 drift(Istio zone 라벨 비문서화, OBI experimental, Kubecost 라벨 개명 이력) | 코어는 의존하지 않음. M6 어댑터는 탐침 + 버전 고정 녹화 응답 + 경고 |
| usage type 이름 미확인. PublicIP operation은 re:Post에만 있음(billing-rules 부록 A), NatGateway-Bytes는 제3자 자료 | 접미사 매칭, 모르는 값은 `family[other]` + 경고. 조용히 버리지 않음 |
| 1인 유지보수 범위 확장 | 규칙은 billing-rules.md 행이 먼저, 어댑터는 2일 예산, M6는 측정된 트리거로만 착수 |
| 경쟁 가속: Vantage K8s network cost 2026-08-06 출시, Kubecost develop/3.3 NAT flow-log 감지, tollwing(2026-06, CUR 대조는 유료 tier, README만 확인), DoiT Attribute 2026-07-07 출시(인수 보도 약 $65M, DoiT 자체는 출시로만 표기), New Relic CCI GA 2026-04-28, CloudBolt의 StormForge 인수(2025-03-31)와 K8s 배분 기능 | 차별점을 "네트워크 숫자가 있음"이 아니라 공개 규칙, 양측·무료 경로, 잔차, 수정 명령에 둠 |
| 틀린 chargeback은 틀린 추정보다 신뢰를 크게 해침 | A 라벨 "청구 배분(모델 가중치)" 고정, 팀 신뢰도 상한 medium, T/modeled 비율, T-team(§9.4, §9.6) |
| owner 정책이 공유 서비스 팀에 불공정하게 느껴짐 | callerModeled 비율 표시, 플랫폼 공유비용 재배분 안내, M6 consumption |
| 단측 서비스(RDS 등)로 가는 바이트가 endpoint pod의 tx에 섞여 그 서비스 팀이 과다 부과됨 | K_CAP로 피해 한정, T/modeled 조건. 같은 zone에 과금 단측 경로가 있는 노드-일을 inRange에서 뺄지는 M3 [R] 결과로 결정 |
| 저신뢰 규칙 비중이 크면(kube-proxy 대체 클러스터) 팀 금액이 줄어듦 | unconfirmed-rule을 이름 있게 표시, G6-팀, AWS 자격증명 재실행 Fix, B 권장 |
| 플랫폼 필터 범위 ≠ crossaz 범위 | JSON `scope`, usage type 수준 범위(§3.4), 버킷 원소 포함, GT2 |
| 플랫폼 내장 네트워크 분할(Finout pod 바이트, Vantage conntrack, Datadog CNM)과 겹침 | README에 "플랫폼 내장 K8s 네트워크 배분을 끄거나 InterZone 줄을 제외한 뒤 사용", GT2 |
| 같은 계정·리전의 여러 클러스터 CSV를 합쳐 이중계산 | 계정·리전당 클러스터 하나만 지원 명시, not-in-cluster > 10%이면 경고 |
| 팀 기능 공수(코어 +1.5주, 1인 유지보수) | 모든 계산은 그룹핑. 규칙·수집·IAM 불변. `--team-csv`를 M4로 미룰 수 있음 |

---

## 7. 차별점: 왜 Kubecost/Coroot/NFM 대신 crossaz인가

**겹치는 부분 (정직하게)**
- 청구를 트래픽 비율로 나누는 방식은 Datadog CCM과 Finout이 이미 상용으로 제공합니다.
- Flow Logs를 비용으로 바꾸는 것은 Vantage Network Flow Reports가 합니다.
- CUR InterZone 분석은 WA Labs 쿼리가 무료로 있습니다.
- `trafficDistribution` 권고는 AWS NFM 블로그에도 나옵니다.
- 서비스 맵과 지연 관측이 필요하면 Coroot나 NFM이 더 맞습니다.
- 팀 매핑과 공유비용 분할은 CloudZero, Finout, Vantage, Harness CACM, Cloudability, Flexera, Umbrella, Kubecost Collections, AWS Cost Categories가 이미 합니다.
- 교차 AZ 달러를 팀 태그까지 청구 배분하는 것은 Datadog CCM이 합니다. 단 CNM이 모든 호스트에 있어야 하고, 가중치가 비공개이며, 미모니터 몫은 n/a입니다.
- 양측 부과는 Spot Ocean이 합니다. 목록가이고 privileged DS가 필요하며 Ocean 관리 노드만 됩니다.
- CAST AI는 allocation group별 interZone 달러를 냅니다(provider 가격).
- 자세한 비교는 docs/team-allocation-landscape.md §2–§4에 있습니다.

**crossaz만 하는 것**
- **과금 측·무료 경로 규칙에 출처와 검증일이 붙어 있습니다**(rules.yaml, billing-rules.md): EC2 양측, RDS 등은 client만, internal ALB·VPCE·TGW 무료, NLB cross-zone, NodePort hop.
  - Kubecost/OpenCost는 egress-only 전역 단가이고 기본값이 multi-AZ를 $0으로 만듭니다. Coroot는 RDS가 2배입니다. Vantage는 cross-AZ를 in_region에 합칩니다.
- **청구 달러에 경로 판정과 구체적 수정이 붙습니다:** trafficDistribution, topologySpread, NLB TG cross-zone, IP target mode, NAT per AZ, S3 gateway endpoint. 조사한 도구 중 이렇게 하는 것은 없었습니다.
- **잔차를 숨기지 않습니다.** 노드×측 k, K_CAP, 이름 있는 버킷을 보여 줍니다. 반대로 Kubecost는 Adjustment에 흡수하고, Datadog은 AWS 쪽을 n/a로, Finout은 idle로 보냅니다. 청구서가 규칙을 검증하기도 합니다: k < 0.5면 과대 모델 규칙, 잔차는 누락된 규칙을 가리킵니다.
- **설치가 없고 MIT 오픈소스입니다.** 대부분의 팀이 이미 가진 CE, CUR, KSM, cAdvisor로 동작합니다. Datadog CNM(유료 agent), Vantage/Kubecost(privileged 또는 sysctl), NFM(add-on + Monitor)은 모두 뭔가를 설치해야 합니다.
- **기존 도구 사용자용 진단:** Kubecost가 왜 cross-AZ를 $0이나 절반으로 보이는지 알려 줍니다(M1). Istio ambient 판정도 보정합니다.
- **팀 금액에도 과금 측·무료 경로 규칙이 적용됩니다.** 저신뢰 규칙 달러는 팀에 부과하지 않고 `__crossaz_unconfirmed-rule__`로 이름을 붙입니다(§9.3).
- **노드-일 대조 잔차와 미매핑이 이름 있는 원소로 플랫폼 CSV까지 갑니다**(§9.6).
- **팀이 받는 금액과 그 금액을 없애는 Fix 명령, 고칠 수 있는 금액(`fixableUsd`)이 함께 도착합니다.**
- **팀 신뢰도가 B 착수를 결정합니다**(T-team, §9.6).
- 팀 기능도 설치가 없습니다.

**crossaz를 쓰지 말아야 할 때**
- 시간 단위 실시간이 필요할 때
- overlay CNI이거나 EKS가 아닐 때
- 이미 전 호스트에 Datadog CNM이 있고 팀 쇼백만 필요할 때
- compute·storage chargeback, UI, 예산이 주 목적일 때
- consumer-pays를 A에서 바로 확정해야 할 때(B 필요)

---

## 8. 라이선스: 재사용 가능 여부 (crossaz = MIT)

| 대상 | 라이선스 | 판정 |
|---|---|---|
| OpenCost (`athenaquerier.go` SelectAWSCategory, `athenaintegration.go` CUR 컬럼 목록) | Apache-2.0 | 코드 복사 가능. 해당 파일에 LICENSE/NOTICE 유지, 변경 표시. 계획은 아이디어를 재구현하고 복사할 때만 NOTICE를 추가 |
| WA Labs CUR SQL | Apache-2.0 | 짧은 SQL 각색 가능(고지 유지). CUR 2.0 버전(Workshop Studio)은 **[미조회]** |
| Kubecost 차트 | Apache-2.0 | 템플릿 재사용은 가능하나 가치가 낮음. ConfigMap 이름·키는 사실 정보라 doctor에 사용 |
| Kubecost network-costs 이미지 / network-costs-rs | 비공개(404, **[미조회]**) | 재사용·재배포 불가 |
| kubecost/conntrack-parser, kubecost/docs v1.x | 라이선스 없음 | 참고만, 문구 복사 금지 |
| egressd, Kvisor | Apache-2.0 (archived) | 합법이나 vendoring하지 않음. 아이디어만 |
| Coroot 코드 / coroot cloud-pricing 데이터 | Apache-2.0 / infracost 상류 조건 **[미조회]** | 코드는 가능. 가격 데이터는 쓰지 않음(요율은 billing-rules.md 출처 사용) |
| NFM agent, OBI/Beyla, Pixie, datadog-agent | Apache-2.0 | 코드 이식 가능(고지). 계획은 메트릭 이름만 읽음 |
| Microsoft Retina, Vantage helm chart | MIT | 가능(고지) |
| aws-samples inter-az | MIT-0 | 고지 없이 가능. 결함 사례를 fixture 아이디어로만 사용 |
| aws-vpc-flowlogs-enricher | 저장소 MIT-0, 파일 헤더 Apache-2.0 | Apache-2.0로 간주, 재사용 안 함 |
| mahasiva istio-topology-routing-example | 라이선스 없음 | 복사 금지, 아이디어만(EKS ENI 설명 매처는 EKS 문서로 검증 후) |
| nudgebee | BSL 1.1 | MIT와 비호환, 코드 금지 |
| Datadog documentation | NOASSERTION | 참고만 |
| Datadog / Vantage / Finout / NFM 백엔드 | 상용 비공개 | 방법론 아이디어와 용어만 |
| AWS 문서의 필드명·usage type·API 이름, 타 도구 메트릭 이름·라벨 스키마 | 사실 정보 / 인터페이스 | 인코딩과 상호운용 읽기에 제약 없음 |

**결론:** v2 계획은 외부 코드를 복사하지 않는 것을 기본으로 합니다. OpenCost나 WA Labs 조각을 복사하게 되면 그 파일에만 Apache-2.0 고지를 붙입니다. 외부 문서 인용은 출처당 15단어 미만으로 제한합니다. 팀 기능은 외부 코드를 복사하지 않습니다. Backstage `CostInsightsApi`(Apache-2.0)는 인터페이스 참고만 합니다.

---

## 9. 팀별 교차 AZ 비용 배분 (옵션 C)

### 9.0 전제

- **A(설치 없음)** 청구 워터폴, 규칙 진단, 수정 명령, 노드-일/AZ-일 청구 대조 아래의 모델 가중치 배분, 그리고 이 절의 팀 롤업을 냅니다.
- **B(조건부)** 트리거가 켜진 노드-일-측에서만 가중치를 기존 텔레메트리의 실측 pair로 바꿉니다. 분모는 계속 청구서입니다.
- **해석:** 옵션 C의 "노드/AZ 단위 배분"은 "청구 대조 키가 노드-일(T1) 또는 AZ-일(Tz)이고, 그 아래 경로 분할은 M3의 모델 가중치"로 읽었습니다. A가 워크로드 분할을 아예 하지 않는다는 뜻이라면 A에서 팀 달러를 낼 수 없습니다. 그 경우 A는 경로마다 소유 팀 이름만 보여 주고, 팀 달러는 B부터 나옵니다.
- **외부 사실**은 docs/team-allocation-landscape.md(2026-09-15 조회)를 따릅니다. 조회하지 못한 사실은 **[미조회]**, 실제 테넌트에서 확인해야 하는 연동 동작은 **[미검증]**으로 표시합니다.

### 9.1 목표와 비목표

**목표**
1. **팀 금액은 새 가중치를 만들지 않습니다.** §3.5가 만든 `Row`와 `Bucket`에 팀 이름을 붙여 묶기만 합니다.
2. **날마다 보존식이 정수 센트로 성립합니다:** Σ팀 + Σ팀 뷰 요소 + Σ버킷 = 범위 내 청구(§3.4). 테스트로 강제합니다.
3. **팀 키는 이미 읽는 객체에서 해석합니다.** pod/namespace 라벨 키 하나와 선택 매핑 파일만 씁니다. 새 AWS 권한, 새 RBAC, 설치는 없습니다.
4. **팀마다 신뢰도, 노출액, 고칠 수 있는 금액, 수정 목록을 냅니다.** "이 금액은 대부분 모델"인 팀을 chargeback 전에 구분할 수 있게 합니다.
5. **청구서를 분모로 유지하는 비가산형 가져오기 경로용 CSV 하나를 stdout으로 냅니다.**
6. **팀 정밀도가 부족하면 B를 권고합니다.** 소비 기준 정책은 B에서만 제공합니다.

**비목표**
- UI, 예산, 알림, 팀 RBAC
- 태그 거버넌스, 라벨 강제, 자동 라벨링
- 공유비용 재배분 엔진, 조직 계층
- compute·storage·idle 배분
- 외부 SaaS에 직접 쓰기
- `scan` 모델 가격의 팀별 합계. 팀 달러 숫자가 둘이 되기 때문입니다.
- 과거 라벨 이력 재구성
- A 모드의 소비·개시자 기준 팀 금액

### 9.2 팀 키 해석

**새로 읽는 것 (최소)**

| 데이터 | 위치 | 용도 |
|---|---|---|
| pod의 `--team-label` 키 값(기본 `team`) | 기존 Pods List, `k8sPod`에서 그 키만 복사 | 워크로드 팀 |
| namespace의 같은 키 값 | M1 Namespaces List(istio 라벨 때문에 이미 추가 예정)에 합류. Forbidden이면 경고 후 건너뜀 | 대체 소스 |
| `--team-map teams.yaml` | 로컬 파일(선택) | 라벨을 달 수 없거나 권한이 없는 namespace용 |

- 기본 키 `team`은 OpenCost `TeamLabel`, Kubecost `team_label`의 기본값과 같습니다. SCAD도 pod 라벨을 태그로 가져오므로, 같은 라벨을 쓰면 compute와 네트워크 팀 키가 플랫폼에서 일치합니다(landscape §3).
- **읽지 않는 것**
  - Deployment 메타데이터 라벨
  - AWS 리소스 태그
  - KSM `kube_*_labels`: allowlist 없이는 노출되지 않습니다(kube-state-metrics cli-arguments.md, 2026-09-15 조회).

**조인 단위: 워크로드(namespace, 해소된 owner)**
- owner는 팀 값이 아닙니다. pod와 과거 pod(`PodInterval`)를 한 워크로드로 묶는 키로만 씁니다.
- Service 경로의 소유 워크로드는 EndpointSlice `PodName`으로 찾습니다. pod가 없는 엔드포인트는 Service의 namespace로 해석합니다.

**우선순위: 첫 번째로 성립하는 것을 씁니다**
1. **pod 라벨.** 워크로드의 현재 pod 중 과반(>50%)이 가진 값을 씁니다(`teamSource=pod-label`). 소수 값은 경고에 남깁니다. 정확히 동률이면 추측하지 않고 `__crossaz_unmapped__`로 보냅니다(`detail=conflict`).
2. **매핑 파일.** namespace glob의 첫 매치입니다(`map`). namespace 라벨과 값이 다르면 파일이 이기고 경고를 냅니다. 클러스터 쓰기 권한이 없는 운영자도 교정할 수 있게 하려는 것입니다.
3. **namespace 라벨**(`namespace-label`).
4. **없으면 `__crossaz_unmapped__`.** detail은 `no-label`, `conflict`, `invalid`, `gone` 중 하나입니다.

**teams.yaml**
```yaml
version: 1
namespaces:            # 순서대로 첫 매치, path.Match(stdlib)
  - {match: kube-system, team: platform}
  - {match: "payments-*", team: payments}
```

**검증 (종료 코드는 `--peer-cidrs`와 같은 규율)**
- **exit 2, HTTP 요청 전:**
  - `sigs.k8s.io/yaml` strict 파싱 실패, `version != 1`
  - `path.ErrBadPattern`
  - `team` 값이 `validation.IsValidLabelValue`를 통과하지 못함(apimachinery v0.37.0, 이미 의존성에 있음)
  - 줄 번호를 함께 출력합니다.
- **라벨에서 온 팀 값이 유효하지 않으면** `unmapped[invalid]`와 경고로 처리합니다.
- **경고:**
  - 어떤 namespace에도 맞지 않는 항목
  - 대소문자만 다른 팀 이름
- **이름 충돌은 구조적으로 불가능합니다.** 유효한 라벨 값은 영숫자로 시작하므로 `__crossaz_*__`와 겹치지 않습니다. Kubecost/OpenCost `__unallocated__`, Vantage `__idle__`과 같은 형식입니다.

**시간 축**
- 팀은 실행 시점 스냅샷으로 해석해 `--days` 창 전체에 적용하고, `teamsResolvedAt`을 출력합니다.
- M4 `--out`의 일별 JSON에 그날의 `workload→team`을 저장하고, 닫힌 날은 다시 매핑하지 않습니다.
- CloudZero AnyCost 태그도 시간 독립이라 같은 한계를 가집니다. 이력 엔진은 만들지 않습니다.

### 9.3 귀속 정책

**기본은 owner(책임 소유자)입니다.** 규칙의 Fix가 바꾸는 설정의 주인이 그 경로의 모든 과금 측을 냅니다. 규칙 스키마는 바꾸지 않고 `target_kind`와 규칙 신뢰도에서 파생합니다.

| 행의 경로 | owner 팀 | 이유 |
|---|---|---|
| `target_kind=service`, 규칙 신뢰도 high/medium | 엔드포인트 워크로드 팀. 엔드포인트 워크로드가 여럿이면 그날 endpoint 측 GB 비율로 나눔 | Fix(PreferClose, topologySpread, disable-nlb-xz, relocate)가 이 Service나 워크로드를 바꿈 |
| 과금 규칙인데 **규칙 신뢰도 low** (`hints-unverified`, `lb-exemption-unverified`, `nlb-cross-zone-unknown`, `nlb-client-cross-az`) | 팀에 부과하지 않음. `__crossaz_unconfirmed-rule__`(detail=ruleId)로 보내고 owner 팀에는 노출액으로만 표시 | 무료 경로일 수 있거나(ALB 면제 미확인) 서비스 팀이 못 고침(kube-proxy 대체). v1의 "low 신뢰도에는 금액을 내지 않음" 원칙(§5)과 같음 |
| `rds`/`elasticache`/`msk` | **A: 행이 없음.** §3.5상 볼륨 신호가 없어 node-unexplained로 가고 용의자 팀으로만 표시. **B:** 호출자 팀 | 과금 측이 EC2(호출자)뿐이고(billing-rules.md §1.3), Fix(relocate, 리더 엔드포인트, client.rack)도 호출자 설정 |
| M5 family(NAT, peering, public IP) | 행이 없음. `family[...]`, `non-instance` 버킷 | pod 행이 생기는 규칙이 추가될 때 `Rule.Owner`를 다시 검토 |

**정책 비교**

| 정책 | A 계산 가능성 | B | 판정 |
|---|---|---|---|
| owner | service 경로 전부 가능(PathKey만 필요). caller 노드 배치가 틀려도 팀은 바뀌지 않음 | 같은 매핑에 가중치만 바뀜 | **기본** |
| consumption(각 노드 측에서 과금되는 pod의 팀) | endpoint 행만 가능. caller-modeled 행은 미해결(보통 청구의 절반 가까이) | 양 끝 pod를 아는 소스(ztunnel, OBI, Coroot, Flow Logs + `kube_pod_info`)면 가능 | **M6에서만**, GT3 통과 시 |
| initiator(연결을 연 쪽이 양측 부담) | 불가(v1 Source가 `"*"/cluster`) | 방향을 아는 소스(ztunnel, OBI `direction`)만 가능 | **만들지 않음.** 실사용자 요청과 해당 B 소스가 둘 다 생길 때 검토 |

**공정성**
- 인기 있는 공유 서비스의 팀이 호출자 트래픽 비용까지 떠안는 문제는 사용자의 FinOps 플랫폼 공유비용 재배분에 맡깁니다(CloudZero Allocate By Rules/Streams, Finout Shared Cost Reallocation, Vantage allocation virtual tag, Cloudability Cost Sharing).
- crossaz는 caller-modeled 비율을 함께 보여 줍니다.

### 9.4 산식

**행 필드 (§3.4 `Weight`에 반영)**
- M3에서 `SideRole string // "endpoint" | "caller-modeled"`를 추가합니다.
- `Namespace/Workload`는 **소유(엔드포인트) 워크로드**로 고정합니다. 이전 계획이 정의하지 않았던 caller-modeled 행도 같습니다. BY WORKLOAD의 의미는 바뀌지 않습니다.
- M6에서 `SideRole "pair"`와 `Consumer WorkloadRef`를 추가합니다. 빈 값은 모름입니다.

**배분 (§3.5 T1 그대로)**
```
노드-일-측 (i,d,s): B, Gb = 청구 USD, GB;  Gm = Σ w.GB;  k = Gb/Gm;  u = B/Gb
a(r) = w.GB × min(k, K_CAP) × u                   (K_CAP = 1/VerifyRatioLow = 2)
행과 버킷은 (i,d,s)마다 최대 잔여법으로 센트 반올림(동률은 PathKey 사전순), 합 = B 센트
```

**팀 뷰 (정책 P는 행마다 요소 하나를 돌려주는 전함수)**
```
T_t(P) = Σ_{r: P(r)=t} a(r)
요소:    __crossaz_unmapped__ | __crossaz_unconfirmed-rule__ | __crossaz_policy-unresolved__ (M6)
불변식:  Σ_t T_t + Σ 요소 + Σ Bucket.USD = Σ 범위 내 청구 USD   (날마다, 정수 센트, 모든 정책)
```
행을 나누거나 복제하지 않으므로 양측 과금이 두 번 세어지지 않습니다. 노드 측 행은 이미 하나씩입니다.

**"범위 내 청구 라인"** 정의는 §3.4에 고정했습니다(usage type이 `DataTransfer-Regional-Bytes`로 끝나는 모든 라인, 적용 집합은 JSON `scope`).

**팀 표시 지표 (합계에는 넣지 않음)**

| 지표 | 식 |
|---|---|
| `modeledUsd` | Σ w.GB × u (k=1일 때의 금액) |
| `exposureUsd` | Σ R(i,d,s) × a_t/A(i,d,s) (model-residual 비례 몫) + owner가 t인 경로의 unknown-rule 버킷 + owner가 t인 unconfirmed-rule 요소 |
| `E_t` | exposureUsd / (T_t + exposureUsd) |
| `inRangeShare` | T_t 중 k ∈ [VerifyRatioLow, 1/VerifyRatioLow]인 (i,d,s) 행의 몫 |
| `fixableUsd` | T_t 중 `Fix.Command != ""`인 경로의 몫 |
| `callerModeledUsd` | SideRole=caller-modeled 행의 몫 |

**팀 신뢰도 (`model.Level`, 닫힌 날만)**
- **high:** M6에서만. billed(calibrated) 행이 T_t의 90% 이상이고 medium 조건을 모두 만족.
- **medium:** 다음을 모두 만족.
  - `E_t < 0.10`
  - `inRangeShare ≥ 0.80`
  - `VerifyRatioLow ≤ T_t/modeledUsd ≤ VerifyRatioHigh(1.5)`. K_CAP 아래에서 최대 2배까지 조용히 더 부과되는 경우를 잡기 위한 조건입니다.
- **low:** 그 외. **A의 상한은 medium입니다.**
- 상수: `TeamExposureMax=0.10`, `TeamInRangeMin=0.80`, `TeamCalibratedMin=0.90`. M6 트리거의 10%와는 의미가 달라 별도 상수로 둡니다.

**tier별 동작**
- **Tz:** 같은 식을 AZ-일 키로 적용합니다. inRangeShare도 AZ 단위로 계산합니다.
- **T0:** 워크로드 배분이 없으므로 TEAMS 달러를 내지 않습니다. 경로 표의 TEAM 열만 보여 주고, `--team-csv`는 exit 2로 거부합니다.
- **B(M6):** 트리거된 노드-일-측의 `w.GB`만 pair 바이트로 바꿉니다. k, K_CAP, 버킷은 그대로입니다. 그래서 팀 행에는 A 행과 B 행이 섞일 수 있고, 그 비율을 출력합니다.

### 9.5 계산 예

**tier A 설정 (하루, 측당 $0.01/GB, u = 0.01)**
- 노드: i-1(AZ a), i-2(AZ b)
- namespace: `pay`는 team=payments, `shop`은 storefront, `tools`는 라벨도 매핑도 없음
- 경로
  - P1 `pay/api` clusterip-cross-az(high, PreferClose). 엔드포인트는 i-2, 실제 호출자는 i-1의 `shop/web`.
  - P2 `tools/cache`(service, high). 엔드포인트는 i-1, 실제 호출자는 i-2의 `pay/worker`.
- 청구: i-1 Out 500GB/$5, i-1 In 700/$7, i-2 In 500/$5, i-2 Out 700/$7. 합계 **$24.00**.

**tier A 배분**

| 노드·측 | 가중치 GB | Gm | k → min(k,2) | 배분 | residual |
|---|---|---|---|---|---|
| i-1 Out | P1 caller 300 · P2 endpoint 100 | 400 | 1.25 | P1 3.75 · P2 1.25 | 0 |
| i-1 In | P1 caller 210 · P2 endpoint 70 | 280 | 2.5 → 2 | P1 4.20 · P2 1.40 | 1.40 |
| i-2 In | P1 endpoint 300 · P2 caller 100 | 400 | 1.25 | P1 3.75 · P2 1.25 | 0 |
| i-2 Out | P1 endpoint 210 · P2 caller 70 | 280 | 2.5 → 2 | P1 4.20 · P2 1.40 | 1.40 |

**owner 합계**

| 요소 | 금액 |
|---|---|
| payments | 15.90 |
| `__crossaz_unmapped__`(tools) | 5.30 |
| model-residual | 2.80 |
| **합** | **24.00 = 청구** |

**payments 팀 지표**

| 지표 | 계산 | 값 |
|---|---|---|
| modeledUsd | 1020GB × 0.01 | $10.20 |
| T/modeled | 15.90 / 10.20 | 1.56, 1.5 초과 |
| exposure | 1.40×4.20/5.60 × 2 | $2.10 |
| E | 2.10 / 18.00 | 11.7% |
| inRangeShare | 7.50 / 15.90 | 47% |
| callerModeled | | 50% |
| fixableUsd | | $15.90 |

- 결과: **low**. 청구의 66%를 차지하므로 §9.6 T-team 트리거가 켜집니다.
- unmapped가 22%로 10%를 넘으므로 GT1 경고를 냅니다.
- G6-팀 합계는 $2.80으로 $15.90보다 작아 순위를 표시합니다.

**tier B (i-1, i-2 모두 실측, k=1)**
- 트래픽: web→api 요청 400GB, 응답 600GB. worker→cache 요청 100GB, 응답 100GB.

| 요소 | owner | consumption(M6) |
|---|---|---|
| payments | 20.00 | 12.00 |
| storefront | 0 | 10.00 |
| `__crossaz_unmapped__` | 4.00 | 2.00 |
| **합** | **24.00** | **24.00** |

A의 payments $15.90는 실측 $20.00보다 작습니다. low 신뢰도가 이를 미리 경고했습니다. 정책에 따라 storefront는 $0 또는 $10이 되므로 정책을 먼저 정해야 합니다.

**unconfirmed-rule 예 (i-3 In, B=$10.00, Gb=1000GB)**
- 행
  - P1 `pay/api` endpoint 600GB
  - P4 `search/es-proxy` hints-unverified(low) 200GB
  - P5 `growth/feed` Billable=unknown 100GB
- Gm=900, k=1.111. 원시 센트 666.67 / 222.22 / 111.11을 반올림하면 667 / 222 / 111입니다.
- 팀 뷰
  - payments $6.67
  - `__crossaz_unconfirmed-rule__[hints-unverified]` $2.22. search 팀 USD는 $0이고 exposure가 $2.22입니다.
  - unknown-rule 버킷 $1.11. growth 팀의 노출액으로 표시합니다.
- 합계 $10.00.

### 9.6 출력, 연동, B 트리거

**`bill`의 TEAMS 섹션**
```
TEAMS  policy=owner  청구 배분(모델 가중치)  tier=T1-node-day  2026-09-10  cost=net_unblended  resolved=2026-09-15T02:00Z
TEAM       USD     MODELED  FIXABLE  EXPOSURE  IN-RANGE  CONF  SOURCE           TOP FIX
payments   $15.90  $10.20   $15.90   $2.10     47%       low   namespace-label  pay/api PreferClose
UNALLOCATED  __crossaz_unmapped__ $5.30 (tools: no-label → kubectl label namespace tools team=<TEAM>) · model-residual $2.80
CHECK  Σteam + Σ요소 + Σ버킷 = $24.00 = bill
B 권장: payments (share 66%, E 11.7%, in-range 47%) → i-1 In, i-2 Out
```
- **G6-팀:** (unknown-rule + node-unexplained + model-residual + unconfirmed-rule) > 1위 팀 USD이면 팀 순위를 숨기고 이름순으로 표시하며 경고를 냅니다. unmapped는 라벨 문제라 넣지 않고 GT1 경고로 처리합니다.
- **`bill --team NAME`:** 그 팀의 전체 경로 목록을 출력합니다(경로, 규칙, Fix 종류, 명령, USD 내림차순).
- **`scan`과 `explain`(M1):** 경로마다 TEAM 열과 `team: <값> (source: …)` 한 줄을 추가합니다. 팀별 합계는 내지 않습니다.

**JSON `teams[]` (날마다 한 행)**
- 행 필드: `day, tier, costColumn, policy, keyKind(team|element|bucket), key, detail, usd, gb, modeledUsd, fixableUsd, callerModeledUsd, exposureUsd, inRangeShare, confidence, teamSource, paths[{key, ruleId, fixKind, command, usd}], suspectTeams[]`
- 최상위: `teamLabel, teamMap, teamsResolvedAt, scope`

**`bill --team-csv` (표 대신 stdout, `--json`과 같은 모드 플래그라 새 로컬 쓰기 없음)**
```
date,amount,team,cluster
2026-09-10,15.90,payments,prod-a
2026-09-10,5.30,__crossaz_unmapped__,prod-a
2026-09-10,2.80,__crossaz_model-residual__,prod-a
```
- 규칙
  - owner 정책 고정입니다.
  - 닫힌 날만 냅니다(not-final 제외).
  - (date, team, cluster)당 한 행입니다.
  - amount가 0 이하인 행은 쓰지 않고 경고합니다. CloudZero가 0을 거부합니다.
  - 날짜마다 모든 비영 요소와 버킷을 포함하므로 날짜별 합이 범위 청구 센트와 같습니다.
  - detail은 JSON에만 둡니다.
- 버킷도 원소로 냅니다. 이름은 `__crossaz_<BucketKind>__`(예: `__crossaz_model-residual__`)이고, 같은 날 같은 종류의 버킷은 한 행으로 합칩니다. 빼면 플랫폼이 잔차를 팀들에 흩뿌립니다.
- amount가 USD인 이유: 노드별 k와 cap이 반영된 비율이어야 플랫폼의 비례 재분할이 crossaz 결과와 같아지기 때문입니다.

**가져오기 경로 (landscape §4 기준)**
- 채택 조건 1: 플랫폼이 이미 가진 청구 줄을 비율로 재분할하는 비가산 경로일 것.
- 채택 조건 2: 파일로 넘길 것.

| 경로 | 판정 | 근거와 [미검증] |
|---|---|---|
| Vantage Business Metrics → Business-Metric-Based allocation virtual tag | **1순위**(CSV 그대로) | date, amount, label 열 ≤100, 일 단위, Virtual Tagging은 Pro 이상. [미검증] 비용 집합을 Regional-Bytes usage type으로 좁힐 수 있는지. 25MB 한도와 date+label 중복 거부는 sweep마다 결과가 다름. Vantage K8s network cost를 같이 쓰면 이중계산 |
| Finout S3 CSV Telemetry → telemetry 기반 Shared Cost Reallocation | **2순위**(열 매핑만) | date 열 1, metric 열 1, dimension ≤10, 매일 재계산, 누락 시 fallback. [미검증] 필수 열 이름, Finout 내장 K8s 네트워크 분할(pod 총 바이트)과 재배분의 상호작용 |
| CloudZero Allocation Telemetry(`replace_period`) | README 변환 레시피만 | 레코드 스키마 확인됨. 0 거부, 배치에 빠진 대상 삭제. [미검증] filter로 InterZone usage type을 정확히 고를 수 있는지. 요청 제한은 sweep 간 차이 |
| Cloudability Telemetry CSV(Date, Category, Value) | 수동 안내만 | UI 업로드만 가능, API 업로드 문서 없음 |
| Umbrella Telemetry Pipeline | 보류 | Account ID 열이 필요 |
| Datadog Dynamic by metric | 제외 | Enterprise 전용, 메트릭 쓰기 필요, 팀 값을 규칙마다 나열, CNM 기본 DT 배분과의 상호작용 미문서 |
| 가산형 달러 행(Datadog Custom Costs, Vantage Custom Providers, Harness, Flexera CBI, Umbrella BYOD, Finout Custom Cost Centers, DoiT DataHub) | 제외 | 이미 들어온 CUR 줄 위에 더해져 이중계산 |
| FOCUS 1.4 Allocated* | 제외 | 소비자 미확인. 원천 생성자가 아닌 제3자 행의 적합성이 불명확. AWS export는 1.2 |
| AWS Cost Categories FIXED split | 제외 | 쓰기 API, 소스 값당 월 단일 비율, 결과가 details 페이지에만 표시 |
| OpenCost/Kubecost plugins | 제외 | custom cost의 labels가 내부에서 버려짐 |
| Backstage Cost Insights | 코드 없음 | 사용자가 JSON을 읽어 `CostInsightsApi`를 구현하면 됨 |

**B 트리거 (§4 M6 기존 트리거에 OR로 추가)**
1. **T-team:** 닫힌 14일 기준으로 다음을 모두 만족하는 팀이 있으면 켭니다.
   - T_t가 범위 청구의 5% 이상(`TeamMaterialShare`)
   - 월 환산 $100 이상(`TeamTriggerMinUSDPerMonth`)
   - 신뢰도 low
   - 대상은 그 팀의 노출이나 범위 밖 k를 만든 상위 노드-일-측입니다.
2. **T-consumption:** `--team-policy consumption`(M6)을 요청했는데 policy-unresolved가 범위 청구의 10%를 넘으면 exit 2로 거부하고 "B 필요"를 안내합니다(GT3).
3. **트리거가 아닌 것**
   - unmapped: 라벨로 해결합니다.
   - Tz/T0 강등: 청구 키 문제입니다.
- **B 소스 안내는 M6부터입니다.** 기존 Istio ztunnel, OBI/Beyla, Coroot 메트릭이나 VPC Flow Logs를 씁니다. 없으면 Fix 텍스트로 기존 에이전트를 안내만 합니다. M3에서는 소스를 자동으로 탐지하지 않습니다.

### 9.7 마일스톤 영향과 코드

| M | 추가 | 주 |
|---|---|---|
| M1 | 라벨 키 값 복사(pod, namespace), `--team-label`, `--team-map` strict 파싱(exit 2), 팀 해석, `scan`/`explain` TEAM 열. [F] 파싱 오류 줄 번호, 과반/동률/gone/invalid goldens, map-라벨 불일치 경고, Namespaces Forbidden이면 경고만 | 1.5 → **2** |
| M2 | fixture에 `Pod.Labels`(설정 키만), `Namespaces`, `TeamLabel`. Anonymize는 팀 값을 namespace 이름처럼 유지 | 1 |
| M3 | `Weight.SideRole`, caller-modeled 소유 워크로드 고정, 센트 최대 잔여법, `allocate.RollupTeams`(owner), unconfirmed-rule, 지표와 신뢰도, TEAMS/`--team`/JSON/`--team-csv`, G6-팀, GT0/GT1, T-team 계산. [F] §9.5 두 예시 센트 일치, 모든 fixture 보존식, CSV(0 이하 없음, 유일성, 날짜 합 = 청구, not-final 제외, T0 exit 2). [R] 실 클러스터 unmapped 비중과 상위 namespace. 공수가 넘치면 `--team-csv`를 M4로 먼저 미룸 | 3 → **4** |
| M4 | 일별 JSON에 `workload→team`과 `teamsResolvedAt` 저장, 닫힌 날 고정 | 1 |
| M5 | 새 family 규칙은 버킷으로만 가므로 팀 영향 없음. [F] 열거 테스트: Row를 만드는 모든 과금 규칙이 owner 파생 표에 들어감 | 1.5 |
| M6 | `Weight.Consumer`, `--team-policy consumption`, policy-unresolved, GT3, B 소스 안내. [F] §9.5 tier B golden. [K] Istio 또는 OBI generator로 consumption 팀이 generator 달러의 ≥95% | 3 → **3.5** (+PoC 2) |

**코어 M0–M5 합계: 9주 → 10.5주.** (§4 요약 표와 같습니다.)

**코드 변경**
- `internal/model/snapshot.go`: `Pod.Labels map[string]string`(설정 키만), `Snapshot.Namespaces []Namespace{Name, Labels}`(M1 istio 키와 공유), `Snapshot.TeamLabel`
- `internal/collect/k8s.go`: `NewK8s(cs, labelKeys ...string)`. 가변 인자라 기존 호출부는 그대로 컴파일됩니다.
- `internal/classify/teams.go`(신규, 순수 함수, M1): `LoadTeamMap`, `ResolveTeams`
- `internal/allocate/teams.go`(신규, M3): `RollupTeams(result, resolution, policy)`
- `internal/report`: TEAM 열, TEAMS 섹션, `teams[]`, `teamcsv.go`(`encoding/csv`)
- `cmd/crossaz/flags.go`
  - M1: `--team-label`, `--team-map`
  - M3: `--team`, `--team-csv`
  - M6: `--team-policy`

**변경 없음:** `rules/rules.yaml`, `internal/classify` 규칙 엔진, IAM, RBAC(namespaces list는 M1에 이미 계획됨).

### 9.8 만들지 않을 것

| 항목 | 이유 | 이미 하는 곳 | 재검토 조건 |
|---|---|---|---|
| UI, 예산, 알림, 팀 RBAC | 표시 계층 대체재가 많음 | 모든 FinOps 플랫폼, Backstage, Kubecost Teams(Enterprise) | 없음 |
| 태그 거버넌스, virtual tag, 라벨 강제 | 읽기 전용 원칙 | CloudZero Dimensions, Finout/Vantage Virtual Tags, Harness Cost Categories, AWS Cost Categories | 없음 |
| 공유비용·버킷 재배분 엔진, 조직 계층 | 플랫폼 기본 기능 | CloudZero, Finout, Vantage, Cloudability, Harness, Kubecost Collections, AWS split charge | 없음 |
| compute·storage·idle 배분 | 범위 밖 | SCAD(pod 라벨 50개), Kubecost, OpenCost | 없음 |
| SaaS API 직접 푸시, 벤더 어댑터 | 쓰기 자격증명 필요, §3.3 읽기 전용 경계와 §3.1 어댑터 결정에 어긋남 | 사용자 cron | 없음 |
| `Rule.Owner` 스키마, 객체 단위 매핑 | 현재 규칙은 target_kind와 신뢰도로 충분(YAGNI) | — | pod 행을 만드는 규칙 중 파생이 틀리는 것이 생길 때 |
| initiator 정책 | 요청자 없음. A에서는 계산 불가 | — | 실요청 + 방향을 아는 B 소스 |
| A 모드 consumption 열 | 절반이 미해결인 두 번째 팀 숫자 | — | M6 |
| `scan` 팀 합계 | 팀 달러 숫자가 둘이 됨 | — | 없음 |
| FOCUS Allocated* 출력, 가산 달러 가져오기 | 적합성 불명, 이중계산 | — | 벤더가 가져오기에서 Allocated* 사용을 문서화할 때 |
| 라벨 이력 소스, 여러 라벨 키, 컨트롤러 라벨 | YAGNI, KSM allowlist 필요 | — | 실요청 |
| 한 실행에서 여러 클러스터 합산 | 이중계산 위험 | — | 같은 계정·리전 다중 클러스터 요청 |

### 9.9 리스크와 게이트

**게이트** (§6 게이트 표에도 같은 행이 있습니다)

| 게이트 | 거는 곳 | 통과 | 실패 |
|---|---|---|---|
| GT0 보존(날마다 정수 센트, CSV 날짜 합 포함) | M3 [F] 전 fixture | 팀 출력 | 빌드 실패 |
| G6-팀 | M3 표시 규칙 | 순위 표시 | 이름순 + 경고. CSV는 버킷 원소 포함해 출력 |
| GT1 unmapped > 범위 청구의 10% | M3 | — | 경고 + 상위 namespace와 라벨 명령 |
| GT2 가져오기 일치: 실제 테넌트의 닫힌 하루에서 팀 비율 ±1%p, 버킷 원소가 이름 그대로 보이고, §9.6의 [미검증] 항목 확인 | M3 이후 [R], 선택, 플랫폼별 | README "검증됨(날짜)" | "형식 호환(미검증)" 유지 |
| GT3 policy-unresolved ≤ 10% | M6 | consumption 금액과 CSV | exit 2, owner 안내 |
| T-team | M3 계산, M6 착수 판단 | — | B 권장 |
| G3/G5(기존) | M6 | 팀 high 가능 | medium 이하 |

**리스크** (주요 행은 §6 리스크 표에도 옮겼습니다)

| 리스크 | 완화 |
|---|---|
| 틀린 chargeback은 틀린 추정보다 신뢰를 크게 해침 | A 라벨 "청구 배분(모델 가중치)" 고정, 상한 medium, T/modeled 비율, T-team |
| owner 정책이 공유 서비스 팀에 불공정하게 느껴짐 | callerModeled 비율 표시, 플랫폼 공유비용 재배분 안내, M6 consumption |
| 단측 서비스(RDS 등)로 가는 바이트가 endpoint pod의 tx에 섞여 그 서비스 팀이 과다 부과됨 | K_CAP로 피해 한정, T/modeled 조건. 같은 zone에 과금 단측 경로가 있는 노드-일은 inRange에서 제외하는 방안을 M3 [R] 결과로 결정 |
| 저신뢰 규칙 비중이 크면(kube-proxy 대체 클러스터) 팀 금액이 줄어듦 | unconfirmed-rule을 이름 있게 표시, G6-팀, AWS 자격증명 재실행 Fix, B 권장 |
| 라벨 위생 불량 | 이름 있는 unmapped, GT1, 매핑 파일 |
| 창 안에서 라벨이 바뀌어 과거 날짜가 재매핑됨 | M4 고정, `teamsResolvedAt` |
| 플랫폼 필터 범위 ≠ crossaz 범위 | JSON `scope`, usage type 수준 범위, 버킷 원소 포함, GT2 |
| 같은 계정·리전의 여러 클러스터 CSV를 합쳐 이중계산 | 계정·리전당 클러스터 하나만 지원 명시, not-in-cluster > 10%이면 경고 |
| 플랫폼 내장 네트워크 분할(Finout pod 바이트, Vantage conntrack, Datadog CNM)과 겹침 | README에 "플랫폼 내장 K8s 네트워크 배분을 끄거나 InterZone 줄을 제외한 뒤 사용", GT2 |
| 크레딧 등으로 음수나 0인 날 | CSV에서 빼고 경고. 보존 검사는 JSON에서 |
| 공수(코어 +1.5주, 1인 유지보수) | 모든 계산은 그룹핑. 규칙·수집·IAM 불변. CSV를 M4로 미룰 수 있음 |

---

**참고한 로컬 파일**
- docs/team-allocation-landscape.md
- internal/model/snapshot.go
- cmd/crossaz/flags.go (v1은 `--json`만 있고 `--format` 없음)
- docs/v2-eni-sufficiency.md
- docs/billing-rules.md
- docs/measurement-alternatives.md
- docs/design.md
- internal/collect/ce.go
- internal/collect/collect.go
- internal/model/types.go (VerifyRatioLow = 0.5, VerifyRatioHigh = 1.5)
- internal/estimate/estimate.go (bytesPerGB = 1e9)
- rules/rules.yaml