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

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	keyPairFinalizerName = "keypair.arubacloud.com/finalizer"
)

// KeyPairReconciler reconciles a KeyPair object
type KeyPairReconciler struct {
	*reconciler.Reconciler
	ts *reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]
}

// NewKeyPairReconciler creates a new KeyPairReconciler
func NewKeyPairReconciler(baseReconciler *reconciler.Reconciler) *KeyPairReconciler {
	r := &KeyPairReconciler{
		Reconciler: baseReconciler,
	}

	r.ts = r.newTransitionSet()

	return r
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch

func (r *KeyPairReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *KeyPairReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.KeyPair{}
}

func (r *KeyPairReconciler) Finalizer() string {
	return keyPairFinalizerName
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *KeyPairReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeKp, ok := obj.(*v1alpha1.KeyPair)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.KeyPair")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeKp.Spec.Tenant)
	logger.Info("reconciling key pair")

	arubaClient, err := r.ArubaClient(kubeKp.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}
	if kubeKp.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}

	if kubeKp.GetDeletionTimestamp().IsZero() {
		kubeProject := &v1alpha1.Project{}
		if err := resolveOwnerObject(ctx, r.Client, kubeKp.Spec.ProjectReference, kubeKp.Namespace, kubeProject); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
			}
			logger.V(1).Info("parent project not found for owner reference setup, skipping", "projectName", kubeKp.Spec.ProjectReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeProject, kubeKp)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on keypair: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	kpName, projectName := kubeKp.Name, kubeKp.Spec.ProjectReference.Name
	kpFilter := fmt.Sprintf(`name:eq("%s")`, kpName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if !kubeKp.GetDeletionTimestamp().IsZero() && kubeKp.Status.ProjectID != "" {
		prjID = kubeKp.Status.ProjectID
	} else {
		cmpProjectList, err := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				err, projectName, prjFilter,
			)
		}
		if cmpProjectList.IsError() {
			return ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.StatusCode, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 && kubeKp.Status.ProjectID != "" {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}

		if cmpProjectList.Data.Total > 1 {
			return ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.Data.Total, projectName, prjFilter,
			)
		}

		if cmpProjectList.Data.Total == 0 {
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if kubeKp.Status.ProjectID != "" && kubeKp.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in keypair: kp_name: '%s', kp_project_id: '%s', project_name: '%s', project_id: '%s'",
			kpName, kubeKp.Status.ProjectID, projectName, prjID,
		)
	}

	cmpKpList, err := arubaClient.FromCompute().KeyPairs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &kpFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: %w, kp_name: '%s', kp_filter: '%s', project_name: '%s'",
			err, kpName, kpFilter, projectName,
		)
	}
	if cmpKpList.IsError() && cmpKpList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find keypair in Aruba cloud: status_code: %d, kp_name: '%s', project_name: '%s'",
			cmpKpList.StatusCode, kpName, projectName,
		)
	}

	if !cmpKpList.IsError() && (cmpKpList.Data.Total < 0 || cmpKpList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in keypair list: kp_name: '%s', kp_filter: '%s', project_name: '%s', instances: %d",
			kpName, kpFilter, projectName, cmpKpList.Data.Total,
		)
	}

	var cmpKp *arubatypes.KeyPairResponse
	if cmpKpList.Data != nil && cmpKpList.Data.Total == 1 {
		cmpKp = &cmpKpList.Data.Values[0]
	}

	logger.V(1).Info("CMP key pair state", "found", cmpKp != nil, "projectID", prjID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeKp, cmpKp)
}

// Transition Set Builder

