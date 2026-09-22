# 판정: ENI(VPC Flow Logs)만으로 multi-AZ 전송 비용을 추적할 수 있나

## 1. 결론

Flow Logs는 crossaz가 쓸 수 있는 바이트 소스 중 가장 좋습니다. 하지만 그것만으로 **청구 금액**을 추적할 수는 없습니다. 판정은 이렇게 갈립니다.
- **skeptic이 옳은 부분:** CUR 보정 전에는 flow 기반 금액을 "billed"라고 부르면 안 됩니다.
- **defender가 옳은 부분:** 청구와 같은 ENI 지점에서 LB·NAT·RDS·VPCE ENI와 UDP까지 top-K 없이 보는 소스는 이것뿐입니다 [fl-basics][fl-rec].

단위별로 나누면 다음과 같습니다.

- **계정·VPC 합계: 조건부로 충분합니다.** 조건은 넷입니다: VPC 단위 구독, regional NAT 구독 확인, 규칙 엔진 확장, CUR 계정·일 보정. 공식 미기록 목록 중 과금이 문서화된 것은 Traffic Mirroring과 몇 분 만에 삭제된 regional NAT뿐입니다 [fl-lim][mirror].
- **노드(인스턴스) 단위: 바이트는 충분하지만 청구 대조가 되는지는 아직 모릅니다.**
  - CUR 2.0 사전은 resource ID가 "usage types that aren't associated with an instantiated host, such as data transfers"에서 비어 있다고 적습니다 [cur2-li].
  - 반면 AWS Well-Architected Labs 쿼리(2022)는 InterZone-In/Out 비용을 `line_item_resource_id`로 묶습니다 [wa-labs].
  - 두 자료가 충돌합니다. 실제로는 ID가 채워질 가능성이 있다고 봅니다(추론). PoC 첫날(D0) 확인이 필요합니다.
- **워크로드 단위: VPC 안 목적지로 가는 pod IP 트래픽에는 충분합니다.** pod↔pod, RDS/ElastiCache/MSK, 내부 LB, Fargate가 여기에 해당합니다 [fl-rec]. 아래 경우에는 노드 단위로 무너집니다.
  - 기본 SNAT: VPC CIDR 밖 목적지(peering·TGW·NAT·온프렘) [eks-snat]
  - hostNetwork, overlay CNI
  - EFS 마운트(추론)
  - pod IP 재사용 구간 [vpccni-bp]
- **달러 정확도: 단독으로는 부족합니다.**
  - `bytes`와 청구 GB의 관계(헤더 층위, GB 단위)는 문서가 없습니다 [fl-rec].
  - 어느 쪽이 과금되는지, 무료 경로인지는 레코드에 없습니다.
  - SKIPDATA는 손실이 있었다는 사실만 알려 주고 양은 알려 주지 않습니다 [fl-examples].

**결정:** v2는 역할을 이렇게 나눕니다: flow logs는 바이트와 AZ 쌍, 규칙 엔진은 과금 측, CUR은 보정. 5절 게이트를 통과하기 전에는 "billed" 라벨을 쓰지 않습니다.

## 2. 커버리지 표

