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
	subnetFinalizerName            = "subnet.arubacloud.com/finalizer"
	vpcIDKey            contextKey = "vpcID"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeSubnetBundle struct {
	KubeVpc     *v1alpha1.VPC     // from resolveOwnerObject (already fetched for ownership)
	KubeProject *v1alpha1.Project // from additional K8s lookup for TenantMustMatchProject
}

type cmpSubnetBundle struct {
	CMPVpc *arubatypes.VPCResponse // from the VPC list fetch
}

type subnetBundle struct {
	kubeSubnetBundle
	cmpSubnetBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *kubeSubnetBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *subnetBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewSubnetReconciler creates a new SubnetReconciler
func NewSubnetReconciler(baseReconciler *reconciler.Reconciler) *SubnetReconciler {
	r := &SubnetReconciler{
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

func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SubnetReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.Subnet{}
}

func (r *SubnetReconciler) Finalizer() string {
	return subnetFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SubnetReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeSubnet, ok := obj.(*v1alpha1.Subnet)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.Subnet")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSubnet.Spec.Tenant)
	logger.Info("reconciling Subnet")

	isDeleting := !kubeSubnet.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeSubnet, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeSubnet.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeVpc) {
		logger.V(1).Info("parent VPC not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeSubnetBundle{}
		}
		if validationErr := r.ivs.Run(kubeSubnet, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeSubnet) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSubnet.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeSubnet) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSubnet.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeSubnet.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Resolve CMP dependencies.
	cmpSubnet, cmpVpc, prjID, vpcID, result, err := r.fetchCMPDependencies(ctx, kubeSubnet, arubaClient, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && kubeBdl != nil && cmpSubnet != nil && cmpVpc != nil {
		if validationErr := r.vs.Run(kubeSubnet, cmpSubnet, &subnetBundle{kubeSubnetBundle: *kubeBdl, cmpSubnetBundle: cmpSubnetBundle{CMPVpc: cmpVpc}}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(s *v1alpha1.Subnet) {
					if s.Status.ProjectID == "" {
						s.Status.ProjectID = prjID
					}
					if s.Status.VPCID == "" {
						s.Status.VPCID = vpcID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeSubnet) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeSubnet, cmpSubnet)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

// fetchKubeDependencies fetches the parent VPC and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the VPC is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *SubnetReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeSubnet *v1alpha1.Subnet,
	isDeleting bool,
) (*kubeSubnetBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kv := &v1alpha1.VPC{}
	if err := resolveOwnerObject(ctx, r.Client, kubeSubnet.Spec.VPCReference, kubeSubnet.Namespace, kv); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent vpc for owner reference: %w", err)
		}
		logger.V(1).Info("parent vpc not found for owner reference setup, skipping",
			"vpcName", kubeSubnet.Spec.VPCReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kv, kubeSubnet)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on subnet: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	bdl := &kubeSubnetBundle{KubeVpc: kv}
	kp := &v1alpha1.Project{}
	if err := r.Get(ctx, client.ObjectKey{Name: kubeSubnet.Spec.ProjectReference.Name, Namespace: kubeSubnet.Namespace}, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("fetching k8s project %q for validation: %w", kubeSubnet.Spec.ProjectReference.Name, err)
		}
	} else {
		bdl.KubeProject = kp
	}
	return bdl, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID, VPC ID, and fetches the CMP subnet.
//
//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SubnetReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeSubnet *v1alpha1.Subnet,
	arubaClient arubaclient.Client,
	isDeleting bool,
) (cmpSubnet *arubatypes.SubnetResponse, cmpVpc *arubatypes.VPCResponse, prjID string, vpcID string, result ctrl.Result, err error) {
	logger := log.FromContext(ctx)

	subnetName := kubeSubnet.Name
	projectName := kubeSubnet.Spec.ProjectReference.Name
	vpcName := kubeSubnet.Spec.VPCReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	subnetFilter := fmt.Sprintf(`name:eq("%s")`, subnetName)

	// Stage 3a: Resolve Project ID.
	if isDeleting && kubeSubnet.Status.ProjectID != "" {
		prjID = kubeSubnet.Status.ProjectID
	} else {
		cmpProjectList, listErr := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
		if listErr != nil {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				listErr, projectName, prjFilter,
			)
		}
		if cmpProjectList.IsError() {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.StatusCode, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 && kubeSubnet.Status.ProjectID != "" {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total > 1 {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.Data.Total, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 {
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return nil, nil, "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if kubeSubnet.Status.ProjectID != "" && kubeSubnet.Status.ProjectID != prjID {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in subnet: subnet_name: '%s', subnet_project_id: '%s', project_name: '%s', project_id: '%s'",
			subnetName, kubeSubnet.Status.ProjectID, projectName, prjID,
		)
	}

	// Stage 3b: Resolve VPC ID.
	if isDeleting && kubeSubnet.Status.VPCID != "" {
		vpcID = kubeSubnet.Status.VPCID
	} else {
		cmpVpcList, listErr := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
		if listErr != nil {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
				listErr, vpcName, vpcFilter, projectName,
			)
		}
		if cmpVpcList.IsError() {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s', project_name: '%s'",
				cmpVpcList.StatusCode, vpcName, projectName,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToVPCList(cmpVpcList, vpcName, logger)
		if cmpVpcList.Data.Total == 0 && kubeSubnet.Status.VPCID != "" {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, vpc not found: vpc_name: '%s', vpc_filter: '%s'", vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total > 1 {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s', vpc_filter: '%s'",
				cmpVpcList.Data.Total, vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total == 0 {
			logger.V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
			return nil, nil, "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		cmpVpc = &cmpVpcList.Data.Values[0]
		vpcID = *(cmpVpc.Metadata.ID)
	}

	if kubeSubnet.Status.VPCID != "" && kubeSubnet.Status.VPCID != vpcID {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in subnet: subnet_name: '%s', subnet_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			subnetName, kubeSubnet.Status.VPCID, vpcName, vpcID,
		)
	}

	// Stage 3c: Fetch CMP Subnet.
	cmpSubnetList, listErr := arubaClient.FromNetwork().Subnets().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &subnetFilter})
	if listErr != nil {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"failed to find subnet in Aruba cloud: %w, subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s'",
			listErr, subnetName, subnetFilter, projectName, vpcName,
		)
	}
	if cmpSubnetList.IsError() && cmpSubnetList.StatusCode != http.StatusNotFound {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"failed to find subnet in Aruba cloud: status_code: %d, subnet_name: '%s', project_name: '%s', vpc_name: '%s'",
			cmpSubnetList.StatusCode, subnetName, projectName, vpcName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToSubnetList(cmpSubnetList, subnetName, logger)
	if !cmpSubnetList.IsError() && (cmpSubnetList.Data.Total < 0 || cmpSubnetList.Data.Total > 1) {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in subnet list: subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s', instances: %d",
			subnetName, subnetFilter, projectName, vpcName, cmpSubnetList.Data.Total,
		)
	}

	if cmpSubnetList.Data != nil && cmpSubnetList.Data.Total == 1 {
		cmpSubnet = &cmpSubnetList.Data.Values[0]
	}
	logger.V(1).Info("CMP Subnet state", "found", cmpSubnet != nil, "projectID", prjID, "vpcID", vpcID)

	return cmpSubnet, cmpVpc, prjID, vpcID, ctrl.Result{}, nil
}

// newIntentionValidationSet returns K8s-only validation rules that run at Stage 4,
// before any CMP calls. All rules nil-guard bundle fields to handle an empty bundle.
func (r *SubnetReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *kubeSubnetBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *kubeSubnetBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, _ *kubeSubnetBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, _ *kubeSubnetBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	// 2. Cross-resource rules (nil-guarded — VPC/Project may not be resolved yet)
	ivs.Add("TenantMustMatchVPC", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, b *kubeSubnetBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with VPC: %q != %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	ivs.Add("ProjectMustMatchVPC", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, b *kubeSubnetBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != "" && b.KubeVpc.Spec.ProjectReference.Name != "" && k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference mismatch with VPC: %q != %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, b *kubeSubnetBundle) error {
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

func (r *SubnetReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *subnetBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *subnetBundle]{}
	vs.Add("TenantMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *subnetBundle](
		"tenant",
		func(k *v1alpha1.Subnet) string { return k.Spec.Tenant },
		func(b *subnetBundle) string { return b.KubeVpc.Spec.Tenant },
		"VPC",
	))
	vs.Add("ProjectMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.Subnet, *arubatypes.SubnetResponse, *subnetBundle](
		"project reference",
		func(k *v1alpha1.Subnet) string { return k.Spec.ProjectReference.Name },
		func(b *subnetBundle) string { return b.KubeVpc.Spec.ProjectReference.Name },
		"VPC",
	))
	vs.Add("TenantMustMatchProject", func(k *v1alpha1.Subnet, _ *arubatypes.SubnetResponse, b *subnetBundle) error {
		if b.KubeProject == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeProject.Spec.Tenant != "" && k.Spec.Tenant != b.KubeProject.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with Project: %q != %q", k.Spec.Tenant, b.KubeProject.Spec.Tenant)
		}
		return nil
	})
	return vs
}

func (r *SubnetReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:        cmpSubnetIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 6. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:       "HasDeniedChanges",
		KCondition: kubeSubnetHasDeniedChanges,
		ACondition: cmpSubnetIsFinal,
		KAction: func(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
			return fmt.Errorf("subnet update rejected: %w", checkSubnetDeniedChanges(kubeSubnet, cmpSubnet))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeSubnetSpecInSyncWithCMP,
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 12. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeSubnetShouldUpdate,
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 13. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:        cmpSubnetIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 14. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 15. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 16. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:        cmpSubnetNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *arubatypes.SubnetResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *arubatypes.SubnetResponse]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		ACondition:     cmpSubnetIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *arubatypes.SubnetResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeSubnetHasDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if !kubeSubnet.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSubnet == nil {
		return false
	}
	return checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) != nil
}

