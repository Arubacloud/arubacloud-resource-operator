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

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	securityRuleFinalizerName            = "securityrule.arubacloud.com/finalizer"
	securityGroupIDKey        contextKey = "securityGroupID"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeSecurityRuleBundle struct {
	KubeSG      *v1alpha1.SecurityGroup // from resolveOwnerObject (already fetched for ownership)
	KubeProject *v1alpha1.Project       // from additional K8s lookup for TenantMustMatchProject
}

type cmpSecurityRuleBundle struct {
	CMPSG *arubatypes.SecurityGroupResponse // from the SecurityGroup list fetch
}

type securityRuleBundle struct {
	kubeSecurityRuleBundle
	cmpSecurityRuleBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securityrules/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// SecurityRuleReconciler reconciles a SecurityRule object
type SecurityRuleReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *kubeSecurityRuleBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewSecurityRuleReconciler creates a new SecurityRuleReconciler
func NewSecurityRuleReconciler(baseReconciler *reconciler.Reconciler) *SecurityRuleReconciler {
	r := &SecurityRuleReconciler{
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

func (r *SecurityRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *SecurityRuleReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.SecurityRule{}
}

func (r *SecurityRuleReconciler) Finalizer() string {
	return securityRuleFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityRuleReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeSR, ok := obj.(*v1alpha1.SecurityRule)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.SecurityRule")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeSR.Spec.Tenant)
	logger.Info("reconciling SecurityRule")

	isDeleting := !kubeSR.GetDeletionTimestamp().IsZero()

	// Stage 2: Fetch K8s dependencies + set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeSR, isDeleting)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Stage 3: Wait for parent SecurityGroup to be Active before first CMP creation.
	if !isDeleting && kubeBdl != nil && kubeSR.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeSG) {
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		bdl := kubeBdl
		if bdl == nil {
			bdl = &kubeSecurityRuleBundle{}
		}
		if validationErr := r.ivs.Run(kubeSR, nil, bdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeSR) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSR.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeSR) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeSR.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeSR.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Resolve CMP dependencies.
	cmpSR, cmpSG, prjID, vpcID, sgID, result, err := r.fetchCMPDependencies(ctx, kubeSR, arubaClient, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, securityGroupIDKey, sgID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && kubeBdl != nil && cmpSR != nil && cmpSG != nil {
		if validationErr := r.vs.Run(kubeSR, cmpSR, &securityRuleBundle{
			kubeSecurityRuleBundle: *kubeBdl,
			cmpSecurityRuleBundle:  cmpSecurityRuleBundle{CMPSG: cmpSG},
		}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(sr *v1alpha1.SecurityRule) {
					if sr.Status.ProjectID == "" {
						sr.Status.ProjectID = prjID
					}
					if sr.Status.VPCID == "" {
						sr.Status.VPCID = vpcID
					}
					if sr.Status.SecurityGroupID == "" {
						sr.Status.SecurityGroupID = sgID
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeSR) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeSR, cmpSR)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

func (r *SecurityRuleReconciler) fetchKubeDependencies(ctx context.Context, kubeSR *v1alpha1.SecurityRule, isDeleting bool) (*kubeSecurityRuleBundle, ctrl.Result, error) {
	if isDeleting {
		return nil, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	ksg := &v1alpha1.SecurityGroup{}
	if err := resolveOwnerObject(ctx, r.Client, kubeSR.Spec.SecurityGroupReference, kubeSR.Namespace, ksg); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent security group for owner reference: %w", err)
		}
		logger.V(1).Info("parent security group not found for owner reference setup, skipping",
			"securityGroupName", kubeSR.Spec.SecurityGroupReference.Name)
		return nil, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, ksg, kubeSR)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on security rule: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}
	bdl := &kubeSecurityRuleBundle{KubeSG: ksg}
	kp := &v1alpha1.Project{}
	if err := r.Get(ctx, client.ObjectKey{Name: kubeSR.Spec.ProjectReference.Name, Namespace: kubeSR.Namespace}, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("fetching k8s project %q for validation: %w", kubeSR.Spec.ProjectReference.Name, err)
		}
	} else {
		bdl.KubeProject = kp
	}
	return bdl, ctrl.Result{}, nil
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *SecurityRuleReconciler) fetchCMPDependencies(
	ctx context.Context,
	sr *v1alpha1.SecurityRule,
	arubaClient aruba.Client,
	isDeleting bool,
) (*arubatypes.SecurityRuleResponse, *arubatypes.SecurityGroupResponse, string, string, string, ctrl.Result, error) {
	logger := log.FromContext(ctx)

	srName := sr.Name
	projectName := sr.Spec.ProjectReference.Name
	vpcName := sr.Spec.VPCReference.Name
	sgName := sr.Spec.SecurityGroupReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)
	sgFilter := fmt.Sprintf(`name:eq("%s")`, sgName)
	srFilter := fmt.Sprintf(`name:eq("%s")`, srName)

	var (
		cmpSR *arubatypes.SecurityRuleResponse
		cmpSG *arubatypes.SecurityGroupResponse
		prjID string
		vpcID string
		sgID  string
	)

	// --- Resolve Project ID ---

	if !sr.GetDeletionTimestamp().IsZero() && sr.Status.ProjectID != "" {
		prjID = sr.Status.ProjectID
	} else {
		cmpProjectList, listErr := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
		if listErr != nil {
			return nil, nil, "", "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
				listErr, projectName, prjFilter,
			)
		}
		if cmpProjectList.IsError() {
			return nil, nil, "", "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.StatusCode, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 && sr.Status.ProjectID != "" {
			return nil, nil, "", "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total > 1 {
			return nil, nil, "", "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
				cmpProjectList.Data.Total, projectName, prjFilter,
			)
		}
		if cmpProjectList.Data.Total == 0 {
			logger.V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
			return nil, nil, "", "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		prjID = *(cmpProjectList.Data.Values[0].Metadata.ID)
	}

	if sr.Status.ProjectID != "" && sr.Status.ProjectID != prjID {
		return nil, nil, prjID, "", "", ctrl.Result{}, fmt.Errorf(
			"inconsistent project id in security rule: sr_name: '%s', sr_project_id: '%s', project_name: '%s', project_id: '%s'",
			srName, sr.Status.ProjectID, projectName, prjID,
		)
	}

	// --- Resolve VPC ID ---

	if !sr.GetDeletionTimestamp().IsZero() && sr.Status.VPCID != "" {
		vpcID = sr.Status.VPCID
	} else {
		cmpVpcList, listErr := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
		if listErr != nil {
			return nil, nil, prjID, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: %w, vpc_name: '%s', vpc_filter: '%s', project_name: '%s'",
				listErr, vpcName, vpcFilter, projectName,
			)
		}
		if cmpVpcList.IsError() {
			return nil, nil, prjID, "", "", ctrl.Result{}, fmt.Errorf(
				"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s', project_name: '%s'",
				cmpVpcList.StatusCode, vpcName, projectName,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToVPCList(cmpVpcList, vpcName, logger)
		if cmpVpcList.Data.Total == 0 && sr.Status.VPCID != "" {
			return nil, nil, prjID, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, vpc not found: vpc_name: '%s', vpc_filter: '%s'", vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total > 1 {
			return nil, nil, prjID, "", "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s', vpc_filter: '%s'",
				cmpVpcList.Data.Total, vpcName, vpcFilter,
			)
		}
		if cmpVpcList.Data.Total == 0 {
			logger.V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
			return nil, nil, prjID, "", "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		vpcID = *(cmpVpcList.Data.Values[0].Metadata.ID)
	}

	if sr.Status.VPCID != "" && sr.Status.VPCID != vpcID {
		return nil, nil, prjID, vpcID, "", ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id in security rule: sr_name: '%s', sr_vpc_id: '%s', vpc_name: '%s', vpc_id: '%s'",
			srName, sr.Status.VPCID, vpcName, vpcID,
		)
	}

	// --- Resolve SecurityGroup ID ---

	if !sr.GetDeletionTimestamp().IsZero() && sr.Status.SecurityGroupID != "" {
		sgID = sr.Status.SecurityGroupID
	} else {
		cmpSGList, listErr := arubaClient.FromNetwork().SecurityGroups().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &sgFilter})
		if listErr != nil {
			return nil, nil, prjID, vpcID, "", ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: %w, sg_name: '%s', sg_filter: '%s', project_name: '%s', vpc_name: '%s'",
				listErr, sgName, sgFilter, projectName, vpcName,
			)
		}
		if cmpSGList.IsError() {
			return nil, nil, prjID, vpcID, "", ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: status_code: %d, sg_name: '%s', project_name: '%s', vpc_name: '%s'",
				cmpSGList.StatusCode, sgName, projectName, vpcName,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSecurityGroupList(cmpSGList, sgName, logger)
		if cmpSGList.Data.Total == 0 && sr.Status.SecurityGroupID != "" {
			return nil, nil, prjID, vpcID, "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, sg not found: sg_name: '%s', sg_filter: '%s'", sgName, sgFilter,
			)
		}
		if cmpSGList.Data.Total > 1 {
			return nil, nil, prjID, vpcID, "", ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, found: %d, sg_name: '%s', sg_filter: '%s'",
				cmpSGList.Data.Total, sgName, sgFilter,
			)
		}
		if cmpSGList.Data.Total == 0 {
			logger.V(1).Info("parent security group not found on CMP, requeuing", "sgName", sgName)
			return nil, nil, prjID, vpcID, "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		cmpSG = &cmpSGList.Data.Values[0]
		sgID = *(cmpSG.Metadata.ID)
	}

	if sr.Status.SecurityGroupID != "" && sr.Status.SecurityGroupID != sgID {
		return nil, cmpSG, prjID, vpcID, sgID, ctrl.Result{}, fmt.Errorf(
			"inconsistent security group id in security rule: sr_name: '%s', sr_sg_id: '%s', sg_name: '%s', sg_id: '%s'",
			srName, sr.Status.SecurityGroupID, sgName, sgID,
		)
	}

	// --- Fetch CMP SecurityRule ---

	cmpSRList, listErr := arubaClient.FromNetwork().SecurityGroupRules().List(ctx, prjID, vpcID, sgID, &arubatypes.RequestParameters{Filter: &srFilter})
	if listErr != nil {
		return nil, cmpSG, prjID, vpcID, sgID, ctrl.Result{}, fmt.Errorf(
			"failed to find security rule in Aruba cloud: %w, sr_name: '%s', sr_filter: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s'",
			listErr, srName, srFilter, projectName, vpcName, sgName,
		)
	}
	if cmpSRList.IsError() && cmpSRList.StatusCode != http.StatusNotFound {
		return nil, cmpSG, prjID, vpcID, sgID, ctrl.Result{}, fmt.Errorf(
			"failed to find security rule in Aruba cloud: status_code: %d, sr_name: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s'",
			cmpSRList.StatusCode, srName, projectName, vpcName, sgName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToSecurityRuleList(cmpSRList, srName, logger)
	if !cmpSRList.IsError() && (cmpSRList.Data.Total < 0 || cmpSRList.Data.Total > 1) {
		return nil, cmpSG, prjID, vpcID, sgID, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in security rule list: sr_name: '%s', sr_filter: '%s', project_name: '%s', vpc_name: '%s', sg_name: '%s', instances: %d",
			srName, srFilter, projectName, vpcName, sgName, cmpSRList.Data.Total,
		)
	}

	if cmpSRList.Data != nil && cmpSRList.Data.Total == 1 {
		cmpSR = &cmpSRList.Data.Values[0]
	}
	logger.V(1).Info("CMP SecurityRule state", "found", cmpSR != nil, "projectID", prjID, "vpcID", vpcID, "sgID", sgID)

	_ = isDeleting // consumed above for status ID shortcuts
	return cmpSR, cmpSG, prjID, vpcID, sgID, ctrl.Result{}, nil
}

