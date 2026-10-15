# crossaz 과금 규칙 레퍼런스

- **확인일**: 2026-09-14. 모든 행은 이 날짜에 공식 출처와 대조했다.
- **출처 범위**: AWS 요금 페이지·FAQ, docs.aws.amazon.com, AWS 공식 블로그, AWS Price List API(게시 2026-09-11), kubernetes.io 및 kubernetes/kubernetes 소스. 서드파티 글은 판정 근거로 쓰지 않았다.
- **역할**: 과금 *사실*의 원본이다. 도구가 실제로 적용하는 규칙은 [`rules/rules.yaml`](../rules/rules.yaml), 설계 결정은 [design.md](design.md)에 있다. 이 표가 바뀌면 rules.yaml을 따라 고친다.
- **SPEC 초안**: `SPEC_1.md` §2 (저장소 밖). 초안 대비 바뀐 곳은 [§7](#7-변경-이력-vs-spec-초안)에 모았다.

읽는 법

| 용어 | 뜻 |
|---|---|
| 측당 요율 | 리소스(ENI 등) 하나가 받는 청구. AWS 요금 페이지가 쓰는 "in each direction" 단가 |
| 교차 GB당 요율 | AZ를 넘은 1GB에 대해 청구되는 모든 측의 합. `rules.yaml`의 `rate_gb`와 같은 기준 |
| 과금 여부 | 예 / 아니오 / 조건부(조건에 따라 일부만) / 미확정 |
| 신뢰도 | high = 요금 페이지·API 가격표가 명시 · medium = 공식 문서·블로그가 명시하나 요금 문구에 공백이 있거나 발생 비율이 추정 · low = 공식 근거 부족 |
| AZ 비교 | 계정이 다르면 AZ 이름이 아니라 **AZ ID**로 비교 ([AWS RAM](https://docs.aws.amazon.com/ram/latest/userguide/working-with-az-ids.html)) |

---

## 1. 기본 요율

### 1.1 EC2/ENI ↔ EC2/ENI (사설 IP, 같은 리전, 다른 AZ)

| 항목 | 값 | 근거 |
|---|---|---|
| 측당 | 보내는 ENI $0.01/GB(out) + 받는 ENI $0.01/GB(in) | [EC2 On-Demand 요금 · Data Transfer within the same AWS Region](https://aws.amazon.com/ec2/pricing/on-demand/) |
| 교차 GB당 | **$0.02** | 위 + [CUR 데이터 전송 문서](https://docs.aws.amazon.com/cur/latest/userguide/cur-data-transfers-charges.html) (리소스마다 in/out 두 줄) |
| 적용 바이트 | 요청 바이트와 응답 바이트 **각각**. "왕복 1회 = $0.02"가 아니라 "AZ를 넘은 GB마다 $0.02" | 위 |
| 계정 | 양 끝이 다른 계정이면 각 계정이 $0.01씩 낸다 | CUR 문서 |
| 같은 AZ | 무료 | EC2 요금 페이지 |
| 무료 티어 | 전 리전 합산 월 1GB (`Global-DataTransfer-Regional-Bytes`, $0). 추정에서는 무시 | [Price List ap-northeast-2](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSDataTransfer/current/ap-northeast-2/index.json) |

pod IP의 AZ = 노드 AZ. VPC CNI는 pod에 노드 ENI의 VPC 사설 IP를 주고([VPC CNI](https://docs.aws.amazon.com/eks/latest/userguide/managing-vpc-cni.html)), ENI는 인스턴스와 같은 AZ에만 붙는다([ENI attachment](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/network-interface-attachments.html)).

### 1.2 리전별 요율 (측당, Price List 게시 2026-09-11)

| 위치 | 측당 요율 | 근거 |
|---|---|---|
| 상용·GovCloud 부모 리전 36개 전부: af-south-1, ap-east-1/2, ap-northeast-1/2/3, ap-south-1/2, ap-southeast-1~7, ca-central-1, ca-west-1, eu-central-1/2, eu-north-1, eu-south-1/2, eu-west-1/2/3, il-central-1, me-central-1, me-south-1, mx-central-1, sa-east-1, us-east-1/2, us-west-1/2, us-gov-east-1, us-gov-west-1 | 0.01 USD | [AWSDataTransfer region_index](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSDataTransfer/current/region_index.json) (106개 위치 파일 전수 확인) |
| 중국 cn-north-1, cn-northwest-1 | 0.067 CNY | [aws-cn Price List](https://pricing.cn-north-1.amazonaws.com.cn/offers/v1.0/cn/AWSDataTransfer/current/cn-north-1/index.json) |
| Wavelength eu-central-1-wl1-ber1 / -dtm1 / -muc1, eu-west-3-wl1-cmn1 | 가격표에 행 없음 → 모름 | region_index |
| 그 외 Wavelength zone | 0.01 USD | region_index |
| Local Zone ↔ 부모 리전 AZ | 아래 표 | [EC2 요금 페이지](https://aws.amazon.com/ec2/pricing/on-demand/), [요금 페이지 데이터 파일](https://b0.p.awsstatic.com/pricing/2.0/meteredUnitMaps/datatransfer/USD/current/datatransfer.json), region_index |

Local Zone ↔ 부모 리전 AZ (Local Zone 쪽 EC2의 in/out에 매기는 측당 요율, USD/GB)

| 요율 | Local Zone group |
|---|---|
| 0.01 | us-east-1-atl-1, -bos-1, -chi-1, -dfw-1, -iah-1, -mci-1, -mia-1, -msp-1, -nyc-1, -phl-1, -qro-1 · us-west-2-den-1, -las-1, -lax-1, -pdx-1, -phx-1, -sea-1 · eu-central-1-ham-1, -waw-1 · eu-north-1-cph-1, -hel-1 |
| 0.02 | eu-central-1-ath-1, eu-central-1-ist-1 |
| 0.022 | us-west-2-hnl-1 |
| 0.076 | ap-south-1-ccu-1, ap-south-1-del-1 |
| 0.08 | ap-northeast-1-tpe-1, ap-southeast-1-bkk-1, ap-southeast-1-mnl-1 |
| 0.088 | ap-southeast-2-akl-1, ap-southeast-2-per-1 |
| 0.101 | me-south-1-mct-1 |
| 0.128 | us-east-1-bue-1, us-east-1-lim-1, us-east-1-scl-1 |
| 0.137 | af-south-1-los-1 |
| 0.20 | ap-southeast-1-han-1 |

- 요금 페이지는 Local Zone 쪽만 가격을 매긴다. 부모 AZ 쪽 청구와 Local Zone ↔ Local Zone 전송은 명시가 없다 → [§4 `local-zone-rate`](#4-미확정).
- 노드 zone(`topology.kubernetes.io/zone`)이 표준 AZ가 아니면(예: `us-east-1-bue-1a`) 0.01을 가정하지 않는다.
- VPC peering을 통한 Local Zone ↔ AZ 전송은 [VPC 요금 페이지](https://aws.amazon.com/vpc/pricing/)의 별도 표를 따른다.

### 1.3 대상 서비스별 청구 측

| 상대편 | 청구되는 측 | 교차 GB당 (`rate_gb`) | 신뢰도 | 근거 |
|---|---|---|---|---|
| EC2/ENI (pod, 노드, NLB ENI) | 양쪽 | 0.02 | high | [EC2 요금](https://aws.amazon.com/ec2/pricing/on-demand/) |
| RDS DB 인스턴스 | EC2 쪽만. RDS 쪽 추가 요금 없음 | 0.01 | high | [RDS 요금](https://aws.amazon.com/rds/pricing/) |
| Aurora DB 인스턴스 | EC2 Regional 요금 적용. Aurora 쪽 무과금은 함축될 뿐 명시 없음 | 0.01 | medium | [Aurora 요금](https://aws.amazon.com/rds/aurora/pricing/) |
| ElastiCache 노드 | EC2 쪽만 (단위 GiB) | 0.01 | high | [ElastiCache 요금](https://aws.amazon.com/elasticache/pricing/) |
| MSK Provisioned 브로커 | 클라이언트(EC2) 쪽은 확인. 브로커 쪽 청구는 공식 문구 없음 | 0.01 (하한) | medium | [MSK 요금](https://aws.amazon.com/msk/pricing/) |
| EFS 마운트 타겟 | 페이지는 AZ in/out 각 $0.01만 명시. 양쪽 계량 여부는 문서 없음 | 0.01~0.02 (도구는 0.01) | medium | [EFS 요금](https://aws.amazon.com/efs/pricing/) |
| VPC peering 너머 리소스 | In $0.01 + Out $0.01 | 0.02 | high | [VPC 요금](https://aws.amazon.com/vpc/pricing/) |

### 1.4 AZ와 무관한 청구, 경유 서비스

| 경우 | 청구 | 근거 |
|---|---|---|
| 같은 리전 고객 리소스의 **공인·Elastic IPv4**로 전송 | 방향마다 $0.01/GB, **같은 AZ여도** 과금. AZ 간 요금이 아니다 → `public-ipv4-in-region` | [EC2 요금](https://aws.amazon.com/ec2/pricing/on-demand/), [EC2 IP 주소 문서](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-instance-addressing.html) |
| 공인 IPv4 주소 보유 | 주소당 $0.005/시간(사용·유휴). 전송 요금 아님 → GB당 추정에 넣지 않음 | [VPC 요금](https://aws.amazon.com/vpc/pricing/) |
| 같은 리전 AWS 서비스 공인 엔드포인트(S3, DynamoDB, SQS, SNS, Kinesis, ECR 등) | 전송료 없음. 공인 IPv4 규칙을 적용하지 않는다 | [EC2 요금](https://aws.amazon.com/ec2/pricing/on-demand/), [VPC FAQ](https://aws.amazon.com/vpc/faqs/) |
| 인스턴스 공인 DNS 이름 | VPC 안에서는 사설 IP로 해석된다. 호스트명만으로 공인 IP 경로라고 판단하지 않는다 | [VPC DNS](https://docs.aws.amazon.com/vpc/latest/userguide/AmazonDNS-concepts.html) |
| IPv6, 같은 VPC | ENI 규칙과 같다 (다른 AZ 과금, 같은 AZ 무료) | [EC2 요금](https://aws.amazon.com/ec2/pricing/on-demand/) |
| IPv6, 다른 VPC | 방향마다 $0.01/GB. 단 peering 경유 같은 AZ는 peering 규칙(무료)과 충돌 → [§4 `ipv6-cross-vpc-same-az`](#4-미확정) | EC2 요금, [VPC peering 블로그](https://aws.amazon.com/blogs/networking-and-content-delivery/demystifying-amazon-vpc-peering-charges/) |
| NAT Gateway | GB당 처리료(출발지·목적지 무관) + NAT와 인스턴스 AZ가 다르면 AZ 간 전송료 → `nat-gateway-cross-az` | [VPC 요금](https://aws.amazon.com/vpc/pricing/) |
| Transit Gateway, PrivateLink **인터페이스** 엔드포인트, Client VPN | 2022-04-01부터 AZ 간 전송 **무료**. 각 서비스 처리료는 그대로 | [AWS What's New 2022-04](https://aws.amazon.com/about-aws/whats-new/2022/04/aws-data-transfer-price-reduction-privatelink-transit-gateway-client-vpn-services/) |
| Gateway Load Balancer 엔드포인트 | 위 면제에서 제외. 서브넷 → 다른 AZ GWLBE 라우트, 또는 GWLB cross-zone으로 다른 AZ 어플라이언스에 갈 때만 AZ 간 요금. 엔드포인트 처리료 별도(ELB 요금 예시 $0.0035/GB). v1 범위 밖 | What's New 2022-04, [GWLB 모범 사례 블로그](https://aws.amazon.com/blogs/networking-and-content-delivery/best-practices-for-deploying-gateway-load-balancer/), [ELB 요금](https://aws.amazon.com/elasticloadbalancing/pricing/) |
| PrivateLink 교차 리전 | inter-Region 요금(엔드포인트 소유자가 양방향 부담, 예시 $0.02/GB). AZ 간이 아니다 | [PrivateLink 요금](https://aws.amazon.com/privatelink/pricing/) |
| 인터넷 → AWS 유입 / AWS → 인터넷 응답 | 유입 무료 / 인터넷 전송(DTO) 요금. 둘 다 AZ 간 요금 아님 | [CUR 문서](https://docs.aws.amazon.com/cur/latest/userguide/cur-data-transfers-charges.html) |

---

## 2. 과금되지 않는 경로

| ID | 경로 | 조건 | 근거 URL | 확인일 |
|---|---|---|---|---|
| `same-az` | 호출자와 목적지가 같은 AZ. pod↔pod, pod↔노드, EC2↔RDS/ElastiCache/Redshift/ENI | 사설 IPv4(또는 같은 VPC IPv6). "같은 AZ" = 호출자 AZ ∪ 목적지 AZ가 하나. 계정이 다르면 AZ ID로 비교. **제외**: 공인·EIP IPv4 목적지(같은 AZ도 과금), 다른 VPC IPv6(peering 제외). NAT GW·TGW·PrivateLink 처리료는 별도로 남는다. 신뢰도 high | https://aws.amazon.com/ec2/pricing/on-demand/ · https://docs.aws.amazon.com/ram/latest/userguide/working-with-az-ids.html | 2026-09-14 |
| `alb-private-same-vpc` | 클라이언트(노드·pod) → ALB, ALB → 인스턴스 타겟. **두 다리 모두, AZ 무관** | 사설 IPv4, 클라이언트·ALB·타겟이 같은 VPC, EC2 기반. cross-zone 설정·scheme과 무관하다. 판정은 실제 목적지 주소로 한다. **제외**: VPC 안에서 internet-facing ALB의 공인 IPv4로 접속(`public-ipv4-in-region`), 다른 VPC의 클라이언트·타겟(`vpc-peering-cross-az` 또는 TGW 처리료), IPv6(`alb-ipv6`), Local Zone 배치(근거 없음). LCU는 별도(§5). 신뢰도 high | https://aws.amazon.com/ec2/pricing/on-demand/ · https://aws.amazon.com/blogs/networking-and-content-delivery/exploring-data-transfer-costs-for-classic-and-application-load-balancers/ | 2026-09-14 |
| `clb-private-same-vpc` | 클라이언트 → CLB, CLB → 인스턴스(eth0 primary IP) | 사설 IPv4(CLB는 IPv4 전용), 같은 VPC. CLB cross-zone은 끌 수 있고 API로 만들면 기본 off지만, 켜도 AZ 간 전송료는 없다(CLB FAQ). CLB의 GB당 처리료는 별도. NodePort 뒤 노드→pod 홉은 `nodeport-hop-cross-az`. LB 종류는 AWS API로 확인한다(LBC webhook이 있으면 annotation 없이도 NLB가 생긴다). 신뢰도 high | https://aws.amazon.com/ec2/pricing/on-demand/ · https://aws.amazon.com/elasticloadbalancing/faqs/ · https://docs.aws.amazon.com/elasticloadbalancing/latest/classic/enable-disable-crosszone-lb.html | 2026-09-14 |
| `alb-ip-target-mode` | ALB → pod IP (AWS Load Balancer Controller IP 타겟) | pod IP가 ALB의 VPC 서브넷 CIDR 안, 사설 IPv4. **EC2 노드 위 pod: 신뢰도 medium.** ALB 문서는 인스턴스 인터페이스의 어느 사설 IP든 ip 타겟이 될 수 있다고 하고, EKS Best Practices는 IP 모드에 전송료가 없다고 명시한다. **Fargate pod**: 비과금으로 보되 근거는 AWS 컨테이너 블로그(ECS 기준 EC2와 Fargate 차이 없음). 요금 페이지는 Fargate를 언급하지 않는다. **제외**: VPC 밖 ip 타겟(peered VPC, 온프레미스, Hybrid Nodes), IPv6 타겟그룹(`alb-ipv6`). EKS BP의 "IP 모드 무료" 문장을 NLB에 적용하지 않는다. 과금으로 격상하지 않는다 | https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html · https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-target-groups.html · https://aws.amazon.com/blogs/containers/understanding-data-transfer-costs-for-aws-container-services/ | 2026-09-14 |
| `msk-inter-broker` | MSK 브로커 ↔ 브로커, 브로커 ↔ 메타데이터 노드(ZooKeeper/KRaft) 복제 | MSK Provisioned의 Standard·Express 브로커 모두, AZ 무관. 클러스터 내부 트래픽에만 해당하고 클라이언트 트래픽은 아니다. EKS에서는 보이지 않는 AWS 관리 트래픽. 신뢰도 high | https://aws.amazon.com/msk/pricing/ · https://aws.amazon.com/msk/faqs/ | 2026-09-14 |
| `rds-multiaz-replication` | RDS Multi-AZ 프라이머리 ↔ 스탠바이, 같은 리전 read replica 복제(다른 AZ 포함), Aurora 클러스터 복제, Multi-AZ DB cluster writer → reader | 같은 리전. DB instance Multi-AZ·read replica·Aurora는 명시(high). Multi-AZ DB cluster는 요금 페이지의 일반 문구("Multi-AZ 배포 복제 무료")에 기대는 추론(medium). 교차 리전 복제는 inter-Region 과금. 읽기 가능 스탠바이·리플리카로 가는 **클라이언트** 트래픽은 `rds-cross-az` | https://aws.amazon.com/rds/pricing/ · https://aws.amazon.com/rds/faqs/ · https://docs.aws.amazon.com/AmazonRDS/latest/UserGuide/USER_ReadRepl.html · https://aws.amazon.com/rds/aurora/pricing/ | 2026-09-14 |
| `s3-gateway-endpoint` | pod → 같은 리전 S3/DynamoDB | 같은 리전 S3·DynamoDB와 AWS 서비스 간 전송은 게이트웨이 엔드포인트가 없어도 무료. S3 Express One Zone 디렉터리 버킷에 다른 AZ에서 접근해도 전송료 없음. 게이트웨이 엔드포인트 자체는 시간·처리·전송료 없음. **남는 비용**: 실제 egress 서브넷의 route table에 prefix list → `vpce-` 경로가 없으면 NAT GW 처리료, NAT가 다른 AZ면 `nat-gateway-cross-az`. egress 서브넷 = 기본 SNAT에서는 노드 primary ENI 서브넷, `AWS_VPC_K8S_CNI_EXTERNALSNAT=true` + custom networking에서는 pod ENI 서브넷. 다른 리전 버킷·테이블은 inter-Region. IPv4 전용 게이트웨이 엔드포인트는 IPv6 트래픽이 우회한다. 신뢰도 high | https://docs.aws.amazon.com/vpc/latest/privatelink/gateway-endpoints.html · https://aws.amazon.com/s3/pricing/ · https://aws.amazon.com/dynamodb/pricing/on-demand/ · https://aws.amazon.com/blogs/aws/new-amazon-s3-express-one-zone-high-performance-storage-class/ · https://docs.aws.amazon.com/eks/latest/userguide/external-snat.html | 2026-09-14 |
| `vpce-interface-cross-az` | pod → 다른 AZ의 VPC **인터페이스** 엔드포인트 ENI (같은 VPC·리전). MSK multi-VPC private connectivity 포함 | 2022-04-01부터 AZ 간 전송 무료(CE에서는 `DataTransfer-xAZ-In/Out-Bytes` $0). PrivateLink 처리료(첫 1PB $0.01/GB, 이후 $0.006, $0.004)와 엔드포인트·AZ당 시간료는 AZ와 무관하게 청구되므로 cross-AZ 결과가 아니다. 이후 번복 공지 없음(현행 EKS BP도 무료로 기술). **제외**: GWLB 엔드포인트, Resource·서비스 네트워크 엔드포인트(`vpce-non-interface`), 교차 리전 PrivateLink, ElastiCache Serverless(서비스별 문구 우선 → `elasticache-cross-az`). 신뢰도 high | https://aws.amazon.com/about-aws/whats-new/2022/04/aws-data-transfer-price-reduction-privatelink-transit-gateway-client-vpn-services/ · https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html · https://aws.amazon.com/privatelink/pricing/ | 2026-09-14 |

---

## 3. 과금되는 경로

| ID | 경로 | 요율 | 누가 내는가 | 근거 | 확인일 |
|---|---|---|---|---|---|
| `clusterip-cross-az` | pod → ClusterIP → 다른 AZ 노드의 pod | 교차 GB당 $0.02 (측당 $0.01) × cross-AZ 비율 | 클러스터 계정 (호출자·엔드포인트 노드 ENI) | https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html · https://aws.amazon.com/ec2/pricing/on-demand/ · https://kubernetes.io/docs/reference/networking/virtual-ips/ | 2026-09-14 |
| `headless-cross-az` | headless Service / StatefulSet peer가 pod IP로 직접 연결 | 교차 GB당 $0.02 | 클러스터 계정 | https://aws.amazon.com/ec2/pricing/on-demand/ · https://kubernetes.io/docs/concepts/services-networking/service/ · https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/ | 2026-09-14 |
| `nlb-cross-zone` | NLB 노드 ENI → 다른 AZ 타겟 (타겟그룹의 **유효** cross-zone = true) | 교차 GB당 $0.02, 요청·응답 모두 | NLB ENI와 타겟 ENI의 소유 계정 (보통 같은 계정) | https://aws.amazon.com/elasticloadbalancing/faqs/ · https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html · https://aws.amazon.com/blogs/networking-and-content-delivery/optimizing-data-transfer-costs-when-using-aws-network-load-balancer/ | 2026-09-14 |
| `nlb-client-cross-az` | VPC 내부 클라이언트(사설 주소) → 다른 AZ의 NLB 노드 IP. cross-zone 설정과 무관 | 교차 GB당 $0.02 × 기대 비율 | 클라이언트 ENI 계정 + NLB ENI 계정 | https://aws.amazon.com/blogs/networking-and-content-delivery/optimizing-data-transfer-costs-when-using-aws-network-load-balancer/ · https://aws.amazon.com/blogs/networking-and-content-delivery/exploring-data-transfer-costs-for-aws-network-load-balancers/ · https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-load-balancer-attributes.html | 2026-09-14 |
| `nodeport-hop-cross-az` (신규) | instance 타겟 LB(ALB·NLB instance 모드, 레거시 CLB) → 노드 NodePort → kube-proxy → 다른 AZ 노드의 pod | 교차 GB당 $0.02 | 클러스터 계정 | https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html · https://aws.amazon.com/blogs/containers/understanding-data-transfer-costs-for-aws-container-services/ · https://kubernetes.io/docs/reference/networking/virtual-ips/ | 2026-09-14 |
| `rds-cross-az` | pod → 다른 AZ의 RDS/Aurora **인스턴스** (프라이머리, read replica, Multi-AZ cluster reader, Aurora writer/reader) | 교차 GB당 **$0.01** (EC2 쪽만, 방향마다) | 노드(EC2) 계정 | https://aws.amazon.com/rds/pricing/ · https://aws.amazon.com/rds/aurora/pricing/ | 2026-09-14 |
| `elasticache-cross-az` | pod → 다른 AZ의 ElastiCache 노드. Serverless는 캐시 VPC 엔드포인트가 없는 AZ에서 접근할 때 | 교차 GiB당 **$0.01** (EC2 쪽만) | 노드(EC2) 계정. ElastiCache 노드·엔드포인트 쪽 요금 없음 | https://aws.amazon.com/elasticache/pricing/ | 2026-09-14 |
| `msk-client-cross-az` | pod → 다른 AZ의 MSK **Provisioned** 브로커 ENI (같은 VPC 또는 peered VPC, 사설 IP) | 확인된 하한 교차 GB당 $0.01. 브로커 쪽도 청구되면 $0.02 (미확인) | 클라이언트 노드 계정(확인). 브로커 측은 미확인 | https://aws.amazon.com/msk/pricing/ · https://aws.amazon.com/blogs/big-data/optimize-traffic-costs-of-amazon-msk-consumers-on-amazon-eks-with-rack-awareness/ · https://docs.aws.amazon.com/msk/latest/developerguide/bestpractices-kafka-client.html | 2026-09-14 |
| `efs-cross-az` | 노드 → 다른 AZ의 EFS 마운트 타겟 (실제로 사용 중인 마운트 타겟 기준) | AZ in/out 각 $0.01/GB. 교차 GB당 $0.01~0.02 (도구는 하한 0.01) | 클라이언트·파일시스템 소유 계정. 어느 측이 계량되는지는 문서 없음 | https://aws.amazon.com/efs/pricing/ · https://docs.aws.amazon.com/efs/latest/ug/mounting-fs-mount-cmd-general.html · https://docs.aws.amazon.com/efs/latest/ug/mounting-one-zone.html | 2026-09-14 |
| `public-ipv4-in-region` (신규) | pod/노드 → 같은 리전 고객 리소스의 공인·EIP IPv4 (예: internet-facing ALB/NLB 공인 IP로 헤어핀). **AZ 무관** | 방향마다 측당 $0.01. 같은 AZ여도 과금. NAT 경유 시 NAT 처리료 추가 | 양 끝 리소스 계정 | https://aws.amazon.com/ec2/pricing/on-demand/ · https://aws.amazon.com/blogs/networking-and-content-delivery/exploring-data-transfer-costs-for-classic-and-application-load-balancers/ · https://aws.amazon.com/blogs/networking-and-content-delivery/exploring-data-transfer-costs-for-aws-network-load-balancers/ | 2026-09-14 |
| `vpc-peering-cross-az` (신규) | pod ↔ peered VPC의 리소스(클라이언트, ALB/CLB/NLB, ip 타겟)이고 AZ ID가 다름 | In $0.01 + Out $0.01 = 교차 GB당 $0.02. 같은 AZ는 무료(계정 달라도) | 각 측 계정 | https://aws.amazon.com/vpc/pricing/ · https://aws.amazon.com/blogs/networking-and-content-delivery/demystifying-amazon-vpc-peering-charges/ | 2026-09-14 |
| `nat-gateway-cross-az` (신규) | pod → 다른 AZ의 NAT Gateway (게이트웨이 엔드포인트 경로가 없는 S3/DynamoDB 호출 포함) | EC2 AZ 간 요율(측당 $0.01. NAT 측 계량 여부는 명시 없음) + NAT 처리료(AZ 무관) | 클러스터 계정 | https://aws.amazon.com/vpc/pricing/ · https://docs.aws.amazon.com/vpc/latest/userguide/nat-gateway-pricing.html | 2026-09-14 |

### 3.1 판정 조건

**`clusterip-cross-az`** — 조건부 · high
- kube-proxy(또는 eBPF 대체)가 호출자 노드에서 DNAT한 뒤 백엔드 pod IP로 바로 보낸다. 추가 AWS 홉은 없다. 기본 선택은 zone과 무관한 무작위다.
- 과금 몫 = 호출자 노드 zone과 엔드포인트 노드 zone이 다른 쌍의 비율. 금액은 기대값이지 확정값이 아니다.
- 비율을 낮추거나 없애는 설정과 그 한계는 §6.
- 경로가 클러스터 밖 다른 AZ NAT를 거치면 `nat-gateway-cross-az`, TGW·인터페이스 엔드포인트를 거치면 AZ 간 전송은 무료(처리료만).
- 노드가 Local Zone이면 §1.2 요율.

**`headless-cross-az`** — 조건부 · high
- headless Service(`clusterIP: None`)는 kube-proxy가 프록시하지 않고 DNS가 pod IP를 돌려준다. StatefulSet pod는 pod별 레코드를 받는다.
- 노드 AZ가 다른 peer 쌍에서 오가는 바이트만 과금. 같은 AZ peer는 무료.
- `trafficDistribution`, topology hints, `internalTrafficPolicy`는 효과가 없다. 이것들 때문에 추정을 낮추지 않는다.
- 볼륨은 실제 복제 흐름(리더 → 팔로워)에 달려 있고 모든 쌍이 같은 양을 주고받지 않는다.

**`nlb-cross-zone`** — 조건부 · high
- 유효 cross-zone = 타겟그룹 속성 `load_balancing.cross_zone.enabled`가 `true`/`false`면 그 값, `use_load_balancer_configuration`(기본)이면 LB 속성(NLB 기본 `false`). `alb` 타입 타겟그룹은 항상 LB 값을 따른다.
- 유효값이 true면 NLB 노드마다 **LB에 활성화된 AZ**의 정상 타겟 전체로 분산한다. 활성화 안 된 AZ의 타겟은 트래픽을 받지 않는다.
- AZ X 노드의 교차 비율 ≈ 활성 AZ의 정상 타겟 중 X 밖에 있는 비율. 정상 타겟이 활성 AZ 하나에만 있으면 이 다리는 넘지 않는다. 유효값이 false면 이 다리는 절대 넘지 않는다.
- scheme과 무관하다. internet-facing NLB도 NLB → 타겟 다리는 같은 규칙이다(인터넷 → NLB 유입은 무료, 인터넷 응답은 DTO).
- target type과 무관하다. ip 모드라도 이 규칙은 적용된다.
- 속성을 읽을 수 없으면 "모름". annotation이 없다고 off로 보지 않는다(LBC는 annotation을 지워도 속성을 되돌리지 않는다). 레거시 cloud provider는 annotation이 true가 아니면 LB 속성을 false로 되돌리지만, 타겟그룹 속성은 `aws-load-balancer-target-group-attributes`로 바뀔 수 있다(소스 코드에서 추론).
- 끄는 방법: 타겟그룹 값이 LB 값보다 우선하므로 LBC Service annotation `service.beta.kubernetes.io/aws-load-balancer-target-group-attributes: load_balancing.cross_zone.enabled=false`로 TG 속성을 설정한다(§6.8). LB 속성(`aws-load-balancer-attributes`)만 끄면 TG가 `true`로 설정된 경우 여전히 넘는다.
- 도구(L1 없음): cross-zone 값을 TG 속성 annotation → LB 속성 annotation → 레거시 `aws-load-balancer-cross-zone-load-balancing-enabled` 순으로 읽는다. L1이 있으면 실제 TG/LB 속성이 우선한다.
- `target_group_health.unhealthy_state_routing`이 비정상 타겟으로 보낼 수 있어 "정상 타겟만" 계산은 약간 틀릴 수 있다.

**`nlb-client-cross-az`** — 조건부 · medium (과금 사실은 high, 발생 비율은 추정). crossaz는 VPC 내부 클라이언트가 실제로 있는지조차 모르므로 `rules.yaml`에서 **low**로 둔다
- 클라이언트가 어느 NLB 노드 IP를 받는지는 DNS가 정한다. `dns_record.client_routing_policy` 기본값 `any_availability_zone`이면 AZ가 고르게 퍼졌을 때 약 (N−1)/N이 넘는다.
- `partial_availability_zone_affinity`(85%)와 `availability_zone_affinity`(100%)는 Route 53 Resolver가 질의자의 AZ를 알 때만 줄여 준다. EKS pod는 CoreDNS를 거쳐 질의하고 캐시된 응답을 공유하므로 Resolver가 보는 AZ는 CoreDNS의 AZ다. affinity를 보장이 아니라 기대 비율 감소로 다룬다(AWS가 EKS용으로 문서화하지 않은 추론).
- 같은 VPC, peered VPC, TGW 경유 사설 주소 클라이언트에만 적용. 인터넷 클라이언트는 AZ가 없다. VPC 클라이언트가 internet-facing NLB의 공인 IPv4로 가면 `public-ipv4-in-region`.
- dual-stack NLB에 같은 VPC에서 IPv6로 접속하면 공인 IP 요금이 아니라 이 규칙(같은 AZ면 무료).
- ALB/CLB에는 절대 적용하지 않는다.
- kube-proxy 힌트로 노드 → pod 홉이 zone-local이어도 이 다리는 남는다. 도구는 `nlb-zonal` 경로에 `topology-aware-routing`(비과금)을 적용하지 않는다.

**`nodeport-hop-cross-az`** — 조건부 · medium
- LB → 노드 다리는 ALB/CLB면 무료, NLB면 `nlb-cross-zone` 규칙.
- 노드 → pod 홉은 `externalTrafficPolicy: Cluster`(기본)이고 수신 노드와 다른 AZ에 ready 엔드포인트가 있을 때만 발생. 수신 노드에 pod가 있어도 Cluster 정책에서는 로컬 pod를 우선하지 않는다.
- `externalTrafficPolicy: Local`이면 노드 로컬 엔드포인트로만 보내므로 이 홉은 없다(LBC는 healthCheckNodePort로 로컬 엔드포인트 없는 노드를 빼 준다).
- Cluster 정책에서도 `trafficDistribution`이 외부 트래픽을 같은 zone으로 유도하지만 없애지는 못한다.
- ip 타겟 그룹에는 이 홉이 없다. EKS Auto Mode 기본은 ip(ALB IngressClass controller `eks.amazonaws.com/alb`, NLB `loadBalancerClass: eks.amazonaws.com/nlb`), 셀프 관리 LBC는 `loadBalancerClass`가 있으면 기본 instance다.
- 흐름 데이터가 없으면 "가능성 높음"으로 표시하고 확정 과금으로 쓰지 않는다.
- 도구: route `alb`/`clb`/`nlb-zonal`/`nlb`(cross-zone 모름) + instance + `Cluster` + zone-local 아님이면 이 규칙(medium). NLB cross-zone 설정을 몰라도 이 홉은 과금이므로 `nlb-cross-zone-unknown`보다 먼저 평가한다. cross-zone이 켜진 NLB(`nlb-cross-zone`)는 LB 다리 규칙이 먼저 잡고, 금액에 이 홉이 따로 더해지지 않는다.

**`rds-cross-az`** — 예 · high (Aurora 쪽 무과금은 medium)
- 클라이언트가 붙는 모든 DB 인스턴스가 대상이다. 프라이머리만이 아니다.
- 스캔 시점의 `AvailabilityZone`을 쓴다. Multi-AZ failover로 프라이머리가 옮겨 가므로 `SecondaryAvailabilityZone`을 클라이언트 트래픽 판정에 쓰지 않는다.
- Aurora cluster endpoint는 현재 writer의 AZ. reader endpoint는 리플리카 사이를 돌므로 "부분/가능성"으로 표시.
- RDS Proxy 엔드포인트는 근거 없음 → `rds-proxy-cross-az`.

**`elasticache-cross-az`** — 예 · high (노드 기반), 조건부 (Serverless)
- 노드 기반: 노드별 `CustomerAvailabilityZone`을 쓴다(클러스터 `PreferredAvailabilityZone`은 `Multiple`일 수 있다). 쓰기는 샤드 프라이머리로, 읽기는 클라이언트 설정(READONLY, AZ affinity read-from-replica)에 따라 리플리카로 갈 수 있다. 클라이언트 설정을 모르면 읽기는 "가능성"으로 표시. failover 뒤 프라이머리 AZ가 바뀐다.
- cluster mode enabled에서는 configuration endpoint가 노드 하나로 풀리지 않아 샤드 프라이머리 AZ를 알 수 없다 → 확률로만 표시.
- Serverless: 노드 AZ가 캐시 VPC 엔드포인트가 있는 AZ면 무료, 없는 AZ에서 접근하면 EC2 쪽 AZ 간 요금. 실제 접속한 엔드포인트 ENI의 AZ ID로 비교하고, 모르면 캐시 `SubnetIds`의 AZ ID로 비교한다. 일반 PrivateLink 면제(§2 `vpce-interface-cross-az`)보다 ElastiCache 요금 FAQ가 우선한다.
- 노드 간 복제 → `elasticache-replication-cross-az`(§4).

**`msk-client-cross-az`** — 예 · medium
- Provisioned 클러스터(`ListClustersV2` ClusterType)에서 브로커 ENI로 직접 붙는 경우만. Serverless → `msk-serverless-cross-az`(§4).
- **제외**: MSK multi-VPC private connectivity(managed VPC connection = 클라이언트 VPC의 PrivateLink 인터페이스 엔드포인트, AZ 간 전송 무료, PrivateLink 요금만), public access(공인 IP 규칙).
- 프로듀서는 파티션 리더로 보낸다. 컨슈머는 리더에서 가져오지만 `client.rack` + rack-aware replica selector(KIP-392)가 있으면 같은 AZ 팔로워에서 가져온다. 리더 배치를 모르면 확정이 아니라 "가능성 높음".

**`efs-cross-az`** — 조건부 · medium
- 과금은 **실제로 쓰는 마운트 타겟**의 AZ ID가 노드와 다를 때만. 다른 AZ에 마운트 타겟이 존재한다는 이유만으로 표시하지 않는다.
- Regional 파일시스템을 파일시스템 ID/DNS 이름으로 마운트하면 같은 AZ 마운트 타겟으로 풀린다 → 무료.
- 노드 AZ에 마운트 타겟이 없고 고정도 없으면 마운트가 실패할 뿐 과금되지 않는다(efs-utils는 노드 AZ에서만 찾는다).
- 넘는 경우: One Zone 파일시스템을 다른 AZ에서 사용, `az=` 마운트 옵션, `volumeAttributes.mounttargetip`, `<az>.fs-xxx.efs.<region>.amazonaws.com` 형태 `volumeHandle`, 교차 계정 CSI(`awsRoleArn`)에서 StorageClass `az=`로 박힌 IP 또는 IP 맵 폴백.
- 해당 볼륨을 실제로 쓰는 pod가 있는 노드만 표시한다. Elastic Throughput 데이터 접근료는 AZ와 무관하다.
- v1 수집 범위 밖. 규칙만 기록한다.

**`public-ipv4-in-region`** — 예 · high (IPv4만)
- 실제 패킷의 목적지가 VPC CIDR 밖 공인 IPv4일 때만. scheme(internet-facing)만 보고 판정하지 않는다: dualstack-without-public-ipv4 ALB는 AAAA만 내고, dual-stack LB에 IPv6로 붙으면 VPC 안 주소로 간다.
- AWS 서비스 공인 엔드포인트(§1.4)는 제외.
- AZ 간 요금이 아니므로 cross-AZ 합계와 섞지 않는다. Flow Logs 없이는 탐지가 어렵다.

**`vpc-peering-cross-az`** — 조건부 · high
- ALB/CLB 면제는 같은 VPC에만 적용된다. 클라이언트나 ip 타겟이 LB VPC 밖이면 이 규칙.
- TGW 경유면 AZ 간 전송은 무료이고 TGW에 보낸 GB당 처리료가 AZ와 무관하게 붙는다. 이 규칙을 적용하지 않는다.
- CE에서는 2025-04-11부터 `<prefix>-VpcPeering-In/Out-Bytes`(AmazonVPC)로 보인다.
- ELB 블로그 Figure 5 캡션(다른 AZ·다른 VPC 타겟 무료)은 본문 시나리오와 모순되므로 무시.

**`nat-gateway-cross-az`** — 조건부 · medium
- pod의 실제 egress 서브넷(§2 `s3-gateway-endpoint` 참조) 기본 경로가 다른 AZ NAT Gateway를 가리킬 때.
- Regional NAT Gateway(`AvailabilityMode=regional`)는 한 AZ에 묶이지 않는다 → 금액을 비우고 "모름".

---

## 4. 미확정

| ID | 쟁점 | 현재 처리 | 검증 방법 |
|---|---|---|---|
| `alb-ip-target-mode` (잔여) | 요금 문구는 "EC2 인스턴스와 LB 사이"만 말한다. EC2 노드 pod는 EKS BP로 메워지지만 Fargate는 ECS 기준 블로그뿐 | 비과금 medium. 과금으로 격상하지 않음 | `--verify`로 ALB IP 모드만 있는 계정의 월 `DataTransfer-Regional-Bytes`와 추정치 대조. CUR에서 ALB ENI 관련 InterZone 라인 확인 |
| `alb-ipv6` | 같은 VPC·다른 AZ의 ALB ↔ pod/인스턴스 IPv6 트래픽(dual-stack ALB, IPv6 타겟그룹, EKS IPv6 클러스터, internet-facing dual-stack ALB에 VPC 안에서 IPv6로 접속)이 면제되는지 공식 문구 없음. ELB 문서도 IPv4 TG는 사설 IPv4, IPv6 TG는 IPv6로 통신한다고만 함. CLB는 IPv4 전용이라 해당 없음 | "unverified". 비과금으로 확정하지 않음. 도구는 `lb-exemption-unverified`(IPv6, 비사설 IPv4 타겟, 다른 VPC의 ALB/CLB)를 **과금 가능·low**로 보고한다: UNCERTAIN에 금액 `?`, 총액 제외 | IPv6 전용 테스트 클러스터에서 CUR 라인 확인, AWS Support 문의 |
| `msk-client-cross-az` (브로커 측) | 브로커 ENI 쪽도 청구되는지(→ $0.02) 공식 문구 없음 | 하한 $0.01로 계산, "브로커 측 청구 가능(미확인)" 표기 | CUR에서 MSK 클러스터 소유 계정의 Regional-Bytes 라인과 브로커 ENI 확인 |
| `msk-serverless-cross-az` | Serverless 요금·FAQ는 교차 리전·인터넷 전송만 요금으로 나열. 클라이언트는 VPC 엔드포인트로 접속. 2022 PrivateLink 면제(무료 쪽)와 ElastiCache Serverless의 명시적 과금(유료 쪽)이 충돌 | 과금 합계에서 제외, 정보로만 표시 | CUR에서 Serverless 클러스터 트래픽이 `xAZ-In/Out-Bytes`($0)인지 `Regional-Bytes`인지 확인 |
| `efs-cross-az` (측 수) | 요금 페이지는 AZ in/out 각 $0.01만. 마운트 타겟 측 계량 여부 없음 | 하한 0.01, 상한 0.02 주석 | CUR에서 EFS 관련 Regional-Bytes 라인 수 확인 |
| `rds-multiaz-replication` (DB cluster) | Multi-AZ DB cluster(읽기 가능 스탠바이 2개) 복제 무료가 일반 문구에서 추론됨 | 사실 신뢰도는 비과금 medium. 도구의 `rds-multiaz-replication` 규칙은 엔진·배포 형태를 구분하지 않아 **high**로 보고한다(비과금 판정은 같고 신뢰도만 근사) | RDS 문서 갱신 추적, CUR 확인 |
| `rds-proxy-cross-az` | RDS Proxy 엔드포인트의 AZ 간 전송 처리를 다룬 공식 근거를 찾지 못함 | 표시하지 않음(규칙 없음) | RDS Proxy 요금 페이지·문서 조사 |
| `elasticache-replication-cross-az` | 같은 리전 노드 간 복제가 AZ를 넘을 때 과금 여부 명시 없음. 요금 문구(EC2 쪽만, 노드 측 없음)와 예시(클라이언트 볼륨만 계산)는 무료 쪽 | 표시하지 않음, 과금 합계 제외. EKS에서 관측 불가 | ElastiCache 문서 갱신 추적 |
| `ipv6-cross-vpc-same-az` | EC2 페이지는 다른 VPC IPv6에 방향마다 $0.01. peering 규칙은 같은 AZ 무료. 두 규칙을 조정한 공식 문구 없음 | peering이 아닌 경로(IGW 등)만 과금 표시, peering 경유는 미확정 | CUR 확인, AWS Support 문의 |
| `local-zone-rate` | 부모 AZ 쪽 청구, Local Zone ↔ Local Zone 전송, Wavelength 4곳(가격표 행 없음) | 해당 노드가 있으면 경고하고, 그 zone에 닿는(호출자 또는 목적지 분포에 포함) 과금 경로의 금액을 비워 low로 둔다(UNCERTAIN, 총액 제외, 수신량은 유지) | Price List 갱신 추적, CUR 확인 |
| `vpce-non-interface` | 2022 면제는 인터페이스 엔드포인트만 명시. Resource 엔드포인트·서비스 네트워크 엔드포인트, 자기 소유 endpoint service 뒤 NLB의 cross-zone, peering/TGW 너머에서 엔드포인트 접근은 다루지 않음 | $0으로 확장하지 않음, 판정 보류 | VPC Lattice·PrivateLink 요금 문서 조사, CUR 확인 |
| `nat-gateway-cross-az` (regional) | Regional NAT Gateway는 한 AZ에 속하지 않음 | "모름" | NAT Gateway 요금 문서 갱신 추적 |

---

## 5. ALB 우회 함정

내부 ALB로 우회하면 AZ 간 전송료는 사라지지만(`alb-private-same-vpc`) **LCU 요금과 시간당 기본료**가 생긴다.

### 5.1 LCU 차원 ([ELB 요금](https://aws.amazon.com/elasticloadbalancing/pricing/), 2026-09-14)

| 차원 | 1 LCU당 |
|---|---|
| 새 연결 | 초당 25 |
| 활성 연결 | 분당 3,000 (Mutual TLS 사용 시 1,500) |
| 처리 바이트 | EC2 인스턴스·컨테이너·IP 타겟 시간당 **1 GB**, Lambda 타겟 시간당 **0.4 GB** |
| 규칙 평가 | 초당 1,000 (처리 규칙 중 첫 10개 무료) |

- 매 시간 **가장 많이 쓴 차원 하나**에만 과금된다.
- 처리 바이트 = HTTP 헤더 + 페이로드, 요청과 응답 **양방향**. CloudWatch `ProcessedBytes`는 이미 양방향이고 WebSocket, (사용자 인증 시) IdP 트래픽도 포함한다. `IPv6ProcessedBytes`는 그 부분집합이므로 더하지 않는다. ALB 지표는 헬스체크를 제외한다. ([ALB CloudWatch 지표](https://docs.aws.amazon.com/elasticloadbalancing/latest/application/load-balancer-cloudwatch-metrics.html))

### 5.2 가격 (Price List 게시 2026-09-11)

| 항목 | us-east-1 | ap-northeast-2 | usage type |
|---|---|---|---|
| ALB LCU-시간 | $0.008 | $0.008 | `LCUUsage` / `APN2-LCUUsage` (operation `LoadBalancing:Application`) |
| 예약 LCU-시간 | $0.008 | $0.008 | `ReservedLCUUsage` / `APN2-ReservedLCUUsage` |
| ALB-시간 | $0.0225 | $0.0225 | `LoadBalancerUsage` / `APN2-LoadBalancerUsage` (operation `LoadBalancing:Application`) |
| 혼동 주의: CLB | $0.025/시간, $0.008/GB | — | operation `LoadBalancing`의 `LoadBalancerUsage`, `DataProcessing-Bytes`는 **Classic LB** |

근거: [AWSELB us-east-1](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSELB/current/us-east-1/index.json), [AWSELB ap-northeast-2](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSELB/current/ap-northeast-2/index.json)

### 5.3 함정

1. **"GB당 $0.008"은 바이트가 지배 차원일 때만.** 연결 수나 규칙 평가가 지배하면 바이트의 한계 비용은 $0이다. 예약 LCU 이하 사용분도 한계 비용 $0이다. 그래서 바이트로 계산한 값은 *바이트가 일으킨 비용*의 상한이고 *ALB LCU 총액*의 하한이다. 실제 사용량은 `ConsumedLCUs` 지표로 본다.
2. **분모가 다르다.** ALB는 서비스로 가는 **모든** 바이트에 LCU를 매기고, cross-AZ 요금은 **넘는 몫**에만 붙는다. 아래는 이 문서의 계산이며 출처가 아니다.

   | AZ 수 (균등) | cross-AZ 비용 (전체 GB당) | ALB 바이트 비용 (전체 GB당) | GB당 절감 | ALB 기본료 $0.0225×720h = $16.20/월 손익분기 |
   |---|---|---|---|---|
   | 3 | $0.02 × 2/3 ≈ $0.0133 | $0.008 | ≈ $0.0053 | 월 약 3.0 TB |
   | 2 | $0.02 × 1/2 = $0.010 | $0.008 | $0.002 | 월 약 8.1 TB |

   서비스마다 ALB를 두면 기본료 합이 절감액을 넘기 쉽다. `trafficDistribution`은 엔드포인트가 모든 호출자 AZ에 있으면 넘는 몫을 추가 요금 없이 0에 가깝게 만든다.
3. **instance 타겟이면 절감이 사라질 수 있다.** 노드 → pod 홉이 `nodeport-hop-cross-az`로 다시 과금된다. 우회하려면 IP 타겟 모드.
4. **내부(internal) ALB여야 한다.** VPC 안에서 internet-facing ALB의 공인 IPv4로 부르면 AZ와 무관하게 방향마다 $0.01(`public-ipv4-in-region`), NAT를 거치면 처리료까지.
5. **같은 VPC여야 한다.** 다른 VPC 클라이언트는 `vpc-peering-cross-az`나 TGW 처리료. IPv6 경로는 `alb-ipv6`(미확정).
6. **LCU는 AZ 배치와 무관하다.** cross-AZ 결과나 zone-local 절감액에 LCU를 넣지 않는다.

도구는 "ALB를 세우라"고 권고하지 않는다. 기본 권고는 §6의 `trafficDistribution`이다.

---

## 6. Kubernetes 조치

### 6.1 `spec.trafficDistribution` 값 ([Virtual IPs and Service Proxies](https://kubernetes.io/docs/reference/networking/virtual-ips/), 문서 최신 v1.37)

| 값 | 동작 | 폴백 |
|---|---|---|
| (미설정) | 클러스터 전체 엔드포인트로 고르게 분산 | — |
| `PreferSameZone` | 호출자 zone의 엔드포인트 우선 | 그 zone에 없으면 클러스터 전체 |
| `PreferClose` | `PreferSameZone`과 **완전히 같은 의미의 폐지 예정(deprecated) 별칭**. 제거되지 않았고 제거는 KEP의 비목표 | 같음 |
| `PreferSameNode` | 같은 노드 → 같은 zone → 클러스터 전체 | 트래픽을 버리지 않음 |

EndpointSlice 컨트롤러는 세 값 모두에 ready 엔드포인트마다 자기 zone 하나를 zone hint로 쓴다(`endpoint.zone`이 없으면 hint 없음). node hint는 `PreferSameNode`에만 추가한다. 그래서 node hint를 모르는 kube-proxy에서도 `PreferSameNode`는 최소한 same-zone으로 동작한다. ([trafficdist.go](https://raw.githubusercontent.com/kubernetes/kubernetes/master/staging/src/k8s.io/endpointslice/trafficdist/trafficdist.go), [types.go](https://raw.githubusercontent.com/kubernetes/api/master/core/v1/types.go))

### 6.2 버전

| 기능 게이트 (kube-apiserver, kube-controller-manager, kube-proxy) | alpha | beta | GA |
|---|---|---|---|
| `ServiceTrafficDistribution` (KEP-4444, 필드와 `PreferClose`) | 1.30 (기본 off) | 1.31–1.32 (기본 on) | 1.33 (고정 on) |
| `PreferSameTrafficDistribution` (KEP-3015, `PreferSameZone`·`PreferSameNode`) | 1.33 (기본 off) | 1.34 (기본 on) | 1.35 (고정 on) |
| `TopologyAwareHints` (`topology-mode: Auto`) | — | 1.24–1.32 (기본 on) | 1.33 (고정 on) |

- 게이트가 꺼져 있으면 API 서버가 `PreferSameZone`/`PreferSameNode`를 받지 않는다.
- `PreferClose`의 공식 deprecated 표기는 **1.35**부터다. 1.33 문서는 향후 별칭이 될 것이라고만 했고 1.34 문서에는 표기가 없다. EKS Best Practices도 1.35로 적는다.
- EKS 지원 버전(2026-09-14): 표준 1.34·1.35·1.36, 연장 1.31·1.32·1.33(1.31 연장 지원 종료 2026-11-26). EKS는 alpha 기능을 지원하지 않고 게이트 목록을 공개하지 않으므로 1.31/1.32의 `PreferClose`, 1.34의 `PreferSameZone`은 업스트림 기본값에 기댄다. ([EKS Kubernetes versions](https://docs.aws.amazon.com/eks/latest/userguide/kubernetes-versions.html))

| 컨트롤 플레인 | 권고 값 |
|---|---|
| 1.31–1.33 | `PreferClose` |
| 1.34 이상 | `PreferSameZone` (`PreferClose`도 동작. 오설정으로 표시하지 않음) |
| DaemonSet형 백엔드 | `PreferSameNode` (1.34 이상. EKS는 kube-proxy 1.35 이상 권장) |

출처: [ServiceTrafficDistribution 게이트](https://raw.githubusercontent.com/kubernetes/website/main/content/en/docs/reference/command-line-tools-reference/feature-gates/ServiceTrafficDistribution.md), [PreferSameTrafficDistribution 게이트](https://raw.githubusercontent.com/kubernetes/website/main/content/en/docs/reference/command-line-tools-reference/feature-gates/PreferSameTrafficDistribution.md), [KEP-4444](https://www.kubernetes.dev/resources/keps/4444/), [KEP-3015](https://www.kubernetes.dev/resources/keps/3015/), [EKS Best Practices](https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html)

### 6.3 kube-proxy 버전이 결정한다

- hint를 쓰는지는 **호출자 트래픽을 처리하는 kube-proxy**가 정한다. 컨트롤 플레인 지원만으로는 부족하다.
- kube-proxy ≤ 1.29, 그리고 기본 게이트의 1.30은 Service에 비어 있지 않고 `disabled`가 아닌 `topology-aware-hints`/`topology-mode` annotation이 없으면 hint를 **무시**한다. `trafficDistribution`만 설정된 Service는 클러스터 전체로 간다.
- 1.31부터(게이트 기본 on) annotation 없이 hint를 쓴다. 1.33 이상은 annotation·게이트 검사 없이 hint만 읽는다.
- 업스트림 kube-proxy는 node hint를 1.34(beta)부터 기본으로 쓴다.
- 노드는 컨트롤 플레인보다 최대 3 마이너 뒤처질 수 있다. EKS kube-proxy는 보통 클러스터 전체 add-on DaemonSet 이미지이므로 그 버전을 확인한다.
- 버전을 모르거나 kube-proxy 대체(Cilium, EKS Auto Mode 등)가 검증되지 않았으면 zone-local을 인정하지 않고, 해당 경로는 확정 과금과 분리한 "미검증"으로 둔다. 도구: `trafficDistribution`이나 `topology-mode` annotation을 설정했는데 kube-proxy가 이 서비스의 hint를 쓰는지 확인하지 못한 ClusterIP 경로(버전 모름, 대체 프록시, 버전이 hint를 무시)는 `hints-unverified` — 과금, low(UNCERTAIN, 총액 제외). 1.30 kube-proxy에 `trafficDistribution`만 있는 경우처럼 실제로는 확정 과금인 경로도 low로 보고한다. hint를 쓰는 kube-proxy로 확인됐는데 hint 없는 ready 엔드포인트가 있으면(topology-mode Auto가 엔드포인트 부족·과부하 기준으로 보류하는 경우 등) kube-proxy는 모든 엔드포인트로 분산하므로 `hints-withheld` — 과금, high. `topology-mode` annotation이 `trafficDistribution`보다 우선하므로 조치는 annotation을 지우고 `trafficDistribution`을 설정하는 것(kind v1.35에서 annotation을 둔 채 필드만 설정하면 hint가 없고, annotation을 지우면 같은 zone hint가 생기며 kube-proxy 규칙이 zone별 엔드포인트로 줄어드는 것을 확인). 단 엔드포인트가 없는 호출자 AZ가 있으면 hint와 무관하게 넘어가므로 `clusterip-cross-az-partial`(과금, high)이 먼저 매치된다.

출처: [topology.go v1.29.0](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.29.0/pkg/proxy/topology.go), [v1.30.0](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.30.0/pkg/proxy/topology.go), [v1.31.0](https://raw.githubusercontent.com/kubernetes/kubernetes/v1.31.0/pkg/proxy/topology.go), [EKS kube-proxy add-on](https://docs.aws.amazon.com/eks/latest/userguide/managing-kube-proxy.html)

### 6.4 zone-local로 인정하는 조건 (모두 만족)

1. Service가 프록시 대상: ClusterIP가 있고 ExternalName이 아님. headless는 해당 없음.
2. **모든** ready 엔드포인트가 zone hint를 가짐. 하나라도 없으면 모두 클러스터 전체로 간다.
3. 호출자 노드에 `topology.kubernetes.io/zone` 라벨이 있고, 그 zone으로 hint된 ready 엔드포인트가 하나 이상 있음.
4. 컨트롤러가 `topology-mode: Auto`가 아니라 `trafficDistribution` 휴리스틱을 적용함(`topology-mode`가 우선, 폐지 예정 키 `topology-aware-hints`가 있으면 그것이 더 우선).
5. 해당 트래픽 정책(internal/external)이 `Cluster`. `Local`이면 Local이 이긴다.
6. 트래픽이 실제로 kube-proxy의 ClusterIP/NodePort 규칙을 지남. pod IP 직통은 해당 없음. ingress-nginx는 기본적으로 EndpointSlice의 pod IP/포트를 upstream으로 쓰고, `nginx.ingress.kubernetes.io/service-upstream: "true"`일 때만 Service의 ClusterIP로 보낸다([ingress-nginx annotations](https://kubernetes.github.io/ingress-nginx/user-guide/nginx-configuration/annotations/#service-upstream)). 도구는 ALB가 아닌 Ingress가 이 annotation 없이 가리키는 zone-local 서비스를 비과금 확정 대신 `ingress-direct-backend`(low)로 둔다. 컨트롤러 자체의 zone-aware 설정은 읽지 않는다.

- 조건을 모두 만족해도 ready 엔드포인트가 없는 zone의 호출자는 계속 AZ를 넘는다 → 그 zone만 과금으로 남긴다(rules.yaml `clusterip-cross-az-partial`, 조치는 `topology.kubernetes.io/zone` topologySpreadConstraints).
- ready 엔드포인트가 전혀 없으면 serving-terminating 엔드포인트를 zone 필터 없이 쓴다.
- 판정은 `spec.trafficDistribution` 값이 아니라 EndpointSlice의 `hints.forZones`로 한다.

출처: [topology.go (master)](https://raw.githubusercontent.com/kubernetes/kubernetes/master/pkg/proxy/topology.go), [proxy/util/utils.go](https://raw.githubusercontent.com/kubernetes/kubernetes/master/pkg/proxy/util/utils.go), [endpointslice/utils.go](https://raw.githubusercontent.com/kubernetes/kubernetes/master/staging/src/k8s.io/endpointslice/utils.go)

### 6.5 `service.kubernetes.io/topology-mode: Auto` (Topology Aware Routing)

- 값 `Auto`(코드는 `auto`도 허용)로 켠다. 1.27 이전 키는 `service.kubernetes.io/topology-aware-hints`이고, 있으면 그 키가 우선한다.
- 컨트롤러는 zone별 allocatable CPU 비율로 엔드포인트를 나눈다. 균형을 위해 **다른 zone의 엔드포인트를 hint할 수 있다** → 그 몫은 AZ를 넘는다.
- hint를 붙이지 않는 경우: 엔드포인트 수 < zone 수, 예상 과부하를 임계 0.2 아래로 못 맞춤, allocatable CPU나 zone 라벨이 없는 노드 존재.
- kube-proxy가 전체로 폴백하는 경우: hint 없는 ready 엔드포인트가 있음, 노드 zone으로 hint된 엔드포인트가 없음.
- zone당 엔드포인트 3개 미만이면 폴백 확률이 약 50%.
- `internalTrafficPolicy: Local`과 함께 쓰이지 않는다. unready·컨트롤 플레인 노드는 무시, toleration은 고려하지 않는다.
- 도구는 annotation 존재만으로 zone-local이라 보지 않는다. `forZones`가 자기 zone인 엔드포인트만 같은 zone으로 계산한다. 권고는 `Auto`보다 `trafficDistribution`.

출처: [Topology Aware Routing](https://kubernetes.io/docs/concepts/services-networking/topology-aware-routing/), [topologycache.go](https://raw.githubusercontent.com/kubernetes/kubernetes/master/staging/src/k8s.io/endpointslice/topologycache/topologycache.go), [TopologyAwareHints 게이트](https://raw.githubusercontent.com/kubernetes/website/main/content/en/docs/reference/command-line-tools-reference/feature-gates/TopologyAwareHints.md)

### 6.6 트래픽 정책

| 설정 | 효과 | 주의 |
|---|---|---|
| `internalTrafficPolicy: Local` (1.26 GA) | ClusterIP 트래픽을 호출자 노드의 ready 엔드포인트로만(없으면 로컬 serving-terminating). kube-proxy 경유분은 AZ를 넘지 않음 → 결과 없음 | 로컬 엔드포인트가 없으면 **트래픽을 버린다**. `trafficDistribution`보다 우선, hint 무시. 기본 권고로 쓰지 않는다 |
| `externalTrafficPolicy: Local` | 외부 트래픽을 노드 로컬 엔드포인트로만 → `nodeport-hop-cross-az` 없음 | 로컬 엔드포인트가 없으면 kube-proxy가 전달하지 않음. LBC는 healthCheckNodePort로 그런 노드를 제외 |
| `externalTrafficPolicy: Cluster` (기본) | 모든 ready 엔드포인트로 전달 | `trafficDistribution`이 외부 트래픽에도 적용되어 줄여 줌 |

출처: [Service Internal Traffic Policy](https://kubernetes.io/docs/concepts/services-networking/service-traffic-policy/), [Virtual IPs](https://kubernetes.io/docs/reference/networking/virtual-ips/)

### 6.7 Kubernetes 설정이 **효과 없는** 경로

headless Service DNS(`headless-cross-az`), ALB/NLB ip 타겟(LB → pod IP), pod IP로 직접 다이얼하는 서비스 메시·Ingress 컨트롤러(ingress-nginx 기본값), 클러스터 밖 대상(RDS/ElastiCache/MSK/EFS), NLB 클라이언트 다리.

### 6.8 규칙별 조치

| 규칙 | 조치 |
|---|---|
| `clusterip-cross-az` | §6.2 권고 값 + 모든 AZ에 엔드포인트(topologySpreadConstraints). kube-proxy ≥ 1.31 확인 |
| `headless-cross-az` | 재배치뿐(HA와 맞바꿈). Kafka `broker.rack`/`client.rack`, Elasticsearch shard allocation awareness로 읽기만 zone-local |
| `nodeport-hop-cross-az` | IP 타겟 모드(ALB `alb.ingress.kubernetes.io/target-type: ip`, NLB `service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: ip`), 또는 `externalTrafficPolicy: Local`, 또는 `trafficDistribution` |
| `nlb-cross-zone` | AZ마다 타겟이 충분하면 타겟그룹 cross-zone off: `service.beta.kubernetes.io/aws-load-balancer-target-group-attributes: load_balancing.cross_zone.enabled=false` (TG 값이 LB 값보다 우선. TG 속성 자체는 [ELB 문서](https://docs.aws.amazon.com/elasticloadbalancing/latest/network/edit-target-group-attributes.html)에 있고, LBC annotation은 TG 속성을 그대로 넘기는 일반 pass-through라 cross-zone 예시는 LBC 문서에 없음). TG가 `use_load_balancer_configuration`이면 `service.beta.kubernetes.io/aws-load-balancer-attributes: load_balancing.cross_zone.enabled=false`도 된다. annotation에 다른 속성이 있으면 값을 합칠 것. annotation 삭제로는 꺼지지 않음 |
| `nlb-client-cross-az` | 클러스터 내부 호출이면 NLB 대신 ClusterIP + `trafficDistribution`. 아니면 `dns_record.client_routing_policy=availability_zone_affinity` (CoreDNS 캐시 한계 있음) |
| `msk-client-cross-az` | 컨슈머 `client.rack` = AZ ID(노드 라벨 `topology.k8s.aws/zone-id`) + 브로커 `replica.selector.class=RackAwareReplicaSelector` |
| `rds-cross-az` | 읽기는 같은 AZ 리플리카로, 쓰기 비중 큰 워크로드는 프라이머리 AZ 배치 검토 |
| `elasticache-cross-az` | 클라이언트 AZ affinity 읽기(예: Valkey GLIDE), Serverless는 모든 노드 AZ에 엔드포인트 서브넷 |
| `efs-cross-az` | Regional 파일시스템 + AZ마다 마운트 타겟 + 고정(az=, mounttargetip) 제거. One Zone은 nodeSelector로 그 AZ에만 |
| `nat-gateway-cross-az` | AZ마다 NAT Gateway, S3/DynamoDB 게이트웨이 엔드포인트 경로 추가 |
| `public-ipv4-in-region` | 내부(internal) LB를 따로 두고 사설 주소로 호출 |

---

## 7. 변경 이력 vs SPEC 초안

기준: `SPEC_1.md` §2 (2.1–2.4). 확인일 2026-09-14.

| # | SPEC 위치 · ID | 초안 내용 | 실제 (근거는 §1–§6) | 바뀐 것 |
|---|---|---|---|---|
| 1 | §2 기본 규칙 | "방향당 $0.01, 왕복 실질 $0.02/GB" | $0.01은 흐름 방향이 아니라 **청구 측**(송신 out, 수신 in) 단가. 요청 GB와 응답 GB가 각각 $0.02. "왕복 $0.02"로 읽으면 절반으로 과소추정 | 측당/교차 GB당 용어 정의(§1.1) |
| 2 | §2 기본 규칙 | 모든 경로 $0.02 단일 요율 | RDS·ElastiCache는 EC2 쪽만($0.01), MSK는 하한 $0.01, EFS는 $0.01~0.02. 공인 IPv4는 같은 AZ도 과금. 중국 CNY, Local Zone 최대 $0.20/측 | 서비스별 청구 측 표(§1.3), 리전·Local Zone 표(§1.2), AZ 무관 청구(§1.4) |
| 3 | §2.1 `same-az` | 같은 AZ 내 **모든** 트래픽 무료 | 사설 IP만. 공인·EIP IPv4는 같은 AZ도 과금. 다른 VPC IPv6(peering 제외) 과금. NAT·TGW·PrivateLink 처리료는 남음. 계정 간 AZ ID 비교 | 조건 추가, `public-ipv4-in-region` 신설 |
| 4 | §2.1 `alb-private-same-vpc` | 근거 = ELB FAQ의 "cross-zone 상시 활성". §6 예시는 `scheme: internal` 조건 | 실제 근거는 EC2 요금의 "EC2 ↔ ALB/CLB 사설 IP·같은 VPC 무료". FAQ 이유는 불완전(클라이언트 → ALB 다리를 설명 못 함). ALB cross-zone은 타겟그룹 단위로 끌 수 있고 꺼도 요금은 안 생김. scheme이 아니라 실제 목적지 주소가 조건 | 근거 URL 교체, scheme 조건 제거, 제외(공인 IPv4, 다른 VPC, IPv6, Local Zone) 명시 |
| 5 | §2.1 `clb-private-same-vpc` | ALB와 동일하게 cross-zone 상시 활성 | CLB cross-zone은 토글 가능, API 생성 시 기본 off. 그래도 전송 무료(CLB FAQ). CLB GB당 처리료 별도. NodePort 뒤 노드 → pod 홉은 과금 가능 | 이유 교정, `nodeport-hop-cross-az` 연결 |
| 6 | §2.1 `msk-inter-broker` | 브로커 요금에 포함 | 결론 맞음. 요금 페이지가 Standard·Express 모두 브로커 간·메타데이터 노드 복제 무과금을 명시. 클라이언트 트래픽에는 해당 없음 | 근거·범위 명확화 |
| 7 | §2.1 `rds-multiaz-replication` | Multi-AZ 프라이머리 ↔ 스탠바이 | 맞음. 추가로 같은 리전 read replica 복제, Aurora 복제도 무료(명시). Multi-AZ DB cluster는 추론(medium). 읽기 가능 스탠바이로 가는 클라이언트 읽기는 과금 | 범위 확장, DB cluster medium |
| 8 | §2.1 `s3-gateway-endpoint` | 이유: AZ 간이 아니라 리전 서비스 호출 | 무료인 이유는 (a) 같은 리전 S3/DynamoDB 전송 무료(엔드포인트 없어도), (b) 게이트웨이 엔드포인트 무요금. 엔드포인트 경로가 없으면 NAT 처리료 + 다른 AZ NAT 홉 과금. 다른 리전은 inter-Region. IPv6 우회. S3 Express One Zone 교차 AZ 무료 | 조건 추가, `nat-gateway-cross-az` 신설 |
| 9 | §2.2 `clusterip-cross-az` | pod → pod (ClusterIP/직통) $0.02 | 요율은 교차 GB당 맞음. 다만 넘는 **비율**에만 붙고, "직통"(pod IP)은 `trafficDistribution`이 안 먹는 다른 경로. 조치는 kube-proxy ≥ 1.31과 EndpointSlice hint가 있어야 인정 | 비율·zone-local 조건(§6.4) 추가, 직통은 `headless-cross-az`로 |
| 10 | §2.2 `nlb-cross-zone` | cross-zone 활성 시 **양쪽 끝 모두** 과금 (특수 규칙처럼 서술) | 양 끝 청구는 EC2 일반 규칙이지 NLB 특례가 아님. cross-zone은 **타겟그룹 유효값**이 결정(`use_load_balancer_configuration`일 때만 LB 값). 활성 AZ 밖 타겟 제외, 넘는 몫만 과금. scheme·target type 무관. annotation 부재 ≠ off | 유효값 계산·비율·unknown 처리 |
| 11 | §2.2 `nlb-client-cross-az` | 클라이언트 → NLB가 AZ를 넘음, cross-zone 꺼도 발생 | 맞음. 다만 DNS에 따른 확률 사건. `client_routing_policy`로 줄어듦(CoreDNS 캐시 한계). 인터넷 클라이언트 제외, VPC 안 공인 IPv4 접속은 별도 규칙, IPv6는 이 규칙 | 기대 비율로 표시, 신뢰도 medium |
| 12 | §2.2 `vpce-interface-cross-az` | $0.02/GB 과금 | **틀림.** 2022-04-01부터 인터페이스 엔드포인트 AZ 간 전송 무료. PrivateLink 처리료만 AZ와 무관하게 청구. GWLB 엔드포인트는 면제 제외 | **§3 → §2 비과금으로 이동**. 수집·규칙 제거 |
| 13 | §2.2 `msk-client-cross-az` | $0.02/GB | 공식 확인은 클라이언트 쪽 $0.01뿐, 브로커 쪽 미확인. multi-VPC private connectivity(PrivateLink)·public access는 이 규칙이 아님. Serverless는 미확정 | `rate_gb` 0.01(하한), Provisioned만, 신뢰도 medium, `msk-serverless-cross-az` 미확정 신설 |
| 14 | §2.2 `rds-cross-az` | pod → RDS **프라이머리**, $0.02/GB | **요율 틀림**: EC2 쪽만 $0.01. **범위 좁음**: 리플리카·reader 포함 클라이언트가 붙는 모든 인스턴스. Aurora 쪽 무과금은 함축. RDS Proxy 미확정 | `rate_gb` 0.01, 인스턴스 AZ 분포, `rds-proxy-cross-az` 미확정 |
| 15 | §2.2 `elasticache-cross-az` | $0.02/GB | **요율 틀림**: EC2 쪽만 $0.01/GiB. Serverless는 엔드포인트 AZ 기준 조건부. cluster mode enabled는 샤드 프라이머리 AZ 모름 | `rate_gb` 0.01, Serverless 조건 추가 |
| 16 | §2.2 `headless-cross-az` | StatefulSet 간 복제 $0.02 | 요율 맞음. 과금은 노드 AZ가 다른 peer 쌍만(조건부). zone-aware Service 기능 무효. 볼륨은 리더 → 팔로워 흐름에 좌우 | 조건·볼륨 주석 추가 |
| 17 | §2.3 `alb-ip-target-mode` | 미확정, Low | EC2 노드 pod는 EKS Best Practices와 ALB 타겟 문서로 비과금 근거 있음. Fargate는 AWS 컨테이너 블로그(ECS 기준). VPC 밖 ip 타겟은 면제 없음, IPv6는 미확정 | **§2 비과금 medium으로 이동**, 잔여 쟁점만 §4 |
| 18 | §2.3 `efs-cross-az` | 미확정, v1 범위 밖 | EFS 요금 페이지가 다른 AZ 마운트 타겟 접근의 in/out $0.01을 **명시**. 다만 실제 사용 마운트 타겟 기준이고, 고정·One Zone일 때만 발생 | **§3 과금(조건부)으로 이동**. 수집은 여전히 v1 범위 밖. 측 수만 §4 |
| 19 | §2.4 LCU | 1GB/h = 1 LCU, $0.008/LCU-h → GB당 약 $0.008 | 수치는 맞음(ap-northeast-2도 같음). 그러나 최대 차원 하나에만 과금, 바이트는 양방향 전체에 매겨짐, cross-AZ는 넘는 몫에만 → 초안의 "$0.008 vs $0.02" 비교는 절감을 과장. 기본료 $0.0225/h 수치 누락. instance 타겟이면 노드 홉 과금 | §5 차원 표·손익분기·함정 추가 |
| 20 | §2.4 권고 | `trafficDistribution: PreferClose`가 기본 | `PreferClose`는 1.35부터 deprecated 별칭. 1.34 이상은 `PreferSameZone`. kube-proxy ≥ 1.31 필요. 엔드포인트 없는 zone은 해결 못 함. headless·ip 타겟에는 무효 | §6 버전별 권고·조건 |
| 21 | §2 누락 | — | instance 타겟 NodePort 홉 과금 | `nodeport-hop-cross-az` 신설 |
| 22 | §2 누락 | — | 공인·EIP IPv4 헤어핀은 AZ 무관 과금 | `public-ipv4-in-region` 신설 |
| 23 | §2 누락 | — | ALB/CLB/NLB 클라이언트·타겟이 다른 VPC일 때 peering 규칙(TGW는 처리료) | `vpc-peering-cross-az` 신설 |
| 24 | §2 누락 | — | 다른 AZ NAT Gateway 홉 | `nat-gateway-cross-az` 신설 |
| 25 | §2 누락 | — | IPv6·Local Zone·ElastiCache 복제·비인터페이스 엔드포인트의 공백 | §4에 `alb-ipv6`, `ipv6-cross-vpc-same-az`, `local-zone-rate`, `elasticache-replication-cross-az`, `vpce-non-interface` |

§2 밖이지만 같은 사실에서 파생된 교정

- §6 규칙 예시: `alb-private-same-vpc`가 `alb-ip-target-mode`보다 먼저 선언되어 있어(첫 매치 승리) internal ALB의 IP 타겟은 IP 타겟 규칙에 도달하지 못했다. `scheme: internal` 조건은 제거 대상이다(#4). `nlb-cross-zone`의 `rate_gb: 0.02`는 맞다. `verified: "2026-09"`는 일(日) 단위로 바꿨다.
- §8 수식의 `× 2`는 이미 양쪽 합산인 `rate_gb`에 곱해져 4배가 된다(design.md B1).
- §10 IAM: [부록 B](#부록-b-iam-액션-spec-10-교정).
- §13 한계: "ALB IP 타겟 모드 과금 미확정 — Low"는 비과금 medium으로 바뀌었다. "클러스터 외부 발생분(RDS 복제, MSK)은 범위 밖" 중 복제는 무료로 확정되었고, 클라이언트 → RDS/ElastiCache/MSK는 범위 안이다.

---

## 부록 A. Cost Explorer / CUR에서 보이는 이름

| usage type | 담는 것 | 가격 | 근거 |
|---|---|---|---|
| `<prefix>-DataTransfer-Regional-Bytes` | EC2/ENI AZ 간, 공인·EIP IPv4 리전 내(같은 AZ 포함), ELB 관련 리전 전송. 계정·리전 전체, 여러 서비스 합산. EC2 AZ 간 라인의 operation은 `InterZone-In`/`InterZone-Out` | $0.01/GB | [Price List](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AWSDataTransfer/current/ap-northeast-2/index.json), [CUR 문서](https://docs.aws.amazon.com/cur/latest/userguide/cur-data-transfers-charges.html), [CUR·Athena 블로그](https://aws.amazon.com/blogs/networking-and-content-delivery/understand-aws-data-transfer-details-in-depth-from-cost-and-usage-report-using-athena-query-and-quicksight/) |
| `<prefix>-DataTransfer-xAZ-In-Bytes` / `-xAZ-Out-Bytes` | PrivateLink 인터페이스 엔드포인트, TGW, Client VPN의 AZ 간 전송(무료분 기록) | $0 | [What's New 2022-04](https://aws.amazon.com/about-aws/whats-new/2022/04/aws-data-transfer-price-reduction-privatelink-transit-gateway-client-vpn-services/), Price List |
| `<prefix>-VpcPeering-In-Bytes` / `-Out-Bytes` (AmazonVPC) | 리전 내 AZ 간 VPC peering. 2025-04-11 이전에는 Regional-Bytes에 섞여 있었음 | 각 $0.01/GB | [What's New 2025-04](https://aws.amazon.com/about-aws/whats-new/2025/04/amazon-vpc-peering-billing/), [AmazonVPC Price List](https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/AmazonVPC/current/us-east-1/index.json) |
| `Global-DataTransfer-Regional-Bytes` | 전 리전 월 1GB 무료 티어 | $0 | Price List |

- 공인 IPv4 라인을 operation `PublicIP-In`/`PublicIP-Out`으로 구분한다는 설명은 re:Post 답변에만 있다(공식 문서 아님, 미확인).
- ELB 관련 Regional-Bytes 라인의 operation 이름(`LB-PublicIP-In/Out` 또는 `LoadBalancing-PublicIP-In/Out`)은 출처가 없어 미확인이다. crossaz는 둘 다 lb-publicip로 분류하며, 실계정 [R] 실행으로 확인할 때까지 유지한다.
- `<prefix>-NatGateway-Bytes`(NAT 게이트웨이 처리 요금)는 AZ 간 전송이 아니지만 crossaz bill은 family `natgw-bytes`로 따로 보여 준다(verify 합에는 넣지 않음). 이름은 실계정에서 확인하지 않았다.
- RDS·ElastiCache 클라이언트(EC2) 측 청구가 어느 usage type·서비스로 잡히는지는 이번 조사에서 확인하지 않았다.

**접두어** — `DataTransfer-Regional-Bytes` 기준, AWSDataTransfer Price List 2026-09-11에서 추출

| 리전 | 접두어 | 리전 | 접두어 | 리전 | 접두어 |
|---|---|---|---|---|---|
| us-east-1 | (없음) | ap-south-1 | `APS3-` | eu-west-1 | `EU-` |
| us-east-2 | `USE2-` | ap-south-2 | `APS5-` | eu-west-2 | `EUW2-` |
| us-west-1 | `USW1-` | ap-southeast-1 | `APS1-` | eu-west-3 | `EUW3-` |
| us-west-2 | `USW2-` | ap-southeast-2 | `APS2-` | eu-central-1 | `EUC1-` |
| ca-central-1 | `CAN1-` | ap-southeast-3 | `APS4-` | eu-central-2 | `EUC2-` |
| ca-west-1 | `CAN2-` | ap-southeast-4 | `APS6-` | eu-north-1 | `EUN1-` |
| mx-central-1 | `MXC1-` | ap-southeast-5 | `APS7-` | eu-south-1 | `EUS1-` |
| sa-east-1 | `SAE1-` | ap-southeast-6 | `APS8-` | eu-south-2 | `EUS2-` |
| af-south-1 | `AFS1-` | ap-southeast-7 | `APS9-` | il-central-1 | `ILC1-` |
| ap-east-1 | `APE1-` | ap-northeast-1 | `APN1-` | me-central-1 | `MEC1-` |
| ap-east-2 | `APE2-` | ap-northeast-2 | `APN2-` | me-south-1 | `MES1-` |
| us-gov-east-1 | `UGE1-` | ap-northeast-3 | `APN3-` | us-gov-west-1 | `UGW1-` |
| cn-north-1 | `CNN1-` (CNY) | cn-northwest-1 | `CNW1-` (CNY) | | |

- 접두어 없음은 **`DataTransfer-Regional-Bytes`에만** 해당한다. us-east-1의 xAZ·VpcPeering usage type은 `USE1-` 접두어를 쓴다. 접두어 표로 다른 usage type을 만들지 않는다.
- 접두어는 리전 코드에서 유도되지 않는다(예: ap-south-1 = `APS3`, eu-west-1 = `EU`). 표에 없는 리전은 `GetDimensionValues(Dimension=USAGE_TYPE, SearchString="DataTransfer-Regional-Bytes")`로 찾는다.

**API 사용 규칙**

- 필터: `Filter={"Dimensions":{"Key":"USAGE_TYPE","Values":[<정확한 문자열>]}}`. GetCostAndUsage의 Dimensions `MatchOptions`는 `EQUALS`/`CASE_SENSITIVE`만 된다(접미사·포함 매칭 불가). 한 목록의 값은 OR.
- usage type group `EC2: Data Transfer - Inter AZ`는 존재하지만 EC2만 담고(RDS·S3는 별도 그룹), AWS 페이지마다 표기(대시 종류, "Amazon" 접두어)가 달라 하드코딩하면 조용히 0건이 된다. 필터로만 쓰고 문자열은 `GetDimensionValues(Dimension=USAGE_TYPE_GROUP)`로 얻는다. `GroupBy`에는 쓸 수 없다.
- `GroupBy`는 최대 2개(`SERVICE`, `OPERATION`, `USAGE_TYPE` 등). `Granularity`, `Metrics`, `TimePeriod`(End 배타) 필수. `UsageQuantity`는 usage type으로 필터했을 때만 의미가 있다.
- 엔드포인트: aws 파티션은 us-east-1(`ce.us-east-1.amazonaws.com`)뿐, 중국은 cn-northwest-1. GovCloud는 확인하지 않았다. ([Billing endpoints](https://docs.aws.amazon.com/general/latest/gr/billing.html))
- 비용: 페이지 단위 요청마다 $0.01(GetDimensionValues 포함), HOURLY는 사용 레코드당 추가. 데이터는 최소 24시간마다 갱신, 기본 13개월. 멤버 계정은 자기 비용만, 그것도 관리 계정이 허용했을 때만. ([CE 요금](https://aws.amazon.com/aws-cost-management/aws-cost-explorer/pricing/), [CE 개요](https://docs.aws.amazon.com/cost-management/latest/userguide/ce-what-is.html), [CE 접근](https://docs.aws.amazon.com/cost-management/latest/userguide/ce-access.html))
- `AccessDenied`, `DataUnavailableException`, 빈 결과는 "모름"이지 "비용 없음"이 아니다.
- **crossaz v1의 조회 방식**: 접두어 표를 하드코딩하지 않는다. `Filter={"Dimensions":{"Key":"REGION","Values":[<region>]}}` + `GroupBy=[{"Type":"DIMENSION","Key":"USAGE_TYPE"}]`로 받은 그룹 중 키가 `DataTransfer-Regional-Bytes`로 **끝나는** 것만 합산한다(EQUALS만 되는 필터 제약을 GroupBy 결과 쪽에서 우회). `xAZ-In/Out-Bytes`($0)는 제외된다. 위 접두어 표는 결과 해석용 참고다.
- 경로 단위 귀속 불가: 리소스 단위 데이터는 opt-in, 14일, 별도 API `GetCostAndUsageWithResources`이고, API 문서는 여전히 EC2-Compute 필터를 요구한다. 흐름·pod·ENI 단위 AZ 간 바이트는 어디에도 없다. CE는 계정 합계 교차 확인(상한)으로만 쓴다.
- CE GB를 흐름 바이트와 비교할 때 ÷2는 양 끝이 모두 조회 계정에서 계량될 때만 맞다. 교차 계정 peer, $0 xAZ 계량 끝, RDS/ElastiCache(한쪽만 청구)에는 맞지 않는다.

---

## 부록 B. IAM 액션 (SPEC §10 교정)

액션 이름은 [AWS Policy Generator 액션 카탈로그](https://awspolicygen.s3.amazonaws.com/js/policies.js)로 확인했다(Service Authorization Reference 액션 표는 JS 렌더링이라 읽지 못함). 필드는 botocore 서비스 모델로 확인했다. 전부 `Resource: "*"`.

"crossaz v1" 열이 **도구가 실제로 호출하는 것**이다(README의 IAM 정책과 1:1). "조사 권장"은 정밀도를 높이려면 필요한 것이고 v1은 대신 아래 근사를 쓴다.

| 액션 | 용도 | crossaz v1 | SPEC §10 대비 |
|---|---|---|---|
| `ec2:DescribeInstances` | 노드 → VPC | 사용 | 유지 |
| `ec2:DescribeSubnets` | 서브넷 → AZ, VPC | 사용 | 유지 |
| `ec2:DescribeNetworkInterfaces` | zone 없는 엔드포인트 IP → AZ (`addresses.private-ip-address` 필터). 위임 prefix·branch ENI 형태의 pod IP는 이 필터로 안 잡힐 수 있음 → 해당 엔드포인트는 zone 모름 | 사용 | 유지 |
| `ec2:DescribeAvailabilityZones` | Subnet·ENI가 이미 AZ를 줌 | 미사용 | **제거** |
| `ec2:DescribeVpcEndpoints` | 인터페이스 엔드포인트 AZ 간 전송이 무료가 되어 과금 판정에 불필요 | 미사용 | **제거** |
| `ec2:DescribeNatGateways`, `ec2:DescribeRouteTables` | `nat-gateway-cross-az`, 게이트웨이 엔드포인트 경로 확인 | 미사용 (NAT 경로는 v1 범위 밖) | 조사 권장 |
| `elasticloadbalancing:DescribeLoadBalancers`, `DescribeLoadBalancerAttributes` | LB 종류·scheme·VPC·AZ, NLB cross-zone. Classic과 v2가 같은 접두어 공유 | 사용 | 유지 |
| `elasticloadbalancing:DescribeTargetGroups`, `DescribeTargetGroupAttributes` | 타겟 타입, 타겟그룹 cross-zone 유효값 (v2 전용) | 사용 | 유지 |
| `elasticloadbalancing:DescribeTargetHealth` | 등록 타겟 목록 | 미사용 — 타겟 AZ는 EndpointSlice 엔드포인트 zone으로 대신함 | 조사 권장 |
| `rds:DescribeDBInstances` | 인스턴스 AZ, SecondaryAZ, MultiAZ, `DBClusterIdentifier`, `DBSubnetGroup.VpcId` | 사용 | 유지 |
| `rds:DescribeDBClusters` | writer/reader endpoint → 멤버 | 미사용 — Aurora는 멤버 AZ 분포 전체로 근사 (클라이언트 볼륨을 모르므로 금액은 어차피 `?`) | 조사 권장 |
| `rds:DescribeDBClusterEndpoints`, `rds:DescribeDBProxies` | Aurora custom endpoint, RDS Proxy | 미사용 | 선택 |
| `elasticache:DescribeCacheClusters` | `ShowCacheNodeInfo=true`(요청 플래그)로 노드별 `CustomerAvailabilityZone` | 사용 | 유지 |
| `elasticache:DescribeCacheSubnetGroups` | 캐시 서브넷 그룹 → `VpcId` (클러스터 VPC 밖 캐시 제외) | 사용 | **추가** |
| `elasticache:DescribeReplicationGroups` | cluster mode disabled 그룹의 primary/reader endpoint → 노드 | 미사용 — 그룹 노드 AZ 분포 전체로 근사 | 조사 권장 |
| `elasticache:DescribeServerlessCaches` | Serverless 캐시 서브넷 | 미사용 (Serverless는 v1 범위 밖) | 선택 |
| `kafka:ListClustersV2` | Provisioned만 나열(`clusterTypeFilter=PROVISIONED`) | 사용 | **`kafka:ListClusters` 대체** |
| `kafka:ListNodes` | 브로커별 `clientSubnet` | 미사용 — MSK는 브로커 수가 클라이언트 서브넷 수의 배수여야 하므로 서브넷에 균등 분배로 근사 | 조사 권장 |
| `ce:GetCostAndUsage` | `--verify` | 사용 (페이지당 $0.01) | 유지 |
| `ce:GetDimensionValues` | usage type·group 정확한 문자열 조회 | 미사용 — 부록 A의 GroupBy 방식으로 대체 | 선택 |

- `ce:GetCostAndUsageWithResources`는 경로 귀속에 쓸모가 없어 넣지 않는다.