| 과금 경로 | Flow Logs에 보이나 | pod 귀속 가능한가 | 비고(출처) |
|---|---|---|---|
| pod↔pod 다른 AZ (secondary IP, prefix, custom networking) | 예, 양쪽 노드 ENI | 예. kube_pod_info 시간 구간 조인. IP 재사용 창은 모호 | 양측 각 $0.01. srcaddr는 ENI primary IP라 pkt-* 필드만 씀 [fl-lim][billing-rules §1.1]. prefix IP는 현재 `internal/collect/aws.go` IPZones가 풀지 못함 |
| ClusterIP, NodePort hop(ETP=Cluster) | 예. ClusterIP는 wire에 나오지 않고 백엔드 pod IP로 보임 | ClusterIP는 예. NodePort hop은 백엔드만, 출발지는 노드 | [k8s-vip][eks-fl-blog] |
| hostNetwork pod, 노드 프로세스 | 예(노드 IP) | 노드만. 서버 포트 휴리스틱 | [eks-snat] |
| pod → VPC CIDR 밖 (peering·TGW·NAT·온프렘·같은 리전 공인 IP) | 예 | 아니오. 기본 SNAT로 노드 IP. 예외: EXTERNALSNAT, SGP strict, IPv6 pod의 IPv6 트래픽 | 공인 IP 헤어핀은 InterZone이 아니라 PublicIP로 잡힘 [eks-snat][cni-ipv6] |
| 내부 ALB/CLB (같은 VPC 사설, **무료**) | 예. interface-type enum에 ALB가 없어 `-`로 나옴 | 타겟 pod 예 | ENI Description으로 분류하지 않으면 pod-pod $0.02로 과대계상 [fl-rec][billing-rules §2] |
| NLB: client→NLB, NLB→target(cross-zone) | 예. NLB ENI에 frontend/backend 두 엔트리 | 타겟은 예. 클라이언트는 preservation이 켜졌을 때만 IP가 보임 | preservation이 켜지면 타겟 ENI 레코드에서 NLB hop이 사라지고, NLB에서 들어오는 ingress에는 next-hop 필드가 없음 [nlb-mon][fl-lim][nlb-attr] |
| zonal NAT GW, 다른 AZ | 예(노드 ENI와 NAT ENI) | 노드만 | NAT 측 계량 여부는 문서 없음. 처리료는 AZ와 무관 [fl-examples][vpc-pricing] |
| regional NAT GW | 부분. ResourceType이 따로 있고 interface-id가 `-`. VPC 구독에 포함되는지는 문서 없음 | 노드만 | AZ 확장 중 최대 60분간 다른 zone에서 처리 [api-cfl][nat-regional]. 금액은 unknown |
| RDS, ElastiCache, MSK Provisioned | 예(requester-managed ENI) | 예 | 클라이언트(EC2) 측만 과금되므로 노드 ENI만 셈. MSK 브로커 측은 문서 없음 [fl-basics][billing-rules §1.3] |
| EFS 마운트 타겟 | 예 | 노드만(호스트 네임스페이스 마운트, 추론) | 몇 측이 과금되는지 문서 없음 [billing-rules §3] |
| EKS control-plane x-ENI (exec, logs, webhook) | 예로 추론(명시 문서 없음) | 노드 측 | 노드 측만 과금, 업그레이드 때 ENI 재생성 [eks-netreq]. **rules.yaml에 규칙이 없어** `unclassified`(billable:false)로 떨어짐 |
| 인터페이스 VPCE, TGW | 예 | VPCE는 예. TGW 너머는 SNAT로 노드 | AZ 간 무료(xAZ usage type, $0). 무료로 분류하지 않으면 과대계상 [eks-cost-bp][pl-2022]. InterfaceType 값은 믿을 수 없음 |
| GWLB 엔드포인트 경유 | GWLBE→GWLB hop 기록 여부 문서 없음 | 노드 ENI 기준 | 2022 면제에서 제외됨 [gwlb-mon][billing-rules §1.4] |
| VPC peering(교차 계정) | 자기 쪽 ENI만 | SNAT면 노드 | 상대 AZ를 풀 수 없음. egress만 세면 In 측이 빠짐. VpcPeering usage type [fl-lim][vpc-pricing] |
| Fargate, SGP branch ENI | 예(Fargate), 추론상 예(branch) | 예 | Fargate의 instance-id와 CUR 키, branch/trunk 이중 기록 여부 모두 문서 없음 [eks-fargate][sgpp] |
| Traffic Mirroring | 아니오(소스 측) | 아니오 | 미러 트래픽에도 전송료 부과 [mirror] |
| Overlay CNI (Cilium/Calico VXLAN) | 노드 간 터널로만 보임 | 아니오 | EKS가 EC2 노드에서 지원하는 CNI가 아님 [alt-cni] |
| Local Zone 노드 | 예. az-id가 `-`이고 sublocation-id가 있음 | 예 | 부모 AZ 측 요금 문서 없음 [fl-rec][billing-rules §1.2] |

## 3. 막히는 지점 top 5

