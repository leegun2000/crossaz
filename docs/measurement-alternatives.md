# crossaz v2: 실측 per-pair 바이트 소스 비교와 권고 (2026-09-15 기준)

**스냅샷 헛점 번호**
- ① 호출자 AZ 편중
- ② 연결별 가중치(long-lived connection)
- ③ 응답 바이트를 누구에게 귀속하나
- ④ kube-proxy를 거치지 않는 트래픽(ingress controller가 pod IP로 직접 연결, mesh)
- ⑤ public-IP hairpin
- ⑥ pair 단위 실측값

## 1. 비교표

| 방식 | 관측 지점 | 무엇이 정확해지나(스냅샷 헛점 중) | 남는 헛점 | 설치/전제 | 비용·부하 | 성숙도 |
|---|---|---|---|---|---|---|
| **VPC Flow Logs v5 + Pod IP 타임라인**(S3 Parquet, Athena, `kube_pod_info` 조인) | ENI. AWS가 데이터 경로 밖에서 수집. VPC 단위로 구독하면 노드 ENI와 LB/NAT/RDS/VPCE의 requester-managed ENI가 모두 들어옴 | ①②③④⑥ 모두 해결. egress 레코드만 합산하면 송신측에서 한 번씩만 셈. ⑤는 `traffic-path`와 public `pkt-dstaddr`로 식별 [fl-rec]. TCP와 UDP 모두, top-N 잘림 없음 | 상대 AZ는 DescribeSubnets로 추론해야 함. pod 식별은 시간 구간 조인이 필요(IP 재사용 전 30초 cooldown, start/end 오차 ±60초) [vpccni-bp]. VPC 밖으로 나가면 노드 IP로 SNAT됨(SGP strict pod는 예외) [eks-snat][eks-sgpp]. hostNetwork pod는 포트로만 구분. 전달 지연 5–10분 이상(best effort)이고 SKIPDATA 누락이 있음. `bytes`와 청구 바이트의 관계는 문서에 없음. `az-id`는 best-effort 메타데이터. NLB client IP preservation이 켜지면 NLB hop이 가려짐 [nlb-attr]. regional NAT GW는 별도 구독 필요 [fl-examples] | flow log 1개 생성. 포맷은 바꿀 수 없어 재생성해야 함 [fl-lim]. Athena 테이블 1회 생성. crossaz에는 Athena 실행 권한과 EC2 Describe 권한 필요 | 데이터 경로 부하 없음. us-east-1 기준 S3 vended $0.25/GB(첫 10TB), Parquet 변환 $0.03/GB [cw-price], S3 저장비, Athena $5/TB [athena-price]. 로그 양은 ENI 수 × 5-tuple 수 × 윈도우 수에 비례 | GA |
| **Flow Logs v11 next-hop 필드**(위 방식의 상위 호환) | 같은 ENI 레코드에 `next-hop-az-id`, `next-hop-interface-type`(nat_gateway, network_load_balancer, vpc_endpoint 등)이 추가됨 | 한 레코드에 양쪽 AZ가 있어 `az_id <> next_hop_az_id`만으로 cross-AZ를 고를 수 있음. NAT/NLB/VPCE hop을 과금 규칙에 바로 매핑 [fl-rec] | NLB/TGW에서 들어오는 ingress, IGW, 리전 간 트래픽에는 next-hop이 없음. next hop이 middlebox면 최종 목적지는 `pkt-dstaddr`로 찾아야 함 [fl-lim]. ALB용 enum이 없어 `-`와 ENI 설명 'ELB app/…'로 판별 [cn-v11]. NLB→target hop 기록 여부와 VPC 내부 IPv6 동작은 검증되지 않음. pod 식별은 여전히 조인 필요 | 새 구독 필요. 발표문에 35개 리전(중동·중국 없음) [fl-v11] | 발표문에 가격 언급 없음. 레코드 필드가 늘어 vended GB가 늘어날 것으로 추정 | GA(2026-06-10) |
| **CloudWatch Network Flow Monitor / EKS Container Network Observability**(Top Contributors API) | pod 소켓(eBPF `bpf_sock_ops`, TCP만). AZ 분류는 AWS 백엔드가 함 [nfm-inside] | ①②④⑥. 결과 행에 localAz, remoteAz, pod/service 메타데이터가 있음 [nfm-row]. ClusterIP DNAT를 conntrack로 풀어줌(best effort) | 노드당 30초마다 top-500만 남김(손실 많은 흐름 우선, 그다음 바이트 순). 쿼리도 500행이 한도 → 합계가 작게 나오고 INTER/INTRA 비율이 치우침 [nfm-agent][nfm-eks]. TCP만 봄. DATA_TRANSFERRED의 방향·중복 제거 방식이 문서에 없음. UNCLASSIFIED에 인터넷과 SQS 같은 public AWS endpoint가 섞여 ⑤와 NAT를 구분 못 함 [nfm-blog]. hostNetwork pod는 메타데이터 없음. Fargate/hybrid 노드 미지원 | EKS add-on DaemonSet(hostNetwork, hostPID, BPF/NET_ADMIN/PERFMON 권한, privileged init 컨테이너가 호스트 `rmem_max`를 바꿈). Pod Identity 또는 IRSA, Scope와 Monitor 필요. kernel 5.8+. 17개 리전 [nfm-regions] | monitored resource-hour당 $0.0069(노드-월 약 $5), 10개까지 12개월 무료, vended metrics 요금 별도 [cw-pricing]. 요청 리소스 50m/100Mi | NFM GA 2024-12, EKS 콘솔 2025-11. 에이전트가 AMP로 per-flow를 remote-write하는 기능은 소스 코드에만 있고 문서에 없음 [nfm-agent] |
| **OBI / Grafana Beyla network metrics**(eBPF, Prometheus) | 노드 host 인터페이스 패킷(기본 socket_filter). 바이트에 헤더 포함 [obi-net] | ①②④⑥(owner pair와 `src_zone`/`dst_zone`). ③은 연결 개시자 추적으로 `direction=request/response`가 붙음 [obi-attr] | RDS/NLB/VPCE 같은 비-k8s IP는 zone이 비는데도 `inter_zone` 카운터에 들어감 → `src_zone!="",dst_zone!=""` 필터 필요 [obi-prom]. 이름 붙인 `network.cidrs`로 subnet→AZ를 넣을 수 있음 [obi-cfg]. dedupe가 에이전트 안에서만 동작해 노드 간 흐름이 양쪽에서 두 번 잡힐 것으로 보임(`obi.ip` 기준 한쪽만 남기고 검증 필요) [obi-dedupe]. LB 내부 hop과 ⑤는 못 봄. 헤더 포함이라 청구 바이트와 다름 | privileged DaemonSet(CAP_BPF+CAP_NET_RAW), kernel 5.8+ BTF | OSS 무료. 비용은 주로 Prometheus cardinality | OBI v0.13.0(1.0 이전, inter-zone 메트릭은 experimental) [obi-cfg]. Beyla v3.35.0은 OBI 코드를 가져다 씀(메트릭 접두사 `beyla_`) [beyla] |
| **Retina / NetObserv**(eBPF tc flow, 노드 라벨로 zone) | Retina: pod veth와 host 인터페이스의 tc [retina-pp]. NetObserv: host 인터페이스 tc/tcx | ①②③④⑥ | 비-k8s 목적지는 zone unknown. 노드 양쪽에서 이중 관측(방향 필터 검증 필요). Retina는 client veth가 DNAT 전이라 ClusterIP만 보여 zone을 못 찾을 수 있음. `zone` 라벨은 문서에 없음 [retina-zone]. Standard control plane 폐기 계획이 있는데 대체 경로인 Hubble에는 pod-pod 바이트가 없음 [retina-deprec][retina-1841]. NetObserv는 cert-manager와 trust-manager가 필요 | Retina: DaemonSet+operator, NET_ADMIN/SYS_ADMIN, kernel 6.10+ 노드는 v1.2.1 이상. NetObserv: operator+flowlogs-pipeline, kernel 5.8+ [netobserv] | OSS. Retina는 32코어 이상 노드에서 CPU가 비선형으로 늘 수 있다는 경고가 있음 | Retina v1.2.8(CRD v1alpha1). NetObserv 1.12.0-community |
| **비용 도구 내장 에이전트: Coroot / Kubecost network-costs** | Coroot: 컨테이너 TCP 연결 eBPF(payload 바이트, NAT를 푼 실제 목적지). Kubecost: 노드 conntrack 5초 폴링 | Coroot: ①②③④⑥ [coroot-v14]. Kubecost: ①②④. pod별 in/out과 `same_zone`만 있고 pair는 없음 [kubecost-doc] | Coroot: TCP만, 헤더 제외로 2–4% 작게 나옴. LB ENI처럼 추적되지 않는 클라이언트는 빠짐. NLB/ALB/VPCE IP는 계산에서 제외. RDS에도 양측 요율을 매겨 crossaz 규칙과 충돌 [coroot-code]. hairpin은 internet으로 분류됨. Kubecost: 기본 in-zone CIDR에 10.0.0.0/8이 들어 있어 multi-AZ EKS 트래픽을 같은 zone으로 잘못 분류 [kubecost-values]. hostNetwork pod는 귀속 불가. 이미지 소스와 라이선스를 찾지 못함 | Coroot: server, storage, privileged node-agent(kernel 5.1+, IMDS 접근 필요). Kubecost: chart v3.2.4에서 기본 활성, privileged + hostNetwork | Coroot node-agent는 m5.2xlarge, 10k RPS에서 CPU 약 200m [coroot-perf]. cost monitoring이 유료 tier인지 불명확. Kubecost는 요청 50m/20Mi | Coroot v1.26.1. Kubecost network-costs v0.19.1 |
| **Istio ambient: ztunnel L4 metrics** | 노드 프록시 ztunnel이 중계한 앱 스트림 바이트(HBONE/mTLS 오버헤드는 빠진 것으로 추정) | ①②(흐르는 동안 계속 증가)③④(ambient 등록 pod)⑥. `source_zone`/`destination_zone` 라벨이 기본으로 붙음 [zt-metrics][zt-pr]. 요청 바이트는 `received`, 응답 바이트는 `sent` [istio-metrics] | ambient에 등록되지 않은 pod(hostNetwork, kube-system)는 데이터 없음. RDS 등은 `destination_zone="unknown"`. waypoint/gateway를 거치면 hop별로 합산해야 함. LB hop과 ⑤는 못 봄. TCP만. zone 라벨은 문서에 없음 | 이미 ambient를 쓰면 설치할 것 없음(:15020 scrape). EKS에서 SGP를 쓰면 `POD_SECURITY_GROUP_ENFORCING_MODE=standard` 필요 [istio-eks] | Istio 1.24 테스트에서 1000 rps 기준 ztunnel 약 0.06 vCPU/12MB [istio-perf]. cardinality는 워크로드 pair × zone pair × reporter 2개 | ambient GA. zone 라벨은 1.25.0(2025-03)부터, 최신 1.31.0 |
| **Sidecar mesh: Istio sidecar(Telemetry tagOverrides) / Linkerd** | pod 사이드카(Envoy, linkerd2-proxy) | Istio: ①②(TCP는 5초 단위 delta)③④⑥. zone은 CEL로 붙여야 함. `upstream.locality.zone`은 Istio 1.26 이상, source zone은 pod에 `topology.istio.io/locality` 라벨을 달거나 IMDS에 접근할 수 있어야 함 [istio-cel][istio-boot]. 라벨을 안 붙이면 pair 바이트만 얻고 ①은 여전히 가정. Linkerd: pod별 `outbound_tcp_transfer_cost_bytes_total{zone_locality="Remote",op}`로 ①②③④ [linkerd-zone]. pair는 연결 종료 시점에 기록되는 `tcp_*_bytes_total{peer="dst",dst_zone}` [linkerd-metrics] | 사이드카 없는 pod와 제외 포트는 안 보임. RDS와 PassthroughCluster는 zone 없음. HTTP 바이트는 스트림 바이트라 wire 바이트보다 작음. UDP 없음. CEL 경로와 Linkerd zone 메트릭 모두 문서에 없고 버전에 따라 다름 | 메시가 이미 있어야 함 | Istio 1.24 테스트에서 1000 rps 기준 sidecar 약 0.20 vCPU/60MB [istio-perf]. zone 라벨을 붙이면 series가 최대 약 9배(AZ 3개) | Telemetry API GA. Linkerd OSS는 edge 릴리스만 나옴 |
| **CUR 2.0**(Data Exports HOURLY + INCLUDE_RESOURCES) **/ Cost Explorer** | 청구서 | 노드 × 시간 × 방향별 실제 billed GB와 USD. `line_item_operation`의 InterZone-In/Out과 PublicIP-In/Out으로 ⑤를 분리 [dt-blog-2022]. ①–④가 합계에 미치는 영향을 검증 | pair, 상대방, pod 정보 없음. InterZone 행의 `line_item_resource_id`는 CUR 2.0 사전상 비어 있을 수 있고, 채워진 예시는 2019년 legacy CUR뿐이라 실행 시 확인 필요 [cur2-li][softwhat]. 하루 이상 늦고, 기간 종료 후 2주까지 다시 쓰일 수 있음 [cur-delivery]. Regional-Bytes에 비-EC2 행(NAT 등)이 섞임 [vantage-cur]. VPC peering은 2025-04부터 별도 usage type [peering] | payer 계정의 export와 Athena. Cost Explorer는 `EC2: Data Transfer - Inter AZ` 그룹 필터 [ce-dims] | S3 저장비, Athena $5/TB. CE API는 요청당 $0.01, hourly 데이터는 월 1,000 레코드당 $0.01 [ce-price] | GA |
| **Hybrid: CUR 노드 합계 × crossaz pod 가중치**(방법론) | 청구(노드 단위), cAdvisor rx/tx(pod 단위), 규칙 엔진 | 노드별 합계를 청구액에 맞춤. 노드별 k(청구값/모델값)로 ①④⑤ 이상을 탐지. 새 에이전트 없음 | 노드 안에서 pod별로 나누는 건 여전히 모델. pair 없음. 하루 지연. resource ID가 비어 있으면 계정/AZ 단위로 떨어짐 | 기존 Prometheus와 CUR 읽기 권한 | 실행당 Athena 쿼리 1회 | 방법론. 선행 사례로 Datadog CCM(상용, 에이전트 기반) [datadog-ccm] |

