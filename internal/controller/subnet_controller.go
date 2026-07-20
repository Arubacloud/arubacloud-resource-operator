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

	"github.com/Arubacloud/sdk-go/pkg/aruba"

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
	CMPVpc *aruba.VPC // from the VPC list fetch
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
	ivs *reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *kubeSubnetBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.Subnet, *aruba.Subnet]
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
	arubaClient aruba.Client,
	isDeleting bool,
) (cmpSubnet *aruba.Subnet, cmpVpc *aruba.VPC, prjID string, vpcID string, result ctrl.Result, err error) {
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
		cmpProjectList, listErr := arubaClient.FromProject().List(ctx, aruba.WithFilter(prjFilter))
		if listErr != nil {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				listErr, projectName, prjFilter,
			)
		}
		cmpProjects := cmpProjectList.Items()
		if len(cmpProjects) == 0 && kubeSubnet.Status.ProjectID != "" {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}
		if len(cmpProjects) > 1 {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				len(cmpProjects), projectName, prjFilter,
			)
		}
		if len(cmpProjects) == 0 {
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return nil, nil, "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		prjID = cmpProjects[0].ID()
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
		cmpVpcList, listErr := arubaClient.FromNetwork().VPCs().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(vpcFilter))
		if listErr != nil && !isCMPNotFound(listErr) {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
				listErr, vpcName, vpcFilter, projectName,
			)
		}
		// Client-side name filter workaround: the CMP API ignores name:eq() on
		// network-domain List endpoints (issue https://jira.aruba.it/browse/DEV-66643).
		var cmpVpcs []*aruba.VPC
		if cmpVpcList != nil {
			cmpVpcs = filterByName(cmpVpcList.Items(), vpcName, func(v *aruba.VPC) string { return v.Name() })
		}
		if len(cmpVpcs) == 0 && kubeSubnet.Status.VPCID != "" {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, vpc not found: vpc_name: '%s', vpc_filter: '%s'", vpcName, vpcFilter,
			)
		}
		if len(cmpVpcs) > 1 {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s', vpc_filter: '%s'",
				len(cmpVpcs), vpcName, vpcFilter,
			)
		}
		if len(cmpVpcs) == 0 {
			logger.V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
			return nil, nil, "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		cmpVpc = cmpVpcs[0]
		vpcID = cmpVpc.ID()
	}

	if kubeSubnet.Status.VPCID != "" && kubeSubnet.Status.VPCID != vpcID {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in subnet: subnet_name: '%s', subnet_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			subnetName, kubeSubnet.Status.VPCID, vpcName, vpcID,
		)
	}

	// Stage 3c: Fetch CMP Subnet.
	cmpSubnetList, listErr := arubaClient.FromNetwork().Subnets().List(ctx, aruba.VPCRef(prjID, vpcID), aruba.WithFilter(subnetFilter))
	if listErr != nil && !isCMPNotFound(listErr) {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"failed to find subnet in Aruba cloud: %w, subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s'",
			listErr, subnetName, subnetFilter, projectName, vpcName,
		)
	}
	// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
	var cmpSubnets []*aruba.Subnet
	if cmpSubnetList != nil {
		cmpSubnets = filterByName(cmpSubnetList.Items(), subnetName, func(s *aruba.Subnet) string { return s.Name() })
	}
	if len(cmpSubnets) > 1 {
		return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in subnet list: subnet_name: '%s', subnet_filter: '%s', project_name: '%s', vpc_name: '%s', instances: %d",
			subnetName, subnetFilter, projectName, vpcName, len(cmpSubnets),
		)
	}

	if len(cmpSubnets) == 1 {
		cmpSubnet = cmpSubnets[0]
	}
	logger.V(1).Info("CMP Subnet state", "found", cmpSubnet != nil, "projectID", prjID, "vpcID", vpcID)

	return cmpSubnet, cmpVpc, prjID, vpcID, ctrl.Result{}, nil
}