1. **CUR 보정 입도 (blocking)**
   - 문제: 공식 사전과 Well-Architected Labs 쿼리가 충돌합니다(1절). 인스턴스별 k를 못 구하면 노드 단위 청구 대조가 불가능하고, 계정·일 단위(비-EKS 워크로드 섞임)로 떨어집니다.
   - 완화:
     - D0에 Q0 실행.
     - 실패 시 클러스터 전용 계정에서 계정·일 k로 보정.
     - CE 리소스 단위 데이터는 EC2-Compute·14일 한정이라 대체가 안 됩니다 [billing-rules 부록 A].
   - PoC 검증: **가능.** 읽기 전용이며 export만 사용자가 만듭니다. 기존 CUR이 있으면 바로 확인할 수 있습니다.
2. **bytes ↔ 청구 GB (달러 추적에 blocking)**
   - 문제: `bytes` 정의는 한 문장뿐입니다 [fl-rec]. 헤더 포함 여부, 재전송·거부 패킷 처리, GB 단위는 문서가 없습니다. 10^9와 2^30의 차이(7.4%)가 헤더 오버헤드와 비슷한 크기라, 한 번 비교로는 원인을 가를 수 없습니다.
   - 완화: 조용한 계정에서 패킷 크기 두 가지로 측정해 층위와 단위를 분리합니다. bytes-per-GB는 이름 있는 상수로 두고, k는 In과 Out을 따로 구합니다.
   - PoC 검증: **가능.** 사용자가 테스트 리소스를 만들어야 합니다.
3. **과금 측·무료 판정과 시간에 따라 바뀌는 ENI 정체 (blocking)**
   - rules.yaml(직접 확인)에 NAT, peering, TGW, VPCE, control-plane, public-IPv4 규칙이 없습니다. `zone-unknown`과 `unclassified`는 `billable: false`라 **조용히 과소계상**합니다.
   - interface-type enum은 5종뿐입니다 [fl-rec]. LB IP는 재매핑되고 x-ENI는 업그레이드 때 새로 만들어집니다 [eks-netreq]. 그래서 실행 시점 스냅샷으로는 과거 레코드를 분류할 수 없습니다.
   - 완화:
     - 매시간 DescribeNetworkInterfaces 스냅샷과 CloudTrail 조회.
     - flow 입력의 미분류 바이트는 $0이 아니라 `unknown`으로 보고.
     - 누락 규칙 추가.
   - PoC 검증: **가능.** Q6로 측정합니다.
4. **middlebox hop 은폐 (material, NLB 비중이 큰 클러스터에는 blocking)**
   - 대상: NLB client IP preservation(instance TG 기본값) [nlb-attr], regional NAT 포함 여부, GWLBE hop, 교차 계정 peering의 상대 AZ.
   - 완화: hop마다 송신 ENI에서 셉니다(NLB ENI egress). RegionalNatGateway 구독을 확인하고, peer CIDR→AZ ID 매핑은 설정으로 받습니다. 풀리지 않으면 unknown으로 둡니다.
   - PoC 검증: NLB는 **가능**(T4). regional NAT는 사용자가 만들면 가능. peering과 GWLB는 그런 환경이 있을 때만.
5. **워크로드 귀속 붕괴 (material, 워크로드 단위 목표에는 blocking)**
   - 원인: SNAT, hostNetwork, overlay, EFS, IP 재사용. IP 재사용 창은 cooldown 30초, 타임스탬프 ±60초, scrape 1분이 겹칩니다 [vpccni-bp][fl-rec]. Prometheus 기본 보존 15일은 CUR 재작성 창(최대 2주)과 거의 같습니다 [prom-storage][cur-delivery].
   - 완화:
     - 버킷 분리: exact / ambiguous / node-only / LB·NAT·VPCE / unknown.
     - 노드 버킷은 cAdvisor tx로 나누고 "모델"이라고 표시.
     - `EXCLUDE_SNAT_CIDRS`는 Fix.Command 제안으로만 둠.
     - prefix CIDR containment 추가.
   - PoC 검증: **가능.** 실 클러스터 7일.

## 4. 로그 비용 판단

**요금 구조**
- us-east-1 S3 vended 전달은 첫 10TB까지 $0.25/GB, Parquet 변환은 $0.03/GB입니다(Price List 2026-09-14) [cw-pl]. Athena는 $5/TB입니다 [athena-price].
- 청구 GB가 압축 전 크기인지는 공식 문서가 없습니다. CE usage type(`S3-Egress-Bytes`, `S3-Egress-InputBytes`)으로 실측합니다 [cw-billing][vended-kc].