**표에서 뺀 방식과 이유**
- **Traffic Mirroring**: 혼잡 시 먼저 드롭되고, 미러링 트래픽 자체에 전송비와 대역폭이 들고, LB/NAT ENI는 미러링할 수 없고, 세션 생성이라는 쓰기 작업이 필요함 [mirror-lim]
- **Hubble**: Flow 레코드에 바이트 필드가 없음 [hubble-proto]
- **kubezonnet**: Cilium legacy host routing과 kernel 6.4 이상을 전제로 함 [kubezonnet]
- **Caretta**: zone 정보가 없고 활동이 멈춤 [caretta]
- **App Mesh**: 2026-09-30 지원 종료 [appmesh]
- **EKS Split Cost Allocation**: CPU와 메모리만 나누고 네트워크는 없음 [split-cost]
- **Detective, ALB/NLB access logs**: AZ별 청구 분석이 안 됨. NLB access log는 TLS 리스너만 남기고 target 필드가 없음 [nlb-logs]
- **Envoy zone stats**: zone pair별 바이트가 없음
- **OpenTelemetry eBPF Networking**: ENI→AZ 매핑은 흥미롭지만 마지막 릴리스가 2025-08이고 EKS에서 검증되지 않음 [otel-network]

## 2. 측정 지점

