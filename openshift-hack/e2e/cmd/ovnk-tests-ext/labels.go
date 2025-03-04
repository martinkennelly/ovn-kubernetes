package main

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

// getLabelsSetFromStr accepts an argument which may contain zero or more labels/tags with their brackets, splits them, and returns the union.
// labels are returned in a set with their brackets attached. e.g. [Slow]
func getLabelsSetFromStr(labelsStr string) sets.Set[string] {
	labelsSet := sets.New[string]()
	labelsList := strings.Split(labelsStr, "][")
	// remove any remaining brackets
	for _, label := range labelsList {
		label = strings.ReplaceAll(label, "[", "")
		label = strings.ReplaceAll(label, "]", "")
		if label == "" {
			continue
		}
		// add brackets to label
		labelsSet.Insert("[" + label + "]")
	}
	return labelsSet
}

func getOCPFeatureLabel(labels sets.Set[string]) string {
	for _, label := range labels.UnsortedList() {
		if strings.Contains(label, "OCPFeature") {
			return label
		}
	}
	return ""
}

func addBrackets(labelsWithoutBrackets sets.Set[string]) sets.Set[string] {
	labelsWithBrackets := sets.New[string]()
	for _, labelWithoutBracket := range labelsWithoutBrackets.UnsortedList() {
		if strings.Contains(labelWithoutBracket, "[") || strings.Contains(labelWithoutBracket, "]") {
			panic(fmt.Sprintf("Expected label %q with no square brackets", labelWithoutBracket))
		}
		labelsWithBrackets.Insert("[" + labelWithoutBracket + "]")
	}
	return labelsWithBrackets
}

func getSIGOVNKubernetesLabel() string {
	return "[sig-ovn-kubernetes]"
}

func getOVNKubeJiraLabel() string {
	return "[JIRA:Networking/ovn-kubernetes]"
}