**무엇에 비례하나**
- 레코드 수는 5-tuple 수 × 그 흐름을 보는 ENI 수 × 60초 이하 윈도우 수에 비례합니다. **바이트와는 무관합니다** [fl-rec].
- 반면 전송비는 AZ를 넘은 GB × $0.02입니다.

**로깅비가 추적 대상 요금을 넘는 조건 (추론, 가정 기반 계산)**
- 레코드 184바이트, 연결당 4레코드/분으로 가정하면, 연결-분당 교차 전송이 약 10KB 미만일 때 로깅비가 더 큽니다.
- 해당하는 트래픽:
  - 다른 노드의 CoreDNS로 가는 UDP 질의(질의마다 새 5-tuple)
  - NLB 헬스체크(실제 횟수 문서 없음 [nlb-hc])
  - 짧은 연결 위주 트래픽
  - 이미 zone-local이 잘 된 소규모 클러스터
- 반대로 Kafka, 복제, 셔플처럼 오래 가는 대용량 연결에서는 로깅비가 무시할 만합니다.

**효과 없는 완화**
- 600초 집계: Nitro ENI는 설정과 무관하게 60초 이하 [api-cfl]
- 방향·포트 필터: TrafficType은 ACCEPT/REJECT/ALL뿐
- 노드 서브넷만 구독: pod↔pod 레코드는 줄지 않음
- 따라서 measurement-alternatives.md 100–101행("트래픽 양에 비례")은 틀렸고 고쳐야 합니다.

**효과 있는 완화**
- 필드 최소화, Parquet + 시간 파티션
- NodeLocal DNSCache: 업스트림 `force_tcp`로 5-tuple이 줄고, 실제 DNS 교차 비용도 줄어듦 [nodelocaldns]
- 주 1일 샘플링 후 CE 일별 InterZone GB로 스케일
- 관측비 게이트(G7)
- S3 lifecycle은 CUR 재작성 창 이상으로 유지(추론)

## 5. 착수 전 PoC 계획 (2주)

### 임계값의 공통 근거: 오차 예산 ±10%

billing-rules.md §5.3 표에서 2-AZ ALB 우회의 GB당 절감은 AZ 간 전송비의 20%입니다($0.002 / $0.010). 저장소에 기록된 결정 여유 중 가장 작은 값입니다. 측정 오차가 그 절반을 넘으면 권고가 뒤집힐 수 있어 예산을 ±10%로 둡니다. 정책값이므로 사용자가 조정할 수 있습니다.

### 사용자가 직접 할 쓰기 작업

- **U1.** payer 계정에서 CUR 2.0 export를 만듭니다(HOURLY, INCLUDE_RESOURCES). resource ID가 있는 기존 CUR이 있으면 생략하고 G0를 바로 봅니다.
- **U2.** flow log를 두 개 만듭니다: (a) 조용한 테스트 계정 VPC, (b) 실 클러스터 VPC.
  - 설정: VPC 단위, 전용 S3 prefix, Parquet, 시간 파티션, MaxAggregationInterval 60.
  - 포맷: `${interface-id} ${instance-id} ${az-id} ${sublocation-id} ${flow-direction} ${srcaddr} ${dstaddr} ${pkt-srcaddr} ${pkt-dstaddr} ${srcport} ${dstport} ${protocol} ${bytes} ${packets} ${start} ${end} ${log-status} ${traffic-path} ${interface-type} ${next-hop-az-id} ${next-hop-interface-id} ${next-hop-interface-type} ${resource-id}`
- **U3.** regional NAT를 쓰면 RegionalNatGateway 구독을 추가합니다.
- **U4.** Athena 준비와 IAM 역할:
  - managed-results workgroup, flows/cur2 테이블 DDL(partition projection)
  - 읽기 IAM 역할: `athena:StartQueryExecution/GetQueryExecution/GetQueryResults`, `glue:GetDatabase/GetTable`, `s3:GetObject/ListBucket`, `ec2:Describe*`, `elasticloadbalancing:Describe*`, `ce:GetCostAndUsage`, `cloudtrail:LookupEvents`
- **U5.** 테스트 계정 리소스를 만들고 끝나면 삭제합니다: 다른 AZ의 Nitro 노드 2대, iperf3 pod, internal NLB(instance TG, cross-zone on), (선택) RDS 1대.