청구는 과금 리소스(ENI, 청구서상으로는 instance) 기준으로, AZ를 넘은 바이트마다 in 라인과 out 라인이 하나씩 생긴다 [cur-dt]. 그래서 ENI 가까이에서, 과금되는 쪽에서 센 값일수록 청구와 잘 맞는다.

```
app ─ socket ─ [sidecar/ztunnel] ─ veth ─ node iface ─ ENI ─ (LB/NAT/RDS ENI) ─ ENI ─ … ─ bill
      NFM,Coroot  Istio,Linkerd    Retina  OBI/Beyla,     VPC Flow Logs                     CUR/CE
                                           NetObserv, Kubecost(conntrack)
```

| 지점 | 바이트의 의미 | 청구 기준 정확도에 대한 함의 |
|---|---|---|
| pod 소켓(NFM, Coroot) | TCP payload. 헤더와 UDP는 빠짐. NAT를 푼 실제 pod 기준 | 워크로드 식별은 가장 좋지만 바이트는 청구보다 작다. 상대가 LB나 RDS면 LB ENI IP까지만 보이고, NLB 노드→target hop은 안 보인다. NFM은 top-K로 잘려서 총량으로 쓰면 안 된다 |
| 사이드카/노드 프록시(Istio, Linkerd) | 프록시가 중계한 스트림 바이트, hop 단위 | 메시에 등록된 pod 사이의 pair는 정확하지만 wire 바이트보다 작다. waypoint나 gateway가 끼면 hop마다 따로 AZ를 넘으므로 hop별로 합산해야 청구와 맞는다 |
| veth / host 인터페이스(Retina, OBI, NetObserv, Kubecost) | 패킷 바이트(OBI는 헤더 포함), 노드 관점 | 클러스터 안에서는 ENI에 가장 가깝다. 대신 양쪽 노드 이중 관측, DNAT 전후 중복, 비-k8s 목적지의 zone 공백을 crossaz가 직접 처리해야 한다 |
| ENI(VPC Flow Logs) | ENI를 지난 IP 흐름 바이트. 청구 바이트와의 정확한 관계는 문서에 없음 | 청구 단위와 같은 곳에서 본다. LB/NAT/RDS ENI와 UDP를 포함하고 샘플링도 없다. 다만 k8s 식별 정보와 과금 여부는 모르므로 타임라인 조인과 규칙 엔진이 반드시 필요하다 |
| 청구(CUR/CE) | GB와 USD 그 자체 | 유일한 절대 기준이다. 하지만 노드 단위이고(그마저 불확실), pair가 없고, 하루 늦다. 그래서 데이터 소스가 아니라 **검증기**로 쓴다 |

