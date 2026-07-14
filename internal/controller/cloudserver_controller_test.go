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

const csKPName = "test-cs-keypair"

// csItem stages a CMP cloud server with the zone/flavor the operator compares
// against for immutable-field (denied-change) detection.
func csItem(id, name, state string) map[string]any {
	return map[string]any{
		"metadata": cmpMeta(id, name),
		"status":   map[string]any{"state": state},
		"properties": map[string]any{
			"dataCenter": "ITBG",
			"flavor":     map[string]any{"name": "gp1.small"},
		},
	}
}

func defaultCSSpec(projectName, vpcName, bootVolName, subnetName, sgName string) v1alpha1.CloudServerSpec {
	return v1alpha1.CloudServerSpec{
		Tenant:                  "test-tenant",
		Tags:                    []string{"tag1"},
		Zone:                    "ITBG",
		FlavorName:              "gp1.small",
		Region:                  "ITBG-Bergamo",
		ProjectReference:        v1alpha1.ResourceReference{Name: projectName, Namespace: "default"},
		VPCReference:            v1alpha1.ResourceReference{Name: vpcName, Namespace: "default"},
		BootVolumeReference:     v1alpha1.ResourceReference{Name: bootVolName, Namespace: "default"},
		KeyPairReference:        v1alpha1.ResourceReference{Name: csKPName, Namespace: "default"},
		SubnetReferences:        []v1alpha1.ResourceReference{{Name: subnetName, Namespace: "default"}},
		SecurityGroupReferences: []v1alpha1.ResourceReference{{Name: sgName, Namespace: "default"}},
	}
}

func createTestCloudServer(ctx context.Context, name string, spec v1alpha1.CloudServerSpec) *v1alpha1.CloudServer {
	cs := &v1alpha1.CloudServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, cs)).To(Succeed())
	return cs
}

func setCSStatus(ctx context.Context, cs *v1alpha1.CloudServer, phase v1alpha1.ResourcePhase, reason string, resourceID, projectID, vpcID, bootVolumeID, keyPairID string, subnetIDs, sgIDs []string, observedGen int64, conditionTime time.Time) {
	s := cs.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), s)).To(Succeed())
	s.Status.Phase = phase
	s.Status.ResourceID = resourceID
	s.Status.ProjectID = projectID
	s.Status.VPCID = vpcID
	s.Status.BootVolumeID = bootVolumeID
	s.Status.KeyPairID = keyPairID
	s.Status.SubnetIDs = subnetIDs
	s.Status.SecurityGroupIDs = sgIDs
	s.Status.ObservedGeneration = observedGen
	if phase != "" {
		s.Status.Conditions = []metav1.Condition{{
			Type: string(phase), Status: metav1.ConditionTrue, Reason: reason,
			LastTransitionTime: metav1.NewTime(conditionTime), Message: string(phase) + " " + reason,
		}}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, s)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())
}

type csFake struct {
	r *CloudServerReconciler
	f *fakeCMP
}

