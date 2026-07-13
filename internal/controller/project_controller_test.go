package controller

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubamocks "github.com/Arubacloud/arubacloud-resource-operator/internal/mocks/aruba"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

// --- Builder helpers ---

func buildProjectResponse(id, name string, tags []string, description string, def bool) *arubatypes.ProjectResponse {
	return &arubatypes.ProjectResponse{
		Metadata: arubatypes.ResourceMetadataResponse{
			ID:   &id,
			Name: &name,
			Tags: tags,
		},
		Properties: arubatypes.ProjectPropertiesResponse{
			Description: &description,
			Default:     def,
		},
	}
}

func buildProjectList(responses ...*arubatypes.ProjectResponse) *arubatypes.Response[arubatypes.ProjectListResponse] {
	list := &arubatypes.ProjectListResponse{}
	for _, r := range responses {
		list.Values = append(list.Values, *r)
		list.Total++
	}
	return &arubatypes.Response[arubatypes.ProjectListResponse]{
		Data:       list,
		StatusCode: http.StatusOK,
	}
}

func buildProjectListErrorResponse(statusCode int) *arubatypes.Response[arubatypes.ProjectListResponse] {
	return &arubatypes.Response[arubatypes.ProjectListResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectCRUDResponse(statusCode int) *arubatypes.Response[arubatypes.ProjectResponse] {
	return &arubatypes.Response[arubatypes.ProjectResponse]{
		StatusCode: statusCode,
	}
}

func buildProjectCRUDValidationErrorResponse(statusCode int, field, message string) *arubatypes.Response[arubatypes.ProjectResponse] {
	return &arubatypes.Response[arubatypes.ProjectResponse]{
		StatusCode: statusCode,
		Error: &arubatypes.ErrorResponse{
			Errors: []arubatypes.ValidationError{{Field: field, Message: message}},
		},
	}
}

func buildDeleteResponse(statusCode int) *arubatypes.Response[any] {
	return &arubatypes.Response[any]{StatusCode: statusCode}
}

// --- Test fixture helpers ---

func defaultProjectSpec() v1alpha1.ProjectSpec {
	return v1alpha1.ProjectSpec{
		Tenant:      "test-tenant",
		Description: "test description",
		Tags:        []string{"tag1"},
	}
}

func createTestProject(ctx context.Context, name string, spec v1alpha1.ProjectSpec) *v1alpha1.Project {
	proj := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: spec,
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, proj)).To(Succeed())
	return proj
}

func setProjectStatus(ctx context.Context, proj *v1alpha1.Project, phase v1alpha1.ResourcePhase, reason string, resourceID string, observedGen int64, conditionTime time.Time) {
	p := proj.DeepCopy()
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p)).To(Succeed())
	p.Status.Phase = phase
	p.Status.ResourceID = resourceID
	p.Status.ObservedGeneration = observedGen
	if phase != "" {
		p.Status.Conditions = []metav1.Condition{
			{
				Type:               string(phase),
				Status:             metav1.ConditionTrue,
				Reason:             reason,
				LastTransitionTime: metav1.NewTime(conditionTime),
				Message:            string(phase) + " " + reason + " - OK",
			},
		}
	}
	ExpectWithOffset(1, k8sClient.Status().Update(ctx, p)).To(Succeed())
	ExpectWithOffset(1, k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
}

func newProjectReconcilerWithMocks(t GinkgoTInterface) (*ProjectReconciler, *arubamocks.MockClient, *arubamocks.MockProjectClient) {
	mockArubaClient := arubamocks.NewMockClient(t)
	mockProjectClient := arubamocks.NewMockProjectClient(t)
	r := NewProjectReconciler(newTestReconciler(t, mockArubaClient))
	return r, mockArubaClient, mockProjectClient
}

// --- Tests ---