**결론**
- ALB/CLB 같은 VPC private은 무료, NLB cross-zone은 과금 같은 "어떤 경로가 과금되나"는 어떤 소스도 알려주지 않는다. 그래서 규칙 엔진은 계속 핵심이다.
- 규칙 엔진 자체도 검증해야 한다. 예로 "RDS는 클라이언트측만 과금" 규칙이 있다.
  - RDS 가격 페이지는 이 규칙을 뒷받침한다 [rds-price].
  - 반면 AWS 아키텍처 블로그는 EC2와 RDS 양쪽에 과금한다고 쓰고, Cost Explorer에는 `RDS: Data Transfer – InterAZ` 그룹도 있다 [db-dt-blog][ce-dims].
  - 따라서 CUR에서 AmazonRDS의 Regional-Bytes 행이 있는지로 확인해야 한다.

## 3. crossaz v2 권고 아키텍처

### 구조

현재 레이어는 유지한다.
- **L0**: Kubernetes API
- **L1**: AWS API
- **L2**: `VolumeCollector.PodRecvBytesPerSecond`
- **L3**: `CostCollector`, Cost Explorer 월 합계
- 처리 단계: `classify`(과금 규칙) → `estimate`(fraction × volume) → `estimate.Verify`(월 비율)

바뀌는 점은 L2를 둘로 나누는 것이다.

```
L2a VolumeCollector (현행 cAdvisor rx)      ─ fallback: BytesPerMo × CrossAZFrac
L2b PairCollector   (신규, 실측)
      ├ flowlogs  (Athena, v11 필드 우선, 없으면 v5)
      └ prom-mesh (ztunnel/Linkerd zone 메트릭이 있으면 자동 사용)
          → PairFlow{window, src{ip,kind,workload,azID}, dst{…}, bytes, countedAt=sender,
                     obsPoint, flags[ambiguousIP, azInferred, truncated]}
identity join: kube_pod_info range(L2) + ENI 인벤토리/LBC 태그(L1) + zone 이름↔AZ ID
classify: 기존 규칙 엔진 그대로. Path 키로 매칭하고, 매칭 안 되면 "unexplained" 버킷
estimate: 실측 pair가 있는 Path는 실측 바이트 사용. 없으면 현행 추정값을 쓰고 confidence를 낮춤
L3 verify: CE 월 합계(현행) → CUR 2.0 노드 × 시간 × operation(신규, 선택)
```

**설계 원칙**
1. 바이트는 소스마다 한 번만 센다. flow logs는 egress만, 메시는 hop별 reporter 규칙, eBPF는 관측 노드 한 곳 기준이다.
2. in/out 양측 과금은 규칙 엔진이 명시적으로 적용한다.
3. 보고서의 Path마다 `obsPoint`와 커버리지를 출력한다. 커버리지는 실측 바이트를 같은 pod의 cAdvisor tx로 나눈 값이다.
4. `PairCollector` 인터페이스는 구현이 두 개(flowlogs, prom-mesh) 생길 때 만든다. NFM과 OBI 어댑터는 요청이 있을 때 추가한다.

