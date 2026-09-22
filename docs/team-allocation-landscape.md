# crossaz v2: 팀별 비용 배분 도구와 교차 AZ 전송비 처리 비교 (2026-09-15 조회)

이 문서는 "클라우드·Kubernetes 비용을 팀별로 나누는 일을 누가 전문으로 하고, 교차 AZ 데이터 전송비는 얼마나 잘 다루나"에 답합니다. 네트워크 **측정** 방법론(Kubecost, OpenCost, Datadog CCM, Vantage, Finout, Coroot, NFM의 관측 지점과 바이트 의미)은 docs/v2-plan.md §2와 docs/measurement-alternatives.md에 있으므로 다시 적지 않습니다. 여기서는 **팀 차원**(팀 매핑, 공유비 분할, 네트워크 달러의 팀 귀속, 외부 배분 데이터 가져오기)만 봅니다. 설계 결정은 docs/v2-plan.md §9에 있습니다.

**표기**
- **[미조회]**: 뒷받침하는 1차 출처를 이번에 조회하지 못한 사실입니다. 조회 실패(403, 404, DNS), 검색 스니펫·2차 보도·마케팅만 있음, 또는 조회한 1차 문서에서 찾지 못함이 여기에 해당합니다.
- **[미검증]**: 출처는 조회했지만 crossaz 연동에 필요한 동작이 문서화되지 않아 실제 테넌트에서 확인해야 하는 항목입니다(v2-plan §9.9 GT2 대상).
- **미문서**: 벤더 문서를 읽었고, 그 문서가 해당 방법을 설명하지 않는다는 것 자체가 확인된 사실입니다.
- 출처 키 `[xx]`는 §10에 URL과 버전으로 풀어 적었습니다.

---

## 0. 결론: 팀별 비용을 전문적으로 나누는 곳이 있나

**있습니다.** 다만 "팀으로 나누기"와 "교차 AZ 달러를 팀으로 나누기"는 다른 문제입니다.

- **팀 매핑과 공유비용 분할은 범용 기능입니다.** CloudZero(Dimensions, Streams), Finout·Vantage(Virtual Tags), Harness CACM(Cost Categories), IBM Cloudability(Business Mappings, Cost Sharing), Flexera(Billing Centers), Umbrella, DoiT, nOps, Ternary, Kion, Rafay, IBM Kubecost(Collections), OpenCost(`team` 라벨), AWS Cost Categories가 합니다(§3).
- **교차 AZ 달러를 팀까지 가져가는 곳은 적고, 모두 한계가 있습니다.**
  - **청구 배분형(합계가 청구와 맞음):**
    - Datadog CCM이 가장 완성도가 높습니다. 모든 AWS 호스트에 CNM이 필요하고, 가중치 정의가 비공개이며, 미모니터 몫은 n/a입니다 [dd-cca].
    - Finout과 Umbrella는 AZ 신호 없는 pod 바이트로 나눕니다 [fo-k8s][um-net].
    - Cloudability는 2025-02부터 DT 라인을 namespace에 rx/tx로 나눕니다. 라인 범위는 미문서입니다 [cld-new25][cld-cont].
    - Red Hat Koku는 노드 매칭 DT를 CPU/메모리로 나눕니다. OpenShift만 됩니다 [koku-dist].
    - Kubecost는 cloud integration 시 tx 바이트로 나눈다고 문서에 적혀 있지만 문서 간 불일치가 있습니다 [kc-nettraffic][kc-net].
  - **흐름 가격형(요율 × 바이트, 청구 true-up 없음):**
    - Spot Ocean(Flexera)은 양측에 부과합니다. privileged DS가 필요하고 Ocean 관리 노드만 됩니다 [ocean-ca].
    - CAST AI는 allocation group별로 냅니다(provider 가격) [cast-api].
    - Kubecost는 egress만 보고, 기본값이 RFC1918을 in-zone으로 봐서 multi-AZ가 $0이 됩니다 [kc-values].
    - Vantage는 cross-AZ를 in_region에 합칩니다 [va-net].
    - Coroot는 목록가를 쓰고 RDS를 2배로 셉니다(v2-plan §2).
    - Tollwing은 namespace까지만 롤업합니다 [tw].
  - **네트워크 방법 없음(규칙이나 비율 분할만):** CloudZero, Harness, nOps, Ternary, Cloudchipr, OptScale, Kion, AWS SCAD, GKE cost allocation
  - **알 수 없음:** New Relic CCI, CloudHealth(문서 간 모순), Grafana Cloud, DoiT Attribute, Kentik, CloudBolt, Amnic, Mavvrik, Komodor
- **조사 범위에서 없었던 것:** 과금 측·무료 경로 규칙, 노드-일 대조, 이름 있는 잔차를 팀 금액까지 가져가는 도구. 이 빈자리가 docs/v2-plan.md §9의 자리입니다.

**분류표**

| 분류 | 합계와 청구 | 도구 | 공통 한계 |
|---|---|---|---|
| 청구 배분형 | 맞음(범위는 도구마다 다름) | Datadog CCM, Finout, Umbrella, IBM Cloudability, Red Hat Koku, IBM Kubecost 3.x(문서상, 불일치) | 가중치가 AZ를 모르거나 비공개. 과금 측·무료 경로 규칙 없음 |
| 흐름 가격형 | 안 맞음(요율 × 바이트) | Spot Ocean, CAST AI, Kubecost·OpenCost network-costs, Vantage K8s·NFR, Coroot, Tollwing | 청구 true-up 없음. 대부분 privileged 또는 sysctl 설치 |
| 네트워크 방법 없음 | 청구 줄을 통째로 규칙·비율로만 | CloudZero, Harness CACM, nOps, Ternary, Cloudchipr, OptScale, Kion, AWS SCAD·Cost Categories, GKE cost allocation, AKS cost analysis, Rafay | 교차 AZ 달러를 pod 신호로 나누지 않음 |
| 알 수 없음 | — | New Relic CCI, CloudHealth, Grafana Cloud, DoiT Attribute, Kentik, CloudBolt, Amnic, Mavvrik, Komodor, Pelanor, PointFive | 방법 미문서이거나 문서끼리 모순 |

**질문별 짧은 답**
- **팀별 배분이 본업인 곳:** FinOps 플랫폼(CloudZero, Finout, Vantage, Harness CACM, IBM Cloudability, Flexera One, Umbrella, DoiT, nOps, Ternary, Kion)과 K8s 특화 도구(IBM Kubecost, OpenCost, CAST AI, Rafay)입니다. 관측 벤더(Datadog, New Relic, Grafana)도 비용 배분을 붙였습니다.
- **교차 AZ를 얼마나 잘하나:** 팀 매핑은 모두 성숙했지만, 교차 AZ 달러의 팀 귀속은 "청구와 맞지만 가중치가 거칠거나 비공개" 아니면 "가중치는 세밀하지만 청구와 안 맞음" 중 하나입니다. 양측 과금·무료 경로·RDS 클라이언트 측 과금을 규칙으로 다루는 도구는 없었습니다(무료 S3 gateway endpoint 버킷을 둔 Vantage, 양측 부과를 문서화한 Spot Ocean이 부분 예외).

---

## 1. 조사 방법

- **조회일:** 2026-09-15. 네 sweep(finops-platforms, k8s-native, cloud-native-standards, network-to-team)과 추가 조사(Red Hat Koku, Cloudability 네트워크, Backstage, Kion, CloudBolt, OptScale, Amnic, Mavvrik, FOCUS 1.4)를 했고, 각 sweep의 핵심 주장은 1차 출처로 다시 확인했습니다(confirmed / corrected / unverifiable).
- **1차 출처 우선:** 벤더 문서, GitHub 소스(`gh api`, raw 파일), AWS 문서, FOCUS_Spec 저장소. 보도·검색 스니펫·마케팅만 있는 사실은 본문에 그렇게 적었습니다.
- **`crossAzToTeam` 등급**
  - **yes:** 교차 AZ 달러를 워크로드나 팀에 기본 기능으로 귀속합니다.
  - **partial:** 네트워크 달러를 워크로드에 귀속하지만 cross-AZ가 다른 버킷에 섞였거나, 롤업이 namespace까지이거나, 방법이 빈약합니다.
  - **no:** 청구 행을 규칙·지출 비례·균등·고정 %로만 나눕니다. 사용자가 텔레메트리를 넣어야 팀으로 갈 수 있는 경우도 no입니다(가져오기 경로는 §4에 따로 적음).
  - **unknown:** 방법이 미문서이거나 문서끼리 모순입니다.
- **분류(방식)는 등급과 별개 축입니다.** 청구 배분형은 청구 달러를 분모로 나누고, 흐름 가격형은 관측 바이트에 요율을 곱합니다.
- **버전과 날짜:** 조회일은 모두 2026-09-15이므로 표에는 도구 버전·릴리스일·문서 갱신일만 적었습니다.
- **[미조회]와 [미검증]:** 머리말 정의를 따릅니다. 전체 목록은 §9입니다.

---

## 2. 교차 AZ → 팀 처리 비교표

