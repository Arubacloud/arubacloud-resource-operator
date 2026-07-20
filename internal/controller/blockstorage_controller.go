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

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	blockStorageFinalizerName = "blockstorage.arubacloud.com/finalizer"
)

type contextKey string

const projectIDKey contextKey = "projectID"

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeBlockStorageBundle struct {
	KubeProject *v1alpha1.Project // from resolveOwnerObject (already fetched for ownership)
}

type blockStorageBundle struct {
	kubeBlockStorageBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// BlockStorageReconciler reconciles a BlockStorage object
type BlockStorageReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *kubeBlockStorageBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *blockStorageBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.BlockStorage, *aruba.BlockStorage]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewBlockStorageReconciler creates a new BlockStorageReconciler
func NewBlockStorageReconciler(baseReconciler *reconciler.Reconciler) *BlockStorageReconciler {
	r := &BlockStorageReconciler{
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

func (r *BlockStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *BlockStorageReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.BlockStorage{}
}

func (r *BlockStorageReconciler) Finalizer() string {
	return blockStorageFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *BlockStorageReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeBlockStorage, ok := obj.(*v1alpha1.BlockStorage)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.BlockStorage")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeBlockStorage.Spec.Tenant)
	logger.Info("reconciling block storage")

	isDeleting := !kubeBlockStorage.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeBlockStorage, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeBlockStorage.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeProject) {
		logger.V(1).Info("parent project not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeBlockStorageBundle{}
		}
		if validationErr := r.ivs.Run(kubeBlockStorage, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBlockStorage,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeBlockStorage) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeBlockStorage.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBlockStorage,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeBlockStorage) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeBlockStorage.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBlockStorage,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba CMP client.
	arubaClient, err := r.ArubaClient(kubeBlockStorage.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Fetch CMP dependencies.
	prjID, cmpBlockStorage, result, err := r.fetchCMPDependencies(ctx, kubeBlockStorage, arubaClient, isDeleting)
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
	if !isDeleting && kubeBdl != nil && cmpBlockStorage != nil {
		if validationErr := r.vs.Run(kubeBlockStorage, cmpBlockStorage, &blockStorageBundle{kubeBlockStorageBundle: *kubeBdl}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBlockStorage,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(bs *v1alpha1.BlockStorage) {
					if bs.Status.ProjectID == "" {
						bs.Status.ProjectID = prjID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeBlockStorage) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBlockStorage,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeBlockStorage, cmpBlockStorage)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

// fetchKubeDependencies fetches the parent Project and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the project is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *BlockStorageReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeBlockStorage *v1alpha1.BlockStorage,
	isDeleting bool,
) (*kubeBlockStorageBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kp := &v1alpha1.Project{}
	if err := resolveOwnerObject(ctx, r.Client, kubeBlockStorage.Spec.ProjectReference, kubeBlockStorage.Namespace, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
		}
		logger.V(1).Info("parent project not found for owner reference setup, skipping",
			"projectName", kubeBlockStorage.Spec.ProjectReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeBlockStorage)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on blockstorage: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	return &kubeBlockStorageBundle{KubeProject: kp}, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID and fetches the CMP BlockStorage representation.
// Returns (prjID, nil cmpBlockStorage, zero result, nil) when the BlockStorage does not yet exist on CMP.
func (r *BlockStorageReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeBlockStorage *v1alpha1.BlockStorage,
	arubaClient aruba.Client,
	isDeleting bool,
) (string, *aruba.BlockStorage, ctrl.Result, error) {
	blockStorageName, projectName := kubeBlockStorage.Name, kubeBlockStorage.Spec.ProjectReference.Name
	bsFilter := fmt.Sprintf(`name:eq("%s")`, blockStorageName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if isDeleting && kubeBlockStorage.Status.ProjectID != "" {
		prjID = kubeBlockStorage.Status.ProjectID
	} else {
		cmpProjectList, err := arubaClient.FromProject().List(ctx, aruba.WithFilter(prjFilter))
		if err != nil {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				err, projectName, prjFilter,
			)
		}
		cmpProjects := cmpProjectList.Items()
		if len(cmpProjects) == 0 && kubeBlockStorage.Status.ProjectID != "" {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}
		if len(cmpProjects) > 1 {
			return "", nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				len(cmpProjects), projectName, prjFilter,
			)
		}
		if len(cmpProjects) == 0 {
			log.FromContext(ctx).V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		prjID = cmpProjects[0].ID()
	}

	if kubeBlockStorage.Status.ProjectID != "" && kubeBlockStorage.Status.ProjectID != prjID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in blockstorage: blockstorage_name: '%s', blockstorage_project_id: '%s', project_name: '%s', project_id: '%s'",
			blockStorageName, kubeBlockStorage.Status.ProjectID, projectName, prjID,
		)
	}

	cmpBlockStorageList, err := arubaClient.FromStorage().Volumes().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(bsFilter))
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find blockstorage in Aruba cloud: %w, blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s'",
			err, blockStorageName, bsFilter, projectName,
		)
	}

	cmpVolumes := cmpBlockStorageList.Items()
	if len(cmpVolumes) > 1 {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in blockstorage list: blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s', instances: %d",
			blockStorageName, bsFilter, projectName, len(cmpVolumes),
		)
	}

	var cmpBlockStorage *aruba.BlockStorage
	if len(cmpVolumes) == 1 {
		cmpBlockStorage = cmpVolumes[0]
	}
	log.FromContext(ctx).V(1).Info("CMP block storage state", "found", cmpBlockStorage != nil, "projectID", prjID)
	return prjID, cmpBlockStorage, ctrl.Result{}, nil
}