### 첫 소스: VPC Flow Logs (v11 필드 우선)

**선택 이유**
- **에이전트가 없고 VPC CNI와 잘 맞는다.** pod IP가 VPC IP라 `pkt-srcaddr/pkt-dstaddr`에 그대로 찍힌다 [fl-rec][fl-lim]. 클러스터는 바꿀 것이 없다.
- **청구와 같은 지점에서 본다.** LB/NAT/RDS/VPCE ENI까지 포함하므로 규칙 엔진에 필요한 상대방 종류를 바로 준다. top-N 잘림이 없고 UDP도 포함한다. NFM, 메시, Coroot는 이게 안 된다.
- **Prometheus 사용자는 추가 수집이 필요 없다.** 식별 조인에는 이미 수집 중인 kube-state-metrics `kube_pod_info{pod_ip,node,created_by_kind,created_by_name,uid,host_network}`(STABLE)를 쓴다 [ksm-pod].
- **데이터 경로에는 영향이 없다.** 비용은 us-east-1 기준 vended $0.25/GB, Parquet 변환 $0.03/GB, Athena $5/TB이다 [cw-price][athena-price]. 단 로그 양은 전송 바이트가 아니라 **5-tuple 수 × 관측 ENI 수 × 집계 윈도우 수**에 비례한다. 짧은 연결·UDP DNS가 많으면 로깅비가 추적 대상 요금에 근접할 수 있다([v2-eni-sufficiency.md](v2-eni-sufficiency.md) §4에서 정정).
  - 비용이 부담되면 노드 서브넷만 구독한다. LB/NAT/RDS 쪽 레코드는 잃지만, 노드 ENI egress 레코드의 v11 next-hop 종류는 남는다.
- **다른 후보와 비교**
  - NFM: top-K 편향, 노드당 과금, 에이전트 설치가 필요하다.
  - OBI/Retina: privileged DaemonSet이 필요하다.
  - ztunnel: ambient를 이미 쓰는 사용자에게만 공짜다. PromQL 하나로 구현되므로 **두 번째 소스**로 둔다.

**통합 세부**

1. **탐지(읽기만)**: `ec2:DescribeFlowLogs`로 `logFormat`, `maxAggregationInterval`, `logDestinationType`, `destinationOptions`를 확인한다 [ec2-flowlog-api].
   - 필요한 필드: 기본 5-tuple, `bytes`, `start`, `end`, `log-status`, 그리고 `${instance-id} ${interface-id} ${az-id} ${flow-direction} ${pkt-srcaddr} ${pkt-dstaddr} ${traffic-path}`.
   - 가능하면 `${next-hop-az-id} ${next-hop-interface-type} ${next-hop-interface-id} ${interface-type}`도 넣는다 [fl-rec].
   - 필드가 없어도 crossaz는 flow log를 만들지 않는다. 사용자가 실행할 `aws ec2 create-flow-logs` 명령(S3 대상, parquet)만 기존 `Fix.Command` 방식으로 출력한다.
   - 포맷은 수정할 수 없어 새로 만들어야 한다 [fl-lim].
2. **Athena 쿼리**: region/date/hour 파티션 프로젝션을 쓴다 [fl-athena].
   - AWS 예제 DDL은 v5 필드까지만 있으므로, v11 컬럼 테이블을 만드는 DDL도 출력해야 한다(1회 생성).
   ```sql
   SELECT instance_id, az_id, next_hop_az_id, next_hop_interface_type, pkt_srcaddr, pkt_dstaddr,
          date_trunc('hour', from_unixtime(start)) AS h, sum(bytes) AS b
   FROM flows
   WHERE flow_direction = 'egress' AND date BETWEEN :from AND :to
   GROUP BY 1,2,3,4,5,6,7
   ```
   - v5만 있으면 `pkt_dstaddr`를 DescribeSubnets(AZ ID를 반환)로 풀고 `azInferred` 플래그를 붙인다.
   - AWS 샘플의 "상대 IP의 egress 레코드에서 AZ를 학습"하는 CTE는 쓰지 않는다. 매칭에 실패하면 조용히 빠지고, IP가 여러 AZ에 매핑되면 중복 합산되기 때문이다 [aws-sample].
3. **read-only 가드 예외**: `readonly.go`는 Describe/List/Get만 허용하는데 `athena:StartQueryExecution`은 여기에 들지 않는다.
   - Athena만 명시적인 예외로 둔다.
   - 결과를 쓸 S3 권한이 필요 없도록 managed query results 워크그룹을 권장한다 [athena-managed].
   - IAM:
     - Athena/Glue: `athena:StartQueryExecution/GetQueryExecution/GetQueryResults`, `glue:GetTable/GetPartitions`
     - S3: 로그 버킷 `s3:GetObject/ListBucket`
     - EC2: `ec2:DescribeFlowLogs/DescribeNetworkInterfaces/DescribeSubnets/DescribeAvailabilityZones`
     - ELB: `elasticloadbalancing:DescribeLoadBalancers/DescribeTags`