func kubeSubnetSpecInSyncWithCMP(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		!kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

func kubeSubnetShouldUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpSubnetNotExists(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet == nil
}

func cmpSubnetIsFinal(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil || cmpSubnet.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSubnet.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpSubnetIsTransitory(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil || cmpSubnet.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSubnet.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSubnetNotExistsOrTransitory(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil {
		return true
	}
	if cmpSubnet.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSubnet.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSubnetIsActive(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet != nil && cmpSubnet.Status.State != nil &&
		*cmpSubnet.Status.State == arubatypes.StateActive
}

func cmpSubnetIsFailed(_ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	return cmpSubnet != nil && cmpSubnet.Status.State != nil && *cmpSubnet.Status.State == arubatypes.StateFailed
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *SubnetReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSubnet *v1alpha1.Subnet, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.Subnet){
		func(subnet *v1alpha1.Subnet) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID == "" {
				subnet.Status.ProjectID = prjID
			}
			if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID == "" {
				subnet.Status.VPCID = vID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSubnet, phase, reason, nil, prePatches...)
}

func (r *SubnetReconciler) kubeMarkToDelete(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkDeleting(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkDeleted(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkUpdating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkCreating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	cmpID := ""
	if cmpSubnet != nil && cmpSubnet.Metadata.ID != nil {
		cmpID = *cmpSubnet.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeSubnet, cmpID, nil, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID != "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID != "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeSubnet, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID == "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID == "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeSetFailed(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *SubnetReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Delete(ctx, prjID, vID, *cmpSubnet.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpSubnet.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpSubnet.Metadata.Name, cmpSubnetResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *SubnetReconciler) cmpUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSubnetDeniedChanges(kubeSubnet, cmpSubnet); err != nil {
		return err
	}

	request := buildSubnetUpdateRequest(kubeSubnet, cmpSubnet)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Update(ctx, prjID, vID, *cmpSubnet.Metadata.ID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeSubnet.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeSubnet.Name, cmpSubnetResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *SubnetReconciler) cmpCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *arubatypes.SubnetResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)

	cmpSubnetResp, err := arubaClient.FromNetwork().Subnets().Create(ctx, prjID, vID, *cmpSubnetRequestFromKube(kubeSubnet), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeSubnet.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeSubnet.Name, cmpSubnetResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func checkSubnetDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) error {
	if cmpSubnet == nil {
		return nil
	}

	locationValue := ""
	if cmpSubnet.Metadata.LocationResponse != nil {
		locationValue = string(cmpSubnet.Metadata.LocationResponse.Value)
	}
	if kubeSubnet.Spec.Region != locationValue {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}

	if cmpSubnet.Properties.Network != nil && kubeSubnet.Spec.CIDR != cmpSubnet.Properties.Network.Address {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'network.address' is not allowed"))
	}

	if string(cmpSubnet.Properties.Type) != "" && kubeSubnet.Spec.Type != string(cmpSubnet.Properties.Type) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'type' is not allowed"))
	}

	return nil
}

func kubeSubnetNeedsUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) bool {
	if cmpSubnet == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeSubnet.Spec.Tags, cmpSubnet.Metadata.Tags) {
		return true
	}
	if cmpSubnet.Properties.DHCP != nil && kubeSubnet.Spec.DHCP.Enabled != cmpSubnet.Properties.DHCP.Enabled {
		return true
	}
	return false
}

func buildSubnetUpdateRequest(kubeSubnet *v1alpha1.Subnet, cmpSubnet *arubatypes.SubnetResponse) *arubatypes.SubnetRequest {
	request := cmpSubnetRequestFromCMP(cmpSubnet)
	if request == nil {
		return nil
	}
	tags := make([]string, len(kubeSubnet.Spec.Tags))
	copy(tags, kubeSubnet.Spec.Tags)
	request.Metadata.Tags = tags
	if request.Properties.DHCP != nil {
		request.Properties.DHCP.Enabled = kubeSubnet.Spec.DHCP.Enabled
	} else {
		request.Properties.DHCP = &arubatypes.SubnetDHCPCommon{Enabled: kubeSubnet.Spec.DHCP.Enabled}
	}
	return request
}

func cmpSubnetRequestFromCMP(cmpSubnet *arubatypes.SubnetResponse) *arubatypes.SubnetRequest {
	if cmpSubnet == nil {
		return nil
	}
	name := ""
	if cmpSubnet.Metadata.Name != nil {
		name = *cmpSubnet.Metadata.Name
	}
	tags := make([]string, len(cmpSubnet.Metadata.Tags))
	copy(tags, cmpSubnet.Metadata.Tags)

	location := arubatypes.LocationRequest{}
	if cmpSubnet.Metadata.LocationResponse != nil {
		location.Value = cmpSubnet.Metadata.LocationResponse.Value
	}

	req := &arubatypes.SubnetRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.SubnetPropertiesRequest{
			Type:    cmpSubnet.Properties.Type,
			Default: &cmpSubnet.Properties.Default,
		},
	}
	if cmpSubnet.Properties.Network != nil {
		req.Properties.Network = &arubatypes.SubnetNetworkCommon{
			Address: cmpSubnet.Properties.Network.Address,
		}
	}
	if cmpSubnet.Properties.DHCP != nil {
		req.Properties.DHCP = &arubatypes.SubnetDHCPCommon{
			Enabled: cmpSubnet.Properties.DHCP.Enabled,
		}
	}
	return req
}

func cmpSubnetRequestFromKube(kubeSubnet *v1alpha1.Subnet) *arubatypes.SubnetRequest {
	tags := make([]string, len(kubeSubnet.Spec.Tags))
	copy(tags, kubeSubnet.Spec.Tags)
	defaultVal := false
	return &arubatypes.SubnetRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeSubnet.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: arubatypes.Region(kubeSubnet.Spec.Region)},
		},
		Properties: arubatypes.SubnetPropertiesRequest{
			Type:    arubatypes.SubnetType(kubeSubnet.Spec.Type),
			Default: &defaultVal,
			Network: &arubatypes.SubnetNetworkCommon{
				Address: kubeSubnet.Spec.CIDR,
			},
			DHCP: &arubatypes.SubnetDHCPCommon{
				Enabled: kubeSubnet.Spec.DHCP.Enabled,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Subnet{}).
		Named("subnet").
		Complete(r)
}
