# crossaz

EKS(VPC CNI) 클러스터에서 **실제로 청구되는** cross-AZ 트래픽 경로만 골라내고, 월 비용을 추정하고, 조치를 제시하는 read-only CLI.

대부분의 도구는 "AZ를 넘으면 빨간색"으로 칠합니다. 그래서 ALB 뒤 서비스(사설 IP·동일 VPC 전송은 무료)를 누수로 잘못 잡고, 반대로 엔드포인트가 한 AZ에 몰린 서비스(다른 AZ 호출자는 전부 과금)는 놓칩니다. crossaz가 다른 점은 측정 정밀도가 아니라 **과금 판정**입니다. 경로마다 규칙([`rules/rules.yaml`](rules/rules.yaml))으로 과금 여부를 가리고, 판정마다 AWS 공식 문서 근거 URL을 붙입니다.

---

## 필요 권한

**쓰기 API는 어떤 경로로도 호출하지 않습니다 — SDK 클라이언트를 Describe/List/Get만 가진 인터페이스로 감싸 컴파일 타임에 강제하고, 테스트가 이를 검사합니다 ([`internal/collect/readonly.go`](internal/collect/readonly.go)).**

### AWS IAM (L1, 선택)

자격증명이 없거나 `--no-aws`면 L1 없이 실행됩니다. `elasticloadbalancing:DescribeLoadBalancers`는 ALB/NLB와 Classic LB를 모두 커버합니다.

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

`--verify`(L3)를 쓸 때만 아래 권한을 추가합니다. **Cost Explorer API는 요청(페이지)당 $0.01이 청구됩니다.**

```json
{
  "Sid": "CrossAZVerify",
  "Effect": "Allow",
  "Action": ["ce:GetCostAndUsage"],
  "Resource": "*"
}
```

### Kubernetes RBAC (L0, 필수)

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

서버 버전 조회(`/version`)는 인증된 모든 사용자에게 기본 허용(`system:public-info-viewer`)이라 별도 권한이 필요 없습니다.
EKS access entry의 Kubernetes 그룹에 묶으려면: `kubectl create clusterrolebinding crossaz-reader --clusterrole=crossaz-reader --group=<group>`.

---

## 설치

```sh
# Homebrew
brew install --cask leegun2000/tap/crossaz

# kubectl 플러그인 (krew-index 등록 후)
kubectl krew install crossaz

# Go
go install github.com/leegun2000/crossaz/cmd/crossaz@latest
```

---

## 사용법

```
crossaz scan    [flags]
crossaz explain <ns/name | kind/ref> [flags]
crossaz rules   [--rules file]
crossaz version
```

| 플래그 | 기본값 | 설명 |
|---|---|---|
| `--kubeconfig` | `$KUBECONFIG` / `~/.kube/config` | kubeconfig 경로 |
| `--context` | 현재 컨텍스트 | kubeconfig 컨텍스트 |
| `--cluster` | 컨텍스트의 cluster (EKS ARN이면 `/` 뒤) | 출력에 쓸 클러스터 이름 |
| `--region` | 노드 `topology.kubernetes.io/region` → AWS 설정 | 리전 |
| `--rules` | 내장 [`rules/rules.yaml`](rules/rules.yaml) | 규칙 파일로 **통째로** 교체 |
| `--rate-gb` | 규칙·리전 요율 | 모든 과금 경로의 USD/GB(양쪽 합산)를 강제 |
| `--threshold` | `1` | (scan) 월 추정액이 이 값(USD) 미만인 과금 경로는 숨기고 개수만 표시 |
| `--json` | | (scan) `ScanResult` 전체를 JSON으로 출력 |
| `--prometheus-url` | | L2 볼륨 추정 (예: `http://localhost:9090`). basic auth 자격증명의 특수문자는 퍼센트 인코딩(`https://user:p%40ss@host`). 파싱할 수 없으면 `prometheus: base URL is invalid (percent-encode credentials)`로 실패하고, 어떤 오류·경고에도 사용자 이름·비밀번호를 남기지 않음 |
| `--prom-window` | `7d` | PromQL `rate` 윈도 |
| `--verify` | | (scan 전용) L3: 전월 Cost Explorer 실제 비용과 대조. L1(AWS 인벤토리)을 얻지 못하면(자격증명 없음, 다른 계정 자격증명 등) 호출하지 않고 경고 |
| `--no-aws` | | L1 끄기 (Kubernetes만 사용) |
| `--fixture <file>` | | 클러스터 대신 스냅샷 JSON을 입력으로 사용. `--prometheus-url`/`--verify`는 무시하고 경고 |
| `--dump-fixture` | | (scan) 수집한 스냅샷을 익명화해 JSON으로 출력하고 종료 |
| `--timeout` | `2m` | 전체 수집 제한 시간 |

