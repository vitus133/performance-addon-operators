package mcps

import (
	"context"
	"fmt"
	"k8s.io/klog"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/nodes"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
)

const (
	mcpUpdateTimeoutPerNode = 20
)

// GetByLabel returns all MCPs with the specified label
func GetByLabel(key, value string) ([]machineconfigv1.MachineConfigPool, error) {
	selector := labels.NewSelector()
	req, err := labels.NewRequirement(key, selection.Equals, []string{value})
	if err != nil {
		return nil, err
	}
	selector = selector.Add(*req)
	mcps := &machineconfigv1.MachineConfigPoolList{}
	if err := testclient.Client.List(context.TODO(), mcps, &client.ListOptions{LabelSelector: selector}); err != nil {
		return nil, err
	}
	return mcps.Items, nil
}

// GetByName returns the MCP with the specified name
func GetByName(name string) (*machineconfigv1.MachineConfigPool, error) {
	mcp := &machineconfigv1.MachineConfigPool{}
	key := types.NamespacedName{
		Name:      name,
		Namespace: metav1.NamespaceNone,
	}
	err := testclient.GetWithRetry(context.TODO(), key, mcp)
	return mcp, err
}

// New creates a new MCP with the given name and node selector
func New(mcpName string, nodeSelector map[string]string) *machineconfigv1.MachineConfigPool {
	return &machineconfigv1.MachineConfigPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpName,
			Namespace: metav1.NamespaceNone,
			Labels:    map[string]string{components.MachineConfigRoleLabelKey: mcpName},
		},
		Spec: machineconfigv1.MachineConfigPoolSpec{
			MachineConfigSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      components.MachineConfigRoleLabelKey,
						Operator: "In",
						Values:   []string{"worker", mcpName},
					},
				},
			},
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: nodeSelector,
			},
		},
	}
}

// GetConditionStatus return the condition status of the given MCP and condition type
func GetConditionStatus(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType) corev1.ConditionStatus {
	mcp, err := GetByName(mcpName)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "Failed getting MCP by name")
	for _, condition := range mcp.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Status
		}
	}
	return corev1.ConditionUnknown
}

// GetConditionReason return the reason of the given MCP
func GetConditionReason(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType) string {
	mcp, err := GetByName(mcpName)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "Failed getting MCP by name")
	for _, condition := range mcp.Status.Conditions {
		if condition.Type == conditionType {
			return condition.Reason
		}
	}
	return ""
}

// WaitForCondition waits for the MCP with given name having a condition of given type with given status
func WaitForCondition(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType, conditionStatus corev1.ConditionStatus) {
	mcp, err := GetByName(mcpName)
	Expect(err).ToNot(HaveOccurred(), "Failed getting MCP by name")

	nodeLabels := mcp.Spec.NodeSelector.MatchLabels
	key, _ := components.GetFirstKeyAndValue(nodeLabels)
	req, err := labels.NewRequirement(key, selection.Operator(selection.Exists), []string{})
	Expect(err).ToNot(HaveOccurred(), "Failed creating node selector")

	selector := labels.NewSelector()
	selector = selector.Add(*req)
	cnfNodes, err := nodes.GetBySelector(selector)
	Expect(err).ToNot(HaveOccurred(), "Failed getting nodes by selector")
	Expect(cnfNodes).ToNot(BeEmpty(), "Found no CNF nodes")
	klog.Infof("MCP is targeting %v node(s)", len(cnfNodes))

	// timeout should be based on the number of worker-cnf nodes
	timeout := time.Duration(len(cnfNodes) * mcpUpdateTimeoutPerNode)

	EventuallyWithOffset(1, func() corev1.ConditionStatus {
		return GetConditionStatus(mcpName, conditionType)
	}, timeout*time.Minute, 30*time.Second).Should(Equal(conditionStatus))
}

// WaitForProfilePickedUp waits for the MCP with given name containing the MC created for the PerformanceProfile with the given name
func WaitForProfilePickedUp(mcpName string, profileName string) {
	EventuallyWithOffset(1, func() bool {
		mcp, err := GetByName(mcpName)
		Expect(err).ToNot(HaveOccurred(), "Failed getting MCP by name")
		for _, source := range mcp.Spec.Configuration.Source {
			if source.Name == fmt.Sprintf("%s-%s", components.ComponentNamePrefix, profileName) {
				return true
			}
		}
		return false
	}, 10*time.Minute, 30*time.Second).Should(BeTrue(), "PerformanceProfile's MC was not picked up by MCP in time")
}