// newIntentionValidationSet returns K8s-only validation rules that run at Stage 4,
// before any CMP calls. All rules nil-guard bundle fields to handle an empty bundle.
func (r *SubnetReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *kubeSubnetBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *kubeSubnetBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.Subnet, _ *aruba.Subnet, _ *kubeSubnetBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.Subnet, _ *aruba.Subnet, _ *kubeSubnetBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	// 2. Cross-resource rules (nil-guarded — VPC/Project may not be resolved yet)
	ivs.Add("TenantMustMatchVPC", func(k *v1alpha1.Subnet, _ *aruba.Subnet, b *kubeSubnetBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with VPC: %q != %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	ivs.Add("ProjectMustMatchVPC", func(k *v1alpha1.Subnet, _ *aruba.Subnet, b *kubeSubnetBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != "" && b.KubeVpc.Spec.ProjectReference.Name != "" && k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference mismatch with VPC: %q != %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.Subnet, _ *aruba.Subnet, b *kubeSubnetBundle) error {
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

func (r *SubnetReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle]{}
	vs.Add("TenantMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle](
		"tenant",
		func(k *v1alpha1.Subnet) string { return k.Spec.Tenant },
		func(b *subnetBundle) string { return b.KubeVpc.Spec.Tenant },
		"VPC",
	))
	vs.Add("ProjectMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle](
		"project reference",
		func(k *v1alpha1.Subnet) string { return k.Spec.ProjectReference.Name },
		func(b *subnetBundle) string { return b.KubeVpc.Spec.ProjectReference.Name },
		"VPC",
	))
	vs.Add("TenantMustMatchProject", func(k *v1alpha1.Subnet, _ *aruba.Subnet, b *subnetBundle) error {
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

func (r *SubnetReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.Subnet, *aruba.Subnet] {
	ts := &reconciler.TransitionSet[*v1alpha1.Subnet, *aruba.Subnet]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *aruba.Subnet],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *aruba.Subnet],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.Subnet, *aruba.Subnet](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.Subnet, *aruba.Subnet],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.Subnet, *aruba.Subnet](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *aruba.Subnet],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:        cmpSubnetIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *aruba.Subnet](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 6. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:       "HasDeniedChanges",
		KCondition: kubeSubnetHasDeniedChanges,
		ACondition: cmpSubnetIsFinal,
		KAction: func(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) error {
			return fmt.Errorf("subnet update rejected: %w", checkSubnetDeniedChanges(kubeSubnet, cmpSubnet))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeSubnetSpecInSyncWithCMP,
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 12. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeSubnetShouldUpdate,
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 13. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:        cmpSubnetIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *aruba.Subnet](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 14. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 15. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 16. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:        cmpSubnetNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.Subnet, *aruba.Subnet](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.Subnet, *aruba.Subnet]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.Subnet, *aruba.Subnet],
		ACondition:     cmpSubnetIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.Subnet, *aruba.Subnet],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.Subnet, *aruba.Subnet],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeSubnetHasDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	if !kubeSubnet.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSubnet == nil {
		return false
	}
	return checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) != nil
}

func kubeSubnetSpecInSyncWithCMP(kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		!kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

func kubeSubnetShouldUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSubnet, cmpSubnet) &&
		checkSubnetDeniedChanges(kubeSubnet, cmpSubnet) == nil &&
		kubeSubnetNeedsUpdate(kubeSubnet, cmpSubnet)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpSubnetNotExists(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet == nil
}

func cmpSubnetIsFinal(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet != nil && reconciler.IsFinalState(cmpSubnet.State())
}

func cmpSubnetIsTransitory(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet != nil && cmpSubnet.State().IsTransitory()
}

func cmpSubnetNotExistsOrTransitory(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet == nil || cmpSubnet.State().IsTransitory()
}

func cmpSubnetIsActive(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet != nil && cmpSubnet.State() == aruba.StateActive
}