### 읽기 전용 단계

- **R0.** DescribeFlowLogs로 포맷, interval, 겹치는 구독을 확인합니다. Athena는 `$path`의 flow_log_id로 구독 하나만 읽습니다.
- **R1.** DescribeNetworkInterfaces 스냅샷을 매시간 로컬에 저장합니다(Description, RequesterId, InterfaceType, prefix, attachment).
- **R2.** aws-node 환경변수(EXTERNALSNAT, EXCLUDE_SNAT_CIDRS, IP_COOLDOWN_PERIOD, POD_SECURITY_GROUP_ENFORCING_MODE)와 Prometheus 보존 기간·scrape 간격을 기록합니다.
- **R3.** Athena는 SELECT만, CE는 DAILY × USAGE_TYPE/OPERATION으로 조회합니다.

### 일정

- **D0:** U1–U4, R0–R2. 기존 CUR이 있으면 Q0.
- **D1–D2, 조용한 계정.** 실험마다 UTC 정각 시간 단위로 분리합니다.
  - T1: TCP, 큰 MSS, 고정 속도 6시간
  - T2: UDP 약 100바이트 payload, 같은 초당 바이트로 6시간
  - T3: SG로 수신 차단(REJECT) 2시간
  - T4: NLB 은폐 시나리오 3시간. 클라이언트와 타겟은 AZ-a, AZ-b의 NLB 노드 IP로 접속
  - T5: 교차 AZ `kubectl logs -f`/`exec` 1시간
  - T6(선택): 다른 AZ RDS 2시간
  - 속도는 CUR 반올림이 무의미한 수준으로 잡습니다. 예: 100Mbps ≈ 45GB/h, 전송비 약 $0.9/h(계산값).
- **D1–D8, 실 클러스터:** 7일 연속 수집.
- **D3–D4:** 테스트 계정 CUR이 도착하면 G1, G2 판정.
- **D9–D12:** 실 클러스터 G3–G7 판정. 비교 대상은 D+2 이후 데이터만 씁니다.
- **D13–D14:** 최종 판정. CUR은 최대 2주까지 재작성되므로 [cur-delivery] k는 월 마감 후 다시 계산합니다.

### Athena 쿼리 의도

- **Q0 CUR 키:** Regional-Bytes InterZone 행의 GB를 product_code × resource_id 접두사(`i-` / `nat-` / ELB ARN / 빈 값)로 합산합니다.
- **Q1 수집 상태:**
  - ENI-시간별 log_status 분포(SKIPDATA)
  - deliverLogsStatus [api-fl]
  - 노드-시간당 레코드 수
  - UDP/53과 헬스체크 포트 비중
- **Q2 hop 바이트:**
  - 필터: egress이고 `az_id <> coalesce(next_hop_az_id, 목적지 서브넷 AZ)`
  - 합산 키: 시간 × interface_id × instance_id × interface_type × next_hop_interface_type × pkt 주소
  - 과금 측은 crossaz가 인벤토리와 규칙으로 정합니다.
- **Q3 대칭성:** 구독 안에 있는 ENI 쌍에서 송신 egress 대 수신 ingress의 시간별 합계 비.
- **Q4 보정:** 인스턴스-일(테스트 계정은 인스턴스-시간) × operation마다 예측 GB와 CUR GB를 비교해 k를 구합니다. In/Out은 따로.
- **Q5 NLB:** 세 예측을 CUR 변화량과 비교합니다: 타겟 ENI 주소쌍 조인 / NLB ENI hop별 / v5 필드만.
- **Q6 분류 공백:** unclassified, zone-unknown, unknown, 인벤토리에 없는 interface_id의 바이트 비율.
- **Q7 귀속:** Q2 결과를 crossaz가 kube_pod_info 구간과 프로세스 안에서 조인해 버킷별 USD를 냅니다. pod 테이블을 S3에 쓰면 쓰기 작업이라 하지 않습니다.
- **Q8 관측비:**
  - (CE `S3-Egress-Bytes` + `S3-Egress-InputBytes` + Athena 스캔비) ÷ 같은 날 InterZone USD
  - 청구된 로그 바이트 ÷ 레코드 수 = 레코드당 바이트

