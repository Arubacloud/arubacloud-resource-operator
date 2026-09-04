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
	securityGroupFinalizerName = "securitygroup.arubacloud.com/finalizer"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeSecurityGroupBundle struct {
	KubeVpc     *v1alpha1.VPC     // from resolveOwnerObject (already fetched for ownership)
	KubeProject *v1alpha1.Project // from additional K8s lookup for TenantMustMatchProject
}

type cmpSecurityGroupBundle struct {
	CMPVpc *aruba.VPC // from the VPC list fetch
}

type securityGroupBundle struct {
	kubeSecurityGroupBundle
	cmpSecurityGroupBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *kubeSecurityGroupBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *securityGroupBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewSecurityGroupReconciler creates a new SecurityGroupReconciler
func NewSecurityGroupReconciler(baseReconciler *reconciler.Reconciler) *SecurityGroupReconciler {
	r := &SecurityGroupReconciler{
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

func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SecurityGroupReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.SecurityGroup{}
}

func (r *SecurityGroupReconciler) Finalizer() string {
	return securityGroupFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityGroupReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeSG, ok := obj.(*v1alpha1.SecurityGroup)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.SecurityGroup")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSG.Spec.Tenant)
	logger.Info("reconciling SecurityGroup")

	isDeleting := !kubeSG.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies and set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeSG, isDeleting)
	if err != nil {
		return ctrl.Result{}, err
	}
	if result != (ctrl.Result{}) {
		return result, nil
	}

	// Stage 3: K8s precondition — parent must be Active+Synchronized before the CMP resource
	// is created (ResourceID == ""). Once provisioned, parent state changes don't block the child.
	if !isDeleting && kubeBdl != nil && kubeSG.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeVpc) {
		logger.V(1).Info("parent VPC not yet Active+Synchronized, requeuing")
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeSecurityGroupBundle{}
		}
		if validationErr := r.ivs.Run(kubeSG, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeSG) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSG.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeSG) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSG.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeSG.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Resolve CMP dependencies.
	cmpSG, cmpVpc, prjID, vpcID, result, err := r.fetchCMPDependencies(ctx, kubeSG, arubaClient, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	logger.V(1).Info("CMP SecurityGroup state", "found", cmpSG != nil, "projectID", prjID, "vpcID", vpcID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && kubeBdl != nil && cmpSG != nil && cmpVpc != nil {
		if validationErr := r.vs.Run(kubeSG, cmpSG, &securityGroupBundle{kubeSecurityGroupBundle: *kubeBdl, cmpSecurityGroupBundle: cmpSecurityGroupBundle{CMPVpc: cmpVpc}}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(sg *v1alpha1.SecurityGroup) {
					if sg.Status.ProjectID == "" {
						sg.Status.ProjectID = prjID
					}
					if sg.Status.VPCID == "" {
						sg.Status.VPCID = vpcID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeSG) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeSG, cmpSG)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

// fetchKubeDependencies fetches the parent VPC and sets the owner reference.
// Returns (nil bundle, zero result, nil) if the VPC is not found — non-fatal,
// validation and precondition checks are skipped when kubeBdl is nil.
// Returns (nil, short-requeue result, nil) if the owner reference was just written.
func (r *SecurityGroupReconciler) fetchKubeDependencies(
	ctx context.Context,
	kubeSG *v1alpha1.SecurityGroup,
	isDeleting bool,
) (*kubeSecurityGroupBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kv := &v1alpha1.VPC{}
	if err := resolveOwnerObject(ctx, r.Client, kubeSG.Spec.VPCReference, kubeSG.Namespace, kv); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent vpc for owner reference: %w", err)
		}
		logger.V(1).Info("parent vpc not found for owner reference setup, skipping",
			"vpcName", kubeSG.Spec.VPCReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kv, kubeSG)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on security group: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	bdl := &kubeSecurityGroupBundle{KubeVpc: kv}
	kp := &v1alpha1.Project{}
	if err := r.Get(ctx, client.ObjectKey{Name: kubeSG.Spec.ProjectReference.Name, Namespace: kubeSG.Namespace}, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("fetching k8s project %q for validation: %w", kubeSG.Spec.ProjectReference.Name, err)
		}
	} else {
		bdl.KubeProject = kp
	}
	return bdl, ctrl.Result{}, nil
}

// fetchCMPDependencies resolves the CMP project ID, VPC ID, VPC resource, and SecurityGroup resource.
//
//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityGroupReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeSG *v1alpha1.SecurityGroup,
	arubaClient aruba.Client,
	isDeleting bool,
) (*aruba.SecurityGroup, *aruba.VPC, string, string, ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cmpSG *aruba.SecurityGroup
	var cmpVpc *aruba.VPC
	var prjID string
	var vpcID string

	sgName := kubeSG.Name
	projectName := kubeSG.Spec.ProjectReference.Name
	vpcName := kubeSG.Spec.VPCReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	sgFilter := fmt.Sprintf(`name:eq("%s")`, sgName)

	// Stage 3a: Resolve Project ID.
	if isDeleting && kubeSG.Status.ProjectID != "" {
		prjID = kubeSG.Status.ProjectID
	} else {
		cmpProjectList, listErr := arubaClient.FromProject().List(ctx, aruba.WithFilter(prjFilter))
		if listErr != nil {
			return nil, nil, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				listErr, projectName, prjFilter,
			)
		}
		cmpProjects := cmpProjectList.Items()
		if len(cmpProjects) == 0 && kubeSG.Status.ProjectID != "" {
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

	if kubeSG.Status.ProjectID != "" && kubeSG.Status.ProjectID != prjID {
		return nil, nil, prjID, "", ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in security group: sg_name: '%s', sg_project_id: '%s', project_name: '%s', project_id: '%s'",
			sgName, kubeSG.Status.ProjectID, projectName, prjID,
		)
	}

	// Stage 3b: Resolve VPC ID.
	if isDeleting && kubeSG.Status.VPCID != "" {
		vpcID = kubeSG.Status.VPCID
	} else {
		cmpVpcList, listErr := arubaClient.FromNetwork().VPCs().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(vpcFilter))
		if listErr != nil && !isCMPNotFound(listErr) {
			return nil, nil, prjID, "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
				listErr, vpcName, vpcFilter, projectName,
			)
		}
		// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
		var cmpVpcs []*aruba.VPC
		if cmpVpcList != nil {
			cmpVpcs = filterByName(cmpVpcList.Items(), vpcName, func(v *aruba.VPC) string { return v.Name() })
		}
		if len(cmpVpcs) == 0 && kubeSG.Status.VPCID != "" {
			return nil, nil, prjID, "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, vpc not found: vpc_name: '%s', vpc_filter: '%s'", vpcName, vpcFilter,
			)
		}
		if len(cmpVpcs) > 1 {
			return nil, nil, prjID, "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s', vpc_filter: '%s'",
				len(cmpVpcs), vpcName, vpcFilter,
			)
		}
		if len(cmpVpcs) == 0 {
			logger.V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
			return nil, nil, prjID, "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		cmpVpc = cmpVpcs[0]
		vpcID = cmpVpc.ID()
	}

	if kubeSG.Status.VPCID != "" && kubeSG.Status.VPCID != vpcID {
		return nil, nil, prjID, vpcID, ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in security group: sg_name: '%s', sg_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			sgName, kubeSG.Status.VPCID, vpcName, vpcID,
		)
	}

	// Stage 3c: Fetch CMP SecurityGroup.
	cmpSGList, listErr := arubaClient.FromNetwork().SecurityGroups().List(ctx, aruba.VPCRef(prjID, vpcID), aruba.WithFilter(sgFilter))
	if listErr != nil && !isCMPNotFound(listErr) {
		return nil, nil, prjID, vpcID, ctrl.Result{}, fmt.Errorf(
			"failed to find security group in Aruba cloud: %w, sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s'",
			listErr, sgName, sgFilter, projectName, vpcName,
		)
	}
	// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
	var cmpSGs []*aruba.SecurityGroup
	if cmpSGList != nil {
		cmpSGs = filterByName(cmpSGList.Items(), sgName, func(s *aruba.SecurityGroup) string { return s.Name() })
	}
	if len(cmpSGs) > 1 {
		return nil, nil, prjID, vpcID, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in security group list: sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s', instances: %d",
			sgName, sgFilter, projectName, vpcName, len(cmpSGs),
		)
	}

	if len(cmpSGs) == 1 {
		cmpSG = cmpSGs[0]
	}
	return cmpSG, cmpVpc, prjID, vpcID, ctrl.Result{}, nil
}

