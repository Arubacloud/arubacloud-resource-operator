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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
	vpcFinalizerName = "vpc.arubacloud.com/finalizer"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeVpcBundle struct {
	KubeProject *v1alpha1.Project // from resolveOwnerObject (already fetched for ownership)
}

type vpcBundle struct {
	kubeVpcBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// VPCReconciler reconciles a VPC object
type VPCReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *kubeVpcBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *vpcBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.VPC, *arubatypes.VPCResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewVPCReconciler creates a new VPCReconciler
func NewVPCReconciler(baseReconciler *reconciler.Reconciler) *VPCReconciler {
	r := &VPCReconciler{
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

func (r *VPCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *VPCReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.VPC{}
}

func (r *VPCReconciler) Finalizer() string {
	return vpcFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *VPCReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeVpc, ok := obj.(*v1alpha1.VPC)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.VPC")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeVpc.Spec.Tenant)
	logger.Info("reconciling VPC")

	isDeleting := !kubeVpc.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeVpc, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeVpc.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeProject) {
		logger.V(1).Info("parent project not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeVpcBundle{}
		}
		if validationErr := r.ivs.Run(kubeVpc, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeVpc) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeVpc.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeVpc) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeVpc.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba CMP client.
	arubaClient, err := r.ArubaClient(kubeVpc.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Fetch CMP dependencies.
	prjID, cmpVpc, result, err := r.fetchCMPDependencies(ctx, kubeVpc, arubaClient, isDeleting)
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
	if !isDeleting && kubeBdl != nil && cmpVpc != nil {
		if validationErr := r.vs.Run(kubeVpc, cmpVpc, &vpcBundle{kubeVpcBundle: *kubeBdl}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(vpc *v1alpha1.VPC) {
					if vpc.Status.ProjectID == "" {
						vpc.Status.ProjectID = prjID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeVpc) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeVpc, cmpVpc)
}

// fetchKubeDependencies fetches the parent Project and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the project is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *VPCReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeVpc *v1alpha1.VPC,
	isDeleting bool,
) (*kubeVpcBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kp := &v1alpha1.Project{}
	if err := resolveOwnerObject(ctx, r.Client, kubeVpc.Spec.ProjectReference, kubeVpc.Namespace, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
		}
		logger.V(1).Info("parent project not found for owner reference setup, skipping",
			"projectName", kubeVpc.Spec.ProjectReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeVpc)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on vpc: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	return &kubeVpcBundle{KubeProject: kp}, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID and fetches the CMP VPC representation.
// Returns (prjID, nil cmpVpc, zero result, nil) when the VPC does not yet exist on CMP.
func (r *VPCReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeVpc *v1alpha1.VPC,
	arubaClient aruba.Client,
	isDeleting bool,
) (string, *arubatypes.VPCResponse, ctrl.Result, error) {
	vpcName, projectName := kubeVpc.Name, kubeVpc.Spec.ProjectReference.Name
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	var prjID string

	if isDeleting && kubeVpc.Status.ProjectID != "" {
		prjID = kubeVpc.Status.ProjectID
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
		if cmpProjectList.Data.Total == 0 && kubeVpc.Status.ProjectID != "" {
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

	if kubeVpc.Status.ProjectID != "" && kubeVpc.Status.ProjectID != prjID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in vpc: vpc_name: '%s', vpc_project_id: '%s', project_name: '%s', project_id: '%s'",
			vpcName, kubeVpc.Status.ProjectID, projectName, prjID,
		)
	}

	cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
			err, vpcName, vpcFilter, projectName,
		)
	}
	if cmpVpcList.IsError() && cmpVpcList.StatusCode != http.StatusNotFound {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s', project_name: '%s'",
			cmpVpcList.StatusCode, vpcName, projectName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToVPCList(cmpVpcList, vpcName, log.FromContext(ctx))

	if !cmpVpcList.IsError() && (cmpVpcList.Data.Total < 0 || cmpVpcList.Data.Total > 1) {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in vpc list: vpc_name: '%s', vpc_filter: '%s', project_name: '%s', instances: %d",
			vpcName, vpcFilter, projectName, cmpVpcList.Data.Total,
		)
	}

	var cmpVpc *arubatypes.VPCResponse
	if cmpVpcList.Data != nil && cmpVpcList.Data.Total == 1 {
		cmpVpc = &cmpVpcList.Data.Values[0]
	}
	log.FromContext(ctx).V(1).Info("CMP VPC state", "found", cmpVpc != nil, "projectID", prjID)
	return prjID, cmpVpc, ctrl.Result{}, nil
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

func (r *VPCReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *kubeVpcBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *kubeVpcBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.VPC, _ *arubatypes.VPCResponse, _ *kubeVpcBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	// 2. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.VPC, _ *arubatypes.VPCResponse, b *kubeVpcBundle) error {
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

func (r *VPCReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *vpcBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.VPC, *arubatypes.VPCResponse, *vpcBundle]{}
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.VPC, *arubatypes.VPCResponse, *vpcBundle](
		"tenant",
		func(k *v1alpha1.VPC) string { return k.Spec.Tenant },
		func(b *vpcBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	return vs
}

func (r *VPCReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.VPC, *arubatypes.VPCResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.VPC, *arubatypes.VPCResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.VPC, *arubatypes.VPCResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 5. WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the VPC finalizer is present).
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name: "WaitingChildrenDeletion",
		KCondition: func(k *v1alpha1.VPC, a *arubatypes.VPCResponse) bool {
			return reconciler.KubeShouldBeDeletedOnCMP(k, a) && r.kubeVPCHasOwnedChildren(k, a)
		},
		ACondition:     cmpVpcIsFinal,
		KAction:        r.kubeVPCDeleteOwnedChildren,
		Requeue:        reconciler.LongRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.ShortRequeueAndIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 6. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:        cmpVpcIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.VPC, *arubatypes.VPCResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 7. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVPCNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 8. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 9. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVPCNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 10. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVPCNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 11. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:       "HasDeniedChanges",
		KCondition: kubeVPCHasDeniedChanges,
		ACondition: cmpVpcIsFinal,
		KAction: func(ctx context.Context, kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) error {
			return fmt.Errorf("vpc update rejected: %w", checkVpcDeniedChanges(kubeVpc, cmpVpc))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 12. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeVPCSpecInSyncWithCMP,
		ACondition:     cmpVpcIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 13. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeVPCShouldUpdate,
		ACondition:     cmpVpcIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 14. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:        cmpVpcIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.VPC, *arubatypes.VPCResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 15. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 16. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 17. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 18. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVPCNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 19. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:        cmpVPCNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.VPC, *arubatypes.VPCResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 20. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVPCNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 21. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 22. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	// 23. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.VPC, *arubatypes.VPCResponse]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		ACondition:     cmpVpcIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.VPC, *arubatypes.VPCResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.VPC, *arubatypes.VPCResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Owned-children helpers
// ---------------------------------------------------------------------------

// kubeVPCHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the VPC still exists. Used by the WaitingChildrenDeletion transition.
func (r *VPCReconciler) kubeVPCHasOwnedChildren(k *v1alpha1.VPC, _ *arubatypes.VPCResponse) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.SubnetList{},
		&v1alpha1.SecurityGroupList{},
	)
	if err != nil {
		ctrl.Log.Error(err, "checking owned children for vpc", "vpc", k.GetName())
		return true // conservative: assume children exist on error
	}
	return has
}

// kubeVPCDeleteOwnedChildren deletes all K8s children of the VPC that have not yet
// received a deletionTimestamp. Called by the WaitingChildrenDeletion action.
func (r *VPCReconciler) kubeVPCDeleteOwnedChildren(ctx context.Context, k *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	return deleteOwnedChildren(ctx, r.Client, k, labelKey,
		&v1alpha1.SubnetList{},
		&v1alpha1.SecurityGroupList{},
	)
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeVPCHasDeniedChanges(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	if !kubeVpc.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpVpc == nil {
		return false
	}
	return checkVpcDeniedChanges(kubeVpc, cmpVpc) != nil
}

func kubeVPCSpecInSyncWithCMP(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeVpc, cmpVpc) &&
		checkVpcDeniedChanges(kubeVpc, cmpVpc) == nil &&
		!kubeVpcNeedsUpdate(kubeVpc, cmpVpc)
}

func kubeVPCShouldUpdate(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeVpc, cmpVpc) &&
		checkVpcDeniedChanges(kubeVpc, cmpVpc) == nil &&
		kubeVpcNeedsUpdate(kubeVpc, cmpVpc)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpVPCNotExists(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc == nil
}

func cmpVpcIsFinal(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil || cmpVpc.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpVpc.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpVpcIsTransitory(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil || cmpVpc.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpVpc.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpVPCNotExistsOrTransitory(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil {
		return true
	}
	if cmpVpc.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpVpc.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpVpcIsActive(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc != nil && cmpVpc.Status.State != nil &&
		*cmpVpc.Status.State == reconciler.CSPResourceStateActive
}

func cmpVpcIsFailed(_ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	return cmpVpc != nil && cmpVpc.Status.State != nil && *cmpVpc.Status.State == reconciler.CSPResourceStateFailed
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *VPCReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeVpc *v1alpha1.VPC, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.VPC){
		func(vpc *v1alpha1.VPC) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID == "" {
				vpc.Status.ProjectID = prjID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeVpc, phase, reason, nil, prePatches...)
}

func (r *VPCReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeVpc, func(vpc *v1alpha1.VPC) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID == "" {
			vpc.Status.ProjectID = prjID
		}
	})
}

func (r *VPCReconciler) kubeSetFailed(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VPCReconciler) kubeMarkToDelete(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VPCReconciler) kubeMarkDeleting(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VPCReconciler) kubeMarkDeletingDone(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VPCReconciler) kubeMarkDeleted(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VPCReconciler) kubeMarkToUpdate(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VPCReconciler) kubeMarkUpdating(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VPCReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VPCReconciler) kubeMarkToCreate(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *VPCReconciler) kubeMarkCreating(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *VPCReconciler) kubeMarkCreatingDone(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeVpc, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *VPCReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) error {
	cmpID := ""
	if cmpVpc != nil && cmpVpc.Metadata.ID != nil {
		cmpID = *cmpVpc.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeVpc, cmpID, nil, func(vpc *v1alpha1.VPC) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && vpc.Status.ProjectID != "" {
			vpc.Status.ProjectID = prjID
		}
	})
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *VPCReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Delete(ctx, prjID, *cmpVpc.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpVpc.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpVpc.Metadata.Name, cmpVpcResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *VPCReconciler) cmpUpdate(ctx context.Context, kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkVpcDeniedChanges(kubeVpc, cmpVpc); err != nil {
		return err
	}

	request := buildVpcUpdateRequest(kubeVpc, cmpVpc)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Update(ctx, prjID, *cmpVpc.Metadata.ID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeVpc.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeVpc.Name, cmpVpcResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *VPCReconciler) cmpCreate(ctx context.Context, kubeVpc *v1alpha1.VPC, _ *arubatypes.VPCResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpVpcResp, err := arubaClient.FromNetwork().VPCs().Create(ctx, prjID, *cmpVpcRequestFromKube(kubeVpc), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeVpc.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeVpc.Name, cmpVpcResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func checkVpcDeniedChanges(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) error {
	if cmpVpc == nil {
		return nil
	}

	locationValue := ""
	if cmpVpc.Metadata.LocationResponse != nil {
		locationValue = cmpVpc.Metadata.LocationResponse.Value
	}
	if kubeVpc.Spec.Region != locationValue {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	return nil
}

func kubeVpcNeedsUpdate(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) bool {
	if cmpVpc == nil {
		return false
	}
	return !reconciler.TagsAreEqual(kubeVpc.Spec.Tags, cmpVpc.Metadata.Tags)
}

func buildVpcUpdateRequest(kubeVpc *v1alpha1.VPC, cmpVpc *arubatypes.VPCResponse) *arubatypes.VPCRequest {
	request := cmpVpcRequestFromCMP(cmpVpc)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeVpc.Spec.Tags))
	copy(tags, kubeVpc.Spec.Tags)
	request.Metadata.Tags = tags
	return request
}

func cmpVpcRequestFromCMP(cmpVpc *arubatypes.VPCResponse) *arubatypes.VPCRequest {
	if cmpVpc == nil {
		return nil
	}
	name := ""
	if cmpVpc.Metadata.Name != nil {
		name = *cmpVpc.Metadata.Name
	}
	tags := make([]string, len(cmpVpc.Metadata.Tags))
	copy(tags, cmpVpc.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if cmpVpc.Metadata.LocationResponse != nil {
		location.Value = cmpVpc.Metadata.LocationResponse.Value
	}
	return &arubatypes.VPCRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.VPCPropertiesRequest{},
	}
}

func cmpVpcRequestFromKube(kubeVpc *v1alpha1.VPC) *arubatypes.VPCRequest {
	falseVal := false
	return &arubatypes.VPCRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeVpc.Name,
				Tags: kubeVpc.Spec.Tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeVpc.Spec.Region},
		},
		Properties: arubatypes.VPCPropertiesRequest{
			Properties: &arubatypes.VPCProperties{
				Default: &falseVal,
				Preset:  &falseVal,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *VPCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.VPC{}).
		Watches(&v1alpha1.Subnet{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.Subnet); ok {
					return &v.Spec.VPCReference
				}
				return nil
			}))).
		Watches(&v1alpha1.SecurityGroup{}, handler.EnqueueRequestsFromMapFunc(
			childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
				if v, ok := o.(*v1alpha1.SecurityGroup); ok {
					return &v.Spec.VPCReference
				}
				return nil
			}))).
		Named("vpc").
		Complete(r)
}