### GO/NO-GO

| 게이트 | 지표 | GO | NO-GO / 격하 | 근거 |
|---|---|---|---|---|
| G0 CUR 키 | EC2 InterZone GB 중 resource_id가 `i-`인 비율 | ≥95% | <95%면 노드 단위 billed 대조를 포기하고 계정·일 대조로 격하(v2 전체 중단은 아님) | 키 없는 5%는 예산의 절반 |
| G1 보정 (T1/T2) | 시간별 k 변동계수, k(T2)/k(T1), k_In/k_Out | CV ≤3%, 비 0.98–1.02, In/Out ±2% | CV >10%이거나 k가 패킷 크기에 따라 설명 없이 변함 → bytes를 청구 GB로 환산할 수 없음 | 고정 속도에서 ±60초 오차는 60분 버킷 경계당 약 1.7%. 청구가 Ethernet 14바이트를 센다면 128바이트 패킷에서 약 11%, 9001바이트에서 약 0.2% 차이라 ±2%로 충분히 가려짐. k 값 자체(≈1.00 vs ≈1.074)는 합격 기준이 아니라 GB 단위 결정용. T3는 참고용 |
| G2 NLB (T4) | hop별 예측 vs 클라이언트+타겟 인스턴스의 CUR 변화량 | ±5% | 벗어나거나 NLB ENI egress 레코드가 없음 → NLB 경로는 unknown으로 유지 | 통제 환경이므로 예산의 절반 |
| G3 대조 (실 클러스터 7일) | 노드-일 \|k_i/k_G1 − 1\|, 계정-일 미설명 GB | InterZone USD의 90% 이상을 차지하는 노드-일이 ±10% 이내, 미설명 ≤10% | 실패했는데 G4/G5로 설명되지 않음 → NO-GO | 예산 ±10%. 일 단위라 시간 경계 잡음이 빠짐 |
| G4 분류 공백 | Q6 바이트 ÷ 교차 AZ egress 바이트 | ≤5% | >10% | 이 버킷 단가는 $0–$0.02라 불확실성이 100%. 5%면 USD 오차 ≤5% |
| G5 손실 | SKIPDATA ENI-시간 비율, FAILED, Q3 중앙값 | ≤1%, FAILED 0, 대칭 ±2% | SKIPDATA >5%, 또는 SKIPDATA 없는 시간에 k 이상치가 반복됨(표식 없는 손실) | 그 1% 시간이 평균의 5배 트래픽을 실어도 편향 ≤5% |
| G6 귀속 | 미귀속(node-only+unknown) USD 비율, ambiguousIP 비율 | 미귀속 ≤10%, ambiguous ≤5% → 워크로드 금액까지 GO | 미귀속이 1위 워크로드 비중보다 큼 → 워크로드 순위도 NO-GO, 노드·AZ 쌍 단위만 | 미귀속 버킷이 1위보다 크면 1위 원인을 가릴 수 있음. 중간 구간은 순위만 GO |
| G7 관측비 | Q8 비율 | ≤10%면 상시 수집 GO | 10–70%면 주 1일 샘플링으로만 GO. >70%면 상시·샘플링 모두 NO-GO → v1 hybrid와 단기 구독 | 관측비도 예산 한도. 1/7 샘플링이면 70%가 10%로 내려감(추론) |

**최종 규칙**
- G0–G5 모두 통과: v2 착수, "billed(보정됨)" 라벨 허용.
- G0만 실패: v2는 착수하되 노드 단위는 "flow 추정"으로 표기하고, 계정 합계만 보정합니다.
- G1 또는 G3 실패: billed 주장은 NO-GO. flow logs는 경로 탐지와 순위 보조로만 씁니다.
- G6과 G7은 범위와 운영 모드만 정합니다.

## 6. 그래도 v1(스냅샷)을 버리면 안 되는 이유

- flow logs는 구독 이전 데이터가 없고, 구독과 CUR export라는 쓰기 작업이 필요합니다 [fl-lim]. 반면 "왜 새는가"와 Fix(trafficDistribution hint, cross-zone 속성, kube-proxy 버전)는 읽기 전용 v1 토폴로지에서만 나옵니다.
- G6/G7에서 떨어지는 클러스터(overlay CNI, 저바이트·고빈도 트래픽)와 샘플링하지 않는 날에는 v1 hybrid(CUR 노드 합계 × 가중치)가 유일한 대체입니다.