var _ = Describe("ProjectReconciler", func() {
	var (
		ctx  context.Context
		proj *v1alpha1.Project
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if proj != nil {
			p := &v1alpha1.Project{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), p); err == nil {
				p.Finalizers = nil
				_ = k8sClient.Update(ctx, p)
				_ = k8sClient.Delete(ctx, p)
			}
			proj = nil
		}
	})

	Describe("First reconciliation (no status)", func() {
		It("transitions to Creating+ShallSynchronize when CMP has no project", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-first-reconcile", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("PendingAndDeleting", func() {
		It("transitions directly to Deleted when resource is in Pending and is being deleted", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-proj-pending-deleting", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhasePending, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			proj.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, proj)).To(Succeed())

			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))

			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Create on CMP", func() {
		It("transitions to Creating+Synchronizing after successful CMP create", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-create-cmp", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)
			mockProjectClient.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(buildProjectCRUDResponse(http.StatusCreated), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Waiting creation (Synchronizing, no CMP project yet)", func() {
		It("returns LongRequeue without updating status", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-wait-create", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", 0, time.Now())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			result, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Creation confirmed on CMP", func() {
		It("transitions to Creating+Synchronized when CMP project exists", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-creation-confirmed", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, "", 0, time.Now())

			cmpProj := buildProjectResponse("cmp-id-1", "test-creation-confirmed", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Creation accomplished", func() {
		It("transitions to Active+Synchronized and sets ResourceID", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-creation-accomplished", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, "", 0, time.Now())

			cmpProj := buildProjectResponse("cmp-id-1", "test-creation-accomplished", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ResourceID).To(Equal("cmp-id-1"))
		})
	})

	Describe("Spec change needing update", func() {
		It("transitions to Updating+ShallSynchronize when spec differs from CMP", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-needs-update", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())

			// Force generation change
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Spec.Description = "changed description"
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			// CMP has old description
			cmpProj := buildProjectResponse("cmp-id-1", "test-needs-update", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Spec already in sync with CMP", func() {
		It("re-stamps ObservedGeneration and stays Active when spec matches CMP", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-already-in-sync", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())

			// Force generation change without changing meaningful spec
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			if pFetch.Annotations == nil {
				pFetch.Annotations = map[string]string{}
			}
			pFetch.Annotations["force-gen"] = "2"
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			// CMP has same spec
			spec := defaultProjectSpec()
			cmpProj := buildProjectResponse("cmp-id-1", "test-already-in-sync", spec.Tags, spec.Description, false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
		})
	})

	Describe("Update on CMP", func() {
		It("transitions to Updating+Synchronizing after successful CMP update", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-update-cmp", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())

			cmpProj := buildProjectResponse("cmp-id-1", "test-update-cmp", nil, "old description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)
			mockProjectClient.EXPECT().Update(mock.Anything, "cmp-id-1", mock.Anything, mock.Anything).Return(buildProjectCRUDResponse(http.StatusOK), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Update confirmed on CMP", func() {
		It("transitions to Updating+Synchronized when spec matches CMP", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-update-confirmed", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, "cmp-id-1", 1, time.Now())

			spec := defaultProjectSpec()
			cmpProj := buildProjectResponse("cmp-id-1", "test-update-confirmed", spec.Tags, spec.Description, false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Update accomplished", func() {
		It("transitions back to Active+Synchronized", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-update-accomplished", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())

			cmpProj := buildProjectResponse("cmp-id-1", "test-update-accomplished", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
		})
	})

	Describe("Should delete", func() {
		It("transitions to Deleting+ShallSynchronize when deletion is requested on Active resource", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-should-delete", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			cmpProj := buildProjectResponse("cmp-id-1", "test-should-delete", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete on CMP", func() {
		It("transitions to Deleting+Synchronizing after successful CMP delete", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-delete-cmp", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			cmpID := "cmp-id-1"
			cmpProj := buildProjectResponse(cmpID, "test-delete-cmp", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)
			mockProjectClient.EXPECT().Delete(mock.Anything, cmpID, mock.Anything).Return(buildDeleteResponse(http.StatusOK), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronizing))
		})
	})

	Describe("Deletion not needed on CMP", func() {
		It("transitions to Deleting+Synchronized when CMP resource is already gone", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-deletion-not-needed", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Waiting deletion (CMP still has project)", func() {
		It("returns LongRequeue", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-wait-delete", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			cmpProj := buildProjectResponse("cmp-id-1", "test-wait-delete", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			result, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
		})
	})

	Describe("Deletion confirmed on CMP", func() {
		It("transitions to Deleting+Synchronized when CMP project is gone", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-deletion-confirmed", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})

	Describe("Deletion accomplished", func() {
		It("transitions to Deleted phase", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-deletion-accomplished", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, "cmp-id-1", 1, time.Now())
			Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleted))
		})
	})

	Describe("Phase timeout", func() {
		It("transitions to Failed when stuck in transitory phase too long", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-timeout", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "",
				0, time.Now().Add(-(reconciler.MaxPhaseTimeout + time.Minute)))

			cmpProj := buildProjectResponse("cmp-id-1", "test-timeout", nil, "test description", false)
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("Delete timed out (non-Deleting previous phase)", func() {
		It("transitions to Deleting+ShallSynchronize for timed-out resource with deletion requested", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-delete-timedout", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())

			// Simulate timed-out from Creating
			pFetch2 := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch2)).To(Succeed())
			pFetch2.Status.Phase = v1alpha1.ResourcePhaseFailed
			pFetch2.Status.Conditions = []metav1.Condition{
				{Type: "Creating", Status: metav1.ConditionFalse, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
				{Type: "Failed", Status: metav1.ConditionTrue, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
			}
			Expect(k8sClient.Status().Update(ctx, pFetch2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pFetch2)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
			cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
		})
	})

	Describe("Delete timed out (from Deleting phase)", func() {
		It("stays in Failed phase (no transition matches)", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-delete-timedout-from-deleting", defaultProjectSpec())
			pFetch := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
			pFetch.Finalizers = []string{projectFinalizerName}
			Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())

			// Simulate timed-out from Deleting
			pFetch2 := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch2)).To(Succeed())
			pFetch2.Status.Phase = v1alpha1.ResourcePhaseFailed
			pFetch2.Status.Conditions = []metav1.Condition{
				{Type: "Deleting", Status: metav1.ConditionFalse, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
				{Type: "Failed", Status: metav1.ConditionTrue, Reason: v1alpha1.ConditionReasonFailed, LastTransitionTime: metav1.Now(), Message: "timeout"},
			}
			Expect(k8sClient.Status().Update(ctx, pFetch2)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pFetch2)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
		})
	})

	Describe("CMP List error", func() {
		It("returns error from HandleReconcile when CMP responds with error status", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "test-cmp-error", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectListErrorResponse(http.StatusInternalServerError), nil)

			_, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("CMP error handling", func() {
		DescribeTable("CMP create fails — preserves Creating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
				proj = createTestProject(ctx, name, defaultProjectSpec())
				setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

				mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
				mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil)
				mockProjectClient.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(buildProjectCRUDResponse(statusCode), nil)

				result, err := r.HandleReconcile(ctx, proj)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Project{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseCreating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseCreating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "proj-cmp-err-create-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "proj-cmp-err-create-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP update fails — preserves Updating+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
				proj = createTestProject(ctx, name, defaultProjectSpec())
				setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())

				cmpProj := buildProjectResponse("cmp-id-1", name, nil, "old description", false)
				mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
				mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)
				mockProjectClient.EXPECT().Update(mock.Anything, "cmp-id-1", mock.Anything, mock.Anything).Return(buildProjectCRUDResponse(statusCode), nil)

				result, err := r.HandleReconcile(ctx, proj)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Project{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseUpdating))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseUpdating))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "proj-cmp-err-update-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "proj-cmp-err-update-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		DescribeTable("CMP delete fails — preserves Deleting+ShallSynchronize, surfaces error in condition",
			func(name string, statusCode int, expectedRequeue time.Duration) {
				r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
				proj = createTestProject(ctx, name, defaultProjectSpec())
				pFetch := &v1alpha1.Project{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), pFetch)).To(Succeed())
				pFetch.Finalizers = []string{projectFinalizerName}
				Expect(k8sClient.Update(ctx, pFetch)).To(Succeed())
				setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, "cmp-id-1", 1, time.Now())
				Expect(k8sClient.Delete(ctx, proj)).To(Succeed())
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

				cmpProj := buildProjectResponse("cmp-id-1", name, nil, "test description", false)
				mockArubaClient.EXPECT().FromProject().Return(mockProjectClient)
				mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(cmpProj), nil)
				mockProjectClient.EXPECT().Delete(mock.Anything, "cmp-id-1", mock.Anything).Return(buildDeleteResponse(statusCode), nil)

				result, err := r.HandleReconcile(ctx, proj)
				Expect(err).To(Succeed())
				Expect(result.RequeueAfter).To(Equal(expectedRequeue))

				updated := &v1alpha1.Project{}
				Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
				Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseDeleting))
				cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseDeleting))
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonShallSynchronize))
				Expect(cond.Message).To(ContainSubstring("ERROR"))
			},
			Entry("4xx → LongRequeueAfter, no phase change", "proj-cmp-err-delete-400", http.StatusBadRequest, reconciler.LongRequeueAfter),
			Entry("5xx → ShortRequeueAfter, no phase change", "proj-cmp-err-delete-500", http.StatusInternalServerError, reconciler.ShortRequeueAfter),
		)

		It("CMP create semantic error moves to Failed+ValidationFailed, then recovers on next reconcile after spec change", func() {
			r, mockArubaClient, mockProjectClient := newProjectReconcilerWithMocks(GinkgoT())
			proj = createTestProject(ctx, "proj-semantic-recovery", defaultProjectSpec())
			setProjectStatus(ctx, proj, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, "", 0, time.Now())

			// First reconcile: CMP returns 400 with validation errors → Failed+ValidationFailed.
			// FromProject is called twice: once for the List call and once inside cmpCreate.
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient).Times(2)
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil).Once()
			mockProjectClient.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
				Return(buildProjectCRUDValidationErrorResponse(http.StatusBadRequest, "Tag", "length must be at least 4"), nil).Once()

			result, err := r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(BeZero()) // semantic error → no requeue

			updated := &v1alpha1.Project{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
			failedCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
			Expect(failedCond).NotTo(BeNil())
			Expect(failedCond.Reason).To(Equal(v1alpha1.ConditionReasonValidationFailed))
			Expect(failedCond.Message).To(ContainSubstring("Tag: length must be at least 4"))

			// Simulate spec change to increment generation so IsCMPValidationFailedAndSpecChanged returns true.
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())
			proj.Spec.Description = "updated description after validation fix"
			Expect(k8sClient.Update(ctx, proj)).To(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), proj)).To(Succeed())

			// Second reconcile: generation-gated recovery fires, resets to Pending+Synchronized.
			// The CMP list is still called (before the recovery check), but no CMP create is attempted.
			mockArubaClient.EXPECT().FromProject().Return(mockProjectClient).Once()
			mockProjectClient.EXPECT().List(mock.Anything, mock.Anything).Return(buildProjectList(), nil).Once()

			result, err = r.HandleReconcile(ctx, proj)
			Expect(err).To(Succeed())
			Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))

			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proj), updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhasePending))
			pendingCond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhasePending))
			Expect(pendingCond).NotTo(BeNil())
			Expect(pendingCond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
		})
	})
})
