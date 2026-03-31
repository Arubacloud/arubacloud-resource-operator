/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	elasticIpFinalizerName = "elasticip.arubacloud.com/finalizer"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeElasticIPBundle struct {
	KubeProject *v1alpha1.Project // from resolveOwnerObject (already fetched for ownership)
}

type elasticIPBundle struct {
	kubeElasticIPBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

// ElasticIPReconciler reconciles a ElasticIP object
type ElasticIPReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *kubeElasticIPBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *elasticIPBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewElasticIPReconciler creates a new ElasticIPReconciler
func NewElasticIPReconciler(baseReconciler *reconciler.Reconciler) *ElasticIPReconciler {
	r := &ElasticIPReconciler{
		Reconciler: baseReconciler,
	}
	r.ivs = r.newIntentionValidationSet()
	r.vs = r.newValidationSet()
	r.ts = r.newTransitionSet()
	return r
}

// ---------------------------------------------------------------------------
// Interface methods
// ---------------------------------------------------------------------------

func (r *ElasticIPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *ElasticIPReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.ElasticIP{}
}

func (r *ElasticIPReconciler) Finalizer() string {
	return elasticIpFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *ElasticIPReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeEip, ok := obj.(*v1alpha1.ElasticIP)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.ElasticIP")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeEip.Spec.Tenant)
	logger.Info("reconciling elastic IP")

	isDeleting := !kubeEip.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeEip, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeEip.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeProject) {
		logger.V(1).Info("parent project not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeElasticIPBundle{}
		}
		if validationErr := r.ivs.Run(kubeEip, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeEip) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeEip.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeEip) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeEip.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeEip.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Fetch CMP dependencies.
	prjID, cmpEip, result, err := r.fetchCMPDependencies(ctx, kubeEip, arubaClient, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && kubeBdl != nil && cmpEip != nil {
		if validationErr := r.vs.Run(kubeEip, cmpEip, &elasticIPBundle{kubeElasticIPBundle: *kubeBdl}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(eip *v1alpha1.ElasticIP) {
					if eip.Status.ProjectID == "" {
						eip.Status.ProjectID = prjID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeEip) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeEip, cmpEip)
}

// fetchKubeDependencies fetches the parent Project and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the project is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *ElasticIPReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeEip *v1alpha1.ElasticIP,
	isDeleting bool,
) (*kubeElasticIPBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kp := &v1alpha1.Project{}
	if err := resolveOwnerObject(ctx, r.Client, kubeEip.Spec.ProjectReference, kubeEip.Namespace, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
		}
		logger.V(1).Info("parent project not found for owner reference setup, skipping",
			"projectName", kubeEip.Spec.ProjectReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeEip)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on elasticip: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	return &kubeElasticIPBundle{KubeProject: kp}, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID and fetches the CMP ElasticIP representation.
// Returns (prjID, nil cmpEip, zero result, nil) when the ElasticIP does not yet exist on CMP.
func (r *ElasticIPReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeEip *v1alpha1.ElasticIP,
	arubaClient aruba.Client,
	isDeleting bool,
) (string, *arubatypes.ElasticIPResponse, ctrl.Result, error) {
	eipName, projectName := kubeEip.Name, kubeEip.Spec.ProjectReference.Name
	eipFilter := fmt.Sprintf(`name:eq("%s")`, eipName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if isDeleting && kubeEip.Status.ProjectID != "" {
		prjID = kubeEip.Status.ProjectID
	} else {
		cmpProjectList, err := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
		if err != nil {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				err, projectName, prjFilter,
			)
		}
		if cmpProjectList.IsError() {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.StatusCode, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 && kubeEip.Status.ProjectID != "" {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total > 1 {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.Data.Total, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 {
			log.FromContext(ctx).V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if kubeEip.Status.ProjectID != "" && kubeEip.Status.ProjectID != prjID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in elasticip: eip_name: '%s', eip_project_id: '%s', project_name: '%s', project_id: '%s'",
			eipName, kubeEip.Status.ProjectID, projectName, prjID,
		)
	}

	cmpEipList, err := arubaClient.FromNetwork().ElasticIPs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &eipFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find elasticip in Aruba cloud: %w, eip_name: '%s', eip_filter: '%s', project_name: '%s'",
			err, eipName, eipFilter, projectName,
		)
	}
	if cmpEipList.IsError() && cmpEipList.StatusCode != http.StatusNotFound {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find elasticip in Aruba cloud: status_code: %d, eip_name: '%s', project_name: '%s'",
			cmpEipList.StatusCode, eipName, projectName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToElasticIPList(cmpEipList, eipName, log.FromContext(ctx))

	if !cmpEipList.IsError() && (cmpEipList.Data.Total < 0 || cmpEipList.Data.Total > 1) {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in elasticip list: eip_name: '%s', eip_filter: '%s', project_name: '%s', instances: %d",
			eipName, eipFilter, projectName, cmpEipList.Data.Total,
		)
	}

	var cmpEip *arubatypes.ElasticIPResponse
	if cmpEipList.Data != nil && cmpEipList.Data.Total == 1 {
		cmpEip = &cmpEipList.Data.Values[0]
	}
	log.FromContext(ctx).V(1).Info("CMP elastic IP state", "found", cmpEip != nil, "projectID", prjID)
	return prjID, cmpEip, ctrl.Result{}, nil
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

func (r *ElasticIPReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *kubeElasticIPBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *kubeElasticIPBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse, _ *kubeElasticIPBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	// 2. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse, b *kubeElasticIPBundle) error {
		if b.KubeProject == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeProject.Spec.Tenant != "" && k.Spec.Tenant != b.KubeProject.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with Project: %q != %q", k.Spec.Tenant, b.KubeProject.Spec.Tenant)
		}
		return nil
	})
	return ivs
}

func (r *ElasticIPReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *elasticIPBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *elasticIPBundle]{}
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse, *elasticIPBundle](
		"tenant",
		func(k *v1alpha1.ElasticIP) string { return k.Spec.Tenant },
		func(b *elasticIPBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	return vs
}

func (r *ElasticIPReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:        cmpElasticIpIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 6. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:       "HasDeniedChanges",
		KCondition: kubeElasticIPHasDeniedChanges,
		ACondition: cmpElasticIpIsFinal,
		KAction: func(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
			return fmt.Errorf("elasticip update rejected: %w", checkElasticIPDeniedChanges(kubeEip, cmpEip))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeElasticIPSpecInSyncWithCMP,
		ACondition:     cmpElasticIpIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 12. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeElasticIPShouldUpdate,
		ACondition:     cmpElasticIpIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 13. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:        cmpElasticIpIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 14. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 15. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 16. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:        cmpElasticIpNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		ACondition:     cmpElasticIpIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.ElasticIP, *arubatypes.ElasticIPResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeElasticIPHasDeniedChanges(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if !kubeEip.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpEip == nil {
		return false
	}
	return checkElasticIPDeniedChanges(kubeEip, cmpEip) != nil
}

func kubeElasticIPSpecInSyncWithCMP(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIPDeniedChanges(kubeEip, cmpEip) == nil &&
		!kubeElasticIPNeedsUpdate(kubeEip, cmpEip)
}

func kubeElasticIPShouldUpdate(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeEip, cmpEip) &&
		checkElasticIPDeniedChanges(kubeEip, cmpEip) == nil &&
		kubeElasticIPNeedsUpdate(kubeEip, cmpEip)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpElasticIpNotExists(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip == nil
}

func cmpElasticIpIsFinal(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpEip.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpElasticIpIsTransitory(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpEip.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpElasticIpNotExistsOrTransitory(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return true
	}
	if cmpEip.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpEip.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpElasticIpIsActive(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil || cmpEip.Status.State == nil {
		return false
	}
	switch *cmpEip.Status.State {
	case reconciler.CSPResourceStateActive, reconciler.CSPResourceStateNotUsed, reconciler.CSPResourceStateInUse, reconciler.CSPResourceStateUsed:
		return true
	}
	return false
}

func cmpElasticIpIsFailed(_ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	return cmpEip != nil && cmpEip.Status.State != nil && *cmpEip.Status.State == reconciler.CSPResourceStateFailed
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *ElasticIPReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeEip *v1alpha1.ElasticIP, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.ElasticIP){
		func(eip *v1alpha1.ElasticIP) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
				eip.Status.ProjectID = prjID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeEip, phase, reason, nil, prePatches...)
}

func (r *ElasticIPReconciler) kubeMarkToDelete(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeleting(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeletingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkDeleted(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkToUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkUpdating(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeMarkToCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *ElasticIPReconciler) kubeMarkCreating(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *ElasticIPReconciler) kubeMarkCreatingDone(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *ElasticIPReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	cmpID := ""
	if cmpEip != nil && cmpEip.Metadata.ID != nil {
		cmpID = *cmpEip.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeEip, cmpID, nil, func(eip *v1alpha1.ElasticIP) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID != "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIPReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeEip, func(eip *v1alpha1.ElasticIP) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && eip.Status.ProjectID == "" {
			eip.Status.ProjectID = prjID
		}
	})
}

func (r *ElasticIPReconciler) kubeSetFailed(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeEip, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *ElasticIPReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Delete(ctx, prjID, *cmpEip.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpEip.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpEip.Metadata.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *ElasticIPReconciler) cmpUpdate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkElasticIPDeniedChanges(kubeEip, cmpEip); err != nil {
		return err
	}

	request := buildElasticIPUpdateRequest(kubeEip, cmpEip)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Update(ctx, prjID, *cmpEip.Metadata.ID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeEip.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *ElasticIPReconciler) cmpCreate(ctx context.Context, kubeEip *v1alpha1.ElasticIP, _ *arubatypes.ElasticIPResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpEipResp, err := arubaClient.FromNetwork().ElasticIPs().Create(ctx, prjID, *cmpElasticIPRequestFromKube(kubeEip), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeEip.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeEip.Name, cmpEipResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func checkElasticIPDeniedChanges(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) error {
	if cmpEip == nil {
		return nil
	}

	locationValue := ""
	if cmpEip.Metadata.LocationResponse != nil {
		locationValue = cmpEip.Metadata.LocationResponse.Value
	}
	if kubeEip.Spec.Region != locationValue {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeElasticIPNeedsUpdate(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) bool {
	if cmpEip == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeEip.Spec.Tags, cmpEip.Metadata.Tags) {
		return true
	}
	if kubeEip.Spec.BillingPeriod != cmpEip.Properties.BillingPlan.BillingPeriod {
		return true
	}
	return false
}

func buildElasticIPUpdateRequest(kubeEip *v1alpha1.ElasticIP, cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
	request := cmpElasticIPRequestFromCMP(cmpEip)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeEip.Spec.Tags))
	copy(tags, kubeEip.Spec.Tags)
	request.Metadata.Tags = tags
	request.Properties.BillingPlan.BillingPeriod = kubeEip.Spec.BillingPeriod
	return request
}

func cmpElasticIPRequestFromCMP(cmpEip *arubatypes.ElasticIPResponse) *arubatypes.ElasticIPRequest {
	if cmpEip == nil {
		return nil
	}
	name := ""
	if cmpEip.Metadata.Name != nil {
		name = *cmpEip.Metadata.Name
	}
	tags := make([]string, len(cmpEip.Metadata.Tags))
	copy(tags, cmpEip.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if cmpEip.Metadata.LocationResponse != nil {
		location.Value = cmpEip.Metadata.LocationResponse.Value
	}
	return &arubatypes.ElasticIPRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.ElasticIPPropertiesRequest{
			BillingPlan: arubatypes.BillingPeriodResource{
				BillingPeriod: cmpEip.Properties.BillingPlan.BillingPeriod,
			},
		},
	}
}

func cmpElasticIPRequestFromKube(kubeEip *v1alpha1.ElasticIP) *arubatypes.ElasticIPRequest {
	return &arubatypes.ElasticIPRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeEip.Name,
				Tags: kubeEip.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeEip.Spec.Region},
		},
		Properties: arubatypes.ElasticIPPropertiesRequest{
			BillingPlan: arubatypes.BillingPeriodResource{
				BillingPeriod: kubeEip.Spec.BillingPeriod,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *ElasticIPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ElasticIP{}).
		Named("elasticip").
		Complete(r)
}