```sh
crossaz scan                                    # L0 + L1 (자격증명 있으면)
crossaz scan --prometheus-url http://localhost:9090 --verify
crossaz explain payments/api                    # 판정 근거: facts, 매칭 규칙, 근거 URL, 조치 명령
crossaz explain rds/orders-db                   # 서비스 밖 대상은 kind/ref
crossaz rules                                   # 적용 중인 규칙 표
```

kubectl 플러그인으로도 같습니다. 플래그는 플러그인 이름 **뒤에** 둡니다.

```sh
kubectl -n monitoring port-forward svc/prometheus-server 9090:80 &
kubectl crossaz scan --context prod-a --prometheus-url http://localhost:9090
```

### 출력 예

[`testdata/fixtures/prod-a.json`](testdata/fixtures/prod-a.json)(L0~L3 모두 있는 3 AZ 클러스터)으로 실행한 실제 출력입니다. 같은 내용이 골든 파일 [`testdata/golden/prod-a.txt`](testdata/golden/prod-a.txt)로 테스트됩니다.

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
  Actual:     $171/mo   (Cost Explorer APN2-DataTransfer-Regional-Bytes, 2026-08 — 계정·리전 전체)
  Ratio:      1.07   제외 규칙이 대체로 맞음

  Ratio > 1.5 → 제외 규칙 누락 의심
  Ratio < 0.5 → 클러스터 밖(RDS/MSK/NLB/다른 워크로드) 누수 의심

WARNINGS
  - 추정 총액에 볼륨을 측정하지 못한 과금 경로 2개가 빠져 있음