func (r *SecurityRuleReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *kubeSecurityRuleBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *kubeSecurityRuleBundle]{}
	// 1. Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, _ *kubeSecurityRuleBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, _ *kubeSecurityRuleBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	ivs.Add("SecurityGroupReferenceRequired", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, _ *kubeSecurityRuleBundle) error {
		if k.Spec.SecurityGroupReference.Name == "" {
			return fmt.Errorf("security group reference is required")
		}
		return nil
	})
	// 2. Cross-resource rules (nil-guarded — SG/Project may not be resolved yet)
	ivs.Add("TenantMustMatchSecurityGroup", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, b *kubeSecurityRuleBundle) error {
		if b.KubeSG == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeSG.Spec.Tenant != "" && k.Spec.Tenant != b.KubeSG.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with SecurityGroup: %q != %q", k.Spec.Tenant, b.KubeSG.Spec.Tenant)
		}
		return nil
	})
	ivs.Add("VPCMustMatchSecurityGroup", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, b *kubeSecurityRuleBundle) error {
		if b.KubeSG == nil {
			return nil
		}
		if k.Spec.VPCReference.Name != "" && b.KubeSG.Spec.VPCReference.Name != "" && k.Spec.VPCReference.Name != b.KubeSG.Spec.VPCReference.Name {
			return fmt.Errorf("VPC reference mismatch with SecurityGroup: %q != %q", k.Spec.VPCReference.Name, b.KubeSG.Spec.VPCReference.Name)
		}
		return nil
	})
	ivs.Add("ProjectMustMatchSecurityGroup", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, b *kubeSecurityRuleBundle) error {
		if b.KubeSG == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != "" && b.KubeSG.Spec.ProjectReference.Name != "" && k.Spec.ProjectReference.Name != b.KubeSG.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference mismatch with SecurityGroup: %q != %q", k.Spec.ProjectReference.Name, b.KubeSG.Spec.ProjectReference.Name)
		}
		return nil
	})
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, b *kubeSecurityRuleBundle) error {
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

func (r *SecurityRuleReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle]{}
	vs.Add("TenantMustMatchSecurityGroup", reconciler.FieldMustMatch[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle](
		"tenant",
		func(k *v1alpha1.SecurityRule) string { return k.Spec.Tenant },
		func(b *securityRuleBundle) string { return b.KubeSG.Spec.Tenant },
		"SecurityGroup",
	))
	vs.Add("VPCMustMatchSecurityGroup", reconciler.FieldMustMatch[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle](
		"VPC reference",
		func(k *v1alpha1.SecurityRule) string { return k.Spec.VPCReference.Name },
		func(b *securityRuleBundle) string { return b.KubeSG.Spec.VPCReference.Name },
		"SecurityGroup",
	))
	vs.Add("ProjectMustMatchSecurityGroup", reconciler.FieldMustMatch[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse, *securityRuleBundle](
		"project reference",
		func(k *v1alpha1.SecurityRule) string { return k.Spec.ProjectReference.Name },
		func(b *securityRuleBundle) string { return b.KubeSG.Spec.ProjectReference.Name },
		"SecurityGroup",
	))
	vs.Add("TenantMustMatchProject", func(k *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse, b *securityRuleBundle) error {
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

func (r *SecurityRuleReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 4. ShouldDeleteTimedOut — enter deletion flow for timed-out resources (except those that timed out during Deleting)
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:        cmpSecurityRuleIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 6. DeletionOnCMPNotNeeded — resource marked for deletion but CMP resource doesn't exist; skip CMP delete
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 10. ShouldBeUpdated — generation changed while Active → enter Updating phase
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     reconciler.KubeActiveAndGenerationChanged[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleExists,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 11. UpdateNotSupported — Updating+ShallSynchronize + CMP exists → signal failure
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "UpdateNotSupported",
		KCondition:     reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleExists,
		KAction:        r.kubeMarkUpdatingFailed,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 12. UpdateRollback — Updating+Failed + CMP exists → rollback spec and return to Active
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "UpdateRollback",
		KCondition:     kubeSecurityRuleUpdatingFailed,
		ACondition:     cmpSecurityRuleExists,
		KAction:        r.kubeRollbackSpecAndSetActive,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 13. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 14. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:        cmpSecurityRuleNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 15. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 16. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 17. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	// 18. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		ACondition:     cmpSecurityRuleIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.SecurityRule, *arubatypes.SecurityRuleResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeSecurityRuleUpdatingFailed(kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) bool {
	if !kubeSR.GetDeletionTimestamp().IsZero() {
		return false
	}
	rs := kubeSR.GetResourceStatus()
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

func cmpSecurityRuleExists(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR != nil
}

func cmpSecurityRuleNotExists(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR == nil
}

func cmpSecurityRuleIsFinal(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil || cmpSR.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSR.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpSecurityRuleIsTransitory(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil || cmpSR.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSR.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSecurityRuleNotExistsOrTransitory(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	if cmpSR == nil {
		return true
	}
	if cmpSR.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpSR.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpSecurityRuleIsActive(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR != nil && cmpSR.Status.State != nil &&
		*cmpSR.Status.State == reconciler.CSPResourceStateActive
}

func cmpSecurityRuleIsFailed(_ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) bool {
	return cmpSR != nil && cmpSR.Status.State != nil && *cmpSR.Status.State == reconciler.CSPResourceStateFailed
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *SecurityRuleReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeSR *v1alpha1.SecurityRule, phase v1alpha1.ResourcePhase, reason string, actionErr error) error {
	prePatches := []func(*v1alpha1.SecurityRule){
		func(sr *v1alpha1.SecurityRule) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID == "" {
				sr.Status.ProjectID = prjID
			}
			if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VPCID == "" {
				sr.Status.VPCID = vID
			}
			if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID == "" {
				sr.Status.SecurityGroupID = sgID
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeSR, phase, reason, actionErr, prePatches...)
}

func (r *SecurityRuleReconciler) kubeMarkToDelete(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeleting(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeletingDone(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeMarkDeleted(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeMarkToUpdate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkUpdatingFailed(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonFailed,
		errors.New("updating SecurityRule resources is not supported"))
}

func (r *SecurityRuleReconciler) kubeRollbackSpecAndSetActive(ctx context.Context, kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	// Step 1: rollback spec to match CMP values (object patch, not status patch)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		srCopy := kubeSR.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(kubeSR), srCopy); err != nil {
			return err
		}

		srPatch := srCopy.DeepCopy()
		srPatch.Spec.Tags = cmpSR.Metadata.Tags
		if cmpSR.Metadata.LocationResponse != nil {
			srPatch.Spec.Region = cmpSR.Metadata.LocationResponse.Value
		}
		srPatch.Spec.Protocol = cmpSR.Properties.Protocol
		srPatch.Spec.Port = cmpSR.Properties.Port
		srPatch.Spec.Direction = string(cmpSR.Properties.Direction)
		if cmpSR.Properties.Target != nil {
			srPatch.Spec.Target.Type = string(cmpSR.Properties.Target.Kind)
			srPatch.Spec.Target.Value = cmpSR.Properties.Target.Value
		}

		return r.Patch(ctx, srPatch, client.MergeFrom(srCopy))
	}); err != nil {
		return fmt.Errorf("failed to rollback security rule '%s' spec: %w", kubeSR.Name, err)
	}

	// Step 2: set Active — reads fresh object (with new generation from spec patch)
	return r.kubeSetActiveAndSetID(ctx, kubeSR, cmpSR)
}

func (r *SecurityRuleReconciler) kubeMarkToCreate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *SecurityRuleReconciler) kubeMarkCreating(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *SecurityRuleReconciler) kubeMarkCreatingDone(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *SecurityRuleReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeSR *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	cmpID := ""
	if cmpSR != nil && cmpSR.Metadata.ID != nil {
		cmpID = *cmpSR.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeSR, cmpID, nil, func(sr *v1alpha1.SecurityRule) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID != "" {
			sr.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VPCID != "" {
			sr.Status.VPCID = vID
		}
		if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID != "" {
			sr.Status.SecurityGroupID = sgID
		}
	})
}

func (r *SecurityRuleReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeSR, func(sr *v1alpha1.SecurityRule) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && sr.Status.ProjectID == "" {
			sr.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && sr.Status.VPCID == "" {
			sr.Status.VPCID = vID
		}
		if sgID, ok := ctx.Value(securityGroupIDKey).(string); ok && sr.Status.SecurityGroupID == "" {
			sr.Status.SecurityGroupID = sgID
		}
	})
}

func (r *SecurityRuleReconciler) kubeSetFailed(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeSR, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *SecurityRuleReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.SecurityRule, cmpSR *arubatypes.SecurityRuleResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	sgID := ctx.Value(securityGroupIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroupRules().Delete(ctx, prjID, vID, sgID, *cmpSR.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpSR.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpSR.Metadata.Name, cmpResp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *SecurityRuleReconciler) cmpCreate(ctx context.Context, kubeSR *v1alpha1.SecurityRule, _ *arubatypes.SecurityRuleResponse) error {
	prjID := ctx.Value(projectIDKey).(string)
	vID := ctx.Value(vpcIDKey).(string)
	sgID := ctx.Value(securityGroupIDKey).(string)
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	cmpResp, err := arubaClient.FromNetwork().SecurityGroupRules().Create(ctx, prjID, vID, sgID, *cmpSecurityRuleRequestFromKube(kubeSR), nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeSR.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeSR.Name, cmpResp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func cmpSecurityRuleRequestFromKube(kubeSR *v1alpha1.SecurityRule) *arubatypes.SecurityRuleRequest {
	tags := make([]string, len(kubeSR.Spec.Tags))
	copy(tags, kubeSR.Spec.Tags)
	return &arubatypes.SecurityRuleRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeSR.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{
				Value: kubeSR.Spec.Region,
			},
		},
		Properties: arubatypes.SecurityRulePropertiesRequest{
			Protocol:  kubeSR.Spec.Protocol,
			Port:      kubeSR.Spec.Port,
			Direction: arubatypes.RuleDirection(kubeSR.Spec.Direction),
			Target: &arubatypes.RuleTarget{
				Kind:  arubatypes.EndpointTypeDto(kubeSR.Spec.Target.Type),
				Value: kubeSR.Spec.Target.Value,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecurityRule{}).
		Named("securityrule").
		Complete(r)
}