func (r *BlockStorageReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *kubeBlockStorageBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *kubeBlockStorageBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.BlockStorage, _ *aruba.BlockStorage, _ *kubeBlockStorageBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	// 2. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.BlockStorage, _ *aruba.BlockStorage, b *kubeBlockStorageBundle) error {
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

func (r *BlockStorageReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *blockStorageBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.BlockStorage, *aruba.BlockStorage, *blockStorageBundle]{}
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.BlockStorage, *aruba.BlockStorage, *blockStorageBundle](
		"tenant",
		func(k *v1alpha1.BlockStorage) string { return k.Spec.Tenant },
		func(b *blockStorageBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	return vs
}

func (r *BlockStorageReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.BlockStorage, *aruba.BlockStorage] {
	ts := &reconciler.TransitionSet[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.BlockStorage, *aruba.BlockStorage](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.BlockStorage, *aruba.BlockStorage](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:        cmpBlockStorageIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *aruba.BlockStorage](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 6. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:       "HasDeniedChanges",
		KCondition: kubeBlockStorageHasDeniedChanges,
		ACondition: cmpBlockStorageIsFinal,
		KAction: func(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) error {
			return fmt.Errorf("blockstorage update rejected: %w", checkBlockStorageDeniedChanges(kubeBS, cmpBS))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeBlockStorageSpecInSyncWithCMP,
		ACondition:     cmpBlockStorageIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 12. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeBlockStorageShouldUpdate,
		ACondition:     cmpBlockStorageIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 13. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:        cmpBlockStorageIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *aruba.BlockStorage](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 14. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 15. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 16. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:        cmpBlockStorageNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.BlockStorage, *aruba.BlockStorage](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.BlockStorage, *aruba.BlockStorage]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		ACondition:     cmpBlockStorageIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.BlockStorage, *aruba.BlockStorage],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.BlockStorage, *aruba.BlockStorage],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeBlockStorageHasDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	if !kubeBS.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpBS == nil {
		return false
	}
	return checkBlockStorageDeniedChanges(kubeBS, cmpBS) != nil
}

func kubeBlockStorageSpecInSyncWithCMP(kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeBS, cmpBS) &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		!kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

func kubeBlockStorageShouldUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeBS, cmpBS) &&
		checkBlockStorageDeniedChanges(kubeBS, cmpBS) == nil &&
		kubeBlockStorageNeedsUpdate(kubeBS, cmpBS)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpBlockStorageNotExists(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return cmpBS == nil
}

func cmpBlockStorageIsFinal(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return cmpBS != nil && reconciler.IsFinalState(cmpBS.State())
}

func cmpBlockStorageIsTransitory(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return cmpBS != nil && cmpBS.State().IsTransitory()
}

func cmpBlockStorageNotExistsOrTransitory(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return cmpBS == nil || cmpBS.State().IsTransitory()
}

func cmpBlockStorageIsActive(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	if cmpBS == nil {
		return false
	}
	switch cmpBS.State() {
	case aruba.StateActive, aruba.StateNotUsed, aruba.StateInUse, aruba.StateUsed:
		return true
	default:
		return false
	}
}

func cmpBlockStorageIsFailed(_ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	return cmpBS != nil && cmpBS.State().IsFailure()
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *BlockStorageReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeBS *v1alpha1.BlockStorage, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.BlockStorage){
		func(bs *v1alpha1.BlockStorage) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID == "" {
				bs.Status.ProjectID = prjID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeBS, phase, reason, nil, prePatches...)
}