func (r *KeyPairReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		DefaultRequeue: reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "PhaseTimedOut",
		KCondition: reconciler.KubePhaseTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction: r.kubeSetFailedOnTimeout,
		Requeue: reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 1. ShouldBeDeleted — DeletionTimestamp set + active → mark Deleting+ShallSynchronize
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldBeDeleted",
		KCondition: reconciler.KubeShouldDelete[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		KAction: r.kubeMarkToDelete,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 2. ShouldDeleteTimedOut — enter deletion flow for timed-out resources
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldDeleteTimedOut",
		KCondition: reconciler.KubeShouldDeleteTimedOut[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		KAction: r.kubeMarkToDelete,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 3. ShouldBeDeletedOnCMP — marked Deleting+ShallSynchronize + CMP exists → dispatch delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldBeDeletedOnCMP",
		KCondition: reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		AAction: r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError: reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.SmartRequeueOnError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 4. DeletionOnCMPNotNeeded — marked Deleting+ShallSynchronize but CMP already gone
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "DeletionOnCMPNotNeeded",
		KCondition: reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		KAction: r.kubeMarkDeletingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 5. WaitingDeletionOnCMP — marked Deleting+Synchronizing + CMP still exists → poll
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "WaitingDeletionOnCMP",
		KCondition: reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		Requeue: reconciler.LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 6. DeletionConfirmedOnCMP — marked Deleting+Synchronizing + CMP gone → advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "DeletionConfirmedOnCMP",
		KCondition: reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		KAction: r.kubeMarkDeletingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 7. DeletionAccomplished — marked Deleting+Synchronized + CMP gone → mark Deleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "DeletionAccomplished",
		KCondition: reconciler.KubeDeletionAccomplished[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		KAction: r.kubeMarkDeleted,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 8. ShouldBeUpdated — generation changed while Active → enter Updating phase
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldBeUpdated",
		KCondition: reconciler.KubeActiveAndGenerationChanged[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		KAction: r.kubeMarkToUpdate,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 9. UpdateNotSupported — Updating+ShallSynchronize + CMP exists → signal failure
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "UpdateNotSupported",
		KCondition: reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		KAction: r.kubeMarkUpdatingFailed,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 10. UpdateRollback — Updating+Failed + CMP exists → rollback spec and return to Active
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "UpdateRollback",
		KCondition: kubeKeyPairUpdatingFailed,
		ACondition: cmpKeyPairExists,
		KAction: r.kubeRollbackSpecAndSetActive,
		Requeue: reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 11. ShouldBeCreated — first reconciliation + CMP not found → mark Creating+ShallSynchronize
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldBeCreated",
		KCondition: reconciler.KubeIsFirstReconciliation[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		KAction: r.kubeMarkToCreate,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 12. ShouldBeCreatedInCMP — Creating+ShallSynchronize + CMP not found → dispatch create
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "ShouldBeCreatedInCMP",
		KCondition: reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		AAction: r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError: reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse](r.Client),
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.SmartRequeueOnError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 13. WaitingCreationInCMP — Creating+Synchronizing + CMP not found yet → poll
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "WaitingCreationInCMP",
		KCondition: reconciler.KubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairNotExists,
		Requeue: reconciler.LongRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 14. CreationConfirmedOnCMP — Creating+Synchronizing + CMP found → mark Creating+Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "CreationConfirmedOnCMP",
		KCondition: reconciler.KubeWaitingCreationInCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		KAction: r.kubeMarkCreatingDone,
		Requeue: reconciler.ShortRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	// 15. CreationAccomplished — Creating+Synchronized + CMP found → set Active + store ResourceID
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse]{
		Name: "CreationAccomplished",
		KCondition: reconciler.KubeIsCreatedOnCMP[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		ACondition: cmpKeyPairExists,
		KAction: r.kubeSetActiveAndSetID,
		Requeue: reconciler.NoRequeue[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.KeyPair, *arubatypes.KeyPairResponse],
	})

	return ts
}

// Resource-specific condition functions

func cmpKeyPairExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp != nil
}

func cmpKeyPairNotExists(_ *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) bool {
	return cmpKp == nil
}

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

// Kube action methods

func (r *KeyPairReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeKp *v1alpha1.KeyPair, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeKp, phase, reason, actionErr, func(kp *v1alpha1.KeyPair) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && kp.Status.ProjectID == "" {
			kp.Status.ProjectID = prjID
		}
	})
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
			kpPatch.Spec.Region = cmpKp.Metadata.LocationResponse.Value
		}
		kpPatch.Spec.Value = cmpKp.Properties.Value

		return r.Patch(ctx, kpPatch, client.MergeFrom(kpCopy))
	}); err != nil {
		return fmt.Errorf("failed to rollback keypair '%s' spec: %w", kubeKp.Name, err)
	}

	// Step 2: set Active — reads fresh object (with new generation from spec patch)
	return r.kubeSetActiveAndSetID(ctx, kubeKp, cmpKp)
}

// CMP action methods

func (r *KeyPairReconciler) cmpCreate(ctx context.Context, kubeKp *v1alpha1.KeyPair, _ *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpKpResp, err := arubaClient.FromCompute().KeyPairs().Create(ctx, prjID, cmpKeyPairRequestFromKube(kubeKp), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeKp.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeKp.Name, cmpKpResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

func (r *KeyPairReconciler) cmpDelete(ctx context.Context, kubeKp *v1alpha1.KeyPair, cmpKp *arubatypes.KeyPairResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpKpResp, err := arubaClient.FromCompute().KeyPairs().Delete(ctx, prjID, *cmpKp.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpKp.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpKp.Metadata.Name, cmpKpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

// Helper functions

func cmpKeyPairRequestFromKube(kubeKp *v1alpha1.KeyPair) arubatypes.KeyPairRequest {
	return arubatypes.KeyPairRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeKp.Name,
				Tags: kubeKp.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{
				Value: kubeKp.Spec.Region,
			},
		},
		Properties: arubatypes.KeyPairPropertiesRequest{
			Value: kubeKp.Spec.Value,
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *KeyPairReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.KeyPair{}).
		Named("keypair").
		Complete(r)
}
