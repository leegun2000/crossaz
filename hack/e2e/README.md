# crossaz 로컬 e2e (kind)

실제 Kubernetes(kind, v1.35)에 시나리오를 띄우고 crossaz를 돌려 판정·kube-proxy 실측·Prometheus 볼륨을 확인합니다.
AWS 자격증명은 쓰지 않습니다(`--no-aws`).

## 준비물

- Docker(OrbStack/Docker Desktop, 여유 메모리 약 3 GB), Go(go.mod 버전), `kubectl`, `helm`, `jq`
- kind는 설치하지 않아도 됩니다: 스크립트가 `go run sigs.k8s.io/kind@v0.33.0`으로 실행
- 이미지는 공식 이미지만 digest로 고정: `registry.k8s.io/pause`, `registry.k8s.io/e2e-test-images/agnhost`, `docker.io/library/busybox`, `quay.io/prometheus/prometheus`(차트 `prometheus-community/prometheus` 29.30.0)

## 실행

```sh
hack/e2e/run.sh              # all: up → l2 → test → down (KEEP_CLUSTER=1이면 남김, 테스트 실패 시에도 남김)
hack/e2e/run.sh up           # 클러스터(없으면 생성) + scenarios/traffic 적용, 롤아웃·힌트·클라이언트 완료 대기
hack/e2e/run.sh l2           # Prometheus 설치 + volume 생성기, sink pod의 cAdvisor 시계열 대기(없으면 실패)
hack/e2e/run.sh test         # go test -tags e2e ./hack/e2e/ (Prometheus 릴리스가 있으면 E2E_L2=1)
hack/e2e/run.sh down         # 클러스터 삭제
```

- kind·kubectl·helm·테스트는 모두 `hack/e2e/.kubeconfig`(gitignore)만 씁니다. `~/.kube/config`와 current-context는 건드리지 않습니다. 직접 보려면 `KUBECONFIG=hack/e2e/.kubeconfig kubectl --context kind-crossaz-e2e ...`.
- 모든 kubectl/helm 호출은 `--context kind-crossaz-e2e`(helm은 `--kube-context`)를 명시하고, 그 컨텍스트의 API 서버가 kind 클러스터와 다르면 실행을 거부합니다. `KIND_CLUSTER_NAME`은 `--name`으로, helm의 `HELM_KUBEAPISERVER` 등 서버·자격증명 환경변수는 `unset`으로 무시합니다.
- `go test ./...`(태그 없음)는 이 패키지를 빌드하지 않습니다.

## 검증하는 것

| 테스트 | 내용 |
|---|---|
| `TestScanVerdicts` | L0 판정 15개(규칙 11종): 키별 ruleId·billable, AZ 3개(zone 없는 control-plane 제외), Layers `[L0]`, payments/api 조치의 `PreferSameZone`. 기대 표 자체도 rules.yaml과 대조 |
| `TestDumpFixtureRoundTrip` | `--dump-fixture` → `--fixture` 판정이 라이브와 같음, 덤프에 라이브 노드 이름·pod IP가 없음 |
| `TestUnknownKubeProxyVersion` | 라이브 덤프에서 kube-proxy 버전만 지워(대체 프록시·권한 없음) 재생: 힌트를 설정한 서비스는 `hints-unverified`, 설정하지 않은 서비스는 그대로 과금 |
| `TestKubeProxyCrossZoneFraction` | zone마다 클라이언트 1개가 서비스당 400회 새 연결. zone별 실측 cross-zone 비율을 crossaz가 수집한 엔드포인트·힌트로 계산한 그 zone의 예측과 비교(4σ, kube-proxy에 선택지가 없는 0/1은 정확히 일치). 서비스 전체 실측은 crossaz `crossAzFraction`의 ±0.05, `crossAzFraction`은 모델값 0.667/0.667/0(±0.01) |
| `TestPrometheusVolumeEstimate` | 약 6 MB/s 생성기 → sink. **기본 `--prom-window`(7d)**로 crossaz `bytesPerMo`가 생성기 로그의 실제 속도 × 2,592,000의 ±15%, 규칙 `clusterip-cross-az-partial`·비율 2/3·요율 $0.02·confidence medium, `costPerMo` = bytesPerMo/1e9 × 비율 × 요율. cAdvisor 수집 job이 없거나 pod 라벨이 없으면 건너뛰지 않고 실패 |

- `notify/worker`(`hints-withheld`)는 topology-mode Auto에 엔드포인트 4개(2/1/1)라 컨트롤러가 과부하 기준으로 힌트를 주지 않고, kube-proxy(v1.35)가 모든 엔드포인트로 분산하는 실제 동작입니다.
- sink는 새로 뜬 pod이고 Prometheus 보존기간은 1d라, 7d 창에서 pod가 존재한 구간만 평균하는지를 봅니다. crossaz는 5분 전에도 샘플이 있던 5분 스텝만 세므로 sink가 뜬 뒤 5~10분이 지나야 측정되고, 테스트는 그때까지(최대 12분) 기다립니다.

## AWS가 있어야 하는 것 (여기서는 검증 안 함)

- L1: 실제 ALB/NLB scheme·cross-zone·타겟 타입, ENI 기반 IP→AZ, RDS/ElastiCache/MSK 경로 (LB는 컨트롤러가 없어 pending 상태로 annotation만 판정)
- L3: Cost Explorer `--verify`
- VPC CNI(여기는 kindnet)·EKS Auto Mode 동작, 실제 inter-AZ 과금 금액

## 시간·자원

- 처음 실행 약 10분, 최대 약 15분(측정: up 1분 50초(클러스터 생성 포함), l2 1분 16초, test 6분 29초): test는 Prometheus와 sink가 5~10분 된 뒤 L2를 확인(L0 테스트는 몇 초)
- kind 노드 7개 합계 메모리 약 2–3 GB

## 정리

```sh
hack/e2e/run.sh down
```