func (r *BlockStorageReconciler) kubeMarkToDelete(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeleting(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeletingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkDeleted(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkToUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkUpdating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeMarkToCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *BlockStorageReconciler) kubeMarkCreating(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *BlockStorageReconciler) kubeMarkCreatingDone(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *BlockStorageReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) error {
	cmpID := ""
	if cmpBS != nil {
		cmpID = cmpBS.ID()
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeBS, cmpID, nil, func(bs *v1alpha1.BlockStorage) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID != "" {
			bs.Status.ProjectID = prjID
		}
	})
}

func (r *BlockStorageReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeBS, func(bs *v1alpha1.BlockStorage) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && bs.Status.ProjectID == "" {
			bs.Status.ProjectID = prjID
		}
	})
}

func (r *BlockStorageReconciler) kubeSetFailed(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeBS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *BlockStorageReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	// The fetched wrapper is itself a Ref (carries its project and volume IDs).
	err := arubaClient.FromStorage().Volumes().Delete(ctx, cmpBS)
	return reconciler.CMPErrorFromResult("delete", cmpBS.Name(), err, http.StatusNotFound)
}

func (r *BlockStorageReconciler) cmpUpdate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkBlockStorageDeniedChanges(kubeBS, cmpBS); err != nil {
		return err
	}

	// Mutate only the operator-owned mutable fields on the fetched wrapper; it
	// retains its server-assigned ID, project, region, type, image and bootable flag.
	cmpBS.SizedGB(int(kubeBS.Spec.SizeGB)).
		BilledBy(aruba.BillingPeriod(kubeBS.Spec.BillingPeriod)).
		InZone(aruba.Zone(kubeBS.Spec.Zone)).
		RetaggedAs(kubeBS.Spec.Tags...)
	_, err := arubaClient.FromStorage().Volumes().Update(ctx, cmpBS)
	return reconciler.CMPErrorFromResult("update", kubeBS.Name, err)
}

func (r *BlockStorageReconciler) cmpCreate(ctx context.Context, kubeBS *v1alpha1.BlockStorage, _ *aruba.BlockStorage) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	volume := aruba.NewBlockStorage().
		InProject(aruba.URI("/projects/" + prjID)).
		Named(kubeBS.Name).
		Tagged(kubeBS.Spec.Tags...).
		InRegion(aruba.Region(kubeBS.Spec.Region)).
		InZone(aruba.Zone(kubeBS.Spec.Zone)).
		SizedGB(int(kubeBS.Spec.SizeGB)).
		BilledBy(aruba.BillingPeriod(kubeBS.Spec.BillingPeriod)).
		OfType(aruba.BlockStorageType(kubeBS.Spec.Type))
	if kubeBS.Spec.Bootable {
		volume.AsBootable()
	} else {
		volume.NotBootable()
	}
	if kubeBS.Spec.Image != "" {
		volume.FromImage(kubeBS.Spec.Image)
	}

	_, err := arubaClient.FromStorage().Volumes().Create(ctx, volume)
	return reconciler.CMPErrorFromResult("create", kubeBS.Name, err)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func checkBlockStorageDeniedChanges(kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) error {
	if cmpBS == nil {
		return nil
	}

	var errs []error

	if cmpBS.SizeGB() > int(kubeBS.Spec.SizeGB) {
		errs = append(errs, errors.New("decreasing the 'size' is not allowed"))
	}
	if kubeBS.Spec.Bootable != cmpBS.IsBootable() {
		errs = append(errs, errors.New("change the 'bootable' is not allowed"))
	}
	if cmpBS.Image() != "" && kubeBS.Spec.Image != cmpBS.Image() {
		errs = append(errs, errors.New("change the 'image' is not allowed"))
	}
	if kubeBS.Spec.Type != string(cmpBS.Type()) {
		errs = append(errs, errors.New("change the 'type' is not allowed"))
	}
	if kubeBS.Spec.Region != string(cmpBS.Region()) {
		errs = append(errs, errors.New("change the 'location' is not allowed"))
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.Join(errs...))
	}
	return nil
}

func kubeBlockStorageNeedsUpdate(kubeBS *v1alpha1.BlockStorage, cmpBS *aruba.BlockStorage) bool {
	if cmpBS == nil {
		return false
	}
	return kubeBS.Spec.BillingPeriod != string(cmpBS.BillingPeriod()) ||
		kubeBS.Spec.Zone != string(cmpBS.Zone()) ||
		kubeBS.Spec.SizeGB != int32(cmpBS.SizeGB()) || //nolint:gosec // disk size in GB always fits int32
		!reconciler.TagsAreEqual(kubeBS.Spec.Tags, cmpBS.Tags())
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *BlockStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BlockStorage{}).
		Named("blockstorage").
		Complete(r)
}
