package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/leegun2000/crossaz/internal/collect"
	"github.com/leegun2000/crossaz/internal/model"
)

const (
	credentialsTimeout   = 10 * time.Second
	warnNoAWSCredentials = "L1: AWS 자격증명을 찾지 못해 L0만 실행"
	// Without L1 the credentials may belong to another account, so Cost Explorer would bill and report the wrong one.
	warnVerifyWithoutL1          = "L3: L1(AWS 인벤토리)을 얻지 못해 --verify 건너뜀"
	warnFixtureIgnoresCollection = "--fixture 사용 중: --prometheus-url/--verify는 무시됨 (fixture에 포함된 레이어만 사용)"
	// verifyDays is the closed-day window scan --verify compares against.
	verifyDays = 14
)

// sources are the live data sources. Tests substitute fakes; fixtures need none.
type sources struct {
	// kube returns a collector and the cluster name of the selected kubeconfig context.
	kube func(kubeconfig, kubeContext string) (collect.K8sCollector, string, error)
	// aws loads AWS config and verifies credentials; region "" lets the AWS config decide.
	aws func(ctx context.Context, region string) (awsSession, error)
	now func() time.Time
}

type awsSession struct {
	region    string
	inventory collect.AWSCollector
	bill      collect.BillCollector
}

func liveSources() sources {
	return sources{kube: liveKube, aws: liveAWS, now: time.Now}
}

// loadSnapshot reads the fixture, or collects L0 and whichever of L1-L3 the options ask for.
func loadSnapshot(ctx context.Context, o options, src sources) (model.Snapshot, error) {
	if o.fixture != "" {
		s, err := readFixture(o.fixture)
		if err != nil {
			return model.Snapshot{}, err
		}
		return withFixtureWarning(withOverrides(s, o), o), nil
	}
	s, sess, err := collectL0L1(ctx, o, src)
	if err != nil {
		return model.Snapshot{}, err
	}
	if o.promURL != "" {
		matchers, err := collect.ParsePromSelector(o.promSelector) // already validated with the flags
		if err != nil {
			return model.Snapshot{}, fmt.Errorf("--prom-selector: %w", err)
		}
		prom := collect.NewPrometheus(o.promURL, matchers, http.DefaultClient)
		s = collect.WithVolume(ctx, s, prom, o.promWindow, o.promSelector)
	}
	if o.verify {
		s = withVerify(ctx, s, sess, src)
	}
	return s, nil
}

// collectL0L1 collects L0 and, unless --no-aws, L1. The session is nil when AWS is skipped or unusable.
func collectL0L1(ctx context.Context, o options, src sources) (model.Snapshot, *awsSession, error) {
	k, contextCluster, err := src.kube(o.kubeconfig, o.kubeContext)
	if err != nil {
		return model.Snapshot{}, nil, err
	}
	s, err := collect.GatherK8s(ctx, k, contextCluster)
	if err != nil {
		return model.Snapshot{}, nil, err
	}
	s = withOverrides(s, o)
	if o.noAWS {
		return s, nil, nil
	}
	s, sess := withAWS(ctx, s, src)
	return s, sess, nil
}

func withFixtureWarning(s model.Snapshot, o options) model.Snapshot {
	if o.promURL == "" && !o.verify {
		return s
	}
	out := s
	out.Warnings = append(slices.Clone(s.Warnings), warnFixtureIgnoresCollection)
	return out
}

// withVerify adds L3 only when L1 found the cluster's nodes, i.e. the credentials see the cluster's account.
func withVerify(ctx context.Context, s model.Snapshot, sess *awsSession, src sources) model.Snapshot {
	if sess == nil || s.AWS == nil {
		out := s
		out.Warnings = append(slices.Clone(s.Warnings), warnVerifyWithoutL1)
		return out
	}
	from, to := closedDays(src.now(), verifyDays)
	return collect.WithBill(ctx, s, sess.bill, from, to)
}

func withOverrides(s model.Snapshot, o options) model.Snapshot {
	out := s
	out.Cluster = cmp.Or(o.cluster, s.Cluster)
	out.Region = cmp.Or(o.region, s.Region)
	return out
}

// withAWS adds L1 when credentials work; otherwise it only records why L1 is missing.
func withAWS(ctx context.Context, s model.Snapshot, src sources) (model.Snapshot, *awsSession) {
	sess, err := src.aws(ctx, s.Region)
	if err != nil {
		out := s
		out.Warnings = append(slices.Clone(s.Warnings), fmt.Sprintf("%s: %v", warnNoAWSCredentials, err))
		return out, nil
	}
	out := s
	out.Region = cmp.Or(s.Region, sess.region)
	return collect.WithAWS(ctx, out, sess.inventory), &sess
}

// closedDays is the last days closed UTC days before now: [from, to) with to = today's UTC midnight.
func closedDays(now time.Time, days int) (time.Time, time.Time) {
	u := now.UTC()
	to := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return to.AddDate(0, 0, -days), to
}

func liveKube(kubeconfig, kubeContext string) (collect.K8sCollector, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext})
	restConfig, err := cc.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, "", fmt.Errorf("create kubernetes client: %w", err)
	}
	raw, err := cc.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("read kubeconfig: %w", err)
	}
	return collect.NewK8s(cs), contextClusterName(raw, kubeContext), nil
}

func contextClusterName(raw clientcmdapi.Config, kubeContext string) string {
	kctx, ok := raw.Contexts[cmp.Or(kubeContext, raw.CurrentContext)]
	if !ok {
		return ""
	}
	return clusterName(kctx.Cluster)
}

// clusterName shortens an EKS ARN (arn:aws:eks:<region>:<account>:cluster/<name>) to <name>.
func clusterName(name string) string {
	if !strings.HasPrefix(name, "arn:") {
		return name
	}
	if _, after, ok := strings.Cut(name, ":cluster/"); ok {
		return after
	}
	return name
}

func liveAWS(ctx context.Context, region string) (awsSession, error) {
	var opts []func(*config.LoadOptions) error
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsSession{}, fmt.Errorf("load AWS config: %w", err)
	}
	if cfg.Credentials == nil {
		return awsSession{}, errors.New("no AWS credentials provider")
	}
	credCtx, cancel := context.WithTimeout(ctx, credentialsTimeout)
	defer cancel()
	if _, err := cfg.Credentials.Retrieve(credCtx); err != nil {
		return awsSession{}, fmt.Errorf("retrieve AWS credentials: %w", err)
	}
	return awsSession{
		region:    cfg.Region,
		inventory: collect.NewAWSFromConfig(cfg),
		bill:      collect.NewCostExplorerFromConfig(cfg),
	}, nil
}
