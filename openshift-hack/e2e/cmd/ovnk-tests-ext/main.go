package main

import (
	"os"
	"strings"

	"github.com/ovn-org/ovn-kubernetes/openshift-hack/e2e/pkg/generated"
	// import ovn-kubernetes tests
	_ "github.com/ovn-org/ovn-kubernetes/test/e2e"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/sets"
	// ensure providers are initialised for configuring infra
	_ "k8s.io/kubernetes/test/e2e/framework/providers/aws"
	_ "k8s.io/kubernetes/test/e2e/framework/providers/azure"
	_ "k8s.io/kubernetes/test/e2e/framework/providers/gce"
	_ "k8s.io/kubernetes/test/e2e/framework/providers/kubemark"
	_ "k8s.io/kubernetes/test/e2e/framework/providers/openstack"
	_ "k8s.io/kubernetes/test/e2e/framework/providers/vsphere"
	// ensure that logging flags are part of the command line.
	_ "k8s.io/component-base/logs/testinit"
	"k8s.io/klog/v2"
)

func main() {
	// Create our registry of openshift-tests extensions
	extensionRegistry := extension.NewRegistry()
	ovnTestsExtension := extension.NewExtension("openshift", "payload", "ovn-kubernetes")
	// TODO: register test images using tests extension
	// add ovn-kubernetes test suites into openshift suites

	// Suite: conformance/serial (explicitly serial tests)
	ovnTestsExtension.AddSuite(extension.Suite{
		Name:       "openshift/ovn-kubernetes/serial",
		Parents:    []string{"openshift/conformance/serial"},
		Qualifiers: []string{`name.contains("[Serial]"`},
	})

	// Suite: conformance/parallel (fast, parallel-safe)
	// by default, we treat all tests as parallel and only expose tests as Serial if the test
	// doesn't have labels Serial or Slow label
	ovnTestsExtension.AddSuite(extension.Suite{
		Name:       "openshift/ovn-kubernetes/parallel",
		Parents:    []string{"openshift/conformance/parallel"},
		Qualifiers: []string{`!(name.contains("[Serial]") || name.contains("[Slow]"))`},
	})

	// Suite: optional/slow (long-running tests)
	ovnTestsExtension.AddSuite(extension.Suite{
		Name:       "openshift/ovn-kubernetes/slow",
		Parents:    []string{"openshift/optional/slow"},
		Qualifiers: []string{`name.contains("[Slow]")`},
	})

	specs, err := ginkgo.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		klog.Errorf("Failed to list tests: %s", err)
		os.Exit(1)
	}
	klog.V(5).Infof("Found %d test specs", len(specs))

	// Initialization for kube ginkgo test framework needs to run before all tests execute
	specs.AddBeforeAll(func() {
		if err := initializeTestFramework(); err != nil {
			panic(err)
		}
	})
	// walk through each test and build the new test name which includes labels
	// Final format is:
	// [sig-ovn-kubernetes][JIRA:Networking/ovn-kubernetes][OCPFeature:XYZ] test name [additional:label][additional2:label][additional3]...
	// OCPFeature is optional, however we strongly advice to attach it to each test.
	// Appended labels are also optional
	specs.Walk(func(spec *extensiontests.ExtensionTestSpec) {
		// set all tests as informing for now, so we can judge stability of said all tests
		spec.Lifecycle = extensiontests.LifecycleInforming

		appendedLabelsWithBrackets := sets.New[string]()
		testLabels := addBrackets(spec.Labels)
		// extract feature label aka OCPFeature (u/s it is Feature). Not all tests have Feature label.
		ocpFeatureLabel := getOCPFeatureLabel(testLabels)
		testLabels = testLabels.Delete(ocpFeatureLabel)
		appendedLabelsWithBrackets = appendedLabelsWithBrackets.Union(testLabels) // add ginkgo labels minus the OCPFeature label
		if annotations, ok := generated.AppendedAnnotations[spec.Name]; ok {
			appendedLabelsWithBrackets = appendedLabelsWithBrackets.Union(getLabelsSetFromStr(annotations)) // add labels generated from rules.go
		}
		// remove OCPFeature label because we are going to prepend it to the test name
		appendedLabelsWithBrackets.Delete(ocpFeatureLabel)
		spec.Name = getSIGOVNKubernetesLabel() + getOVNKubeJiraLabel() + ocpFeatureLabel + spec.Name +
			strings.Join(appendedLabelsWithBrackets.UnsortedList(), "")
	})
	// remove disabled tests
	specs = specs.Select(func(spec *extensiontests.ExtensionTestSpec) bool {
		if strings.Contains(spec.Name, "Disabled") {
			return false
		}
		return true
	})

	klog.V(5).Infof("%d test specs remain, after filtering", len(specs))

	ovnTestsExtension.AddSpecs(specs)
	extensionRegistry.Register(ovnTestsExtension)
	root := &cobra.Command{
		Long: "OVN-Kubernetes tests extension for OpenShift",
	}
	root.AddCommand(
		cmd.DefaultExtensionCommands(extensionRegistry)...,
	)
	if err := func() error {
		return root.Execute()
	}(); err != nil {
		klog.Errorf("%v", err)
		os.Exit(1)
	}
}