| 도구(버전) | 등급 | 방식 | 가중치 | 양측 처리 | 무료 경로 | 청구 true-up | 미배분 표시 | 팀 롤업 방식 | 설치·권한 | 가격·라이선스 | 출처 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| Datadog CCM + CNM (문서 소스 커밋 2026-09-04) | yes | 청구 배분 | 노드의 CUR DT 달러를 그날 워크로드에 "network traffic volume"으로. 송신·수신·양쪽 여부 미문서 | InterZone-In/Out 짝짓기 미문서 | AWS 절에 대상 usage type 목록 없음. NAT·LB·VPN·PrivateLink 제외 목록은 Azure/GCP 절 | 노드 단위로 구성상 일치(모니터된 몫) | CNM 없는 호스트는 `allocated_spend_type:not_monitored`, n/a(이름 없음) | 워크로드 태그 전달(pod 태그가 노드 태그보다 우선). 표준 워크로드 6종만, 커스텀 리소스는 클러스터 수준. 라벨은 labels-as-tags 필요 | Agent·Cluster Agent 7.58.0+, 모든 AWS 호스트에 CNM(eBPF, 유료 add-on), CUR | 목록가(연간 결제): CCM Pro $5·Enterprise $10 / 클라우드 지출 $1,000·월, CNM $5/호스트·월 | [dd-cca][dd-price] |
| Spot Ocean (Flexera) (ocean-network-client 1.1.6 / app 1.0.14) | yes | 흐름 가격 | eBPF pod flow 바이트를 Internet / Inter-Region / Inter-AZ / Intra-AZ로 분류 | **양측 부과**: 출발·도착 워크로드가 각각 Inter-AZ 비용을 받음 | 미문서. 같은 AZ라도 공인 IP 경유(DNS, LB, NAT, TGW)는 inter-AZ처럼 가격 | 없음(provider 요율) | 미문서 | 워크로드, 라벨·annotation | privileged DaemonSet(hostNetwork, hostPID, NET_ADMIN+SYS_ADMIN), Ocean 관리 노드만, 클러스터당 약 $10/월 전송 오버헤드. AWS와 AKS | 견적 **[미조회]** | [ocean-ca][ocean-chart][ocean-install] |
| CAST AI (문서 2026-08-25, castai-kvisor 차트 1.165.3) | yes | 흐름 가격 | Kvisor eBPF netflow 바이트 | ingress·egress 둘 다 기록. AWS 측별 요율을 양쪽에 적용하는지 미문서 | 미문서. 인터넷·리전 간은 "아직 미추적" | 없음(provider 가격 데이터, CUR 언급 없음) | 그룹 밖·그룹 겹침 미문서 | allocation group(namespace·라벨), API가 그룹별 `interZoneBytes/Cost` 반환 | Kvisor DaemonSet + controller, egressd 제거 필요, VPC Describe용 IRSA. kvisor repo archived(2026-05-06) | 비용 모니터링 무료(문서), 자동화 유료 | [cast-net][cast-api][cast-ag][cast-repo] |
| IBM Kubecost 3.x (chart v3.2.4, 2026-08-21) | partial | 흐름 가격 + 청구 조정(문서 간 불일치) | network-costs conntrack egress GiB × 단일 단가. 3.x 트래픽 문서는 cloud integration 시 노드 네트워크 비용을 pod `container_network_transmit_bytes_total` 비율로 나눈다고 적음 | egress만, ingress 미가격 | 없음. 기본 in-zone에 127/8·169.254/16·RFC1918 전체 | `reconcile`·`networkCostAdjustment` 있음. pod 분배 방식 비공개. network-cost 페이지에는 CUR 조정 설명 없음 | `__unallocated__`, Collections "High not in Category Cost" | `aggregate=label:team`(기본 `team_label=team`), Collections(Category=Team, 겹치면 최고 우선순위 collection). Teams는 RBAC만(Enterprise) | network-costs DaemonSet 기본 활성(privileged, hostNetwork) | 상용. 무료 tier 기준 **[미조회]**, Enterprise 견적 | [kc-values][kc-alloc][kc-nettraffic][kc-net][kc-coll] |
| OpenCost v1.121.2 (2026-09-11) | partial | 흐름 가격 | Kubecost network-costs 메트릭 egress GiB × 전역 게이지 | egress만, ingress $0 | 없음 | 없음. `NetworkCostAdjustment` 필드는 있지만 청구로 채우는 코드 없음(코드 검색 기준) | `__unallocated__` | `aggregate=team`(`TeamLabel` 기본 `team`), label:/annotation: | OpenCost + Prometheus. 네트워크 바이트는 Kubecost 비공개 privileged 이미지 필요 | Apache-2.0 | [oc-agg][oc-alloc][oc-summary] |
| Finout | partial | 청구 배분 | 노드-시간 CUR `DataTransfer-Regional-Bytes` 달러 ÷ pod 총 바이트(in+out). 스스로 'best-effort weighting'이라 부름. K8s 메트릭에 출발·도착 AZ 없음 | InterZone-In/Out 구분 미문서 | 없음. Regional-Bytes 안의 비 교차 AZ 행(NAT 등) 처리 미문서 | 노드-시간 합계 일치 | node idle. hostNetwork 제외분은 idle로(beta, 2026-09 이후 생성 계정 기본). 네트워크 행에 pod idle 없음 | pod → namespace·라벨 → Virtual Tags | 기존 Prometheus/Datadog 메트릭을 고객 S3로(CronJob), CUR. 네트워크 에이전트 없음 | 약정 지출 tier 정액, 가격 비공개 | [fo-k8s][fo-prom] |
| Umbrella (구 Anodot Cost) (문서 2026-06-11) | partial | 청구 배분 | cAdvisor `container_network_receive/transmit_bytes_total` | 미문서 | 미문서. 풀을 이루는 DT 라인 미문서 | 미문서(노드 범위 미문서) | K8s Allocation Status(pod waste, node waste, actual usage, no K8s metrics) | Business Mapping viewpoint | Umbrella Prometheus agent(기존 Prometheus/Thanos 읽기), CUR. hostNetwork pod는 2026-05-14부터 제외(compute로 이동) | 가격 비공개 | [um-net][um-split] |
| IBM Cloudability (whats-new 2025) | partial | 청구 배분 | `network/tx:allocation`, `network/rx:allocation`(행별 TX/RX 사용량 비율). 에이전트는 kubelet stats/summary를 읽어 AZ·목적지 정보 없음 | 미문서 | 미문서 | 미문서 | IDLE RESOURCES 줄, `fairShare` 열 | Business Mappings(K8s 라벨 키 20개), Container Insights namespace·workload | IBM FinOps Agent(cloudability/metrics-agent 2.14.15, Apache-2.0, 10분마다 S3 업로드), CUR | 견적 | [cld-new25][cld-cont][cld-vendor][cld-agent] |
| Red Hat Lightspeed cost management (Project Koku, SQL 커밋 2025-12-02/08) | partial | 청구 배분 | 노드 resource_id가 일치하는 AmazonEC2 Data Transfer 행 → "Network unattributed" → 클러스터-일 pod effective CPU 코어-시간 또는 메모리 GB-시간 | In/Out 모두 캡처(usagetype·operation CASE) | 없음 | 노드 매칭분만 일치(비매칭 DT 행 미포함), 마크업 가산 | Network unattributed, Platform unallocated, Worker unallocated, Storage unattributed(이름 있는 프로젝트) | OpenShift 프로젝트, 태그 매핑. Platform 범주·Worker unallocated·Storage unattributed는 네트워크 분배를 받지 않음 | OpenShift + Cost Management Metrics Operator, CUR. OpenShift만 | 구독에 포함. Koku 저장소 라이선스 **[미조회]**(licenses/에 Apache-2.0·MIT, 최상위 LICENSE 미탐지) | [rh-new][rh-cm][koku-match][koku-sum][koku-dist] |
| Vantage K8s Network Cost (2026-08-06 출시, agent 1.3.3+ / chart 1.9.5+) | partial | 흐름 가격 | conntrack 송신 바이트를 in_zone, in_region(**교차 AZ 포함**), cross_region, internet, Unclassified Egress, S3 경로(gateway endpoint 무료, NAT GW, NAT instance, IGW)로 분류 | 송신만. 수신 측 InterZone-In 귀속 미기술 | S3 gateway endpoint 무료 버킷 | 없음. 월 blended 요율(provider 실제 DT 요금에서 파생). CUR DT·NAT 행과 이중계산 경고, 제거는 근사 | `__idle__` namespace, unknown / Unclassified Egress | pod·namespace 라벨(pod 라벨 allowlist 최대 10개) → Virtual Tags | 수집기 DaemonSet(hostNetwork, NET_ADMIN), 노드 sysctl `nf_conntrack_acct=1`(Vantage가 못 켬), Cilium kube-proxy 대체 미지원, Fargate·hybrid 불가 | 기능 추가 요금 없음. Virtual Tagging은 Pro($30/월) 이상 | [va-net][va-blog][va-agent][va-price] |
| Vantage Network Flow Reports | partial | 흐름 가격 | VPC Flow Logs 바이트, 카테고리 Public / Cross-AZ / Cross-region / Local / Unknown | 미문서 | 미문서 | 없음(CUR 파생 요율, best effort) | Unknown 카테고리 | pod 정보 없음. 리소스 태그 수준. NFR에 Virtual Tags가 적용되는지 **[미조회]** | VPC Flow Logs(S3, 필수 필드), AWS만, 대규모 환경 자동 샘플링 | tier **[미조회]**, 기본 보관 1주 | [va-nfr] |
| Coroot v1.26.1 (2026-09-14) | partial | 흐름 가격 | node-agent eBPF TCP payload(애플리케이션별 교차 AZ·인터넷 egress) | RDS에도 요율 → 2배(v2-plan §2) | 매칭 안 된 사설 목적지는 무료 | 없음(AWS 0.01/0.01 하드코딩) | 없음 | application category(`<namespace>/<app>` glob 또는 annotation `coroot.com/application-category`) | privileged node-agent(kernel 5.1+) | Apache-2.0 CE + Enterprise. 비용 기능이 Enterprise 전용인지 **[미조회]** | [co-cost][co-cat] |
| Tollwing v0.2.0 (2026-07-02, 저장소 생성 2026-06-19, star 7) | partial | 흐름 가격 | eBPF pod TCP 바이트(UDP는 플래그), kube-proxy DNAT 전 목적지 | `MeterTxRx`: 관측 노드에서 tx와 rx를 모두 계량 → fleet 합에 중복 없음 | 9개 AWS 청구 경로 목록가, 증명 못 하면 Unknown | 무료 tier 없음. CUR 대조는 Enterprise | Unknown | pod·namespace만, 팀 매핑 없음 | eBPF DaemonSet(권한·커널 요구 **[미조회]**) | Apache-2.0 core + Enterprise(early access) | [tw][tw-export] |
| Pelanor (Kela 인수 보도 2025-09-28) | partial | 미문서 | eBPF pod 네트워크 통계 + DNS/HTTP/SQL trace. 배분 전략의 원천 비용을 "Cross AZ Traffic" 하위 범주로 좁혀 메트릭으로 분할 가능 | 미문서 | 미문서 | 미문서 | — | 커스텀 dimension·segment, cluster > namespace > workload > endpoint | eBPF DaemonSet(권한 미문서) | 가격 비공개. pelanor.io 404, 제품 연속성 **[미조회]** | [pel-k8s][pel-strat] |
| DoiT Attribute (2026-07-07 출시) | unknown | 미문서 | eBPF 센서가 전송을 만든 워크로드를 식별한다고 주장(egress, cross-AZ, NAT) | 미문서 | 미문서 | 미문서 | — | 태그 없이 팀·고객·기능 COGS를 낸다고 주장, 공유 K8s·NAT·RDS를 사용량 비례로 나눈다고 주장(알고리즘 없음) | eBPF 센서(약 15분 배포, 벤더 주장) | 비공개 | [doit-attr][doit-prod] |
| Kentik | unknown | 미문서 | VPC/VNet flow logs의 청구 경계별 트래픽, CUR은 "reconciliation layer"(마케팅) | 미문서 | 미문서 | 미문서 | — | Custom Dimensions(태그 → dimension. Pro 4개·populator 5,000, Premium 10개·25,000) | Kappa eBPF agent(pod·namespace·workload 흐름, 비용 기능 없음) | 비공개 | [kentik-dim][kentik-agent][kentik-mkt] |
| New Relic Cloud Cost Intelligence (GA 2026-04-28) | unknown | 청구 기반으로 보이나 **[미조회]** | `KubernetesCost.network_net_unblended_cost`의 내용(inter-AZ 포함 여부)·가중치 미문서 | 미문서 | 미문서 | 미문서 | 미사용·미배분 가시성(방법 미문서) | K8s 이벤트의 cluster·namespace·pod 차원, `CloudCostEstimate.owning_team`. 규칙 기반 배분 엔진 의미 **[미조회]** | 기존 NR K8s 통합, 청구 export, Advanced CCU SKU | 청구 export 파일 크기 기준 | [nr-nrql][nr-ga][nr-overview] |
| CloudHealth by Broadcom | unknown | 미문서 | 미문서 | 미문서 | 미문서 | 미문서 | K8s Cost History: requests 기준은 100% 배분, usage 기준은 미사용분 미배분 | **문서 간 모순**: KB 416086은 Network Egress를 Namespace/Workload/Container에 배분하지 않는다고(perspective에서는 Namespace·Cluster 가능), 2025-09-30 업데이트는 컨테이너에 자동 배분한다고(agent 4.6.3+, 2025-08-01 이후 청구) | K8s collector(Helm) | 비공개 | [ch-kb][ch-upd][ch-k8s] |
| Grafana Cloud K8s Monitoring cost | unknown | OpenCost 상속 | 계산 방법 미문서("network egress 비용"만 언급) | egress만(OpenCost) | 없음 | 없음 | idle | cluster·namespace·workload·pod·container. 팀 라벨 그룹핑 **[미조회]**. "Cost attribution"은 Grafana Cloud 자체 청구만 대상 | k8s-monitoring 차트 4.5.2 + OpenCost(Alloy scrape) | 사용량 기반, 비용 기능 별도 요금 여부 **[미조회]** | [gc-costs][gc-attr] |

**얼마나 잘하나 (한 줄 평가)**
- **Datadog:** 조사 범위에서 교차 AZ 달러를 청구와 맞게 팀 태그까지 가져가는 가장 완성된 구현입니다. 대신 전 호스트 CNM 유료 설치가 전제이고, 가중치·In/Out·대상 usage type이 공개되지 않아 팀 금액을 외부에서 검증할 수 없습니다.
- **Spot Ocean:** 양측 부과를 문서화한 유일한 도구입니다. 목록가라 청구와 맞지 않고, Ocean 관리 노드와 privileged DS에 묶입니다.
- **CAST AI:** 그룹별 interZone 달러 API가 가장 직접적입니다. provider 가격이고, 인터넷·리전 간은 미추적이며, 에이전트 저장소가 archived입니다.
- **IBM Kubecost:** 팀 집계(label, Collections)는 성숙했습니다. 네트워크는 egress·단일 단가이고, 기본 CIDR이 multi-AZ를 $0으로 만들며, 청구 조정의 pod 분배는 비공개입니다.
- **OpenCost:** 팀 집계는 되지만 네트워크는 Kubecost 비공개 이미지에 의존하고, 청구 대조가 없으며, 공유비 파라미터가 HTTP API에 노출되지 않습니다.
- **Finout:** CUR 교차 AZ 줄을 노드-시간으로 나누는 방법을 가장 구체적으로 문서화했습니다. 가중치에 AZ가 없고 In/Out·NAT 행 처리가 미문서입니다.
- **Umbrella:** 청구 기반 pod 배분이지만 대상 라인과 노드 범위가 미문서입니다.
- **Cloudability:** 2025-02부터 DT 라인을 namespace까지 배분합니다. 가중치·라인 범위·AZ 인식이 미문서입니다.
- **Red Hat Koku:** 이름 있는 미배분 프로젝트와 분배 스위치는 좋은 선례입니다. 네트워크를 CPU/메모리로 나누고 OpenShift만 됩니다.
- **Vantage K8s:** cross-AZ가 in_region에 섞여 교차 AZ 팀 금액을 따로 뽑을 수 없고, 설치형입니다. **NFR**은 리소스 수준이라 팀까지 가지 않습니다.
- **Coroot:** 애플리케이션 카테고리까지 롤업하지만 목록가이고 RDS를 2배로 셉니다.
- **Tollwing:** 측별 계량 규칙은 청구 구조에 맞지만 namespace까지만 롤업하고, 대조는 유료이며, 매우 새 프로젝트입니다.
- **Pelanor:** 원천을 "Cross AZ Traffic"으로 좁혀 나누는 기능은 있지만 식별·가중치가 미문서이고 벤더 연속성이 불확실합니다.
- **DoiT Attribute, Kentik, New Relic CCI, CloudHealth, Grafana Cloud:** 판단할 수 없습니다(방법 미문서 또는 모순).