func newCSReconcilerWithFake() *csFake {
	f := newFakeCMP()
	DeferCleanup(f.close)
	return &csFake{r: NewCloudServerReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

// stageDeps stages all the CMP dependencies a CloudServer resolves (boot volume
// and subnet in a final state so the create-readiness workaround is satisfied).
func (m *csFake) stageDeps(prjID, prjName, vpcID, vpcName, bootVolID, bootVolName, subnetID, subnetName, sgID, sgName, kpID string) {
	m.f.stage("projects", projectItem(prjID, prjName, nil, "", false))
	m.f.stage("vpcs", cmpItem(vpcID, vpcName, "Active"))
	m.f.stage("blockStorages", bsItem(bootVolID, bootVolName, "Active"))
	m.f.stage("subnets", subnetItem(subnetID, subnetName, "Active"))
	m.f.stage("securityGroups", cmpItem(sgID, sgName, "Active"))
	m.f.stage("keyPairs", kpItem(kpID, csKPName, "ssh-rsa test", nil))
}

var _ = Describe("CloudServerReconciler", func() {
	const (
		csPrjName = "test-cs-project-ref"
		csPrjID   = "cs-proj-id-1"
		csVpcName = "test-cs-vpc-ref"
		csVpcID   = "cs-vpc-id-1"
		csVolName = "test-cs-bootvol"
		csVolID   = "cs-vol-id-1"
		csSubName = "test-cs-subnet"
		csSubID   = "cs-sub-id-1"
		csSGName  = "test-cs-sg"
		csSGID    = "cs-sg-id-1"
		csKPID    = "cs-kp-id-1"
	)

	var (
		ctx context.Context
		cs  *v1alpha1.CloudServer
	)

	BeforeEach(func() { ctx = context.Background() })

	AfterEach(func() {
		if cs != nil {
			c := &v1alpha1.CloudServer{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), c); err == nil {
				c.Finalizers = nil
				_ = k8sClient.Update(ctx, c)
				_ = k8sClient.Delete(ctx, c)
			}
			cs = nil
		}
	})

	It("dispatches CMP create (Creating+Synchronizing) once all dependencies resolve", func() {
		m := newCSReconcilerWithFake()
		cs = createTestCloudServer(ctx, "test-cs-create", defaultCSSpec(csPrjName, csVpcName, csVolName, csSubName, csSGName))
		setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", "", "", "", "", nil, nil, 0, time.Now())
		m.stageDeps(csPrjID, csPrjName, csVpcID, csVpcName, csVolID, csVolName, csSubID, csSubName, csSGID, csSGName, csKPID)
		// CloudServer itself not staged → absent → create is dispatched.

		_, err := m.r.HandleReconcile(ctx, cs)
		Expect(err).To(Succeed())

		updated := &v1alpha1.CloudServer{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("transitions to Active+Synchronized when the CMP cloud server is running", func() {
		m := newCSReconcilerWithFake()
		cs = createTestCloudServer(ctx, "test-cs-active", defaultCSSpec(csPrjName, csVpcName, csVolName, csSubName, csSGName))
		setCSStatus(ctx, cs, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", csPrjID, csVpcID, csVolID, csKPID, []string{csSubID}, []string{csSGID}, 0, time.Now())
		m.stageDeps(csPrjID, csPrjName, csVpcID, csVpcName, csVolID, csVolName, csSubID, csSubName, csSGID, csSGName, csKPID)
		m.f.stage("cloudServers", csItem("cs-id-1", "test-cs-active", "Running"))

		_, err := m.r.HandleReconcile(ctx, cs)
		Expect(err).To(Succeed())

		updated := &v1alpha1.CloudServer{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		Expect(updated.Status.ResourceID).To(Equal("cs-id-1"))
	})

	It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
		m := newCSReconcilerWithFake()
		cs = createTestCloudServer(ctx, "test-cs-delete", defaultCSSpec(csPrjName, csVpcName, csVolName, csSubName, csSGName))
		cFetch := &v1alpha1.CloudServer{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cFetch)).To(Succeed())
		cFetch.Finalizers = []string{cloudServerFinalizerName}
		Expect(k8sClient.Update(ctx, cFetch)).To(Succeed())
		setCSStatus(ctx, cs, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "cs-id-1", csPrjID, csVpcID, csVolID, csKPID, []string{csSubID}, []string{csSGID}, 1, time.Now())
		Expect(k8sClient.Delete(ctx, cs)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())
		m.f.stage("cloudServers", csItem("cs-id-1", "test-cs-delete", "Running"))

		_, err := m.r.HandleReconcile(ctx, cs)
		Expect(err).To(Succeed())

		updated := &v1alpha1.CloudServer{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting)).Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
	})

	It("sets Failed+ValidationFailed when CloudServer tenant differs from parent project tenant", func() {
		m := newCSReconcilerWithFake()
		proj := createTestProject(ctx, csPrjName, v1alpha1.ProjectSpec{Tenant: "other-tenant"})
		defer func() { _ = k8sClient.Delete(ctx, proj) }()

		cs = createTestCloudServer(ctx, "test-cs-validation", defaultCSSpec(csPrjName, csVpcName, csVolName, csSubName, csSGName))
		setCSStatus(ctx, cs, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cs-id-val", csPrjID, csVpcID, csVolID, csKPID, []string{csSubID}, []string{csSGID}, 0, time.Now())

		result, err := m.r.HandleReconcile(ctx, cs)
		Expect(err).To(Succeed())
		Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), cs)).To(Succeed())

		_, err = m.r.HandleReconcile(ctx, cs)
		Expect(err).To(Succeed())

		updated := &v1alpha1.CloudServer{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cs), updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		Expect(findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed)).Message).To(ContainSubstring("tenant mismatch with Project"))
	})
})