```

읽는 법:

- `cache/redis`: 엔드포인트가 3개 AZ 중 1개에만 있습니다. 나머지 두 AZ의 호출자는 항상 AZ를 넘으므로 **과금**이고, `trafficDistribution`만으로는 해결되지 않아 조치가 `topologySpread`입니다.
- `shop/storefront`: ALB instance 타겟 + `externalTrafficPolicy: Cluster`. ALB→노드는 무료지만 kube-proxy가 다른 AZ pod로 한 번 더 보내는 홉이 과금됩니다.
- `api/internal`: 같은 ALB instance 타겟이라도 `externalTrafficPolicy: Local`이면 홉이 없어 비과금(`high`)입니다.
- `ingress/web`: ALB IP 타겟 모드는 비과금(`medium`)으로 판정하지만 요금 문구에 공백이 있어 `--verify` 대조를 권장합니다.
- `orders/api`: 모든 엔드포인트에 같은 AZ 힌트가 있어(`trafficDistribution: PreferClose`) kube-proxy가 zone-local로 보냅니다. 비과금이어도 L2가 있으면 수신량을 보여줍니다.
- `rds/orders-db`: 복제는 무료, 클라이언트 경로는 과금 규칙이지만 클러스터가 실제로 통신하는지·볼륨을 모르므로 `low`, 금액 `?`입니다.
- `monitoring/node-exporter`: hostNetwork pod는 노드 전체 트래픽이 잡히므로 미측정으로 두고, 측정된 pod가 없어 `low`입니다.
- `grpc/ledger-api`: cross-zone을 끈 내부 NLB. 클라이언트가 DNS로 다른 AZ의 NLB 노드를 받으면 과금되지만 DNS 라우팅에 좌우돼 `low`이므로 수신량만 보여주고 금액은 비웁니다.
- `WARNINGS`의 "볼륨을 측정하지 못한 과금 경로 2개": `default/kubernetes`(pod 없는 엔드포인트)와 `monitoring/node-exporter`는 과금 규칙에 걸리지만 볼륨을 재지 못해 UNCERTAIN으로 갔고, 그만큼 총액이 빠졌다는 뜻입니다.
- AZ SPREAD = `목적지 AZ 수/호출자 AZ 수`, 목적지 AZ가 2개 이상이고 고르게 퍼져 있으면 `even`.
- L2가 없으면 금액은 `?`, 총액은 `Estimated total: ? (L2 없음 — --prometheus-url)`로 나옵니다([`testdata/golden/l0-only.txt`](testdata/golden/l0-only.txt)). 이 골든에는 EKS Auto Mode ALB(`marketing/site`, ip 타겟 기본), ingress-nginx가 pod IP로 직접 붙는 zone-local 서비스(`admin/console`, `ingress-direct-backend`), 힌트를 설정했지만 확인되지 않은 서비스(`notify/worker`, `hints-unverified`), 비사설 IPv4 ALB 타겟(`partners/webhook`, `lb-exemption-unverified`), 판정 보류(`edge/relay`, `unclassified`) 예도 있습니다.

판정 근거는 `explain`으로 봅니다. 수집·분석 경고는 `crossaz: warning:`으로 stderr에 먼저 나옵니다(설명은 stdout).

```
$ crossaz explain cache/redis --fixture testdata/fixtures/prod-a.json
crossaz: warning: 추정 총액에 볼륨을 측정하지 못한 과금 경로 2개가 빠져 있음
cache/redis  (service)
  route:       clusterip  (evidence: k8s)
  callers:     cluster/*  ap-northeast-2a=8 ap-northeast-2b=8 ap-northeast-2c=8
  destination: ap-northeast-2a=3
  facts:       az_spread=multi zone_coverage=partial topology_hints=false hints_configured=false zone_local=false direct_ingress_backend=false same_vpc=true private_ip=true traffic=client
  rule:        clusterip-cross-az-partial  (billable: true, rule confidence: high)
  reason:      엔드포인트가 없는 AZ의 호출자는 항상 다른 AZ로 넘어감 (trafficDistribution으로도 해결 안 됨)
  source:      https://docs.aws.amazon.com/eks/latest/best-practices/cost-opt-networking.html  (verified 2026-09-14)
  estimate:    recv 2.1 TB/mo × cross-AZ 0.667 × $0.02/GB = ~$28
  confidence:  medium
  fix:         topologySpread — 모든 AZ에 엔드포인트가 생기도록 topologySpreadConstraints(topology.kubernetes.io/zone) 추가 후 trafficDistribution 설정
```

단일 AZ이고 과금 경로가 없으면 두 줄로 끝납니다.

```
Cluster: dev-b   AZs: 1   Nodes: 3
Exposure: NONE — 모든 노드가 단일 AZ에 있고 과금 경로가 없습니다.
```

---

## 레이어

각 레이어는 독립적으로 동작하고, 상위 레이어 없이도 결과를 냅니다.

| 레이어 | 필요한 것 | 산출 | 조건 |
|---|---|---|---|
| **L0 토폴로지** | Kubernetes API (위 ClusterRole) | 노드 AZ 분포, 서비스 라우트(ALB Ingress·LB annotation·ClusterIP·headless), EndpointSlice AZ 분포·힌트, 과금 판정 | 항상 |
| **L1 AWS 확인** | AWS 자격증명 + 위 IAM 정책 | LB 실제 scheme·cross-zone·target type·AZ, zone 없는 엔드포인트 IP→AZ(클러스터 VPC의 ENI만), RDS/ElastiCache/MSK 경로 | 자격증명이 있고 `--no-aws`가 아닐 때. 일부 LB 조회 실패는 그 LB만 빼고(annotation 근거로 폴백) 경고 |
| **L2 볼륨 추정** | Prometheus (cAdvisor `container_network_receive_bytes_total`) | 월 수신 바이트 → 금액 | `--prometheus-url`. 결과에 이 클러스터의 엔드포인트 pod가 하나도 없으면 L2 없음으로 처리하고 경고 |
| **L3 자가 검증** | `ce:GetCostAndUsage` | 추정 대비 실제 비율 | `--verify` + L1 |
| L4 실측 | VPC Flow Logs | 정밀 금액 | v1 범위 밖 |

### CONF와 섹션

- **CONF = min(규칙, 근거, 볼륨)**
  - 규칙: 매칭된 규칙의 `confidence`
  - 근거: `k8s`(ClusterIP·headless)·`aws`(L1로 확인) → high, `annotation`(L1 없이 annotation·기본값으로 추정) → medium
  - 볼륨: L2 추정이 들어가면 최대 medium. 엔드포인트 pod 메트릭이 하나도 없으면 low, 금액 `?`
  - 클러스터 밖 대상(RDS/ElastiCache/MSK)이 과금 경로면 low, 금액 `?`
  - 과금 경로의 엔드포인트 AZ를 하나도 모르면 L2 유무와 무관하게 low, 금액 `?`
  - Local/Wavelength Zone에 닿는 과금 경로는 요율이 달라 low, 금액 `?`(수신량은 표시)
- **섹션**
  - `UNCERTAIN`: CONF가 low인 경로 (과금 여부와 무관)
  - `NOT BILLABLE`: 비과금이고 low가 아닌 경로
  - `BILLABLE`: 나머지. 금액이 알려졌고 `--threshold` 미만이면 숨기고 개수만 표시(총액에는 포함)
  - 금액 내림차순(모르는 금액은 뒤), 나머지 섹션은 키 순. 비어 있는 섹션은 생략
- **Exposure**
  - `LIKELY`: BILLABLE 경로가 있음(숨김 포함), 또는 규칙은 확실한데 추정 단계에서 UNCERTAIN으로 내려간 서비스 과금 경로가 있음(메트릭 없음·AZ 모름·Local Zone)
  - `NONE`: 노드 AZ가 1개 이하이고 UNCERTAIN에도 과금 경로가 없음
  - `UNLIKELY`: 그 외
- **Estimated total**: L2가 있을 때 BILLABLE 경로(숨김 포함)의 알려진 금액 합. 위처럼 내려간 서비스 과금 경로 중 볼륨을 못 잰 것이 있으면 `WARNINGS`에 "추정 총액에 볼륨을 측정하지 못한 과금 경로 N개가 빠져 있음"

---

## 판정 규칙

과금 사실과 근거는 [`docs/billing-rules.md`](docs/billing-rules.md), 적용 중인 규칙 원본은 [`rules/rules.yaml`](rules/rules.yaml)입니다.

규칙은 **선언 순서대로 평가하고 첫 매치가 이깁니다.** 마지막 규칙은 조건이 비어 있는 catch-all이어야 합니다.

| 규칙 ID | 판정 | 규칙 CONF | 요율 | 조치 |
|---|---|---|---|---|
| `same-az` | 비과금 | high | 리전 | |
| `zone-unknown` | 비과금 | low | 리전 | |
| `nlb-cross-zone` | **과금** | high | 리전 | `disable-nlb-xz` |
| `nodeport-hop-cross-az` (ALB/CLB/NLB instance 타겟 + `externalTrafficPolicy: Cluster`, zone-local 아님) | **과금** | medium | 리전 | `PreferClose` |
| `nlb-cross-zone-unknown` | **과금** | low | 리전 | |
| `ingress-direct-backend` (zone-local ClusterIP인데 ALB가 아닌 Ingress가 pod IP로 직접 연결) | 비과금 | low | 리전 | |
| `topology-aware-routing` (clusterip/alb/clb) | 비과금 | medium | 리전 | |
| `alb-ip-target-mode` | 비과금 | medium | 리전 | |
| `alb-private-same-vpc`, `clb-private-same-vpc` | 비과금 | high | 리전 | |
| `lb-exemption-unverified` (IPv6·비사설 IPv4 타겟, 다른 VPC) | **과금** | low | 리전 | |
| `nlb-client-cross-az` | **과금** | low | 리전 | `zonal-dns` |
| `nlb-zonal-internet` | 비과금 | medium | 리전 | |
| `clusterip-cross-az-partial` | **과금** | high | 리전 | `topologySpread` |
| `hints-unverified` (trafficDistribution/topology-mode 설정, 힌트 사용 미확인. 엔드포인트가 모든 AZ에 있을 때만 — 없는 AZ가 있으면 위 규칙이 먼저 과금 확정) | **과금** | low | 리전 | |
| `clusterip-cross-az` | **과금** | high | 리전 | `PreferClose` |
| `headless-cross-az` | **과금** | high | 리전 | `relocate` |
| `rds-multiaz-replication`, `msk-inter-broker` | 비과금 | high | 리전 | |
| `rds-cross-az`, `elasticache-cross-az` (EC2 쪽만) | **과금** | high (최종 low) | $0.01 | `relocate` |
| `msk-client-cross-az` (공식 확인된 클라이언트 쪽만 — 하한) | **과금** | medium (최종 low) | $0.01 | `relocate` |
| `unclassified` (catch-all) | 비과금 | low | 리전 | |

- 요율 `리전` = 규칙에 `rate_gb`가 없어 리전 요율을 씁니다(내장 36개 리전 모두 $0.02/GB, 양쪽 합산). 비과금 규칙에는 적용되지 않습니다.
- EKS Auto Mode: IngressClass controller `eks.amazonaws.com/alb`인 ALB와 `loadBalancerClass: eks.amazonaws.com/nlb`인 NLB는 target-type annotation이 없으면 ip 타겟으로 봅니다. ALB 판정은 셀프 관리 LBC(`ingress.k8s.aws/alb`)와 Auto Mode 컨트롤러를 모두 인식하고, IngressClass 이름이 아니라 controller로 가립니다(`alb`라는 이름은 그 IngressClass 객체가 없을 때만 ALB로 봄).
- `PreferClose` 조치 명령은 컨트롤 플레인이 1.34 이상이면 같은 의미의 `PreferSameZone`으로 렌더링됩니다.
- VPC 인터페이스 엔드포인트의 AZ 간 전송은 2022-04-01부터 무료라 판정 대상이 아닙니다.

### 규칙 바꾸기

```sh
cp rules/rules.yaml my-rules.yaml               # 원본에서 시작
crossaz rules --rules my-rules.yaml             # 검증 + 규칙 표 확인
crossaz scan --rules my-rules.yaml
```

`--rules` 파일은 내장 규칙과 병합하지 않고 **통째로 대체**합니다. 로드 시 검증합니다: id 유일, `confidence` ∈ {high, medium, low}, 마지막 규칙이 catch-all, `default_rate_gb > 0`, 조치 명령 템플릿 파싱 가능, match 값이 사실이 가질 수 있는 값([`rules.yaml`](rules/rules.yaml) 머리 주석의 목록, 대소문자 구분). 표에 표시용으로 줄여 쓴 `nlb-xz` 같은 값은 거부됩니다.

---

## 추정 방식

```
recv_bytes/mo = Σ (엔드포인트 pod 수신 bytes/s ÷ 그 pod가 속한 서비스 수)
                × (전체 pod 수 / 측정된 pod 수) × 2,592,000        # 30일
월 비용       = recv_bytes/mo / 1e9 × CrossAZFraction × rate_gb
```

**`× 2`는 없습니다.** `rate_gb: 0.02`가 이미 양쪽($0.01/GB씩)을 합친 값입니다. 상용·GovCloud 36개 리전의 inter-AZ 요율은 모두 측당 $0.01입니다.

```
CrossAZFraction = 1 − Σ_z (1 / 호출자 AZ 수) × 목적지 비중(z)
```

- 호출자 3 AZ, 엔드포인트 3 AZ 균등 → `1 − 1/3 = 0.667`
- 호출자 3 AZ, 엔드포인트가 한 AZ에만 → 역시 `0.667` (0이 아님: 다른 두 AZ 호출자는 전부 넘어감)
- `zone_local`(힌트가 모든 호출자 AZ를 덮음, 또는 `internalTrafficPolicy: Local`) → `0`. AZ를 넘는 다리가 kube-proxy 홉뿐인 `clusterip`·`alb`·`clb`·`nlb-zonal` 경로에만 적용
- 힌트는 있지만 일부 호출자 AZ를 못 덮음 → `엔드포인트 없는 호출자 AZ 수 / 호출자 AZ 수` (위 경로에만)
- `nlb-cross-zone`·`nlb`·headless·외부 대상은 힌트와 무관하게 위 기본 식. 힌트는 LB 노드 → 타겟 다리를 없애지 못합니다
- 호출자 분포: ClusterIP/headless는 노드 AZ, LB 라우트는 LB AZ(L1) 또는 노드 AZ

Prometheus 쿼리 (`WINDOW` = `--prom-window`, 기본 `7d`):

```promql
sum by (namespace, pod) (max by (namespace, pod, interface) (rate(container_network_receive_bytes_total{pod!=""}[WINDOW])))
```

`max by (…, interface)`는 런타임에 따라 컨테이너별로 중복되는 시계열을 제거합니다. 현재 ready 엔드포인트 pod만 매칭하므로 롤링 배포로 사라진 pod의 트래픽은 빠집니다.

가정:

1. 호출자는 노드(또는 LB) AZ에 **균등** 분포하고, 요청은 엔드포인트 수에 비례해 분배된다.
2. 수신 바이트만 센다. 모든 바이트는 누군가 한 번 수신하므로 총합은 맞지만, 응답 바이트는 호출자 서비스로 귀속된다.
3. hostNetwork pod(노드 전체 트래픽이 잡힘)와 메트릭 없는 pod는 미측정으로 두고, 측정된 pod 비율로 외삽한다(`k/n pods measured`).
4. 수신 바이트에는 클러스터 밖 유입이 섞인다. ALB 뒤 경로처럼 무료인 경우는 규칙이 비과금으로 걸러낸다.

목적은 자릿수 판별입니다. $0.06과 $800을 구분하면 충분합니다.

---

## 한계

- **추정치이며 실측이 아닙니다.** `--verify`로 교차 확인을 권장합니다.
- 클러스터 밖에서 시작하는 트래픽(다른 워크로드·EC2·Lambda 등)은 범위 밖입니다. RDS/ElastiCache/MSK는 경로와 AZ 분포만 보고, 클러스터와 실제 통신하는지·볼륨은 모르므로 `low`, 금액 `?`입니다.
- **AWS VPC CNI + kube-proxy**를 전제합니다. Cilium kube-proxy replacement, EKS Auto Mode의 힌트 동작은 미검증이라 kube-proxy DaemonSet이 없으면(또는 `get daemonsets/kube-proxy` 권한이 없으면) zone-local을 인정하지 않습니다. `trafficDistribution`/`topology-mode`를 설정한 서비스는 `hints-unverified`(과금 가능, low)로, 설정하지 않은 서비스는 과금으로 판정합니다. 실측이 필요하면 [kubezonnet](https://github.com/polarsignals/kubezonnet)을 권장합니다.
- pod IP로 직접 연결하는 Ingress 컨트롤러(ingress-nginx 기본값 등)는 kube-proxy 힌트를 우회합니다. 그런 Ingress가 가리키는 zone-local 서비스는 `ingress-direct-backend`(low)로 두며, 컨트롤러 자체의 zone-aware 설정은 읽지 않습니다. `nginx.ingress.kubernetes.io/service-upstream: "true"`면 ClusterIP를 거치므로 zone-local을 인정합니다.
- ALB IP 타겟 모드는 EKS Best Practices를 근거로 비과금(`medium`)으로 판정하지만, EC2 요금 문구가 "EC2 인스턴스"만 명시하므로 여전히 `--verify` 대조 가치가 있습니다.
- 호출자 AZ 분포를 균등으로 가정합니다. 호출자가 한 AZ에 몰린 워크로드에서는 오차가 커집니다.
- 요율은 상용·GovCloud 36개 리전만 내장합니다. 그 외(중국 리전 등)는 기본값과 경고가 나오므로 `--rate-gb`로 지정하세요. Local/Wavelength Zone 노드가 있으면 경고하고, 그 zone에 닿는 과금 경로는 요율이 달라 금액을 비워 UNCERTAIN으로 보냅니다(총액 제외, 수신량은 표시).
- 공인 IP 헤어핀(pod가 internet-facing LB 호스트명을 호출)은 AZ와 무관하게 과금되지만 Flow Logs 없이는 탐지할 수 없습니다.
- 서비스당 경로는 1개입니다. ALB Ingress가 가리키는 서비스는 `alb`, type=LoadBalancer 서비스는 NLB/CLB 경로로만 판정하며, 같은 서비스로 들어오는 클러스터 내부 ClusterIP 트래픽은 분리하지 않습니다(수신량에는 포함).
- ALB Ingress 백엔드는 일반 서비스 백엔드와 `use-annotation` 액션(`alb.ingress.kubernetes.io/actions.<name>`의 `forwardConfig.targetGroups[].serviceName`, 레거시 `serviceName`)을 인식합니다. 가중치 forward·blue/green·Argo Rollouts canary가 여기에 해당합니다. 액션 JSON이 깨졌거나 타겟그룹 ARN만 있으면 서비스를 연결하지 못합니다.
- 노드가 한 AZ에만 있고 ALB/CLB/cross-zone off NLB가 여러 AZ에 걸쳐 있어도 `same-az`로 봅니다. ALB/CLB → 노드는 무료이고 zonal NLB는 타겟 없는 AZ를 DNS에서 빼기 때문입니다. cross-zone이 켜졌거나 모르는 NLB만 L1에서 확인한 LB AZ를 호출자에 합칩니다(L1이 없으면 LB AZ를 모르므로 노드 AZ 기준).
- `--verify`의 Actual은 **계정·리전 전체**의 inter-AZ 전송 비용(`DataTransfer-Regional-Bytes`)입니다. ELB·공인 IP·다른 클러스터·EC2가 모두 포함된 상한이므로 Ratio가 1보다 작게 나오는 것이 자연스럽습니다. L1이 없으면(자격증명 없음, 노드를 찾지 못한 다른 계정 자격증명 등) 다른 계정의 비율을 보여주지 않도록 L3를 건너뜁니다.
- Prometheus 쿼리에 cluster 라벨 범위가 없습니다. 여러 클러스터를 담은 Prometheus(Thanos, AMP 등)면 같은 `namespace/pod` 이름이 다른 클러스터 값과 섞일 수 있습니다. 이 클러스터 pod가 결과에 **하나도** 없을 때만 경고하고 L2를 버리므로, 일부만 겹치는 혼입은 잡지 못합니다. 클러스터 하나로 범위가 좁혀진 엔드포인트를 쓰세요.
- 응답 바이트는 호출자 서비스의 수신으로 귀속됩니다.
- VPC CNI prefix delegation 환경에서 zone 없는 엔드포인트 IP는 ENI 조회로 AZ를 못 찾을 수 있음 (pod IP가 ENI의 개별 IP가 아니라 위임된 prefix 안에 있어 `addresses.private-ip-address` 필터에 걸리지 않음). EndpointSlice에 zone이 있는 엔드포인트(대부분)는 영향이 없습니다. ENI 조회는 클러스터 VPC 안으로 한정하고, ENI들이 서로 다른 AZ를 보고하는 IP는 AZ 모름으로 둡니다.

---

## 개발

```sh
go test -race ./...
go test ./internal/scan -update                          # 골든 파일(testdata/golden) 갱신
crossaz scan --dump-fixture > testdata/fixtures/x.json   # 익명화된 스냅샷
crossaz scan --fixture testdata/fixtures/x.json          # 클러스터 없이 재현
```

`--dump-fixture`는 IP·노드 이름·인스턴스 ID·VPC/서브넷 ID·LB와 타겟그룹 이름/DNS를 sha256 기반으로 결정적으로 치환합니다(조인 보존, 사설/공인 IPv4·IPv6 구분 유지). 판정에 쓰지 않는 annotation은 버리고(LB/TG 속성 annotation은 cross-zone 값만 남김), 경고는 치환한 값을 바꾼 뒤 `<레이어>: <대상>: <redacted>`까지만 남겨 AWS/Kubernetes/HTTP 에러 원문(계정 ID, principal ARN, 이메일, 홈 경로 등)을 지웁니다. **클러스터 이름, 네임스페이스·서비스·pod·Ingress·owner 이름, RDS/ElastiCache/MSK 식별자, Cost Explorer `actualUsd`는 그대로 남으므로** 커밋 전에 확인하세요.

설계와 패키지 계약: [`docs/design.md`](docs/design.md). 과금 규칙 근거: [`docs/billing-rules.md`](docs/billing-rules.md).
