package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

func subnetItem(id, name, state string) map[string]any {
	return map[string]any{
		"metadata": cmpMeta(id, name),
		"status":   map[string]any{"state": state},
		"properties": map[string]any{
			"type":    "Advanced",
			"network": map[string]any{"address": "192.168.1.0/24"},
			"dhcp":    map[string]any{"enabled": true},
		},
	}
}

func defaultSubnetSpec(projectName, vpcName string) v1alpha1.SubnetSpec {
	return v1alpha1.SubnetSpec{
		Tenant:           "test-tenant",
		Tags:             []string{"tag1"},
		Region:           "ITBG-Bergamo",
		Type:             "Advanced",
		CIDR:             "192.168.1.0/24",
		DHCP:             v1alpha1.SubnetDHCP{Enabled: true},
		ProjectReference: v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
		VPCReference:     v1alpha1.ResourceReference{Name: vpcName, Namespace: "default"},
	}
}

func createTestSubnet(ctx context.Context, name string, spec v1alpha1.SubnetSpec) *v1alpha1.Subnet {
	s := &v1alpha1.Subnet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, s)).To(Succeed())
	return s
}

func setSubnetStatus(ctx context.Context, subnet *v1alpha1.Subnet, phase v1alpha1.ResourcePhase, reason string, resourceID, projectID, vpcID string, observedGen int64, conditionTime time.Time) {
	s := subnet.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.ObservedGeneration = observedGen
	if phase != "" {
		s.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, s)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())
}

type subnetFake struct {
	r *SubnetReconciler
	f *fakeCMP
}

func newSubnetReconcilerWithFake() *subnetFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &subnetFake{r: NewSubnetReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

func (m *subnetFake) stageParents(prjID, prjName, vpcID, vpcName string) {
	m.f.stage("projects", projectItem(prjID, prjName, nil, "", false))
	m.f.stage("vpcs", cmpItem(vpcID, vpcName, "Active"))
}
func (m *subnetFake) stageSubnets(items ...map[string]any) { m.f.stage("subnets", items...) }

var _ = Describe("SubnetReconciler", func() {
	const (
		snPrjName = "test-subnet-project-ref"
		snPrjID   = "sn-proj-id-1"
		snVpcName = "test-subnet-vpc-ref"
		snVpcID   = "sn-vpc-id-1"
	)

	var (
		ctx    context.Context
		subnet *v1alpha1.Subnet
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if subnet != nil {
			s := &v1alpha1.Subnet{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			subnet = nil
		}
	})

	It("transitions to Creating+ShallSynchronize when CMP has no subnet", func() {
		m := newSubnetReconcilerWithFake()
		subnet = createTestSubnet(ctx, "test-subnet-first", defaultSubnetSpec(snPrjName, snVpcName))
		setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())
		m.stageParents(snPrjID, snPrjName, snVpcID, snVpcName)

		_, err := m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
	})

	It("transitions to Active+Synchronized when CMP subnet is active", func() {
		m := newSubnetReconcilerWithFake()
		subnet = createTestSubnet(ctx, "test-subnet-active", defaultSubnetSpec(snPrjName, snVpcName))
		setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", "", "", 0, time.Now())
		m.stageParents(snPrjID, snPrjName, snVpcID, snVpcName)
		m.stageSubnets(subnetItem("sn-id-1", "test-subnet-active", "Active"))

		_, err := m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("sn-id-1"))
	})

	It("transitions to Failed when CMP subnet is in a failure state", func() {
		m := newSubnetReconcilerWithFake()
		subnet = createTestSubnet(ctx, "test-subnet-failed", defaultSubnetSpec(snPrjName, snVpcName))
		setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "sn-id-1", snPrjID, snVpcID, 1, time.Now())
		m.stageParents(snPrjID, snPrjName, snVpcID, snVpcName)
		m.stageSubnets(subnetItem("sn-id-1", "test-subnet-failed", "Failed"))

		_, err := m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newSubnetReconcilerWithFake()
		subnet = createTestSubnet(ctx, "test-subnet-delete", defaultSubnetSpec(snPrjName, snVpcName))
		sFetch := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), sFetch)).To(Succeed())
		sFetch.Finalizers = []string{subnetFinalizerName}
		Expect(k8sClient.Update(ctx, sFetch)).To(Succeed())
		setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "sn-id-1", snPrjID, snVpcID, 1, time.Now())
		Expect(k8sClient.Delete(ctx, subnet)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())
		m.stageParents(snPrjID, snPrjName, snVpcID, snVpcName)
		m.stageSubnets(subnetItem("sn-id-1", "test-subnet-delete", "Active"))

		_, err := m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("sets Failed+ValidationFailed when subnet tenant differs from parent VPC tenant", func() {
		m := newSubnetReconcilerWithFake()
		kubeVpc := createTestVpc(ctx, snVpcName, v1alpha1.VPCSpec{
			Tenant: "other-tenant", Region: "ITBG-Bergamo",
			ProjectReference: v1alpha1.ResourceReference{Name: snPrjName, Namespace: "default"},
		})
		defer func() { _ = k8sClient.Delete(ctx, kubeVpc) }()

		subnet = createTestSubnet(ctx, "test-subnet-validation", defaultSubnetSpec(snPrjName, snVpcName))
		setSubnetStatus(ctx, subnet, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "sn-id-val", snPrjID, snVpcID, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), subnet)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, subnet)
		Expect(err).To(Succeed())

		updated := &v1alpha1.Subnet{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(subnet), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with VPC"))
	})
})