4. **식별 조인**
   - `query_range kube_pod_info{pod_ip!=""}`로 pod_ip별 소유 구간(pod, uid, owner, node)을 만든다.
   - 레코드의 `[start-60s, end+60s]`가 두 소유자와 겹치면 `ambiguousIP`로 표시한다 [ksm-pod][vpccni-bp].
   - flow logs는 AZ ID만 쓰므로 zone 이름을 `DescribeAvailabilityZones`로 AZ ID에 매핑한다.
   - LB ENI는 설명 `ELB app/…`, `ELB net/…`, `ELB <name>`으로 찾는다 [elb-eni]. 그다음 LB ARN과 LBC 태그 `service.k8s.aws/stack`, `ingress.k8s.aws/stack`으로 Service/Ingress를 알아낸다 [lbc-tags]. LBC가 관리하는 LB만 해당한다.
   - `host_network="true"` pod와 SNAT된 트래픽은 노드 단위로만 귀속한다.
5. **classify 매핑(v11)**

   | next-hop 조건 | 적용할 과금 규칙 |
   |---|---|
   | `nat_gateway` / `regional_nat_gateway` | NAT |
   | `network_load_balancer` | NLB cross-zone |
   | `vpc_endpoint` | endpoint |
   | `-`이고 ALB ENI | ALB(같은 VPC면 무료) |
   | `-`이고 pod/노드 ENI | pod-pod |

   - NLB에서 들어오는 ingress에는 next-hop이 없고 NLB→target hop 기록은 검증되지 않았다. 그래서 5-tuple이나 subnet 조회로 보완한다 [fl-lim].
   - `-`이거나 AZ가 서로 맞지 않는 값은 DescribeSubnets로 다시 확인한다. 이 메타데이터는 best-effort다 [fl-rec].
6. **윈도우**: S3 전달은 보통 약 10분이 걸리고 보장되지 않는다 [fl-rec].
   - 최근 구간은 쿼리에서 뺀다. 시작값 20–30분을 조정 가능한 knob으로 둔다.
   - 윈도우를 고정 시각 경계로 잘라, 다시 실행하면 늦게 도착한 레코드가 포함되게 한다.

### CUR / Cost Explorer 검증 루프

현행 `Verify`(월 USD 비율)를 세 단계로 늘린다.

1. **Cost Explorer(기본, 설정 불필요)**: `GetCostAndUsage`를 DAILY로 호출하고 `USAGE_TYPE_GROUP = "EC2: Data Transfer - Inter AZ"`로 필터한 뒤 `GroupBy OPERATION`한다.
   - 이 그룹은 모든 리전 접두사를 포함한다. us-east-1은 접두사 없이 `DataTransfer-Regional-Bytes`다 [ce-dims][dt-pricelist].
   - InterZone과 PublicIP를 나눠 hairpin이 있는지 표시한다. 계정의 다른 워크로드가 섞인다는 경고는 유지한다.
2. **CUR 2.0(선택, `--cur-athena-table`)**: export 설정은 `TIME_GRANULARITY=HOURLY`, `INCLUDE_RESOURCES=TRUE` [cur2-table].
   ```sql
   SELECT date_trunc('hour', line_item_usage_start_date) AS h, line_item_resource_id, line_item_product_code,
          line_item_operation, sum(line_item_usage_amount) AS gb, sum(line_item_unblended_cost) AS usd
   FROM cur2
   WHERE billing_period = :period AND line_item_line_item_type = 'Usage'
     AND (line_item_usage_type LIKE '%DataTransfer-Regional-Bytes' OR line_item_usage_type LIKE '%VpcPeering-%-Bytes')
   GROUP BY 1,2,3,4
   ```
   - 결과는 세 버킷으로 보고한다 [vantage-cur][peering].
     - `i-` 행: 노드와 조인 가능
     - `i-`가 아닌 Regional-Bytes 행: product code별(NAT, ELB, RDS 등)
     - VpcPeering 행
   - `i-` 행이 하나도 없으면 "resource ID가 비어 있어 노드 단위 검증 불가"로 낮춘다 [cur2-li].
3. **노드 × 시간 대조**: flow logs의 `instance_id`는 CUR `line_item_resource_id`와 바로 조인된다 [fl-rec].
   - 규칙 엔진이 과금이라고 판정한 cross-AZ 흐름에서 egress 합계는 InterZone-Out, ingress 합계는 InterZone-In 예측값이 된다.
   - 노드·시간별로 k(청구값 ÷ 실측값)를 출력하고, k가 크게 벗어난 노드를 표시한다.
   - 허용 범위는 초기 관측 후 정한다. `bytes`의 정의와 GB 환산 방식이 문서에 없어 k≈1이 보장되지 않는다.
   - `i-`가 아닌 버킷은 규칙 검증에 쓴다. 예: AmazonRDS 행이 있으면 RDS 규칙을 재검토한다.
   - **주기**: CUR은 하루 한 번 이상 갱신되고, 기간이 끝난 뒤 2주까지 다시 쓰일 수 있다. 매일 전날분을 돌리고, 월초 2주 동안은 전월도 다시 돌린다. 최신 파일은 `Manifest.json`으로 찾는다 [cur-delivery].
   - **단가**: $0.01 하드코딩 대신 `line_item_unblended_cost`나 AWSDataTransfer Price List를 쓴다. $0.01은 us-east-1과 sa-east-1에서만 확인됐다 [dt-pricelist].
   - **선택**: Cost Anomaly Detection(추가 요금 없음, usage type 기준 원인 분석)을 EventBridge로 연결해 crossaz 재실행을 트리거한다 [cad].

## 4. 에이전트 없이 할 수 있는 저비용 개선 (v2 전에)

