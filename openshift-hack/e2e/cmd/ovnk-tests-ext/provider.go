package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ocphacke2e "github.com/ovn-org/ovn-kubernetes/openshift-hack/e2e"
	"github.com/ovn-org/ovn-kubernetes/test/e2e/deploymentconfig"
	"github.com/ovn-org/ovn-kubernetes/test/e2e/infraprovider"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	kclientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/test/e2e/framework"
)

// partially copied from github.com/openshift/origin/cmd/openshift-tests/provider.go
// and github.com/openshift/origin/test/extended/util/test.go
func initializeTestFramework() error {
	provider := os.Getenv("TEST_PROVIDER")
	if len(provider) == 0 {
		provider = "{\"type\":\"skeleton\"}"
	}
	providerInfo := &ClusterConfiguration{}
	if err := json.Unmarshal([]byte(provider), providerInfo); err != nil {
		return fmt.Errorf("provider must be a JSON object with the 'type' key at a minimum: %w", err)
	}
	if len(providerInfo.ProviderName) == 0 {
		return fmt.Errorf("provider must be a JSON object with the 'type' key")
	}
	config := &ClusterConfiguration{}
	if err := json.Unmarshal([]byte(provider), config); err != nil {
		return fmt.Errorf("provider must decode into the ClusterConfig object: %w", err)
	}

	// update testContext with loaded config
	testContext := &framework.TestContext
	testContext.Provider = config.ProviderName
	testContext.CloudConfig = framework.CloudConfig{
		ProjectID:   config.ProjectID,
		Region:      config.Region,
		Zone:        config.Zone,
		Zones:       config.Zones,
		NumNodes:    config.NumNodes,
		MultiMaster: config.MultiMaster,
		MultiZone:   config.MultiZone,
		ConfigFile:  config.ConfigFile,
		Provider:    framework.NullProvider{},
	}
	testContext.AllowedNotReadyNodes = 0
	testContext.MinStartupPods = -1
	testContext.MaxNodesToGather = 0
	testContext.KubeConfig = os.Getenv("KUBECONFIG")
	if testContext.KubeConfig == "" {
		return fmt.Errorf("KUBECONFIG env variable must be set")
	}
	testContext.DeleteNamespace = os.Getenv("DELETE_NAMESPACE") != "false"
	testContext.VerifyServiceAccount = false
	testContext.KubectlPath = "oc"

	if ad := os.Getenv("ARTIFACT_DIR"); len(strings.TrimSpace(ad)) == 0 {
		if err := os.Setenv("ARTIFACT_DIR", filepath.Join(os.TempDir(), "artifacts")); err != nil {
			return fmt.Errorf("unable to set ARTIFACT_DIR: %v", err)
		}
	}
	// "debian" is used when not set. At least GlusterFS tests need "custom".
	// (There is no option for "rhel" or "centos".)
	testContext.NodeOSDistro = "custom"
	testContext.MasterOSDistro = "custom"
	// load and set the host variable for kubectl
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(&clientcmd.ClientConfigLoadingRules{ExplicitPath: testContext.KubeConfig}, &clientcmd.ConfigOverrides{})
	cfg, err := clientConfig.ClientConfig()
	if err != nil {
		return fmt.Errorf("failed to get client config: %v", err)
	}
	testContext.Host = cfg.Host
	testContext.CreateTestingNS = func(ctx context.Context, baseName string, c kclientset.Interface, labels map[string]string) (*corev1.Namespace, error) {
		return ocphacke2e.CreateTestingNS(ctx, baseName, c, labels, true)
	}
	testContext.DumpLogsOnFailure = true
	gomega.RegisterFailHandler(ginkgo.Fail)
	testContext.ReportDir = os.Getenv("TEST_JUNIT_DIR")

	err = infraprovider.Set(cfg)
	framework.ExpectNoError(err, "must configure infrastructure provider")
	err = deploymentconfig.Set(cfg)
	framework.ExpectNoError(err, "must configure deployment config")
	return nil
}