---
출처 상태 표기: 기본은 선행 조사에서 조회한 문서입니다. 이번 판정에서 직접 조회한 것은 [cur2-li], [wa-labs](gh api), 2022 CUR/Athena 블로그입니다. [미조회]는 본문을 가져오지 못한 출처입니다.

[fl-rec]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-log-records.html
[fl-lim]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-limitations.html
[fl-examples]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-records-examples.html
[fl-basics]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-basics.html
[api-cfl]: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateFlowLogs.html
[api-fl]: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_FlowLog.html
[cur2-li]: https://docs.aws.amazon.com/cur/latest/userguide/table-dictionary-cur2-line-item.html
[wa-labs]: https://github.com/awslabs/aws-well-architected-labs/blob/HEAD/static/Cost/300_CUR_Queries/Code/Networking_%26_Content_Delivery/imbalance-interaz-transfer.sql
[cur-delivery]: https://docs.aws.amazon.com/cur/latest/userguide/dataexports-export-delivery.html
[nlb-mon]: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-monitoring.html
[nlb-attr]: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html
[nlb-hc]: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/target-group-health-checks.html
[nat-regional]: https://docs.aws.amazon.com/vpc/latest/userguide/nat-gateways-regional.html
[vpc-pricing]: https://aws.amazon.com/vpc/pricing/
[eks-snat]: https://docs.aws.amazon.com/eks/latest/userguide/external-snat.html
[cni-ipv6]: https://docs.aws.amazon.com/eks/latest/userguide/cni-ipv6.html
[vpccni-bp]: https://docs.aws.amazon.com/eks/latest/best-practices/vpc-cni.html
[k8s-vip]: https://kubernetes.io/docs/reference/networking/virtual-ips/
[eks-fl-blog]: https://aws.amazon.com/blogs/networking-and-content-delivery/using-vpc-flow-logs-to-capture-and-query-eks-network-communications/
[eks-netreq]: https://docs.aws.amazon.com/eks/latest/userguide/network-reqs.html
[eks-cost-bp]: https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html
[pl-2022]: https://aws.amazon.com/about-aws/whats-new/2022/04/aws-data-transfer-price-reduction-privatelink-transit-gateway-client-vpn-services/
[gwlb-mon]: https://docs.aws.amazon.com/elasticloadbalancing/latest/gateway/monitoring.html
[eks-fargate]: https://docs.aws.amazon.com/eks/latest/userguide/fargate.html
[sgpp]: https://docs.aws.amazon.com/eks/latest/best-practices/sgpp.html
[mirror]: https://docs.aws.amazon.com/vpc/latest/mirroring/what-is-traffic-mirroring.html
[alt-cni]: https://docs.aws.amazon.com/eks/latest/userguide/alternate-cni-plugins.html
[cw-pl]: https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonCloudWatch/current/us-east-1/index.json
[cw-billing]: https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/cloudwatch_billing.html
[vended-kc]: https://repost.aws/knowledge-center/cloudwatch-vended-log-billed-charges
[athena-price]: https://aws.amazon.com/athena/pricing/
[nodelocaldns]: https://kubernetes.io/docs/tasks/administer-cluster/nodelocaldns/
[prom-storage]: https://prometheus.io/docs/prometheus/latest/storage/
[billing-rules §1.1]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules §1.2]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules §1.3]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules §1.4]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules §2]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules §3]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
[billing-rules 부록 A]: /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md

[미조회] 표기 대상:
- [cur-delivery]: 재작성 창은 measurement-alternatives.md에서 인용
- [pl-2022]: billing-rules.md에서 인용
- EC2 On-Demand 요금 페이지: $0.01/GB 측당 요율은 billing-rules.md §1.1(2026-09-14 확인)에서 인용

로컬 근거 파일:
- /Users/orbis/Documents/GitHub/crossaz/rules/rules.yaml
- /Users/orbis/Documents/GitHub/crossaz/docs/billing-rules.md
- /Users/orbis/Documents/GitHub/crossaz/docs/measurement-alternatives.md
- /Users/orbis/Documents/GitHub/crossaz/internal/collect/aws.go