| 개선 | 구현 | 고치는 헛점 |
|---|---|---|
| cAdvisor **tx 추가 수집** | `container_network_transmit_bytes_total`를 추가로 읽는다 [cadvisor]. 송신측은 out, 수신측은 in으로 넣고 규칙 엔진이 양측 과금을 적용 | ③ 응답 바이트를 수신 호출자에게 몰아주던 문제(부분 해결). pair는 여전히 없음 |
| **시간가중 zone 분포와 Pod IP 타임라인** | 스냅샷 한 장 대신 `query_range kube_pod_info`로 윈도우 동안 replica가 각 노드/AZ에 머문 시간으로 가중한다 [ksm-pod]. 같은 데이터를 나중에 flow logs 조인에 재사용. 종료된 노드의 zone은 KSM 노드 메트릭 이력이 필요(라벨 노출 여부 확인 필요) | HPA, 롤아웃, 스팟 교체로 생기는 스냅샷 편향(① 일부). `host_network` pod 표시 |
| **주기 스냅샷 diff** | cron이나 CronJob으로 crossaz를 반복 실행해 JSON으로 보관. EndpointSlice hints, `trafficDistribution`, kube-proxy 버전, LB cross-zone 속성이 바뀐 시각을 표시 | 한 번의 스냅샷으로는 알 수 없는 "언제부터 새기 시작했나" |
| **LB 인벤토리와 LB 실측 볼륨** | ENI 설명과 LBC 태그로 Service/Ingress ↔ LB ↔ AZ를 연결 [elb-eni][lbc-tags]. `AWS/NetworkELB ProcessedBytes`(dimension `AvailabilityZone`, `LoadBalancer`)로 NLB 노드 AZ별 바이트를 얻음(연구에서 공식 URL은 확인 못 함) | NLB cross-zone 규칙의 입력이 추정에서 실측으로 바뀜. target AZ로 나누는 부분은 여전히 가정 |
| **CE를 일별 + OPERATION으로** | 현행 `ce.go`의 월 합계를 DAILY와 `GroupBy OPERATION`으로 바꿈 [ce-dims]. 요청당 $0.01 [ce-price] | ⑤ hairpin 존재 탐지(PublicIP-In/Out), 날짜별로 추정과 급변 비교 |
| **리전별 단가** | Price List AWSDataTransfer에서 리전별 Regional-Bytes 단가를 읽음 [dt-pricelist] | us-east-1 외 리전의 USD 오차 |
| **메시 메트릭 자동 탐지** | Prometheus에 `istio_tcp_sent_bytes_total{source_zone}`나 `outbound_tcp_transfer_cost_bytes_total`이 있으면 해당 Path를 실측으로 표시 [zt-metrics][linkerd-zone] | 메시 사용자는 추가 설치 없이 ①②③④⑥ |

## 출처