---

## 3. 팀 매핑과 공유비용 분할 (범용 기능)

| 도구 | 팀 매핑 모델 | 미매핑 표시 | 공유비 분배 | 이력 재매핑 | 외부 데이터 import(§4) | 한도·한계 | 출처 |
|---|---|---|---|---|---|---|---|
| CloudZero | CostFormation(YAML/Python)·Dimension Studio의 Dimensions. 태그, 계정, 서비스, K8s cluster/namespace/workload/label | "Not In Dimension". Default Value를 두면 이름 있는 원소(예: Other) | Allocate By Rules(Even, 기존 지출 비례), Allocate By Streams(텔레메트리 비례, filter로 대상 지출 한정, rate 기반은 하위 옵션). K8s는 청구 VM 비용을 시간 단위로 CPU·메모리·GPU에, 사용·요청 중 큰 값 평균 75% 미만은 Idle | AnyCost CBF 태그는 시간 독립(리소스별 최신 태그 집합만) → AnyCost 태그로 과거 재매핑 불가 | 비가산: Allocation Telemetry. 가산: AnyCost | K8s 라벨·annotation·태그 합계 300개(초과분 버림). 에이전트는 Apache-2.0 Helm, 데이터 약 48h 후 | [cz-split][cz-k8s][cz-tele][cz-cbf] |
| Finout | Virtual Tags: 키-값 규칙을 위에서 아래로 차례로, 태그 위의 태그 가능. K8s 메트릭은 cluster/node 라벨로 청구 노드에 조인(첫 매치) | 미태그 기본 처리 미문서. node idle은 "Idle Namespace" | Shared Cost Reallocation: Even, Custom %(합 100%), Telemetry 기반(매일 계산, 텔레메트리 누락 시 설정한 fallback). K8s 노드 $/h를 CPU:메모리(기본 88:12)로 | 재배분 가상 태그는 raw data에 없음. Custom Cost Center 과거 파일 수정 시 덮어씀 | 비가산: S3 CSV Telemetry. 가산: Custom Cost Centers, Custom Costs | requests만 있고 사용 메트릭 없는 pod는 Idle | [fo-vt][fo-scr][fo-csv][fo-ccc] |
| Vantage | Virtual Tags(필터 규칙, 첫 매치, Tag Key Collapsing), Segments(계층, priority 0 최고, 자식이 아래에서 위로 청구) | Segments의 선택형 "Unallocated", K8s idle은 `__idle__` | allocation virtual tag: Cost-Based(다른 비용 집합 비례), Business-Metric-Based(가져온 메트릭의 라벨 열 분포), Percent-Based(합 100%) | 미문서(가상 태그 처리 1시간 이내) | 비가산: Business Metrics. 가산: Custom Providers | 가상 태그 50개·값 100개, pod 라벨 allowlist 10개, Virtual Tagging은 Pro 이상 | [va-vt][va-seg][va-bm][va-price] |
| Harness CACM (구 CCM) | Cost Categories: Operand-Operator-Value 규칙(Cluster, AWS/GCP/Azure, External Data, 계정 태그 / IN, NOT IN, NULL, NOT NULL, LIKE 정규식 / AND·OR), 5단계 중첩 | "Unattributed"(이름 지정 또는 무시), 다중 그룹핑에서는 "No <Category>" | shared bucket: Equal, Proportional(직접 비용 비례), Fixed %. 카테고리 간 겹치면 이중계산. K8s는 노드 비용을 CPU·메모리 반반, 클라우드 커넥터가 있으면 청구로 true-up | 미문서 | 가산: Custom Cost Provider, External Data Provider API. 텔레메트리 가중 분할 없음 | shared bucket 10개 한도 **[미조회]**(검색 스니펫). Free Forever 지출 한도가 자체 문서에서 연·월로 불일치 | [hn-cc][hn-kc][hn-ccp] |
| IBM Cloudability | Business Mappings(태그·계정 직접 매핑 규칙). K8s 라벨 키 20개, 태그와 충돌하면 라벨 우선. Tag & Label Mapping의 K8s 라벨 지원 확인 | 컨테이너 뷰 IDLE RESOURCES 줄 | Cost Sharing(GA 2025-01-30): even, 직접 비용 비례, 고정 가중(합 100%), Telemetry/Consumption. Container Insights 공유 namespace 규칙(기본 kube-system·Cloudability, 조직 또는 클러스터별, 합집합). idle은 노드별 직접 비용 비례 | 컨테이너 공유 규칙은 미래 날짜만, 기능 출시일 이후만 계산 | 비가산: Telemetry CSV(UI). 가산: FOCUS Ingress | Business Mapping 안에서 K8s 라벨 사용 **[미조회]**(검색 스니펫). 텔레메트리 규칙을 컨테이너·네트워크 풀에 걸 수 있는지 미문서 | [cld-cont][cld-shared][cld-tele][cld-rules][cld-tag] |
| Flexera One CCO | Billing Centers(계정·태그 규칙, 부모에게 배분된 비용만 자식이 받는 하향 계층), Rule-Based Dimensions | Billing Centers 미배분 처리 **[미조회]** | CCO 공유비 분할 방식 **[미조회]**. Ocean: requests를 CPU:메모리 가격 비율로, idle은 소비 비례, headroom은 별도 줄 | RBD 규칙 목록은 월 단위로 효력 | 가산: CBI | Ocean 데이터가 CCO Billing Centers로 가는지 **[미조회]** | [flx-bc][flx-cbi][ocean-ca] |
| Umbrella | Business Mapping viewpoint(순서 규칙, AI Business Mapping), K8s namespace·workload | K8s Allocation Status 4종 | Equal, Ratio, Ratio Monthly, Custom %, Metric/KPI 기반, Group-by split, split-over-split, "Allocate Waste Cost" | BYOD 같은 날짜 재업로드 시 교체. unified K8s 뷰는 옛 매핑 재생성 필요 | 비가산: Telemetry Pipeline. 가산: BYOD | 시간 단위 K8s는 enterprise만 | [um-split][um-tp][um-byod] |
| DoiT Cloud Intelligence | Allocations(단일 규칙, 그룹), EKS Lens가 K8s 라벨(`label_*`, `EKS_NSLabel/*`) 추가 | 그룹마다 미리 정의된 Unallocated 규칙, 클러스터 수준 비용은 `[Label N/A]`, pod에 안 가는 노드 비용은 `UNREQUESTED_COSTS` | 리포트 단위 분할: Custom(% 또는 균등), Proportional, Driver Metric(early access) | 분할은 설정한 리포트에만 적용 | 가산: DataHub. DataHub 데이터가 Allocations에 들어가는지 **[미조회]** | EKS Lens 차트는 네트워크 메트릭을 수집하지 않음 | [doit-split][doit-alloc][doit-datahub][doit-lens] |
| nOps | Showbacks(태그, K8s 라벨, 비용 배분 태그, 계정, 리전, usage type, operation include/exclude). K8s는 nOps agent | 어느 태그에도 귀속 안 된 지출, 규칙으로 분산 | `BY_SPEND`, `CUSTOM_PERCENT`, `CUSTOM_BY_SPEND`, `EVENLY`. 텔레메트리 없음 | 미문서 | **[미조회]** | 네트워크 방법 미문서 | [nops-api] |
| Ternary | Custom Labels(조건 규칙 3단계, TEMPLATE / MAPPING / DATA_MAPPING) | 미문서 | Billing Rules Engine `REALLOCATE_COSTS`(Cost·Usage·Quantity 동적 가중 또는 정적 %) | 미문서 | 가산: Custom Data, BYOD. custom data가 `REALLOCATE_COSTS` 가중치가 되는지 **[미조회]** | EKS는 CloudWatch 기반 CPU·메모리만 | [tern-alloc][tern-eks][tern-byod] |
| Kion | Org Chart, Scopes(계정·프로젝트·환경 묶음), Labels(가상 태그), Funding Sources, Billing Rules | 미문서 | 마케팅 페이지만: % 또는 지출 비례 | 월 파일 전체 교체, 삭제한 파일 데이터는 재처리 전까지 남음 | 가산: FOCUS 1.2 | Allocated* 열 미언급 | [kion-alloc][kion-support] |
| Rafay | Chargeback groups(project, cluster, namespace, workload·namespace 라벨) | 미문서 | 기본 비활성. unallocated·control plane은 자원 배분 비율 또는 균등. Common Services 정책은 공유 서비스 namespace 비용을 사용 비례 | 하루 중 붙은 namespace 라벨은 그날 24h 전체에 있다고 가정 | 없음(pod 일별 CSV 다운로드만) | 보존 기간·데이터 원천 **[미조회]** | [rafay-cg] |
| IBM Kubecost 3.x | label·annotation 매핑(기본 `owner`, `team`, `dept`, `product`, `env`), Collections(Category), Teams는 RBAC | `__unallocated__`, "High not in Category Cost" | `shareIdle`, `idleByNode`/`splitIdle`, `shareNamespaces`, `shareLabels`, `shareCost`/`sharedOverhead`, `shareSplit` weighted·even, `shareTenancyCosts`. Collections는 Weighted/Even, 겹침은 chargeback 모드에서 최고 우선순위(표준 모드는 음수 줄) | 미문서 | 사실상 없음(§4.3) | Teams·Custom Pricing은 Enterprise. Azure/GCP에서 공유·LB 비용 겹침 미고려 | [kc-alloc][kc-coll][kc-teams][kc-coll-blog] |
| OpenCost v1.121.2 | `aggregate=team`, label:/annotation:, 다중 집계. collection 없음 | `__unallocated__`, SharedHourlyCosts는 `__shared__` | HTTP 핸들러는 `includeIdle`, `shareIdle`, `idleByNode`, `sharelb`, `includeProportionalAssetResourceCosts`만. core의 ShareSplit·SharedNamespaces·SharedLabels는 핸들러에 없음 → 클라이언트에서 계산 | 미문서 | plugins(labels 버림, §4.3) | 커스텀 비용은 메모리 저장 | [oc-agg][oc-alloc][oc-matcher] |
| Datadog CCM | 태그. 컨테이너 배분이 pod·node·container·task 태그를 붙임. Tag Pipelines(규칙 100개, 최근 3개월 적용) | n/a 버킷, `not_monitored` | Custom Allocation Rules(매일, 목록 순서, `allocated_by_rule`): Even, Custom %, 지출 비례, Dynamic by metric(Enterprise). cluster idle 재분배(cluster·node·nodepool) | 규칙 변경은 최대 24h, 당월·전월만 backfill | 비가산: Dynamic by metric. 가산: Custom Costs | Dynamic by metric은 대상 태그 값을 규칙에 나열 → 팀이 늘 때마다 규칙 수정 | [dd-tp][dd-car][dd-cca] |
| CAST AI | allocation group(cluster·조직 단위, namespace·워크로드 라벨, UI/API/Terraform) | 그룹 밖·겹침 미문서 | requests 기준, "Include Idle Cost"는 노드 idle을 요청 비율로 | 미문서 | 없음(Reporting API 내보내기만) | 조직 그룹 워크로드 20,000개 | [cast-ag] |
| AWS Cost Categories | 규칙(계정, 태그, usage type 등), 기본값, CUR `costCategory` 열 | 기본값 또는 미분류 | split charge: EVEN, FIXED, PROPORTIONAL(대상 비용 비례). 결과는 details 페이지·CSV만 | 콘솔은 당월부터. API `EffectiveStart`는 최대 12개월 전(같은 API 페이지에 이전 달은 안 바뀐다는 문장이 있어 모순) | 행 가져오기 없음 | 카테고리 50개, 규칙 500(API)/100(UI), split 규칙 10개, 대상 500개 | [aws-cc][aws-split][aws-ccapi] |
| AWS SCAD (EKS) | 기본 태그 `aws:eks:*`, pod 라벨 최대 50개를 사용자 정의 비용 배분 태그로(2025-10-30) | 라벨 없는 pod는 빈 태그 값 | `SplitUsage = max(reserved, actual)`, `UnusedCost`는 SplitUsage 비례로 pod에 | 라벨 태그는 payer에서 활성화 필요. 50개가 차면 기존 태그를 비활성화해야 새 라벨이 들어옴 | 없음 | Cost Explorer·FOCUS 1.2 미포함, 네트워크 없음 | [aws-scad][aws-scad-labels] |
| Cloudchipr | Dimensions·categories(태그·계정·프로젝트·리소스 ID 필터) | 미문서 | 문서: Manual %, Even. 텔레메트리는 마케팅만 | 미문서 | **[미조회]** | requests 없는 pod는 비용 0 | [cc-dim][cc-k8s] |
| Hystax OptScale (2026080701-public) | Pools 계층 + assignment rules(이름, 태그, 소스, 리소스 타입, 리전, 우선순위) | Data Source pool | 없음(issue #723은 사용자 요청) | 미문서 | **[미조회]** | Apache-2.0 | [os-assign][os-723] |
| CloudHealth by Broadcom | Perspectives, FlexOrgs, container cost distribution rules | usage 기준이면 미사용분 미배분 | 동적 재배분 가중(Total / Effective / Amortized Cost) **[미조회]**(검색 스니펫). 2025-08-01 이후 청구분은 OpenCost 명세 | 미문서 | 가산: Data Connect BYOD | Perspective·배분에서 BYOD 사용 여부 미문서 | [ch-k8s][ch-byod] |
| Red Hat Lightspeed cost management | OpenShift 프로젝트, pod·node 라벨 = 태그, 태그 매핑(2024-04), AWS Cost Categories(2023-10), `openshift-*`·`kube-*`는 Platform | 이름 있는 미배분 프로젝트 4종 | 비용 모델 Distribute 스위치, CPU 또는 메모리 effective usage 비례. 태그별 요율, % 마크업 | SaaS는 90일 표시, 5개월 후 삭제 | 없음 | OpenShift만 | [rh-cm][rh-new] |
| Backstage Cost Insights (npm 0.30.0) | 그룹 소속(`getUserGroups`), 카탈로그 엔티티. AWS 백엔드는 annotation `aws.amazon.com/cost-insights-tags`, `aws.amazon.com/cost-insights-cost-categories` | 태그 안 된 비용은 보이지 않음 | 없음 | 일 단위 | `CostInsightsApi` 구현(표시 계층, §4.3) | Apache-2.0 | [bs-api][bs-aws] |

**한도 수치 모음:** Vantage 가상 태그 50개·값 100개, Cloudability 라벨 키 20개, SCAD pod 라벨 50개, CloudZero 라벨·annotation·태그 300개, Vantage pod 라벨 allowlist 10개, Datadog Tag Pipeline 규칙 100개, AWS split charge 규칙 10개.

---

## 4. 외부 배분 데이터 가져오기 경로

crossaz가 만든 팀별 금액을 플랫폼에 넣는 경로는 두 종류입니다. **비가산**은 플랫폼이 이미 가진 청구 줄을 들어온 비율로 다시 나누므로 분모가 청구서로 남습니다. **가산**은 달러 행을 새로 더하므로, 이미 들어온 CUR InterZone 줄 위에 같은 달러가 한 번 더 쌓입니다.

### 4.1 비가산(비율) 경로

| 경로 | 형식 | 한도 | 확인된 동작 | [미검증] / [미조회] | 출처 |
|---|---|---|---|---|---|
| Vantage Business Metrics → Business-Metric-Based allocation virtual tag | CSV `date`, `amount`, 라벨 열 최대 100개. 또는 API, CloudWatch, Datadog, Snowflake, BigQuery, Metronome | 일 단위 최소 집계, 한 번에 한 달. 25MB 한도와 date+label 중복 거부는 sweep 간 충돌 | 라벨 열(예: team)의 값이 가상 태그 값이 됨. Virtual Tagging은 Pro 이상 | [미검증] 비용 집합을 Regional-Bytes usage type으로 좁힐 수 있는지. **[미조회]** `PUT /business_metrics/{token}/values.csv` 경로(SDK·검색만) | [va-bm][va-vt] |
| Finout S3 CSV Telemetry → telemetry 기반 Shared Cost Reallocation | S3 CSV: date 열 1개, metric 열 1개, dimension 열 0–10개 | 매일 읽음 | 할당 비율은 매일 계산해 고정. 텔레메트리 누락·빈 값이면 설정한 fallback. 재배분 가상 태그는 raw data에 없음 | [미검증] 필수 열 이름, 내장 K8s 네트워크 분할(pod 총 바이트)과 재배분의 상호작용 | [fo-csv][fo-scr] |
| CloudZero Allocation Telemetry → Allocate By Streams | `POST /unit-cost/v1/telemetry/allocation/{stream}` (`/sum`, `/replace`, `/replace_period`, `/delete`는 soft delete). 레코드 {timestamp, granularity, element_name, filter, value}. CSV 업로드도 가능 | 스트림 안의 모든 레코드가 같은 filter 키·granularity. 한 sweep: filter dimension 5개·값 20개, 예시는 DAILY만. 다른 sweep(OpenAPI v1.3.0): HOURLY / DAILY / MONTHLY | filter가 분할 대상 지출을 한정. `replace_period`는 배치에 있는 날짜의 레코드를 모두 교체(빠진 대상은 삭제), 값 0 거부, 스트림이 먼저 있어야 함 | [미검증] filter로 InterZone usage type을 정확한 이름으로 고를 수 있는지. **[미조회]** 텔레메트리가 없는 기간의 범위 내 지출 처리 | [cz-rp][cz-api][cz-split] |
| Cloudability Telemetry/Consumption Cost Sharing | CSV `Date`, `Category`, `Value`, 일 단위 | UI 업로드만. 규칙 API(`telemetry_consumption`)는 규칙만 관리 | 대상 값은 CSV에서 옴 | 텔레메트리 API 업로드 미문서, 컨테이너·네트워크 풀 대상 가능 여부 미문서 | [cld-tele][cld-rules] |
| Umbrella Telemetry Pipeline → Metric/KPI 기반 분할 | S3 CSV, Date·Account ID·measure·dimension 열 매핑 | 파이프라인당 measure 5개·dimension 5개, 샘플 파일 1,000행 | Metric/KPI 분할의 가중 평균에 사용 | Account ID 열 필요 | [um-tp][um-split] |
| Datadog Custom Allocation Rule "Dynamic by metric" | Datadog 메트릭 쿼리(사용자가 메트릭을 제출해야 함) | CCM Enterprise. 변경은 최대 24h, 당월·전월만 backfill | partition 태그가 비용·메트릭 양쪽에 있어야 함. 대상 태그 값은 규칙에 명시 | [미검증] 컨테이너 배분이 이미 워크로드에 붙인 DT 비용과의 상호작용(미문서) | [dd-car] |

**그 밖의 비율형 후보(확인 안 됨 또는 쓰기 어려움)**
- DoiT Driver Metric 분할: early access이고 리포트 범위에만 적용됩니다 [doit-split].
- Pelanor Custom Metrics API: 개요는 리포트·Economics 대시보드·API 조회 용도만 적었고, 배분 전략의 driver로 쓸 수 있는지 **[미조회]** [pel-metrics].
- Ternary `REALLOCATE_COSTS`: custom data를 동적 가중치로 쓸 수 있는지 **[미조회]** [tern-alloc].
- AWS Cost Categories FIXED split: 쓰기 API이고 원천 값당 비율 하나이며 결과가 details 페이지에만 보입니다(§5).

### 4.2 가산(달러 행) 경로와 이중계산 경고

아래 경로로 crossaz 팀 금액을 넣으면 **이미 수집된 CUR InterZone 줄 위에 같은 달러가 더해집니다.** Datadog 문서는 같은 파일을 두 번 올리면 비용이 두 배가 된다고 경고합니다 [dd-custom].

| 경로 | 형식 | 한도·동작 | 출처 |
|---|---|---|---|
| Datadog Custom Costs (beta, private) | FOCUS 정렬 CSV/JSON. 필수 ProviderName, ChargeDescription, ChargePeriodStart, ChargePeriodEnd, BilledCost, BillingCurrency(USD) + 태그 열 | UI 또는 `PUT /api/v2/cost/custom_costs`. 24h 후 표시, 미래 날짜 거부, 빈 값 불가, 열 이름·값 1,000자 | [dd-custom] |
| Vantage Custom Providers | FOCUS 1.1–1.3 subset CSV(필수 ChargeCategory, ChargePeriodStart, BilledCost, ServiceName, Tags JSON) | 10,000행·2MB, 일 집계, 같은 파일 이름은 덮어씀. AvailabilityZone 같은 추가 FOCUS 열은 받지만 보고에 쓰지 않음. `POST /v2/integrations/custom_provider` 후 `/integrations/{token}/costs.csv` | [va-cp][va-focus] |
| Harness Custom Cost Provider | FOCUS v1(1.1 PDF 링크) CSV | 20MB, UI 업로드. Perspectives·Budgets·Cost Categories에서 사용 | [hn-ccp] |
| Flexera Common Bill Ingestion | FOCUS 1.0 + 일부 1.2(`cbi-oi-focus`) 또는 CBI 기본 CSV | API로 Bill Connect 생성 후 업로드. 선례: cbi-oi-kubecost-exporter(MIT, 마지막 push 2025-07-25)가 Kubecost/OpenCost 일별 CSV를 올림 | [flx-cbi][flx-exporter] |
| Umbrella Bring Your Own Data | 일별 FOCUS 1.0 파일, Umbrella S3 경로 | 파일당 5GB, 같은 날짜 재업로드 시 교체 | [um-byod] |
| Finout Custom Cost Centers | S3 CSV `UsageDate`, `Cost`(USD), dimension 열, 하루 한 파일 | 지원 티켓 온보딩(최대 48h), 과거 파일 수정 시 덮어씀 | [fo-ccc] |
| DoiT DataHub | JSON 이벤트(provider·time 필수, dimensions, metrics cost/usage/savings/custom) 또는 CSV 업로드 | 요청당 이벤트 255개, CSV 30MB(zip/gz), 약 15분 후 조회, 90분 이내 삭제 가능 | [doit-datahub] |
| CloudZero AnyCost Stream | CBF 달러 행(`resource/tag:*`, `k8s/cluster`, `k8s/namespace`, `k8s/labels`), `replace_drop` / `replace_hourly` / `sum`, S3 `.csv.gz` 대안 | 요청 5MB, 연결·월 100만 행·400MB, 한 번에 한 요청. 태그는 시간 독립 | [cz-anycost][cz-cbf] |
| Kion custom billing source | FOCUS 1.2 CSV/GZIP/ZIP, S3 또는 Azure Storage의 YYYYMM 폴더, 필수 열 16개 | 월 전체 파일 교체, 삭제한 파일은 재처리 전까지 남음. S3 v3.14.0+, Azure v3.16.4+ | [kion-support] |
| CloudHealth Data Connect BYOD | `.csv`, `.csv.gz`, `.parquet` 월 스냅샷 버전 | 파일 100MB, 월 한 파일(이전 데이터 포함), 최신 버전 우선, 24h 내 Reports·FlexReports | [ch-byod] |
| Cloudability FOCUS Ingress | FOCUS 1.0/1.1 CSV/Parquet(S3, Azure Blob, GCS), 매니페스트 경로 | 업로드마다 월누계 전체 | [cld-focus] |
| Ternary Custom Data / BYOD | S3, GCS, Azure Blob, BigQuery, EXTERNAL_PUSH, 드래그앤드롭 CSV/ZIP 100MB(Charge Period Start 필수) | 열 매핑 설정 | [tern-byod] |
| Pelanor Custom Invoices | CSV `usage_date`, `cost`, `tag:*`, `consumption_type` | 같은 달 새 업로드가 덮어씀 | [pel-inv] |

### 4.3 쓸 수 없는 경로

- **OpenCost plugins:** proto에는 `labels` 맵이 있지만 내부 `CustomCost` 구조체에 labels 필드가 없고, 매처는 맵 필드를 지원하지 않는다고 반환합니다. 가져온 행의 팀 라벨로 그룹핑·필터링할 수 없습니다. 저장소는 메모리이고 재시작 후 다시 조회합니다. 기본 plugin은 datadog, mongodb-atlas, openai(opencost-plugins v0.0.16, 2026-06-11)입니다 [oc-matcher][oc-proto][oc-plugins].
- **Kubecost External Costs:** 2.x beta 기능으로 Datadog만 지원하는 모니터링 대시보드입니다. allocation이나 팀 몫에 합쳐진다는 문서가 없습니다. 3.x에서 팀 라벨로 집계되는지 **[미조회]** [kc-ext]. Enterprise Custom Pricing은 asset 가격만 바꿉니다 [kc-alloc].
- **Backstage Cost Insights:** 배분 엔진이 없는 표시 계층입니다. `CostInsightsApi` 인터페이스를 구현해 팀×일 비용을 공급하면 crossaz JSON도 보여 줄 수 있습니다. AWS 백엔드는 Cost Explorer 요청당 $0.01을 내고 기본 1일 캐시합니다 [bs-api][bs-aws].
- **AWS Billing Conductor custom line item:** 계정 단위 pro forma 조정입니다. 팀 = 계정일 때만 팀 금액을 실을 수 있고, 청구 도메인과 분리됩니다(§5).
- **CID FOCUS Dashboard consolidation:** 다른 원천의 FOCUS 표를 합칠 수 있지만 문서 예시는 1.0/1.2이고 Allocated* 열을 읽는지 **[미조회]** [cid-focus].
- **Tollwing export sidecar:** 가져오기가 아니라 내보내기이고, 기본 모드는 Enterprise 서버를 조회합니다 [tw-export].

---

## 5. AWS 네이티브와 표준

| 기능 | 팀 차원 | 교차 AZ | 가져오기 | 한계 | 출처 |
|---|---|---|---|---|---|
| Cost Categories + split charge | 계정·태그·usage type 규칙, 기본값, CUR `costCategory` 열 | Usage Type 규칙으로 InterZone 달러 전체를 "shared-network" 같은 값에 모은 뒤 EVEN / FIXED / PROPORTIONAL로 분할 가능. pod 신호 없음, 측 구분·무료 경로 없음 | 행 가져오기 없음. FIXED split을 `UpdateCostCategoryDefinition`으로 쓸 수는 있음(원천 값당 비율 하나) | split 결과는 cost category details 페이지와 CSV만. CUR·Cost Explorer에 나타나지도, 영향을 주지도 않음. billing transfer의 bill source 계정은 split 불가. `EffectiveStart` 최대 12개월 전(같은 페이지의 "이전 달 불변" 문장과 모순) | [aws-cc][aws-split][aws-ccapi] |
| 비용 배분 태그(리소스, 계정, backfill, custom billing view) | 리소스 태그·계정 태그(2025-12-19, 계정의 모든 계량 사용량에 적용, CUR 2.0·FOCUS에 `accountTag/`). backfill 최대 12개월(2024-03-27). custom billing view로 팀에 Cost Explorer 공유 | CUR은 계량 리소스당 `DataTransfer-Regional-Bytes` 줄 2개(in, out). 그 줄에 인스턴스·LB 태그가 붙는다는 현행 문서 문장은 없음(2019 블로그만) **[미조회]** | 없음 | 활성 태그 키 500개, 요청당 활성화 20개, 리소스당 태그 50개, 활성화 지연 최대 24h+24h. 태그는 공유 리소스를 나누지 않음 | [aws-tags][aws-acct-tags][aws-backfill][aws-cur-dt][aws-dt-blog-2019] |
| Split Cost Allocation Data (EKS) | pod 시간당 행, `aws:eks:*` 태그, pod 라벨 50개 가져오기(2025-10-30) | 없음. EC2 CPU·메모리(EKS는 GPU·Trainium·Inferentia 포함)만 | 없음 | Cost Explorer·FOCUS 1.2 미포함, pod-시간당 2–3행, 50개 라벨 알파벳순 절단. 요금 **[미조회]** | [aws-scad][aws-scad-labels][aws-scad-news][aws-dx-faq] |
| Billing Conductor | billing group(계정 묶음) pro forma | 없음 | custom line item: flat $0–1,000,000 또는 % 0–10000, 선택 allocated account. line item filter는 Savings Plans 할인 제외만 | 계정 단위만, pro forma는 청구 도메인과 별개. standard billing group 요금 비공개, billing transfer 고객 관리 요금제 $50/조직 | [aws-bcd][aws-bcd-cli][aws-bcd-price] |
| Data Exports: FOCUS 1.2(GA 2025-11-19) + CUR 2.0 | 태그 열, CUR 2.0 split line item 열 | DT 행은 리소스 수준. FOCUS 1.2 export에 Allocated* 열 없음 | 없음(내보내기만). crossaz의 입력 | SCAD는 FOCUS 1.2에 없음. 1.3/1.4 export 목록에 없음 | [aws-focus12][aws-dx-faq] |
| FOCUS 1.3 / 1.4 Allocated* | `AllocatedTags`(할당 행의 태그) | 네트워크 방법 없음. 노드 InterZone 줄을 워크로드 행으로 나누는 모양은 표현 가능 | 형식 목표로 쓸 수 있으나 아래 적합성 문제 | 아래 참조 | [focus-rel][focus-14][focus-handling][focus-amid][focus-glossary] |
| CID SCAD Containers / FOCUS Dashboard (framework 4.4.17, 2026-08-14, MIT-0) | SCAD 대시보드 v4.0.0+ K8s 라벨·TCO(같은 태그의 단일 테넌트 리소스 합산), FOCUS 대시보드는 다른 원천 FOCUS 표 통합 | SCAD 대시보드 KPI는 CPU, GPU, RAM, shared, total. 네트워크·LB·PV는 Kubecost 대시보드만 | FOCUS 표를 `focus_consolidation_view`에 추가(`cid-cmd update`) | FAQ(2025-07)는 낡음(라벨 미지원, GPU 없음으로 적힘). 운영비 약 $100–200/월 | [cid-scad][cid-focus][cid-faq] |
| CloudWatch NFM / EKS Container Network Observability | 없음(pod·namespace·AZ 메타데이터만) | 바이트만. 블로그(2026-05-05)가 요율을 인용하지만 곱하지 않음 | 없음 | 노드당 30초마다 top 500 흐름, TCP, monitored resource-hour당 $0.0069 | [nfm-eks][nfm-blog] |
| FinOps Framework Allocation(+ Calculating Container Costs, 2026-03-16) | 쇼백·차지백 전략, 태그 전략, 미배분 식별, KPI(Unallocated Cost Percentage 등) | 방법 없음. 대역폭은 자주 간과된다고만 | 없음 | 비규범 지침 | [finops-alloc][finops-container] |
| GKE cost allocation(비교용) | BigQuery 청구 export에 namespace·cluster·pod 라벨(`k8s-label/<key>`, 50개 초과면 전부 제외) | 없음(vCPU, RAM, GPU, PD, TPU SKU만). 구 usage metering의 egress 계측은 inter-zone 구분 여부 **[미조회]** | 없음 | requests 기준, idle은 `kube:unallocated` | [gke-ca][gke-meter] |
| AKS cost analysis(비교용) | Kubernetes clusters·namespaces·assets 뷰, Idle / System / Service / Unallocated 요금 유형 | Azure는 같은 리전 AZ 간 전송을 과금하지 않음(Microsoft Learn). 대역폭의 namespace 배분 **[미조회]**. 무료 시작일(2024-05)은 2차 보도만 **[미조회]** | 없음 | Standard/Premium tier, 약 7,000 컨테이너, OpenCost 기반 | [aks-ca][az-azfree] |

**FOCUS Allocated* 요약**
- **릴리스(GitHub):** 1.2 2025-05-29, 1.3 2025-12-08(FinOps Foundation 비준 2025-12-05), 1.4 2026-06-04(FinOps 블로그 2026-06-10). main 브랜치 초안은 `AllocatedServiceName`을 추가합니다.
- **열:** `AllocatedMethodId`, `AllocatedMethodDetails`, `AllocatedResourceId`, `AllocatedResourceName`, `AllocatedTags`(1.3에서 도입). `AllocatedTags`는 split cost allocation 기능의 직접 종속 열 목록에는 없습니다.
- **규칙:**
  - 할당 행의 dimension·비합산 열은 원래 행과 같아야 하고, 합산 지표(비용·수량)는 원래 행과 합이 같아야 합니다.
  - 한 원래 행의 `AllocatedRatio` 합은 1이어야 합니다.
  - 미배분 몫 행은 `AllocatedResourceId`가 null입니다.
  - 커스텀 키는 `x_` 접두사를 씁니다.
- **제3자 적합성:**
  - 이 기능은 data generator가 스스로 계산한 분할을 위한 것이고, `AllocatedMethodId`는 서비스 제공자가 정의한다고 적혀 있습니다.
  - glossary는 FinOps 도구 벤더도 data generator로 보지만, 1.4 병합 규칙은 원천 데이터를 만들지 않은 주체가 생성한 요금의 effective cost를 0으로 둡니다.
  - 그래서 crossaz가 AWS 요금을 재분할해 내보내는 행은 FOCUS 모양일 뿐 적합하다고 보기 어렵습니다.
- **소비자:** 가져오기에서 Allocated* 열을 읽는 벤더는 찾지 못했습니다 **[미조회]**. Vantage Custom Providers는 1.3 추가 열을 받되 보고에 쓰지 않고, Kion의 FOCUS 1.2 문서는 이 열을 언급하지 않습니다.
- **라이선스:** v1.4 태그의 license.md는 CC-BY-4.0, W3C 모드 특허 정책입니다.

---

## 6. 인수·개명·중단 연표

| 날짜 | 사건 | 비고 | 출처 |
|---|---|---|---|
| 2023-06-26 | IBM, Apptio(Cloudability) 인수 발표 $4.6B | 2023-08 완료. TechCrunch는 $4.3B로 보도 | [ibm-apptio][tc-kubecost] |
| 2024-09-17 | IBM, Kubecost 인수 발표 | 금액 비공개, 완료일 **[미조회]** | [kc-ibm][tc-kubecost] |
| 2025-02-04 | DoiT, PerfectScale 인수 발표 | K8s 최적화(네트워크 배분 아님) | [doit-ps] |
| 2025-02-19 | DigitalEx → Mavvrik 개명, $6.2M 시드 | Mavvrik 블로그는 아직 app.digitalex.io 링크 | [mav-pr] |
| 2025-03-03 | Flexera, NetApp Spot 포트폴리오(Ocean, Eco, Elastigroup, CloudCheckr) 인수 완료 | Spot Ocean 문서가 docs.flexera.com으로 이동 | [flx-spot] |
| 2025-03-31 | CloudBolt, StormForge 인수 | 조건 비공개. K8s 배분 기능 GA 날짜 **[미조회]**(KubeCon NA 2025 private preview는 날짜 없는 2차 기사만, 현재 CloudBolt 페이지는 "Built Into CloudBolt") | [cb-acq][cb-k8s][cb-ec] |
| 2025-04-02 | Anodot, FinOps 사업부를 Umbrella로 분리 | 인수가 아니라 리브랜드·조직 개편 | [um-pr] |
| 2025-09-28 | Kela, Pelanor 인수 보도(Calcalist, "수천만 달러") | 2026-09-15 www.pelanor.io·pelanor.io 404, docs.pelanor.io는 응답 | [pel-ctech] |
| 2025-09-30 | IBM Kubecost 3.0 GA | Prometheus 의존 제거, ClickHouse aggregator. IBM 지원 페이지 스니펫은 2025-09-29 | [kc-3blog] |
| 2026-01-06 | Flexera, ProsperOps·Chaos Genius 인수 발표 | | [flx-prosperops] |
| 2026-04-23 / 2026-05-06 | castai/egressd, castai/kvisor 저장소 마지막 push, archived | castai-kvisor 차트는 1.165.3(2026-09-07)까지 배포 계속. 비공개 소스 전환은 추정 **[미조회]** | [cast-repo] |
| 2026-04-28 | New Relic Cloud Cost Intelligence GA | CloudCost 이벤트는 CloudSpend로 대체 예정 | [nr-ga] |
| 2026-07-07 | DoiT Attribute 출시 | 인수 보도(Calcalist/Ctech 약 $65M 추정, Dealroom 등)는 2차 출처만 **[미조회]**, DoiT 자체는 출시로만 표기 | [doit-attr][ctech-attr] |
| 2026-08-06 | Vantage K8s network cost 출시 | 추가 요금 없음 | [va-blog] |
| 날짜 **[미조회]** | Yotascale → CoreStack | yotascale.com이 "Yotascale → CoreStack Redirect" 페이지를 냄. 인수 사실은 검색 스니펫만, sweep 간 404/리다이렉트 결과 충돌, docs.yotascale.io DNS 실패 | [yota] |
| 날짜 **[미조회]** | Harness CCM → Cloud & AI Cost Management(CACM) | 문서 경로가 `use-cacm`으로 바뀜 | [hn-kc] |
| 날짜 **[미조회]** | VMware (Tanzu) CloudHealth → CloudHealth by Broadcom | TechDocs가 ca-enterprise-software 트리로 이동 | [ch-k8s] |
| 날짜 **[미조회]** | Red Hat Insights cost management → Red Hat Lightspeed cost management | 현재 문서 제목에 "Insights" 없음 | [rh-new] |

**조사에서 제외한 것:** Amberflo(AI/LLM 거버넌스, 클라우드·K8s 배분 없음), groundcover(`is_cross_az` 라벨은 있으나 비용 배분 없음, measured-pair 소스 후보일 뿐) [groundcover].

---

## 7. 공개 사례

| 사례 | 날짜 | 측정 | 팀 귀속 | 결과 | 출처 |
|---|---|---|---|---|---|
| Grab Coban Kafka "Zero traffic cost for Kafka consumers" | 2023-07-07 | Cost Explorer 교차 AZ 금액, AWS 양측 과금 인지. produce(작음)·replication(작음)·consume(대부분, 소비자의 약 2/3가 교차 AZ)로 분해 | 없음. 플랫폼 팀이 흡수(chargeback 언급 없음) | rack-aware follower fetching(KIP-392), 3개월 후 12월 평균 대비 교차 AZ 트래픽 25% 감소 | [grab] |
| DoorDash "Staying in the Zone" | 2024-01-16 **[미조회]** | 1차 게시물이 403. InfoQ(2024-01)로 확인된 것은 zone-aware Envoy 라우팅과, 클라우드 사업자가 장애 여부를 물어 왔다는 일화뿐 | 검색 스니펫상 subnet 범주·서비스 롤업, 팀 chargeback 아님 | 스니펫상 교차 AZ 비용의 약 20%가 Iguazu ELB 한 곳, Envoy `cx_rx/tx_bytes_total` 사용 **[미조회]** | [doordash][doordash-infoq] |
| Datadog 내부 K8s 마이그레이션(CNM 가이드) | 날짜 없음 | CNM 바이트를 서비스·팀 태그로 그룹핑, 출발은 서비스·도착은 AZ로 비대칭 쿼리 | 팀 태그로 바이트(달러 아님) | stateless 서비스를 먼저 옮기자 한 AZ에 몰린 stateful 서비스로 TB 단위 교차 AZ가 생겼고, stateful 샤딩으로 줄임. 벤더 작성 | [dd-cnm-guide] |
| Polar Signals kubezonnet (eBPF Foundation 사례) | 2025-01-15 **[미조회]** | netfilter postrouting eBPF, 10초 창, 출발·도착 IP를 pod·노드로 해석 | 없음(pod·노드 수준) | 2024 중반 cross-zone이 클라우드 지출의 거의 절반, AZ별 모니터링 스택 중복 제거 등 인프라 변경으로 cross-zone 비용 50% 감소. 청구 대조 없음. 코드 Apache-2.0 | [pol-case][pol-gh] |
| OneUptime 블로그 "Attribute NAT Gateway and Cross-AZ Cost to Traffic Generators" | 2026-08-04 | VPC Flow Logs + CUR + route table 스냅샷. 할당 가능 비용 = CUR 비용 × 측정 커버리지 | 시간 구간 인벤토리로 출발 리소스 → 소유자 | 실사례 아닌 가이드. 잔차 이름 `flow_logs_not_enabled`, `SKIPDATA_or_delivery_gap`, `source_ip_not_in_inventory`. AZ 이름보다 AZ ID 권장. 저자 **[미조회]** | [oneuptime] |

- **뺀 사례:** Mercari(2023-10-30, GKE zone-aware 라우팅, 바이트만), Zalando(2026-06-23, 청구 수준 추정), Netflix FlowExporter/FlowCollector(비용·zone 내용 없음), Isovalent Hubble(비용 기능 없음). 모두 팀 귀속 내용이 없습니다.
- **찾지 못한 것:** Pinterest, Airbnb(2021 글은 CUR 태그·프로젝트 이름만), Lyft, Uber, Stripe, Figma, Slack, Robinhood, Monzo, Zendesk, Coinbase에서 교차 AZ 달러를 팀에 chargeback한 공개 글은 없었습니다. Segment "$10M engineering problem" URL은 Twilio 색인으로 리다이렉트되어 읽지 못했습니다 **[미조회]**.
- **결론:** 교차 AZ 달러를 팀에 chargeback한 공개 엔지니어링 사례는 찾지 못했습니다. 공개된 사례는 모두 "바이트나 청구 줄로 원인을 찾고 아키텍처를 고쳤다"는 진단 사례입니다.

---

## 8. crossaz에 주는 시사점

| 조사 사실 | docs/v2-plan.md §9 결정 |
|---|---|
| 팀 매핑·공유비 분할·UI·예산은 거의 모든 플랫폼의 기본 기능(§3) | 거버넌스·재배분 엔진·UI를 만들지 않고, 팀 금액은 기존 Row의 그룹핑으로만(v2-plan §9.1, §9.8) |
| OpenCost `TeamLabel`, Kubecost `team_label`의 기본값이 `team`. SCAD도 pod 라벨을 태그로 가져옴 | `--team-label` 기본 `team`(v2-plan §9.2) |
| 미배분 원소 이름 관례: Kubecost/OpenCost `__unallocated__`, Vantage `__idle__` | `__crossaz_*__` 원소(v2-plan §9.2) |
| CloudZero AnyCost 태그는 시간 독립, Rafay는 하루 중 붙은 라벨을 24h 전체에 적용 | 실행 시점 스냅샷 해석 + M4 일별 고정, `teamsResolvedAt` 출력(v2-plan §9.2) |
| Datadog은 n/a, Finout은 idle, Kubecost는 adjustment로 잔차를 흡수 | 잔차·미매핑·저신뢰 규칙 달러를 이름 있는 원소로 CSV까지(v2-plan §9.4, §9.6) |
| 양측 부과를 문서화한 곳은 Spot Ocean뿐. Tollwing은 관측 노드에서 tx·rx 모두 계량 | 노드 측 행을 하나씩 두고 복제하지 않는 owner 롤업(v2-plan §9.3, §9.4) |
| 교차 AZ 달러의 팀 금액에 과금 측·무료 경로 규칙을 적용하는 도구 없음 | 저신뢰 과금 규칙 달러는 팀에 부과하지 않고 `__crossaz_unconfirmed-rule__`(v2-plan §9.3) |
| 가산형 가져오기는 이미 들어온 CUR 줄 위에 달러를 더함(§4.2) | 비가산 비율 CSV 하나(`--team-csv`), 가산 경로 제외(v2-plan §9.6) |
| FOCUS Allocated*는 제3자 적합성이 불명확하고 가져오는 소비자를 찾지 못함, AWS export는 1.2 | FOCUS Allocated* 출력 제외(v2-plan §9.6, §9.8) |
| OpenCost plugins는 labels를 버림 | OpenCost/Kubecost 가져오기 제외(v2-plan §9.6) |
| Finout(pod 바이트), Vantage(conntrack), Datadog(CNM)에 내장 네트워크 분할이 있음 | "내장 K8s 네트워크 배분을 끄거나 InterZone 줄을 제외한 뒤 사용" 안내, GT2(v2-plan §9.9) |
| Backstage Cost Insights는 표시 계층 | 코드 없음. 사용자가 JSON으로 `CostInsightsApi` 구현(v2-plan §9.6) |

**GT2에서 확인할 항목(§4.1의 [미검증])**
- Vantage: Business-Metric-Based 가상 태그의 비용 집합을 Regional-Bytes usage type으로 좁힐 수 있는지, 25MB 한도와 date+label 중복 거부.
- Finout: S3 CSV Telemetry 필수 열 이름, 내장 K8s 네트워크 분할과 telemetry 재배분의 상호작용.
- CloudZero: stream filter로 InterZone usage type을 정확히 고를 수 있는지, 요청 한도·granularity.
- Datadog(제외했지만 재검토 시): Dynamic by metric과 CNM 기본 DT 배분의 상호작용.

---

## 9. 미조회·미검증과 sweep 간 충돌 목록

**sweep 간 충돌**
- **Vantage Business Metrics 25MB 한도·date+label 중복 거부:** finops-platforms sweep은 문서에서 찾지 못했고, network-to-team sweep은 per_unit_costs 페이지에서 확인했다고 적었습니다.
- **CloudZero Allocation Telemetry 한도와 granularity:** 한 sweep은 filter dimension 5개·값 20개와 DAILY 예시만(문서 갱신 2025-09-10), 다른 sweep은 OpenAPI v1.3.0(2026-07-09)에서 HOURLY/DAILY/MONTHLY를 확인했습니다.
- **Kubecost 3.x 네트워크 청구 조정:** 트래픽 비용 배분 문서는 cloud integration 시 tx 바이트 분할을 적고, network-cost 설정 페이지는 CUR 조정을 설명하지 않습니다.
- **Kubecost 등급:** k8s-native 항목은 yes였다가 요약에서 partial로 낮췄습니다. Coroot도 같습니다. 이 문서는 partial을 씁니다.
- **Kubecost 3.0 GA 날짜:** Apptio 블로그 기준 2025-09-30, IBM 지원 페이지 스니펫 기준 2025-09-29.
- **CloudHealth:** KB 416086(Network Egress는 namespace 미배분, perspective에서는 가능)과 2025-09-30 업데이트(컨테이너 자동 배분)가 모순입니다. 한 sweep은 no, 다른 sweep은 unknown으로 평가했고 이 문서는 unknown을 씁니다.
- **Flexera 문서 접근:** 403(비브라우저 fetcher)과 200(브라우저 User-Agent)이 갈렸습니다. Ocean 네트워크 비용 범위는 GitHub 소스가 AWS만, 라이브 페이지가 AWS와 AKS이며 라이브 페이지를 따랐습니다.
- **Yotascale 상태:** 한 조회는 404, 다른 조회는 CoreStack 리다이렉트 페이지였습니다.
- **Datadog:** Tag Pipelines의 Reference Tables 매핑은 한 sweep만 확인했고, CCM·CNM 가격은 한 sweep만 읽었습니다.
- **CAST AI 등급:** k8s-native는 yes, network-to-team은 partial. 그룹별 interZone 달러 API가 확인되어 yes를 씁니다.
- **AWS Cost Categories·비용 배분 태그 등급:** cloud-native-standards는 partial, network-to-team은 no. §1 척도(규칙·비율 분할만이면 no)에 따라 no로 봅니다. CloudZero도 텔레메트리 경로를 이유로 partial로 본 sweep이 있었지만 같은 이유로 no입니다.
- **Cloudability 등급:** finops-platforms는 unknown, 추가 조사는 2025-02-10 릴리스 노트로 partial. partial을 씁니다.

**[미조회] 목록**
- DoorDash 1차 게시물(403): 20% Iguazu, VPC Flow Log subnet 표, BPFAgent, 90%/45%/91% 수치, 게시일 2024-01-16.
- Yotascale → CoreStack 인수 사실과 날짜.
- Red Hat Insights → Lightspeed, Harness CCM → CACM, VMware CloudHealth → Broadcom의 개명 날짜.
- DoiT의 Attribute 인수(2차 보도만), IBM–Kubecost 인수 완료일.
- CloudBolt K8s 배분 GA 날짜와 "99.9% bill accuracy" 주장.
- CAST AI Kvisor 비공개 소스 전환(추정), 자동화 기능 가격, "read-only agent만으로 부족" 주장.
- Kubecost: network-costs 5초 conntrack 폴링(조회한 values.yaml·3.x 문서에 없음), `networkCostAdjustment`의 pod 분배, 3.x External Costs의 팀 집계, 무료 tier 기준(250코어 또는 30일 $100k).
- Vantage: `PUT /business_metrics/{token}/values.csv` 경로, NFR tier와 Virtual Tags 적용 여부.
- Harness shared bucket 10개 한도.
- Flexera: Billing Centers 미배분 처리, CCO 공유비 분할 방식, Ocean 데이터의 CCO 유입, 가격.
- Cloudability: Business Mapping 안의 K8s 라벨 사용.
- nOps 가져오기 경로·가격, Cloudchipr 텔레메트리 업로드 형식, PointFive 배분 문서, Rafay 데이터 원천·보존 기간, Komodor 팀 배분 방식.
- DoiT: Driver Metric 원천, DataHub → Allocations.
- Ternary: custom data를 `REALLOCATE_COSTS` 가중치로 쓰는지, GKE 문서(404).
- Pelanor: 제품 연속성, Custom Metrics를 배분 driver로 쓰는지, "Cross AZ Traffic" 바이트 식별·가중치.
- CloudHealth 동적 재배분 가중 기준(검색 스니펫).
- New Relic CCI: `network_net_unblended_cost` 내용, 팀 매핑 규칙.
- Grafana Cloud: 팀 라벨 그룹핑, 비용 기능 요금.
- Coroot: 비용 기능의 Enterprise 전용 여부. Tollwing: 에이전트 권한·커널 요구.
- Kentik: 달러 산식, K8s 비용 기능, 가격.
- CloudZero: 에이전트가 수집하는 `container_network_*` 메트릭의 비용 사용, 텔레메트리 없는 기간 처리.
- AWS: InterZone 줄에 인스턴스·LB 태그가 붙는지(현행 문서), SCAD·Cost Categories 요금, Cost Category 태그 규칙이 SCAD split 행에 평가되는지, FOCUS 1.2 export의 Tags 내용.
- CID `focus_consolidation_view`의 Allocated* 지원, Allocated*를 가져오는 벤더.
- GKE usage metering egress의 inter-zone 구분, AKS 대역폭의 namespace 배분, Azure AZ 간 무료 시작일(2024-05).
- Red Hat Koku 저장소 라이선스, OpenShift 밖 EKS 지원.
- Mavvrik 에이전트, Amnic 가상 태그, OptScale의 metrics-server 요구.
- Polar Signals 사례 게시일, OneUptime 저자, Segment 게시물.

**[미검증] 목록(GT2에서 확인)**
- §8 "GT2에서 확인할 항목"과 같습니다.

---

## 10. 출처

모든 URL은 2026-09-15에 조회했습니다. 버전·갱신일은 조회 시점 값입니다.

**Datadog**
- [dd-cca] https://docs.datadoghq.com/cloud_cost_management/allocation/container_cost_allocation/ (문서 소스 https://raw.githubusercontent.com/DataDog/documentation/master/hugo/content/en/cloud_cost_management/allocation/container_cost_allocation.mdoc.md, 마지막 커밋 2026-09-04)
- [dd-car] https://docs.datadoghq.com/cloud_cost_management/allocation/custom_allocation_rules/
- [dd-tp] https://docs.datadoghq.com/cloud_cost_management/allocation/tag_pipelines/ , https://docs.datadoghq.com/cloud_cost_management/tags/
- [dd-custom] https://docs.datadoghq.com/cloud_cost_management/setup/custom/ (front matter `is_beta: true`, `private: true`)
- [dd-price] https://www.datadoghq.com/pricing/list/
- [dd-cnm-guide] https://docs.datadoghq.com/network_monitoring/cloud_network_monitoring/guide/manage_traffic_costs_with_cnm/

**Spot Ocean / Flexera**
- [ocean-ca] https://docs.flexera.com/spot/ocean/features/cost-analysis , https://github.com/spotinst/help/blob/master/src/docs/ocean/features/cost-analysis.md (마지막 커밋 2026-02-15)
- [ocean-chart] https://github.com/spotinst/charts/blob/main/charts/ocean-network-client/templates/daemon_set.yaml (chart 1.1.6, app 1.0.14)
- [ocean-install] https://docs.flexera.com/spot/ocean/tutorials/install-network-client-v2
- [flx-bc] https://docs.flexera.com/flexera-one/cloud/using-cloud-cost-optimization/analyzing-cloud-costs-in-billing-centers/billing-center-hierarchies/how-billing-center-hierarchies-work
- [flx-cbi] https://docs.flexera.com/flexera-one/administration/cloud-settings/bill-data-connections/bill-connect-configurations/finops-open-cost-and-usage-specification-focus/creating-focus-compatible-cbi-bill-connect
- [flx-exporter] https://github.com/flexera-public/cbi-oi-kubecost-exporter (MIT, 마지막 push 2025-07-25)
- [flx-spot] https://www.flexera.com/about-us/press-center/flexera-completes-acquisition-of-netapps-spot-finops-portfolio
- [flx-prosperops] https://www.globenewswire.com/news-release/2026/01/06/3213673/0/en/flexera-acquires-prosperops-and-chaos-genius-to-expand-its-finops-solution-with-agentic-and-ai-enabled-cost-optimization.html

**CAST AI**
- [cast-net] https://docs.cast.ai/docs/network-cost (갱신 2026-08-25)
- [cast-api] https://docs.cast.ai/reference/allocationgroupapi_getcostallocationgroupdatatransfersummary.md
- [cast-ag] https://docs.cast.ai/docs/cluster-allocation-groups.md , https://docs.cast.ai/docs/organizational-allocation-groups.md , https://docs.cast.ai/docs/workloads.md
- [cast-repo] https://api.github.com/repos/castai/kvisor , https://api.github.com/repos/castai/egressd (둘 다 archived), castai/helm-charts의 castai-kvisor 1.165.3(2026-09-07)

**IBM Kubecost**
- [kc-values] https://raw.githubusercontent.com/kubecost/kubecost/v3.2.4/kubecost/values.yaml (v3.2.4, 2026-08-21)
- [kc-alloc] https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=apis-allocation-api
- [kc-nettraffic] https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=dashboard-network-traffic-cost-allocation
- [kc-net] https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=configuration-network-cost
- [kc-coll] https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=ui-collections
- [kc-teams] https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=ui-teams
- [kc-coll-blog] https://www.apptio.com/blog/how-kubecost-collections-works/ (2025-04-30)
- [kc-3blog] https://www.apptio.com/blog/ibm-kubecost-3-0-faster-smarter-and-built-for-scale/
- [kc-ext] https://www.ibm.com/docs/en/SSW0JQG_2.x/using-kubecost/navigating-the-kubecost-ui/external-costs.html
- [kc-ibm] https://newsroom.ibm.com/blog-ibm-acquires-kubecost-to-broaden-hybrid-cloud-cost-management-capabilities
- [tc-kubecost] https://techcrunch.com/2024/09/17/ibm-acquires-kubernetes-cost-optimization-startup-kubecost/

**OpenCost** (develop 커밋 e22df84, 2026-09-11)
- [oc-agg] https://github.com/opencost/opencost/blob/develop/pkg/costmodel/aggregation.go
- [oc-alloc] https://github.com/opencost/opencost/blob/develop/core/pkg/opencost/allocation.go , https://github.com/opencost/opencost/blob/develop/core/pkg/opencost/config.go
- [oc-summary] https://github.com/opencost/opencost/blob/develop/core/pkg/opencost/summaryallocation.go
- [oc-matcher] https://github.com/opencost/opencost/blob/develop/pkg/customcost/matcher.go
- [oc-proto] https://github.com/opencost/opencost/blob/develop/protos/customcost/messages.proto
- [oc-plugins] https://github.com/opencost/opencost-plugins (v0.0.16, 2026-06-11), https://opencost.io/docs/integrations/plugins/

**Finout**
- [fo-k8s] https://docs.finout.io/kubernetes-integrations/kubernetes/how-finout-calculates-kubernetes-costs
- [fo-scr] https://docs.finout.io/user-guide/inform/shared-cost-reallocation/how-to-use-shared-cost-reallocation
- [fo-vt] https://docs.finout.io/user-guide/inform/virtual-tags
- [fo-csv] https://docs.finout.io/telemetry-integrations/telemetry/s3-csv-telemetry
- [fo-ccc] https://docs.finout.io/billing-integrations/custom-cost-sources/custom-cost-centers
- [fo-prom] https://docs.finout.io/kubernetes-integrations/kubernetes/prometheus/prometheus-per-cluster-integration

**Umbrella**
- [um-net] https://docs.umbrellacost.io/docs/kubernetes-network-cost-enhancement (갱신 2026-06-11)
- [um-split] https://docs.umbrellacost.io/docs/split-shared-costs-using-business-mapping , https://docs.umbrellacost.io/docs/allocate-costs-based-telemtry-kpis
- [um-tp] https://docs.umbrellacost.io/docs/telemetry-pipeline-creation (갱신 2026-07-06)
- [um-byod] https://docs.umbrellacost.io/docs/bring-your-own-data
- [um-pr] https://www.prnewswire.com/news-releases/anodot-forms-umbrella-business-unit-to-focus-on-cloud-cost-management-offering-302417568.html

**IBM Cloudability**
- [cld-new25] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-standard/saas?topic=cloudability-standard-whats-new-in-2025
- [cld-cont] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=allocation-analyze-data-your-containers
- [cld-vendor] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=allocation-container-costs-allocated-by-vendor
- [cld-shared] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=insights-shared-cost-allocation-container-ui
- [cld-tele] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=cloudability-cost-sharing-telemetry-consumption-based-allocations
- [cld-rules] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=points-cost-sharing-rules
- [cld-focus] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=cloudability-connect-custom-data-focus-ingress
- [cld-tag] https://www.ibm.com/docs/en/cloudability-commercial/cloudability-premium/saas?topic=spend-cloudability-tag-label-mapping
- [cld-agent] https://github.com/cloudability/metrics-agent (metrics-agent-2.14.15, 2026-08-21)
- [ibm-apptio] https://newsroom.ibm.com/2023-06-26-IBM-to-Acquire-Apptio-Inc-,-Providing-Actionable-Financial-and-Operational-Insights-Across-Enterprise-IT

**Red Hat Lightspeed cost management / Koku**
- [rh-new] https://docs.redhat.com/en/documentation/cost_management_service/1-latest/html-single/whats_new_in_cost_management/index
- [rh-cm] https://docs.redhat.com/en/documentation/cost_management_service/1-latest/html-single/using_cost_models/index
- [koku-match] https://github.com/project-koku/koku/blob/main/koku/masu/database/trino_sql/aws/openshift/populate_daily_summary/1_resource_matching_by_cluster.sql (마지막 커밋 2025-05-15)
- [koku-sum] https://github.com/project-koku/koku/blob/main/koku/masu/database/trino_sql/aws/openshift/populate_daily_summary/2_summarize_data_by_cluster.sql (마지막 커밋 2025-12-08)
- [koku-dist] https://github.com/project-koku/koku/blob/main/koku/masu/database/sql/openshift/cost_model/distribute_cost/distribute_unattributed_network_cost.sql (마지막 커밋 2025-12-02)

**Vantage**
- [va-net] https://docs.vantage.sh/kubernetes_network_costs
- [va-blog] https://www.vantage.sh/blog/kubernetes-network-cost-attribution (2026-08-06)
- [va-agent] https://docs.vantage.sh/kubernetes_agent , https://docs.vantage.sh/kubernetes
- [va-vt] https://docs.vantage.sh/virtual_tagging
- [va-seg] https://docs.vantage.sh/segments
- [va-bm] https://docs.vantage.sh/business_metrics , https://docs.vantage.sh/per_unit_costs
- [va-cp] https://docs.vantage.sh/connecting_custom_providers
- [va-focus] https://www.vantage.sh/blog/focus-1-3-support (2026-03-12)
- [va-nfr] https://docs.vantage.sh/network_flow_reports
- [va-price] https://www.vantage.sh/pricing

**Coroot, Tollwing, Pelanor**
- [co-cost] https://docs.coroot.com/costs/overview/ (v1.26.1, 2026-09-14)
- [co-cat] https://docs.coroot.com/configuration/application-categories/
- [tw] https://github.com/tollwing/tollwing (v0.2.0, 2026-07-02), https://pkg.go.dev/github.com/tollwing/tollwing/pkg/cost
- [tw-export] https://github.com/tollwing/tollwing/blob/main/opencost-plugin/README.md
- [pel-k8s] https://docs.pelanor.io/integrations/platform/kubernetes.md
- [pel-strat] https://docs.pelanor.io/features/cost-allocation-strategies.md
- [pel-metrics] https://docs.pelanor.io/api-reference/custom-metrics/overview.md
- [pel-inv] https://docs.pelanor.io/integrations/services/custom-invoices.md
- [pel-ctech] https://www.calcalistech.com/ctechnews/article/rkytxtingg

**DoiT, Kentik, New Relic, CloudHealth, Grafana**
- [doit-attr] https://www.doit.com/changelog/introducing-attribute (2026-07-07), https://www.doit.com/blog/doit-launches-attribute-ai-tokenomics-without-tags-sdks-or-code-changes
- [doit-prod] https://www.doit.com/products/attribute-chargeback
- [doit-datahub] https://developer.doit.com/docs/datahub-api , https://help.doit.com/docs/integrations/datahub/tutorial-datahub/tutorial-ingest-data
- [doit-split] https://help.doit.com/docs/analytics/cost-splitting
- [doit-alloc] https://help.doit.com/docs/operate/allocations , https://help.doit.com/docs/kubernetes/eks-intelligence/analyze-eks-costs
- [doit-lens] https://github.com/doitintl/doit-eks-lens-helm-chart (마지막 push 2024-11-20)
- [doit-ps] https://www.businesswire.com/news/home/20250203440309/en/DoiT-Uplevels-Kubernetes-Capabilities-with-Acquisition-of-PerfectScale
- [ctech-attr] https://www.calcalistech.com/ctechnews/article/sycvenedzx
- [kentik-dim] https://kb.kentik.com/KB_Articles/Cb06.htm (갱신 2026-04-24)
- [kentik-agent] https://kb.kentik.com/docs/kentik-kubernetes-agents (갱신 2025-07-31)
- [kentik-mkt] https://www.kentik.com/solutions/reduce-cloud-spend/ , https://www.kentik.com/kentipedia/cloud-egress-costs/
- [nr-nrql] https://docs.newrelic.com/docs/cci/financial-intelligence/nrql-for-cci/
- [nr-ga] https://docs.newrelic.com/whats-new/2026/04/whats-new-04-28-cci-ga/
- [nr-overview] https://docs.newrelic.com/docs/cci/overview/
- [ch-kb] https://knowledge.broadcom.com/external/article/416086/unallocated-kubernetes-load-balancer-net.html
- [ch-upd] https://help.cloudhealthtech.com/prod-updates/09-30-2025/
- [ch-k8s] https://techdocs.broadcom.com/us/en/ca-enterprise-software/it-operations-management/cloudhealth/saas/index/working-with-reports-and-recommendation-of-tanzu-cloudhealth-working-with-reports-recommendations/working-with-reports-and-recommendation-of-tanzu-cloudhealth-kubernetes-reports-new.html (갱신 2026-03-31)
- [ch-byod] https://techdocs.broadcom.com/us/en/ca-enterprise-software/it-operations-management/cloudhealth/saas/index/exploring-cloudhealth-new-experience-exploring-cloudhealth-index/exploring-cloudhealth-new-experience-getting-started/bring-your-own-data--byod--with-data-connect.html
- [gc-costs] https://grafana.com/docs/grafana-cloud/monitor-infrastructure/kubernetes-monitoring/manage-costs/ , https://raw.githubusercontent.com/grafana/k8s-monitoring-helm/main/charts/k8s-monitoring/charts/feature-cost-metrics/values.yaml (k8s-monitoring 4.5.2, 2026-09-11)
- [gc-attr] https://grafana.com/docs/grafana-cloud/cost-management-and-billing/cost-attributions/

**CloudZero, Harness, nOps, Ternary, Kion, Rafay, Cloudchipr, OptScale, 기타 벤더**
- [cz-split] https://docs.cloudzero.com/docs/splitting-shared-costs
- [cz-k8s] https://docs.cloudzero.com/docs/container-cost-track (갱신 2026-09-09), https://github.com/Cloudzero/cloudzero-charts (1.2.14, 2026-08-11)
- [cz-tele] https://docs.cloudzero.com/docs/telemetry-streams
- [cz-rp] https://docs.cloudzero.com/reference/replaceperiodallocationtelemetry.md (OpenAPI v1.3.0, 갱신 2026-07-09)
- [cz-api] https://docs.cloudzero.com/reference/allocation-telemetry-api-1 (갱신 2025-09-10)
- [cz-anycost] https://docs.cloudzero.com/docs/anycost-stream-getting-started
- [cz-cbf] https://docs.cloudzero.com/docs/anycost-common-bill-format-cbf (갱신 2026-07-16)
- [hn-cc] https://developer.harness.io/cloud-cost-management/cost-reporting/cost-categories/cost-categories
- [hn-kc] https://developer.harness.io/cloud-cost-management/use-cacm/key-concepts , https://developer.harness.io/cloud-cost-management/use-cacm/scad
- [hn-ccp] https://developer.harness.io/cloud-cost-management/integrations/custom-cost-provider
- [nops-api] https://help.nops.io/api-reference/cost-allocation/create-allocation-rule.md , https://help.nops.io/api-reference/cost-allocation/create-showback.md
- [tern-alloc] https://docs.ternary.app/docs/cost-allocation
- [tern-eks] https://docs.ternary.app/docs/kubernetes-insights-amazon-elastic-kubernetes-service
- [tern-byod] https://docs.ternary.app/docs/bring-your-own-data-byod-upload.md , https://docs.ternary.app/reference/createcustomdataintegration.md
- [kion-alloc] https://kion.io/platform/finops/cost-allocation/
- [kion-support] https://support.kion.io/hc/en-us/articles/39072743302157-Custom-Accounts-Custom-Billing-Sources
- [rafay-cg] https://docs.rafay.co/cost_management/cost_governance/
- [cc-dim] https://docs.cloudchipr.com/docs/dimensions
- [cc-k8s] https://docs.cloudchipr.com/docs/kubernetes-cost-allocation.md
- [os-assign] https://hystax.com/documentation/optscale/assign-resources.html , https://raw.githubusercontent.com/hystax/optscale/integration/trapper/trapper_worker/processor.py (기본 브랜치 integration)
- [os-723] https://github.com/hystax/optscale/issues/723
- [cb-acq] https://www.cloudbolt.io/company/news/cloudbolt-acquires-stormforge/
- [cb-k8s] https://www.cloudbolt.io/stormforge/kubernetes-cost-allocation/ , https://www.cloudbolt.io/capabilities/kubernetes-cost-allocation/
- [cb-ec] https://www.efficientlyconnected.com/cloudbolt-launches-container-level-kubernetes-cost-allocation/
- [mav-pr] https://www.prnewswire.com/news-releases/finops-platform-digitalex-rebrands-as-mavvrik-announces-6-2mm-seed-round-to-address-spiraling-it-costs-302379322.html
- [yota] https://www.yotascale.com/
- [groundcover] https://docs.groundcover.com/capabilities/infrastructure-monitoring
- 기타(표 밖 언급): Komodor https://komodor.com/blog/kubernetes-cost-optimization-done-right/ , Amnic https://amnic.com/k8s-cost-observability , PointFive https://www.pointfive.co/kubernetes

**Backstage**
- [bs-api] https://github.com/backstage/community-plugins/blob/main/workspaces/cost-insights/plugins/cost-insights/src/api/CostInsightsApi.ts (npm @backstage-community/plugin-cost-insights 0.30.0, 2026-08-29)
- [bs-aws] https://github.com/awslabs/backstage-plugins-for-aws/blob/main/plugins/cost-insights/README.md (마지막 커밋 2026-08-10)

**AWS**
- [aws-cc] https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/manage-cost-categories.html
- [aws-split] https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/splitcharge-cost-categories.html
- [aws-ccapi] https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_UpdateCostCategoryDefinition.html , https://docs.aws.amazon.com/aws-cost-management/latest/APIReference/API_CostCategorySplitChargeRule.html
- [aws-tags] https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/cost-alloc-tags.html , https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/billing-limits.html
- [aws-acct-tags] https://aws.amazon.com/about-aws/whats-new/2025/12/cost-allocation-tags/ (2025-12-19)
- [aws-backfill] https://aws.amazon.com/about-aws/whats-new/2024/03/aws-cost-allocation-tags-retroactive-application/ (2024-03-27)
- [aws-cur-dt] https://docs.aws.amazon.com/cur/latest/userguide/cur-data-transfers-charges.html
- [aws-dt-blog-2019] https://aws.amazon.com/blogs/mt/using-aws-cost-explorer-to-analyze-data-transfer-costs (2019-06-12)
- [aws-scad] https://docs.aws.amazon.com/cur/latest/userguide/split-cost-allocation-data.html , https://docs.aws.amazon.com/cur/latest/userguide/split-line-item-columns.html
- [aws-scad-labels] https://docs.aws.amazon.com/cur/latest/userguide/split-cost-allocation-data-kubernetes-labels.html
- [aws-scad-news] https://aws.amazon.com/about-aws/whats-new/2025/10/split-cost-allocation-data-amazon-eks-kubernetes-labels (2025-10-30)
- [aws-bcd] https://docs.aws.amazon.com/billingconductor/latest/userguide/what-is-billingconductor.html
- [aws-bcd-cli] https://docs.aws.amazon.com/billingconductor/latest/userguide/create-cli-percentage.html , https://docs.aws.amazon.com/billingconductor/latest/APIReference/API_LineItemFilter.html
- [aws-bcd-price] https://aws.amazon.com/aws-cost-management/aws-billing-conductor/pricing/
- [aws-focus12] https://aws.amazon.com/about-aws/whats-new/2025/11/aws-data-exports-focus-1-2-available (2025-11-19), https://docs.aws.amazon.com/cur/latest/userguide/table-dictionary-focus-1-2-aws.html
- [aws-dx-faq] https://aws.amazon.com/aws-cost-management/aws-data-exports/faqs/
- [cid-scad] https://docs.aws.amazon.com/guidance/latest/cloud-intelligence-dashboards/scad-containers-dashboard.html , https://docs.aws.amazon.com/guidance/latest/cloud-intelligence-dashboards/scad-containers-dashboard-tco.html
- [cid-focus] https://docs.aws.amazon.com/guidance/latest/cloud-intelligence-dashboards/focus-dashboard.html
- [cid-faq] https://docs.aws.amazon.com/guidance/latest/cloud-intelligence-dashboards/faq.html
- [nfm-eks] https://docs.aws.amazon.com/eks/latest/userguide/network-observability.html
- [nfm-blog] https://aws.amazon.com/blogs/containers/track-inter-az-and-nat-gateway-traffic-with-eks-container-network-observability/ (2026-05-05)

**FOCUS, FinOps Foundation, GKE, AKS**
- [focus-rel] https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/releases (v1.4 2026-06-04, v1.3 2025-12-08, v1.2 2025-05-29)
- [focus-14] https://www.finops.org/insights/introducing-focus-1-4/ (2026-06-10)
- [focus-handling] https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/blob/v1.4/specification/attributes/data_generator_calculated_split_cost_allocation_handling.md , https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/blob/v1.4/specification/supported_features/data_generator_calculated_split_cost_allocation.md
- [focus-amid] https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/blob/v1.4/specification/datasets/cost_and_usage/columns/allocatedmethodid.md , 같은 폴더의 allocatedmethoddetails.md, allocatedresourceid.md, allocatedtags.md
- [focus-glossary] https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/blob/v1.4/specification/glossary.md , https://github.com/FinOps-Open-Cost-and-Usage-Spec/FOCUS_Spec/blob/v1.4/license.md
- [finops-alloc] https://www.finops.org/framework/capabilities/allocation/
- [finops-container] https://www.finops.org/wg/calculating-container-costs/
- [gke-ca] https://docs.cloud.google.com/kubernetes-engine/docs/how-to/cost-allocations (갱신 2026-09-09)
- [gke-meter] https://docs.cloud.google.com/kubernetes-engine/docs/how-to/cluster-usage-metering
- [aks-ca] https://learn.microsoft.com/en-us/azure/aks/cost-analysis (ms.date 2026-07-29), https://learn.microsoft.com/en-us/azure/cost-management-billing/costs/view-kubernetes-costs
- [az-azfree] https://learn.microsoft.com/en-us/azure/reliability/availability-zones-overview (ms.date 2026-02-11), 2차: https://www.infoq.com/news/2024/06/azure-az-transfer-fees/

**공개 사례**
- [grab] https://engineering.grab.com/zero-traffic-cost (2023-07-07)
- [doordash] https://careersatdoordash.com/blog/staying-in-the-zone-how-doordash-used-a-service-mesh-to-manage-data-transfer-reducing-hops-and-cloud-spend/ (403, **[미조회]**)
- [doordash-infoq] https://www.infoq.com/news/2024/01/doordash-service-mesh
- [pol-case] https://ebpf.foundation/case-study-polar-signals-uses-ebpf-to-monitor-internal-cross-zone-network-traffic-on-kubernetes-reducing-these-operating-costs-by-50/
- [pol-gh] https://github.com/polarsignals/kubezonnet (Apache-2.0, 생성 2024-12-06)
- [oneuptime] https://oneuptime.com/blog/post/2026-08-04-allocate-nat-gateway-cross-az-network-cost/view