func cmpSubnetIsFailed(_ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	return cmpSubnet != nil && cmpSubnet.State().IsFailure()
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

func (r *SubnetReconciler) kubeMarkToDelete(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkDeleting(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkDeleted(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkUpdating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeMarkToCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SubnetReconciler) kubeMarkCreating(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SubnetReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SubnetReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) error {
	cmpID := ""
	if cmpSubnet != nil {
		cmpID = cmpSubnet.ID()
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

func (r *SubnetReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeSubnet, func(subnet *v1alpha1.Subnet) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && subnet.Status.ProjectID == "" {
			subnet.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && subnet.Status.VPCID == "" {
			subnet.Status.VPCID = vID
		}
	})
}

func (r *SubnetReconciler) kubeSetFailed(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSubnet, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *SubnetReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	err := arubaClient.FromNetwork().Subnets().Delete(ctx, cmpSubnet)
	return reconciler.CMPErrorFromResult("delete", cmpSubnet.Name(), err, http.StatusNotFound)
}

func (r *SubnetReconciler) cmpUpdate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSubnetDeniedChanges(kubeSubnet, cmpSubnet); err != nil {
		return err
	}

	// Mutable fields are tags and DHCP-enabled; type/CIDR/default round-trip from
	// the fetched wrapper unchanged.
	cmpSubnet.RetaggedAs(kubeSubnet.Spec.Tags...).WithDHCP(subnetDHCP(kubeSubnet))
	_, err := arubaClient.FromNetwork().Subnets().Update(ctx, cmpSubnet)
	return reconciler.CMPErrorFromResult("update", kubeSubnet.Name, err)
}

func (r *SubnetReconciler) cmpCreate(ctx context.Context, kubeSubnet *v1alpha1.Subnet, _ *aruba.Subnet) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	subnet := aruba.NewSubnet().
		InVPC(aruba.VPCRef(prjID, vID)).
		Named(kubeSubnet.Name).
		Tagged(kubeSubnet.Spec.Tags...).
		InRegion(aruba.Region(kubeSubnet.Spec.Region)).
		OfType(aruba.SubnetType(kubeSubnet.Spec.Type)).
		WithCIDR(kubeSubnet.Spec.CIDR).
		NotDefault().
		WithDHCP(subnetDHCP(kubeSubnet))
	_, err := arubaClient.FromNetwork().Subnets().Create(ctx, subnet)
	return reconciler.CMPErrorFromResult("create", kubeSubnet.Name, err)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

// subnetDHCP builds the DHCP sub-block from the operator-managed Enabled flag.
// The operator does not manage DHCP ranges, routes or DNS servers.
func subnetDHCP(kubeSubnet *v1alpha1.Subnet) *aruba.SubnetDHCPCommon {
	dhcp := aruba.NewSubnetDHCP()
	if kubeSubnet.Spec.DHCP.Enabled {
		dhcp.Enabled()
	}
	return dhcp
}

func checkSubnetDeniedChanges(kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) error {
	if cmpSubnet == nil {
		return nil
	}
	if kubeSubnet.Spec.Region != string(cmpSubnet.Region()) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}
	if cmpSubnet.CIDR() != "" && kubeSubnet.Spec.CIDR != cmpSubnet.CIDR() {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'network.address' is not allowed"))
	}
	if string(cmpSubnet.Type()) != "" && kubeSubnet.Spec.Type != string(cmpSubnet.Type()) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'type' is not allowed"))
	}
	return nil
}

func kubeSubnetNeedsUpdate(kubeSubnet *v1alpha1.Subnet, cmpSubnet *aruba.Subnet) bool {
	if cmpSubnet == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeSubnet.Spec.Tags, cmpSubnet.Tags()) {
		return true
	}
	if dhcp := cmpSubnet.DHCP(); dhcp != nil && kubeSubnet.Spec.DHCP.Enabled != dhcp.IsEnabled() {
		return true
	}
	return false
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