[fl-rec]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-log-records.html
[fl-lim]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-limitations.html
[fl-examples]: https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs-records-examples.html
[fl-athena]: https://docs.aws.amazon.com/athena/latest/ug/vpc-flow-logs-partition-projection-parquet-example.html
[fl-v11]: https://aws.amazon.com/about-aws/whats-new/2026/06/amazon-flow-logs-metadata/
[cn-v11]: https://dev.classmethod.jp/en/articles/vpc-flow-logs-next-hop-ec2-tag-metadata/
[ec2-flowlog-api]: https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_FlowLog.html
[cw-price]: https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonCloudWatch/current/us-east-1/index.json
[athena-price]: https://aws.amazon.com/athena/pricing/
[athena-managed]: https://docs.aws.amazon.com/athena/latest/ug/managed-results.html
[eks-snat]: https://docs.aws.amazon.com/eks/latest/userguide/external-snat.html
[eks-sgpp]: https://docs.aws.amazon.com/eks/latest/best-practices/sgpp.html
[vpccni-bp]: https://docs.aws.amazon.com/eks/latest/best-practices/vpc-cni.html
[nlb-attr]: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html
[elb-eni]: https://repost.aws/knowledge-center/elb-find-load-balancer-ip
[lbc-tags]: https://github.com/kubernetes-sigs/aws-load-balancer-controller/blob/main/pkg/deploy/tracking/provider.go
[ksm-pod]: https://github.com/kubernetes/kube-state-metrics/blob/main/docs/metrics/workload/pod-metrics.md
[aws-sample]: https://github.com/aws-samples/amazon-eks-inter-az-traffic-visibility
[nfm-eks]: https://docs.aws.amazon.com/eks/latest/userguide/network-observability.html
[nfm-inside]: https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-NetworkFlowMonitor-inside-network-flow-monitor.html
[nfm-row]: https://docs.aws.amazon.com/networkflowmonitor/2.0/APIReference/API_MonitorTopContributorsRow.html
[nfm-agent]: https://github.com/aws/network-flow-monitor-agent
[nfm-blog]: https://aws.amazon.com/blogs/containers/track-inter-az-and-nat-gateway-traffic-with-eks-container-network-observability/
[nfm-regions]: https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-NetworkFlowMonitor-Regions.html
[cw-pricing]: https://aws.amazon.com/cloudwatch/pricing/
[obi-net]: https://opentelemetry.io/docs/zero-code/obi/network/
[obi-cfg]: https://opentelemetry.io/docs/zero-code/obi/network/config/
[obi-prom]: https://github.com/open-telemetry/opentelemetry-ebpf-instrumentation/blob/main/pkg/export/prom/prom_net.go
[obi-dedupe]: https://github.com/open-telemetry/opentelemetry-ebpf-instrumentation/blob/main/pkg/internal/netolly/flow/deduper.go
[obi-attr]: https://github.com/open-telemetry/opentelemetry-ebpf-instrumentation/blob/v0.13.0/pkg/export/attributes/attr_defs.go
[beyla]: https://grafana.com/docs/beyla/latest/network/inter-az/
[retina-pp]: https://github.com/microsoft/retina/blob/main/docs/03-Metrics/plugins/Linux/packetparser.md
[retina-zone]: https://github.com/microsoft/retina/pull/1657
[retina-deprec]: https://github.com/microsoft/retina/blob/main/docs/03-Metrics/01-metrics-intro.md
[retina-1841]: https://github.com/microsoft/retina/issues/1841
[netobserv]: https://github.com/netobserv/network-observability-operator/blob/main/docs/Metrics.md
[coroot-v14]: https://coroot.com/blog/coroot-v1-4-data-transfer-cost-monitoring-and-more/
[coroot-perf]: https://docs.coroot.com/installation/performance-impact/
[coroot-code]: https://github.com/coroot/coroot/blob/main/constructor/containers.go
[kubecost-doc]: https://www.ibm.com/docs/en/kubecost/self-hosted/3.x?topic=configuration-network-cost
[kubecost-values]: https://github.com/kubecost/cost-analyzer-helm-chart/blob/develop/kubecost/values.yaml
[zt-metrics]: https://github.com/istio/ztunnel/blob/master/src/proxy/metrics.rs
[zt-pr]: https://github.com/istio/ztunnel/pull/1441
[istio-metrics]: https://istio.io/latest/docs/reference/config/metrics/
[istio-perf]: https://istio.io/latest/docs/ops/deployment/performance-and-scalability/
[istio-eks]: https://istio.io/latest/docs/ambient/install/platform-prerequisites/
[istio-cel]: https://istio.io/latest/docs/tasks/observability/metrics/customize-metrics/
[istio-boot]: https://github.com/istio/istio/blob/master/pkg/bootstrap/config.go
[linkerd-zone]: https://github.com/linkerd/linkerd2-proxy/blob/main/linkerd/transport-metrics/src/zone.rs
[linkerd-metrics]: https://linkerd.io/2-edge/reference/proxy-metrics/
[cur-dt]: https://docs.aws.amazon.com/cur/latest/userguide/cur-data-transfers-charges.html
[cur2-li]: https://docs.aws.amazon.com/cur/latest/userguide/table-dictionary-cur2-line-item.html
[cur2-table]: https://docs.aws.amazon.com/cur/latest/userguide/table-dictionary-cur2.html
[cur-delivery]: https://docs.aws.amazon.com/cur/latest/userguide/dataexports-export-delivery.html
[dt-blog-2022]: https://aws.amazon.com/blogs/networking-and-content-delivery/understand-aws-data-transfer-details-in-depth-from-cost-and-usage-report-using-athena-query-and-quicksight/
[softwhat]: https://softwhat.com/drilling-down-to-amazon-ec2-instances-data-transfer-costs/
[vantage-cur]: https://cur.vantage.sh/aws/nat-gateways/data-transfer/datatransfer-regional-bytes/
[peering]: https://aws.amazon.com/about-aws/whats-new/2025/04/amazon-vpc-peering-billing/
[ce-dims]: https://docs.aws.amazon.com/awsaccountbilling/latest/aboutv2/selectdim.html
[ce-price]: https://aws.amazon.com/aws-cost-management/aws-cost-explorer/pricing/
[dt-pricelist]: https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSDataTransfer/current/region_index.json
[datadog-ccm]: https://docs.datadoghq.com/cloud_cost_management/allocation/container_cost_allocation/
[rds-price]: https://aws.amazon.com/rds/pricing/
[db-dt-blog]: https://aws.amazon.com/blogs/architecture/exploring-data-transfer-costs-for-aws-managed-databases/
[cad]: https://docs.aws.amazon.com/cost-management/latest/userguide/manage-ad.html
[cadvisor]: https://github.com/google/cadvisor/blob/master/docs/storage/prometheus.md
[mirror-lim]: https://docs.aws.amazon.com/vpc/latest/mirroring/traffic-mirroring-network-limitations.html
[hubble-proto]: https://github.com/cilium/cilium/blob/main/api/v1/flow/flow.proto
[kubezonnet]: https://github.com/polarsignals/kubezonnet
[caretta]: https://github.com/groundcover-com/caretta
[appmesh]: https://docs.aws.amazon.com/app-mesh/latest/userguide/what-is-app-mesh.html
[split-cost]: https://docs.aws.amazon.com/cur/latest/userguide/split-line-item-columns.html
[nlb-logs]: https://docs.aws.amazon.com/elasticloadbalancing/latest/network/load-balancer-access-logs.html
[otel-network]: https://github.com/open-telemetry/opentelemetry-network

**코드 기준 참고** (이 저장소):
- `internal/collect/collect.go`: L0–L3 인터페이스
- `internal/collect/readonly.go`: Describe/List/Get 가드. Athena 예외가 필요한 곳
- `internal/collect/ce.go`: CE 월 합계. DAILY + OPERATION으로 바꿀 곳
- `internal/estimate/verify.go`: 월 비율 검증
- `internal/model/types.go`: `Path.BytesPerMo`/`CrossAZFrac`. 실측값으로 대체할 필드