// newIntentionValidationSet returns K8s-only validation rules that run at Stage 4,
// before any CMP calls. All rules nil-guard bundle fields to handle an empty bundle.
func (r *SecurityGroupReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *kubeSecurityGroupBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *kubeSecurityGroupBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, _ *kubeSecurityGroupBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, _ *kubeSecurityGroupBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	// 2. Cross-resource rules (nil-guarded — VPC/Project may not be resolved yet)
	ivs.Add("TenantMustMatchVPC", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, b *kubeSecurityGroupBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with VPC: %q != %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	ivs.Add("ProjectMustMatchVPC", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, b *kubeSecurityGroupBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != "" && b.KubeVpc.Spec.ProjectReference.Name != "" && k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference mismatch with VPC: %q != %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, b *kubeSecurityGroupBundle) error {
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

func (r *SecurityGroupReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *securityGroupBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *securityGroupBundle]{}
	vs.Add("TenantMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *securityGroupBundle](
		"tenant",
		func(k *v1alpha1.SecurityGroup) string { return k.Spec.Tenant },
		func(b *securityGroupBundle) string { return b.KubeVpc.Spec.Tenant },
		"VPC",
	))
	vs.Add("ProjectMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.SecurityGroup, *aruba.SecurityGroup, *securityGroupBundle](
		"project reference",
		func(k *v1alpha1.SecurityGroup) string { return k.Spec.ProjectReference.Name },
		func(b *securityGroupBundle) string { return b.KubeVpc.Spec.ProjectReference.Name },
		"VPC",
	))
	vs.Add("TenantMustMatchProject", func(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup, b *securityGroupBundle) error {
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

func (r *SecurityGroupReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup] {
	ts := &reconciler.TransitionSet[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.SecurityGroup, *aruba.SecurityGroup](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.SecurityGroup, *aruba.SecurityGroup](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 5. WaitingChildrenDeletion — block CMP delete until all owned K8s children are gone.
	// The kAction explicitly deletes children because the K8s GC only cascades after the
	// owner is fully removed from etcd (impossible while the SecurityGroup finalizer is present).
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name: "WaitingChildrenDeletion",
		KCondition: func(k *v1alpha1.SecurityGroup, a *aruba.SecurityGroup) bool {
			return reconciler.KubeShouldBeDeletedOnCMP(k, a) && r.kubeSecurityGroupHasOwnedChildren(k, a)
		},
		ACondition:     cmpSecurityGroupIsFinal,
		KAction:        r.kubeSecurityGroupDeleteOwnedChildren,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.ShortRequeueAndIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 6. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:        cmpSecurityGroupIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 7. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 8. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 9. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 10. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 11. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:       "HasDeniedChanges",
		KCondition: kubeSecurityGroupHasDeniedChanges,
		ACondition: cmpSecurityGroupIsFinal,
		KAction: func(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) error {
			return fmt.Errorf("security group update rejected: %w", checkSecurityGroupDeniedChanges(kubeSG, cmpSG))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 12. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP; just re-stamp ObservedGeneration
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeSecurityGroupSpecInSyncWithCMP,
		ACondition:     cmpSecurityGroupIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 13. ShouldBeUpdated — spec changed and CMP is ready
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeSecurityGroupShouldUpdate,
		ACondition:     cmpSecurityGroupIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 14. ShouldBeUpdatedOnCMP — send update to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:        cmpSecurityGroupIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 15. WaitingUpdateOnCMP — CMP is still processing the update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 16. UpdateConfirmedOnCMP — CMP has settled; advance to Synchronized
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 17. UpdateAccomplished — transition back to Active and stamp generation
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 18. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 19. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:        cmpSecurityGroupNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 20. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 21. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 22. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	// 23. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityGroup, *aruba.SecurityGroup]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		ACondition:     cmpSecurityGroupIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityGroup, *aruba.SecurityGroup],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Owned-children helpers
// ---------------------------------------------------------------------------

// kubeSecurityGroupHasOwnedChildren returns true when any Kubernetes resource directly owned
// by the SecurityGroup still exists. Used by the WaitingChildrenDeletion transition.
func (r *SecurityGroupReconciler) kubeSecurityGroupHasOwnedChildren(k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) bool {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	has, err := hasOwnedChildren(context.Background(), r.Client, k, labelKey,
		&v1alpha1.SecurityRuleList{},
	)
	if err != nil {
		ctrl.Log.Error(err, "checking owned children for security group", "securityGroup", k.GetName())
		return true // conservative: assume children exist on error
	}
	return has
}

// kubeSecurityGroupDeleteOwnedChildren deletes all K8s children of the SecurityGroup that
// have not yet received a deletionTimestamp. Called by the WaitingChildrenDeletion action.
func (r *SecurityGroupReconciler) kubeSecurityGroupDeleteOwnedChildren(ctx context.Context, k *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	labelKey, _ := ownerLabelKey(r.Scheme, k)
	return deleteOwnedChildren(ctx, r.Client, k, labelKey,
		&v1alpha1.SecurityRuleList{},
	)
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeSecurityGroupHasDeniedChanges(kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	if !kubeSG.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpSG == nil {
		return false
	}
	return checkSecurityGroupDeniedChanges(kubeSG, cmpSG) != nil
}

func kubeSecurityGroupSpecInSyncWithCMP(kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSG, cmpSG) &&
		checkSecurityGroupDeniedChanges(kubeSG, cmpSG) == nil &&
		!kubeSecurityGroupNeedsUpdate(kubeSG, cmpSG)
}

func kubeSecurityGroupShouldUpdate(kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeSG, cmpSG) &&
		checkSecurityGroupDeniedChanges(kubeSG, cmpSG) == nil &&
		kubeSecurityGroupNeedsUpdate(kubeSG, cmpSG)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpSecurityGroupNotExists(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG == nil
}

func cmpSecurityGroupIsFinal(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG != nil && reconciler.IsFinalState(cmpSG.State())
}

func cmpSecurityGroupIsTransitory(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG != nil && cmpSG.State().IsTransitory()
}

func cmpSecurityGroupNotExistsOrTransitory(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG == nil || cmpSG.State().IsTransitory()
}

func cmpSecurityGroupIsActive(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG != nil && cmpSG.State() == aruba.StateActive
}

func cmpSecurityGroupIsFailed(_ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	return cmpSG != nil && cmpSG.State().IsFailure()
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *SecurityGroupReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.SecurityGroup){
		func(sg *v1alpha1.SecurityGroup) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID == "" {
				sg.Status.ProjectID = prjID
			}
			if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID == "" {
				sg.Status.VPCID = vID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSG, phase, reason, nil, prePatches...)
}

func (r *SecurityGroupReconciler) kubeMarkToDelete(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeleting(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkDeleted(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkToUpdate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkUpdating(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeMarkToCreate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityGroupReconciler) kubeMarkCreating(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityGroupReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityGroupReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) error {
	cmpID := ""
	if cmpSG != nil {
		cmpID = cmpSG.ID()
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeSG, cmpID, nil, func(sg *v1alpha1.SecurityGroup) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID != "" {
			sg.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID != "" {
			sg.Status.VPCID = vID
		}
	})
}

func (r *SecurityGroupReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeSG, func(sg *v1alpha1.SecurityGroup) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sg.Status.ProjectID == "" {
			sg.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sg.Status.VPCID == "" {
			sg.Status.VPCID = vID
		}
	})
}

func (r *SecurityGroupReconciler) kubeSetFailed(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSG, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *SecurityGroupReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	err := arubaClient.FromNetwork().SecurityGroups().Delete(ctx, cmpSG)
	return reconciler.CMPErrorFromResult("delete", cmpSG.Name(), err, http.StatusNotFound)
}

func (r *SecurityGroupReconciler) cmpUpdate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkSecurityGroupDeniedChanges(kubeSG, cmpSG); err != nil {
		return err
	}

	// Tags are the only mutable field; mutate the fetched wrapper in place.
	cmpSG.RetaggedAs(kubeSG.Spec.Tags...).NotDefault()
	_, err := arubaClient.FromNetwork().SecurityGroups().Update(ctx, cmpSG)
	return reconciler.CMPErrorFromResult("update", kubeSG.Name, err)
}

func (r *SecurityGroupReconciler) cmpCreate(ctx context.Context, kubeSG *v1alpha1.SecurityGroup, _ *aruba.SecurityGroup) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	sg := aruba.NewSecurityGroup().
		InVPC(aruba.VPCRef(prjID, vID)).
		Named(kubeSG.Name).
		Tagged(kubeSG.Spec.Tags...).
		NotDefault()
	_, err := arubaClient.FromNetwork().SecurityGroups().Create(ctx, sg)
	return reconciler.CMPErrorFromResult("create", kubeSG.Name, err)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

// checkSecurityGroupDeniedChanges reports immutable-field violations — for a Security
// Group, only the region.
//
// Unlike the other resources, aruba.SecurityGroup has no regionalMixin and therefore no
// Region() accessor (the region is inherited from the parent VPC and is not part of the
// request body), so the region is read off the raw response instead. This is the same
// field the SDK's own regionalMixin is hydrated from for VPC and Subnet.
func checkSecurityGroupDeniedChanges(kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) error {
	if cmpSG == nil || cmpSG.Raw() == nil {
		return nil
	}
	loc := cmpSG.Raw().Metadata.LocationResponse
	if loc == nil || loc.Value == "" {
		return nil
	}
	if kubeSG.Spec.Region != string(loc.Value) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'location' is not allowed"))
	}
	return nil
}

func kubeSecurityGroupNeedsUpdate(kubeSG *v1alpha1.SecurityGroup, cmpSG *aruba.SecurityGroup) bool {
	if cmpSG == nil {
		return false
	}
	return !reconciler.TagsAreEqual(kubeSG.Spec.Tags, cmpSG.Tags())
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityGroup{}).
		Named("securitygroup").
		Complete(r)
}
