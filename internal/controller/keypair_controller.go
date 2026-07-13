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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	arubaclient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	keyPairFinalizerName = "keypair.arubacloud.com/finalizer"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeKeyPairBundle struct {
	KubeProject *v1alpha1.Project // from resolveOwnerObject (already fetched for ownership)
}

type keyPairBundle struct {
	kubeKeyPairBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

// KeyPairReconciler reconciles a KeyPair object
type KeyPairReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *kubeKeyPairBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *keyPairBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewKeyPairReconciler creates a new KeyPairReconciler
func NewKeyPairReconciler(baseReconciler *reconciler.Reconciler) *KeyPairReconciler {
	r := &KeyPairReconciler{
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

func (r *KeyPairReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *KeyPairReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.KeyPair{}
}

func (r *KeyPairReconciler) Finalizer() string {
	return keyPairFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *KeyPairReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeKp, ok := obj.(*v1alpha1.KeyPair)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.KeyPair")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeKp.Spec.Tenant)
	logger.Info("reconciling key pair")

	isDeleting := !kubeKp.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeKp, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeKp.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeProject) {
		logger.V(1).Info("parent project not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeKeyPairBundle{}
		}
		if validationErr := r.ivs.Run(kubeKp, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeKp) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeKp.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeKp) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeKp.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeKp.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Fetch CMP dependencies.
	prjID, cmpKp, result, err := r.fetchCMPDependencies(ctx, kubeKp, arubaClient, isDeleting)
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
	if !isDeleting && kubeBdl != nil && cmpKp != nil {
		if validationErr := r.vs.Run(kubeKp, cmpKp, &keyPairBundle{kubeKeyPairBundle: *kubeBdl}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(kp *v1alpha1.KeyPair) {
					if kp.Status.ProjectID == "" {
						kp.Status.ProjectID = prjID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeKp) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeKp, cmpKp)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

// fetchKubeDependencies fetches the parent Project and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the project is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *KeyPairReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeKp *v1alpha1.KeyPair,
	isDeleting bool,
) (*kubeKeyPairBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kp := &v1alpha1.Project{}
	if err := resolveOwnerObject(ctx, r.Client, kubeKp.Spec.ProjectReference, kubeKp.Namespace, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
		}
		logger.V(1).Info("parent project not found for owner reference setup, skipping",
			"projectName", kubeKp.Spec.ProjectReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeKp)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on keypair: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	return &kubeKeyPairBundle{KubeProject: kp}, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID and fetches the CMP KeyPair representation.
// Returns (prjID, nil cmpKp, zero result, nil) when the KeyPair does not yet exist on CMP.
func (r *KeyPairReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeKp *v1alpha1.KeyPair,
	arubaClient arubaclient.Client,
	isDeleting bool,
) (string, *arubatypes.KeyPairResponse, ctrl.Result, error) {
	kpName, projectName := kubeKp.Name, kubeKp.Spec.ProjectReference.Name
	kpFilter := fmt.Sprintf(`name:eq("%s")`, kpName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if isDeleting && kubeKp.Status.ProjectID != "" {
		prjID = kubeKp.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeKp.Status.ProjectID != "" {
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

	if kubeKp.Status.ProjectID != "" && kubeKp.Status.ProjectID != prjID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in keypair: kp_name: '%s', kp_project_id: '%s', project_name: '%s', project_id: '%s'",
			kpName, kubeKp.Status.ProjectID, projectName, prjID,
		)
	}

	cmpKpList, err := arubaClient.FromCompute().KeyPairs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &kpFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: %w, kp_name: '%s', kp_filter: '%s', project_name: '%s'",
			err, kpName, kpFilter, projectName,
		)
	}
	if cmpKpList.IsError() && cmpKpList.StatusCode != http.StatusNotFound {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: status_code: %d, kp_name: '%s', project_name: '%s'",
			cmpKpList.StatusCode, kpName, projectName,
		)
	}

	if !cmpKpList.IsError() && (cmpKpList.Data.Total < 0 || cmpKpList.Data.Total > 1) {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in keypair list: kp_name: '%s', kp_filter: '%s', project_name: '%s', instances: %d",
			kpName, kpFilter, projectName, cmpKpList.Data.Total,
		)
	}

	var cmpKp *arubatypes.KeyPairResponse
	if cmpKpList.Data != nil && cmpKpList.Data.Total == 1 {
		cmpKp = &cmpKpList.Data.Values[0]
	}
	log.FromContext(ctx).V(1).Info("CMP key pair state", "found", cmpKp != nil, "projectID", prjID)
	return prjID, cmpKp, ctrl.Result{}, nil
}

func (r *KeyPairReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *kubeKeyPairBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *kubeKeyPairBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse, _ *kubeKeyPairBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	// 2. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse, b *kubeKeyPairBundle) error {
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

func (r *KeyPairReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *keyPairBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *keyPairBundle]{}
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse, *keyPairBundle](
		"tenant",
		func(k *v1alpha1.KeyPair) string { return k.Spec.Tenant },
		func(b *keyPairBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	return vs
}

func (r *KeyPairReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 3. ShouldBeDeleted — DeletionTimestamp set + active → mark Deleting+ShallSynchronize
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 5. ShouldBeDeletedOnCMP — marked Deleting+ShallSynchronize + CMP exists → dispatch delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:        cmpKeyPairExists,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 6. DeletionOnCMPNotNeeded — marked Deleting+ShallSynchronize but CMP already gone
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 7. WaitingDeletionOnCMP — marked Deleting+Synchronizing + CMP still exists → poll
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		Requeue:        reconciler.LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 8. DeletionConfirmedOnCMP — marked Deleting+Synchronizing + CMP gone → advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 9. DeletionAccomplished — marked Deleting+Synchronized + CMP gone → mark Deleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 10. ShouldBeUpdated — generation changed while Active → enter Updating phase
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     reconciler.KubeActiveAndGenerationChanged[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 11. UpdateNotSupported — Updating+ShallSynchronize + CMP exists → signal failure
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "UpdateNotSupported",
		KCondition:     reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeMarkUpdatingFailed,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 12. UpdateRollback — Updating+Failed + CMP exists → rollback spec and return to Active
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "UpdateRollback",
		KCondition:     kubeKeyPairUpdatingFailed,
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeRollbackSpecAndSetActive,
		Requeue:        reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 13. ShouldBeCreated — first reconciliation + CMP not found → mark Creating+ShallSynchronize
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 14. ShouldBeCreatedInCMP — Creating+ShallSynchronize + CMP not found → dispatch create
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:        cmpKeyPairNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 15. WaitingCreationInCMP — Creating+Synchronizing + CMP not found yet → poll
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairNotExists,
		Requeue:        reconciler.LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 16. CreationConfirmedOnCMP — Creating+Synchronizing + CMP found → mark Creating+Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 17. CreationAccomplished — Creating+Synchronized + CMP found → set Active + store ResourceID
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition:     cmpKeyPairExists,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

// kubeKeyPairUpdatingFailed returns true when the resource is in Updating phase
// with a Failed condition reason, indicating a rollback is needed.
func kubeKeyPairUpdatingFailed(kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) bool {
	if !kubeKp.GetDeletionTimestamp().IsZero() {
		return false
	}
	rs := kubeKp.GetResourceStatus()
	if rs == nil || rs.Phase != v1alpha1.ResourcePhaseUpdating {
		return false
	}
	condition := meta.FindStatusCondition(rs.Conditions, string(v1alpha1.ResourcePhaseUpdating))
	return condition != nil &&
		condition.Status == metav1.ConditionTrue &&
		condition.Reason == v1alpha1.ConditionReasonFailed
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpKeyPairExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp != nil
}

func cmpKeyPairNotExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp == nil
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *KeyPairReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeKp *v1alpha1.KeyPair, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	prePatches := []func(*v1alpha1.KeyPair){
		func(kp *v1alpha1.KeyPair) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
				kp.Status.ProjectID = prjID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp, phase, reason, actionErr, prePatches...)
}

func (r *KeyPairReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeKp, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
}

func (r *KeyPairReconciler) kubeMarkToDelete(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *KeyPairReconciler) kubeMarkDeleting(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *KeyPairReconciler) kubeMarkDeletingDone(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeMarkDeleted(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeMarkToUpdate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

// kubeMarkUpdatingFailed sets the Updating phase with a Failed reason, signalling
// that the update is not supported for KeyPair resources.
func (r *KeyPairReconciler) kubeMarkUpdatingFailed(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonFailed,
		errors.New("updating KeyPair resources is not supported"))
}

// kubeRollbackSpecAndSetActive restores the spec fields from the CMP response and
// then sets the resource back to Active phase.
func (r *KeyPairReconciler) kubeRollbackSpecAndSetActive(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	// Step 1: rollback spec to match CMP values (object patch, not status patch)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		kpCopy := kubeKp.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeKp), kpCopy); err != nil {
			return err
		}

		kpPatch := kpCopy.DeepCopy()
		kpPatch.Spec.Tags = cmpKp.Metadata.Tags
		if cmpKp.Metadata.LocationResponse != nil {
			kpPatch.Spec.Region = string(cmpKp.Metadata.LocationResponse.Value)
		}
		kpPatch.Spec.Value = cmpKp.Properties.Value

		return r.Patch(ctx, kpPatch, client.MergeFrom(kpCopy))
	}); err != nil {
		return fmt.Errorf("failed to rollback keypair '%s' spec: %w", kubeKp.Name, err)
	}

	// Step 2: set Active — reads fresh object (with new generation from spec patch)
	return r.kubeSetActiveAndSetID(ctx, kubeKp, cmpKp)
}

func (r *KeyPairReconciler) kubeMarkToCreate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *KeyPairReconciler) kubeMarkCreating(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *KeyPairReconciler) kubeMarkCreatingDone(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeKp, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *KeyPairReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	cmpID := ""
	if cmpKp != nil && cmpKp.Metadata.ID != nil {
		cmpID = *cmpKp.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeKp, cmpID, nil, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *KeyPairReconciler) cmpDelete(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpKpResp, err := arubaClient.FromCompute().KeyPairs().Delete(ctx, prjID, *cmpKp.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpKp.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpKp.Metadata.Name, cmpKpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *KeyPairReconciler) cmpCreate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpKpResp, err := arubaClient.FromCompute().KeyPairs().Create(ctx, prjID, cmpKeyPairRequestFromKube(kubeKp), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeKp.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeKp.Name, cmpKpResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func cmpKeyPairRequestFromKube(kubeKp *v1alpha1.KeyPair) arubatypes.KeyPairRequest {
	return arubatypes.KeyPairRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeKp.Name,
				Tags: kubeKp.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{
				Value: arubatypes.Region(kubeKp.Spec.Region),
			},
		},
		Properties: arubatypes.KeyPairPropertiesRequest{
			Value: kubeKp.Spec.Value,
		},
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *KeyPairReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KeyPair{}).
		Named("keypair").
		Complete(r)
}
