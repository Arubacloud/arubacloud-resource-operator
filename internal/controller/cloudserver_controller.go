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
	"strings"

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
	cloudServerFinalizerName            = "cloudserver.arubacloud.com/finalizer"
	bootVolumeIDKey          contextKey = "bootVolumeID"
	keyPairIDKey             contextKey = "keyPairID"
	elasticIPIDKey           contextKey = "elasticIPID"
	subnetIDsKey             contextKey = "subnetIDs"
	securityGroupIDsKey      contextKey = "securityGroupIDs"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type kubeCloudServerBundle struct {
	KubeProject        *v1alpha1.Project
	KubeVpc            *v1alpha1.VPC
	KubeBootVolume     *v1alpha1.BlockStorage
	KubeSubnets        []*v1alpha1.Subnet
	KubeSecurityGroups []*v1alpha1.SecurityGroup
	KubeKeyPair        *v1alpha1.KeyPair   // nil when not referenced
	KubeElasticIP      *v1alpha1.ElasticIP // nil when not referenced
}

type cmpCloudServerBundle struct {
	CMPVpc            *arubatypes.VPCResponse
	CMPBootVolume     *arubatypes.BlockStorageResponse
	CMPSubnets        []*arubatypes.SubnetResponse
	CMPSecurityGroups []*arubatypes.SecurityGroupResponse
	CMPKeyPair        *arubatypes.KeyPairResponse   // nil when no KeyPair referenced
	CMPElasticIP      *arubatypes.ElasticIPResponse // nil when no ElasticIP referenced
}

type cloudServerBundle struct {
	kubeCloudServerBundle
	cmpCloudServerBundle
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=cloudservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=keypairs,verbs=get;list;watch
// +kubebuilder:rbac:groups=arubacloud.com,resources=elasticips,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// CloudServerReconciler reconciles a CloudServer object
type CloudServerReconciler struct {
	*reconciler.Reconciler
	ivs *reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *kubeCloudServerBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *cloudServerBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewCloudServerReconciler creates a new CloudServerReconciler
func NewCloudServerReconciler(baseReconciler *reconciler.Reconciler) *CloudServerReconciler {
	r := &CloudServerReconciler{
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

func (r *CloudServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *CloudServerReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.CloudServer{}
}

func (r *CloudServerReconciler) Finalizer() string {
	return cloudServerFinalizerName
}

// ---------------------------------------------------------------------------
// HandleReconcile
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// Stage 1: Setup.
	kubeCS, ok := obj.(*v1alpha1.CloudServer)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.CloudServer")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeCS.Spec.Tenant)
	logger.Info("reconciling CloudServer")

	isDeleting := !kubeCS.GetDeletionTimestamp().IsZero()

	// WORKAROUND: used to gate the dependency readiness checks in resolveBootVolumeID and resolveSubnetIDs.
	// TODO: Remove once the CMP Infra Team fixes the root cause.
	isCreating := !isDeleting && kubeCS.Status.ResourceID == ""

	// Stage 2: Fetch K8s dependencies + set owner reference.
	kubeBdl, result, err := r.fetchKubeDependencies(ctx, kubeCS, isDeleting)
	if err != nil || result.RequeueAfter > 0 {
		return result, err
	}

	// Stage 3: Wait for parent Project to be Active before first CMP creation.
	if !isDeleting && kubeBdl.KubeProject != nil && kubeCS.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeBdl.KubeProject) {
		return ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
	if !isDeleting {
		if validationErr := r.ivs.Run(kubeCS, nil, kubeBdl); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsIntentionValidationFailed(kubeCS) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeCS.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
		if reconciler.IsCMPValidationFailedAndSpecChanged(kubeCS) {
			resetPhase := v1alpha1.ResourcePhasePending
			if kubeCS.Status.ResourceID != "" {
				resetPhase = v1alpha1.ResourcePhaseActive
			}
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS,
				resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 5: Create Aruba client.
	arubaClient, err := r.ArubaClient(kubeCS.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Stage 6: Resolve CMP dependencies.
	cmpCS, cmpBdl, ctx, result, err := r.fetchCMPDependencies(ctx, kubeCS, arubaClient, isDeleting, isCreating)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && cmpCS != nil && kubeBdl.KubeProject != nil {
		if validationErr := r.vs.Run(kubeCS, cmpCS, &cloudServerBundle{
			kubeCloudServerBundle: *kubeBdl,
			cmpCloudServerBundle:  *cmpBdl,
		}); validationErr != nil {
			setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS,
				v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
				func(cs *v1alpha1.CloudServer) {
					if cs.Status.ProjectID == "" {
						cs.Status.ProjectID = ctx.Value(projectIDKey).(string)
					}
				},
			)
			if setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{}, nil
		}
		if reconciler.IsPostValidationFailed(kubeCS) {
			if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS,
				v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
				return ctrl.Result{}, setErr
			}
			return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
		}
	}

	// Stage 8: Run transitions.
	return r.ts.Run(ctx, kubeCS, cmpCS)
}

// ---------------------------------------------------------------------------
// Major HandleReconcile helpers
// ---------------------------------------------------------------------------

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) fetchKubeDependencies(ctx context.Context, kubeCS *v1alpha1.CloudServer, isDeleting bool) (*kubeCloudServerBundle, ctrl.Result, error) {
	if isDeleting {
		return &kubeCloudServerBundle{}, ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx)
	kp := &v1alpha1.Project{}
	if err := resolveOwnerObject(ctx, r.Client, kubeCS.Spec.ProjectReference, kubeCS.Namespace, kp); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
		}
		logger.V(1).Info("parent project not found for owner reference setup, skipping", "projectName", kubeCS.Spec.ProjectReference.Name)
		return &kubeCloudServerBundle{}, ctrl.Result{}, nil
	}
	requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeCS)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting owner reference on cloudserver: %w", err)
	}
	if requeue {
		return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
	}

	bdl := &kubeCloudServerBundle{KubeProject: kp}

	kv := &v1alpha1.VPC{}
	if err := r.Get(ctx, client.ObjectKey{Name: kubeCS.Spec.VPCReference.Name, Namespace: kubeCS.Namespace}, kv); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("fetching k8s vpc %q for validation: %w", kubeCS.Spec.VPCReference.Name, err)
		}
	} else {
		bdl.KubeVpc = kv
	}
	kbv := &v1alpha1.BlockStorage{}
	if err := r.Get(ctx, client.ObjectKey{Name: kubeCS.Spec.BootVolumeReference.Name, Namespace: kubeCS.Namespace}, kbv); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf("fetching k8s boot volume %q for validation: %w", kubeCS.Spec.BootVolumeReference.Name, err)
		}
	} else {
		bdl.KubeBootVolume = kbv
	}
	if kubeCS.Spec.KeyPairReference.Name != "" {
		kkp := &v1alpha1.KeyPair{}
		if err := r.Get(ctx, client.ObjectKey{Name: kubeCS.Spec.KeyPairReference.Name, Namespace: kubeCS.Namespace}, kkp); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, ctrl.Result{}, fmt.Errorf("fetching k8s key pair %q for validation: %w", kubeCS.Spec.KeyPairReference.Name, err)
			}
		} else {
			bdl.KubeKeyPair = kkp
		}
	}
	if kubeCS.Spec.ElasticIPReference != nil && kubeCS.Spec.ElasticIPReference.Name != "" {
		keip := &v1alpha1.ElasticIP{}
		if err := r.Get(ctx, client.ObjectKey{Name: kubeCS.Spec.ElasticIPReference.Name, Namespace: kubeCS.Namespace}, keip); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, ctrl.Result{}, fmt.Errorf("fetching k8s elastic ip %q for validation: %w", kubeCS.Spec.ElasticIPReference.Name, err)
			}
		} else {
			bdl.KubeElasticIP = keip
		}
	}
	for _, ref := range kubeCS.Spec.SubnetReferences {
		ks := &v1alpha1.Subnet{}
		if err := r.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: kubeCS.Namespace}, ks); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, ctrl.Result{}, fmt.Errorf("fetching k8s subnet %q for validation: %w", ref.Name, err)
			}
		} else {
			bdl.KubeSubnets = append(bdl.KubeSubnets, ks)
		}
	}
	for _, ref := range kubeCS.Spec.SecurityGroupReferences {
		ksg := &v1alpha1.SecurityGroup{}
		if err := r.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: kubeCS.Namespace}, ksg); err != nil {
			if !apierrors.IsNotFound(err) {
				return nil, ctrl.Result{}, fmt.Errorf("fetching k8s security group %q for validation: %w", ref.Name, err)
			}
		} else {
			bdl.KubeSecurityGroups = append(bdl.KubeSecurityGroups, ksg)
		}
	}

	return bdl, ctrl.Result{}, nil
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) fetchCMPDependencies(
	ctx context.Context,
	kubeCS *v1alpha1.CloudServer,
	arubaClient arubaclient.Client,
	isDeleting bool,
	isCreating bool,
) (cmpCS *arubatypes.CloudServerResponse, cmpBdl *cmpCloudServerBundle, enrichedCtx context.Context, result ctrl.Result, err error) {
	// --- Resolve Project ID ---
	prjID, result, err := r.resolveProjectID(ctx, arubaClient, kubeCS, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve VPC ID ---
	vpcID, cmpVpc, result, err := r.resolveVpcID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve Boot Volume ID ---
	bootVolumeID, cmpBootVolume, result, err := r.resolveBootVolumeID(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve Subnet IDs ---
	subnetIDs, cmpSubnets, result, err := r.resolveSubnetIDs(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve Security Group IDs ---
	securityGroupIDs, cmpSecurityGroups, result, err := r.resolveSecurityGroupIDs(ctx, arubaClient, kubeCS, isDeleting, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve KeyPair ID (optional) ---
	keyPairID, cmpKeyPair, result, err := r.resolveKeyPairID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Resolve Elastic IP ID (optional) ---
	elasticIPID, cmpElasticIP, result, err := r.resolveElasticIPID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, nil, ctx, result, err
	}

	// --- Fetch CMP CloudServer ---
	csName := kubeCS.Name
	csFilter := fmt.Sprintf(`name:eq("%s")`, csName)

	cmpCSList, err := arubaClient.FromCompute().CloudServers().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &csFilter})
	if err != nil {
		return nil, nil, ctx, ctrl.Result{}, fmt.Errorf(
			"failed to find cloud server in Aruba cloud: %w, name: '%s'",
			err, csName,
		)
	}
	if cmpCSList.IsError() && cmpCSList.StatusCode != http.StatusNotFound {
		return nil, nil, ctx, ctrl.Result{}, fmt.Errorf(
			"failed to find cloud server in Aruba cloud: status_code: %d, name: '%s'",
			cmpCSList.StatusCode, csName,
		)
	}
	if !cmpCSList.IsError() && (cmpCSList.Data.Total < 0 || cmpCSList.Data.Total > 1) {
		return nil, nil, ctx, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in cloud server list: name: '%s', instances: %d",
			csName, cmpCSList.Data.Total,
		)
	}

	if cmpCSList.Data != nil && cmpCSList.Data.Total == 1 {
		cmpCS = &cmpCSList.Data.Values[0]
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeCS.Spec.Tenant)
	logger.V(1).Info("CMP CloudServer state", "found", cmpCS != nil, "projectID", prjID, "vpcID", vpcID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, bootVolumeIDKey, bootVolumeID)
	ctx = context.WithValue(ctx, subnetIDsKey, subnetIDs)
	ctx = context.WithValue(ctx, securityGroupIDsKey, securityGroupIDs)
	ctx = context.WithValue(ctx, keyPairIDKey, keyPairID)
	ctx = context.WithValue(ctx, elasticIPIDKey, elasticIPID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	cmpBdl = &cmpCloudServerBundle{
		CMPVpc:            cmpVpc,
		CMPBootVolume:     cmpBootVolume,
		CMPSubnets:        cmpSubnets,
		CMPSecurityGroups: cmpSecurityGroups,
		CMPKeyPair:        cmpKeyPair,
		CMPElasticIP:      cmpElasticIP,
	}

	return cmpCS, cmpBdl, ctx, ctrl.Result{}, nil
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *kubeCloudServerBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *kubeCloudServerBundle]{}
	// Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, _ *kubeCloudServerBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, _ *kubeCloudServerBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	ivs.Add("BootVolumeReferenceRequired", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, _ *kubeCloudServerBundle) error {
		if k.Spec.BootVolumeReference.Name == "" {
			return fmt.Errorf("boot volume reference is required")
		}
		return nil
	})
	ivs.Add("SubnetReferencesRequired", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, _ *kubeCloudServerBundle) error {
		if len(k.Spec.SubnetReferences) == 0 {
			return fmt.Errorf("at least one subnet reference is required")
		}
		return nil
	})
	ivs.Add("SecurityGroupReferencesRequired", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, _ *kubeCloudServerBundle) error {
		if len(k.Spec.SecurityGroupReferences) == 0 {
			return fmt.Errorf("at least one security group reference is required")
		}
		return nil
	})
	// 1. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeProject == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeProject.Spec.Tenant != "" && k.Spec.Tenant != b.KubeProject.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with Project: %q != %q", k.Spec.Tenant, b.KubeProject.Spec.Tenant)
		}
		return nil
	})
	// 2. VPC must match all Subnets (K8s-only)
	ivs.Add("VPCMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		csVPC := k.Spec.VPCReference.Name
		var msgs []string
		for _, kubeSubnet := range b.KubeSubnets {
			if kubeSubnet.Spec.VPCReference.Name != csVPC {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", kubeSubnet.Name, csVPC, kubeSubnet.Spec.VPCReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("VPC reference mismatch with Subnets: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 10. VPC must match all SecurityGroups (K8s-only)
	ivs.Add("VPCMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		csVPC := k.Spec.VPCReference.Name
		var msgs []string
		for _, kubeSG := range b.KubeSecurityGroups {
			if kubeSG.Spec.VPCReference.Name != csVPC {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", kubeSG.Name, csVPC, kubeSG.Spec.VPCReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("VPC reference mismatch with SecurityGroups: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 11. Tenant must match VPC (nil-guarded)
	ivs.Add("TenantMustMatchVPC", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match VPC tenant %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	// 12. Tenant must match BootVolume (nil-guarded)
	ivs.Add("TenantMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeBootVolume.Spec.Tenant != "" && k.Spec.Tenant != b.KubeBootVolume.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match BlockStorage tenant %q", k.Spec.Tenant, b.KubeBootVolume.Spec.Tenant)
		}
		return nil
	})
	// 13. Tenant must match KeyPair (nil-guarded, optional)
	ivs.Add("TenantMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeKeyPair.Spec.Tenant != "" && k.Spec.Tenant != b.KubeKeyPair.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match KeyPair tenant %q", k.Spec.Tenant, b.KubeKeyPair.Spec.Tenant)
		}
		return nil
	})
	// 14. Tenant must match ElasticIP (nil-guarded, optional)
	ivs.Add("TenantMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeElasticIP.Spec.Tenant != "" && k.Spec.Tenant != b.KubeElasticIP.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match ElasticIP tenant %q", k.Spec.Tenant, b.KubeElasticIP.Spec.Tenant)
		}
		return nil
	})
	// 15. Tenant must match all Subnets
	ivs.Add("TenantMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		var msgs []string
		for _, s := range b.KubeSubnets {
			if k.Spec.Tenant != "" && s.Spec.Tenant != "" && k.Spec.Tenant != s.Spec.Tenant {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", s.Name, k.Spec.Tenant, s.Spec.Tenant))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("tenant mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 16. Tenant must match all SecurityGroups
	ivs.Add("TenantMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		var msgs []string
		for _, sg := range b.KubeSecurityGroups {
			if k.Spec.Tenant != "" && sg.Spec.Tenant != "" && k.Spec.Tenant != sg.Spec.Tenant {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", sg.Name, k.Spec.Tenant, sg.Spec.Tenant))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("tenant mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 17. Project reference must match VPC (nil-guarded)
	ivs.Add("ProjectMustMatchVPC", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match VPC project reference %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 18. Project reference must match BootVolume (nil-guarded)
	ivs.Add("ProjectMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeBootVolume.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match BlockStorage project reference %q", k.Spec.ProjectReference.Name, b.KubeBootVolume.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 19. Project reference must match KeyPair (nil-guarded, optional)
	ivs.Add("ProjectMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeKeyPair.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match KeyPair project reference %q", k.Spec.ProjectReference.Name, b.KubeKeyPair.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 20. Project reference must match ElasticIP (nil-guarded, optional)
	ivs.Add("ProjectMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeElasticIP.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match ElasticIP project reference %q", k.Spec.ProjectReference.Name, b.KubeElasticIP.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 21. Project reference must match all Subnets
	ivs.Add("ProjectMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		var msgs []string
		for _, s := range b.KubeSubnets {
			if k.Spec.ProjectReference.Name != s.Spec.ProjectReference.Name {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", s.Name, k.Spec.ProjectReference.Name, s.Spec.ProjectReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("project reference mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 22. Project reference must match all SecurityGroups
	ivs.Add("ProjectMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *kubeCloudServerBundle) error {
		var msgs []string
		for _, sg := range b.KubeSecurityGroups {
			if k.Spec.ProjectReference.Name != sg.Spec.ProjectReference.Name {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", sg.Name, k.Spec.ProjectReference.Name, sg.Spec.ProjectReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("project reference mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	return ivs
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *cloudServerBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *cloudServerBundle]{}
	// 1. Tenant must match Project
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse, *cloudServerBundle](
		"tenant",
		func(k *v1alpha1.CloudServer) string { return k.Spec.Tenant },
		func(b *cloudServerBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	// 2. VPC must match all Subnets (K8s-only)
	vs.Add("VPCMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		csVPC := k.Spec.VPCReference.Name
		var msgs []string
		for _, kubeSubnet := range b.KubeSubnets {
			if kubeSubnet.Spec.VPCReference.Name != csVPC {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", kubeSubnet.Name, csVPC, kubeSubnet.Spec.VPCReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("VPC reference mismatch with Subnets: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 10. VPC must match all SecurityGroups (K8s-only)
	vs.Add("VPCMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		csVPC := k.Spec.VPCReference.Name
		var msgs []string
		for _, kubeSG := range b.KubeSecurityGroups {
			if kubeSG.Spec.VPCReference.Name != csVPC {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", kubeSG.Name, csVPC, kubeSG.Spec.VPCReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("VPC reference mismatch with SecurityGroups: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 11. Tenant must match VPC (K8s-side, nil-guarded)
	vs.Add("TenantMustMatchVPC", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match VPC tenant %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	// 12. Tenant must match BootVolume (K8s-side, nil-guarded)
	vs.Add("TenantMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeBootVolume.Spec.Tenant != "" && k.Spec.Tenant != b.KubeBootVolume.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match BlockStorage tenant %q", k.Spec.Tenant, b.KubeBootVolume.Spec.Tenant)
		}
		return nil
	})
	// 13. Tenant must match KeyPair (K8s-side, nil-guarded, optional)
	vs.Add("TenantMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeKeyPair.Spec.Tenant != "" && k.Spec.Tenant != b.KubeKeyPair.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match KeyPair tenant %q", k.Spec.Tenant, b.KubeKeyPair.Spec.Tenant)
		}
		return nil
	})
	// 14. Tenant must match ElasticIP (K8s-side, nil-guarded, optional)
	vs.Add("TenantMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeElasticIP.Spec.Tenant != "" && k.Spec.Tenant != b.KubeElasticIP.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match ElasticIP tenant %q", k.Spec.Tenant, b.KubeElasticIP.Spec.Tenant)
		}
		return nil
	})
	// 15. Tenant must match all Subnets (K8s-side)
	vs.Add("TenantMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		var msgs []string
		for _, s := range b.KubeSubnets {
			if k.Spec.Tenant != "" && s.Spec.Tenant != "" && k.Spec.Tenant != s.Spec.Tenant {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", s.Name, k.Spec.Tenant, s.Spec.Tenant))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("tenant mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 16. Tenant must match all SecurityGroups (K8s-side)
	vs.Add("TenantMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		var msgs []string
		for _, sg := range b.KubeSecurityGroups {
			if k.Spec.Tenant != "" && sg.Spec.Tenant != "" && k.Spec.Tenant != sg.Spec.Tenant {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", sg.Name, k.Spec.Tenant, sg.Spec.Tenant))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("tenant mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 17. Project reference must match VPC (K8s-side, nil-guarded)
	vs.Add("ProjectMustMatchVPC", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match VPC project reference %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 18. Project reference must match BootVolume (K8s-side, nil-guarded)
	vs.Add("ProjectMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeBootVolume.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match BlockStorage project reference %q", k.Spec.ProjectReference.Name, b.KubeBootVolume.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 19. Project reference must match KeyPair (K8s-side, nil-guarded, optional)
	vs.Add("ProjectMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeKeyPair.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match KeyPair project reference %q", k.Spec.ProjectReference.Name, b.KubeKeyPair.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 20. Project reference must match ElasticIP (K8s-side, nil-guarded, optional)
	vs.Add("ProjectMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeElasticIP.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match ElasticIP project reference %q", k.Spec.ProjectReference.Name, b.KubeElasticIP.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 21. Project reference must match all Subnets (K8s-side)
	vs.Add("ProjectMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		var msgs []string
		for _, s := range b.KubeSubnets {
			if k.Spec.ProjectReference.Name != s.Spec.ProjectReference.Name {
				msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", s.Name, k.Spec.ProjectReference.Name, s.Spec.ProjectReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("project reference mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	// 22. Project reference must match all SecurityGroups (K8s-side)
	vs.Add("ProjectMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse, b *cloudServerBundle) error {
		var msgs []string
		for _, sg := range b.KubeSecurityGroups {
			if k.Spec.ProjectReference.Name != sg.Spec.ProjectReference.Name {
				msgs = append(msgs, fmt.Sprintf("SecurityGroup %q: %q != %q", sg.Name, k.Spec.ProjectReference.Name, sg.Spec.ProjectReference.Name))
			}
		}
		if len(msgs) > 0 {
			return fmt.Errorf("project reference mismatch: %s", strings.Join(msgs, "; "))
		}
		return nil
	})
	return vs
}

func (r *CloudServerReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse] {
	ts := &reconciler.TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 4. ShouldDeleteTimedOut
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:        cmpCloudServerIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 6. DeletionOnCMPNotNeeded
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:       "HasDeniedChanges",
		KCondition: kubeCSHasDeniedChanges,
		ACondition: cmpCloudServerIsFinal,
		KAction: func(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
			return fmt.Errorf("cloud server update rejected: %w", checkCSDeniedChanges(kubeCS, cmpCS))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeCSSpecInSyncWithCMP,
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 12. ShouldBeUpdated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeCSShouldUpdate,
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 13. ShouldBeUpdatedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:        cmpCloudServerIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 14. WaitingUpdateOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 15. UpdateConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 16. UpdateAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:        cmpCloudServerNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		ACondition:     cmpCloudServerIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	return ts
}

// ---------------------------------------------------------------------------
// CMP resolve helpers
// ---------------------------------------------------------------------------

func (r *CloudServerReconciler) resolveProjectID(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
) (string, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.ProjectID != "" {
		return kubeCS.Status.ProjectID, ctrl.Result{}, nil
	}

	projectName := kubeCS.Spec.ProjectReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	cmpProjectList, err := arubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: %w, project_name: '%s'", err, projectName,
		)
	}
	if cmpProjectList.IsError() {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: status_code: %d, project_name: '%s'",
			cmpProjectList.StatusCode, projectName,
		)
	}
	if cmpProjectList.Data.Total == 0 && kubeCS.Status.ProjectID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: project not found but already recorded: project_name: '%s'", projectName,
		)
	}
	if cmpProjectList.Data.Total > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in project list: expected: 1, found: %d, project_name: '%s'",
			cmpProjectList.Data.Total, projectName,
		)
	}
	if cmpProjectList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	prjID := *(cmpProjectList.Data.Values[0].Metadata.ID)
	if kubeCS.Status.ProjectID != "" && kubeCS.Status.ProjectID != prjID {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent project id: recorded: '%s', found: '%s', project_name: '%s'",
			kubeCS.Status.ProjectID, prjID, projectName,
		)
	}
	return prjID, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveVpcID(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, *arubatypes.VPCResponse, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.VPCID != "" {
		return kubeCS.Status.VPCID, nil, ctrl.Result{}, nil
	}

	vpcName := kubeCS.Spec.VPCReference.Name
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)

	cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: %w, vpc_name: '%s'", err, vpcName,
		)
	}
	if cmpVpcList.IsError() {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s'",
			cmpVpcList.StatusCode, vpcName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToVPCList(cmpVpcList, vpcName, log.FromContext(ctx))
	if cmpVpcList.Data.Total == 0 && kubeCS.Status.VPCID != "" {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data: vpc not found but already recorded: vpc_name: '%s'", vpcName,
		)
	}
	if cmpVpcList.Data.Total > 1 {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s'",
			cmpVpcList.Data.Total, vpcName,
		)
	}
	if cmpVpcList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
		return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	cmpVpc := &cmpVpcList.Data.Values[0]
	vpcID := *(cmpVpc.Metadata.ID)
	if kubeCS.Status.VPCID != "" && kubeCS.Status.VPCID != vpcID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id: recorded: '%s', found: '%s', vpc_name: '%s'",
			kubeCS.Status.VPCID, vpcID, vpcName,
		)
	}
	return vpcID, cmpVpc, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveBootVolumeID(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	// WORKAROUND: isCreating gates the readiness check below.
	// TODO: remove once CMP Infra Team fixes the root cause.
	isCreating bool,
	prjID string,
) (string, *arubatypes.BlockStorageResponse, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.BootVolumeID != "" {
		return kubeCS.Status.BootVolumeID, nil, ctrl.Result{}, nil
	}

	volName := kubeCS.Spec.BootVolumeReference.Name
	volFilter := fmt.Sprintf(`name:eq("%s")`, volName)

	cmpVolList, err := arubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &volFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find boot volume in Aruba cloud: %w, volume_name: '%s'", err, volName,
		)
	}
	if cmpVolList.IsError() {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find boot volume in Aruba cloud: status_code: %d, volume_name: '%s'",
			cmpVolList.StatusCode, volName,
		)
	}
	if cmpVolList.Data.Total == 0 && kubeCS.Status.BootVolumeID != "" {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data: boot volume not found but already recorded: volume_name: '%s'", volName,
		)
	}
	if cmpVolList.Data.Total > 1 {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in volume list: expected: 1, found: %d, volume_name: '%s'",
			cmpVolList.Data.Total, volName,
		)
	}
	if cmpVolList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("boot volume not found on CMP, requeuing", "volumeName", volName)
		return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// WORKAROUND: CMP bug causes CloudServer creation to stall when the boot volume is not yet
	// ready. Wait for the volume to reach a final CMP state before proceeding with the create.
	// TODO: Remove this block once the CMP Infra Team fixes the root cause.
	if isCreating {
		stateNature := reconciler.AssessCSPResourceStateNature(&cmpVolList.Data.Values[0].Status)
		if stateNature != reconciler.CSPResourceStateNatureFinal {
			state := "<nil>"
			if cmpVolList.Data.Values[0].Status.State != nil {
				state = string(*cmpVolList.Data.Values[0].Status.State)
			}
			log.FromContext(ctx).V(1).Info("boot volume not ready on CMP, requeuing", "volumeName", volName, "cmpState", state)
			return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
	}

	cmpVol := &cmpVolList.Data.Values[0]
	volID := *(cmpVol.Metadata.ID)
	if kubeCS.Status.BootVolumeID != "" && kubeCS.Status.BootVolumeID != volID {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent boot volume id: recorded: '%s', found: '%s', volume_name: '%s'",
			kubeCS.Status.BootVolumeID, volID, volName,
		)
	}
	return volID, cmpVol, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveSubnetIDs(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	// WORKAROUND: isCreating gates the readiness check below.
	// TODO: remove once CMP Infra Team fixes the root cause.
	isCreating bool,
	prjID, vpcID string,
) ([]string, []*arubatypes.SubnetResponse, ctrl.Result, error) {
	if isDeleting && len(kubeCS.Status.SubnetIDs) > 0 {
		return kubeCS.Status.SubnetIDs, nil, ctrl.Result{}, nil
	}

	ids := make([]string, 0, len(kubeCS.Spec.SubnetReferences))
	responses := make([]*arubatypes.SubnetResponse, 0, len(kubeCS.Spec.SubnetReferences))
	for _, ref := range kubeCS.Spec.SubnetReferences {
		filter := fmt.Sprintf(`name:eq("%s")`, ref.Name)
		cmpList, err := arubaClient.FromNetwork().Subnets().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &filter})
		if err != nil {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"failed to find subnet in Aruba cloud: %w, subnet_name: '%s'", err, ref.Name,
			)
		}
		if cmpList.IsError() {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"failed to find subnet in Aruba cloud: status_code: %d, subnet_name: '%s'",
				cmpList.StatusCode, ref.Name,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSubnetList(cmpList, ref.Name, log.FromContext(ctx))
		if cmpList.Data.Total > 1 {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in subnet list: expected: 1, found: %d, subnet_name: '%s'",
				cmpList.Data.Total, ref.Name,
			)
		}
		if cmpList.Data.Total == 0 {
			log.FromContext(ctx).V(1).Info("subnet not found on CMP, requeuing", "subnetName", ref.Name)
			return nil, nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		// WORKAROUND: CMP bug causes CloudServer creation to stall when a subnet is not yet
		// ready. Wait for the subnet to reach a final CMP state before proceeding with the create.
		// TODO: Remove this block once the CMP Infra Team fixes the root cause.
		if isCreating {
			stateNature := reconciler.AssessCSPResourceStateNature(&cmpList.Data.Values[0].Status)
			if stateNature != reconciler.CSPResourceStateNatureFinal {
				state := "<nil>"
				if cmpList.Data.Values[0].Status.State != nil {
					state = string(*cmpList.Data.Values[0].Status.State)
				}
				log.FromContext(ctx).V(1).Info("subnet not ready on CMP, requeuing", "subnetName", ref.Name, "cmpState", state)
				return nil, nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
			}
		}

		cmpSubnet := &cmpList.Data.Values[0]
		ids = append(ids, *(cmpSubnet.Metadata.ID))
		responses = append(responses, cmpSubnet)
	}
	return ids, responses, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveSecurityGroupIDs(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID, vpcID string,
) ([]string, []*arubatypes.SecurityGroupResponse, ctrl.Result, error) {
	if isDeleting && len(kubeCS.Status.SecurityGroupIDs) > 0 {
		return kubeCS.Status.SecurityGroupIDs, nil, ctrl.Result{}, nil
	}

	ids := make([]string, 0, len(kubeCS.Spec.SecurityGroupReferences))
	responses := make([]*arubatypes.SecurityGroupResponse, 0, len(kubeCS.Spec.SecurityGroupReferences))
	for _, ref := range kubeCS.Spec.SecurityGroupReferences {
		filter := fmt.Sprintf(`name:eq("%s")`, ref.Name)
		cmpList, err := arubaClient.FromNetwork().SecurityGroups().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &filter})
		if err != nil {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: %w, sg_name: '%s'", err, ref.Name,
			)
		}
		if cmpList.IsError() {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: status_code: %d, sg_name: '%s'",
				cmpList.StatusCode, ref.Name,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSecurityGroupList(cmpList, ref.Name, log.FromContext(ctx))
		if cmpList.Data.Total > 1 {
			return nil, nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, found: %d, sg_name: '%s'",
				cmpList.Data.Total, ref.Name,
			)
		}
		if cmpList.Data.Total == 0 {
			log.FromContext(ctx).V(1).Info("security group not found on CMP, requeuing", "sgName", ref.Name)
			return nil, nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		cmpSG := &cmpList.Data.Values[0]
		ids = append(ids, *(cmpSG.Metadata.ID))
		responses = append(responses, cmpSG)
	}
	return ids, responses, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveKeyPairID(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, *arubatypes.KeyPairResponse, ctrl.Result, error) {
	if kubeCS.Spec.KeyPairReference.Name == "" {
		return "", nil, ctrl.Result{}, nil
	}
	if isDeleting && kubeCS.Status.KeyPairID != "" {
		return kubeCS.Status.KeyPairID, nil, ctrl.Result{}, nil
	}

	kpName := kubeCS.Spec.KeyPairReference.Name
	kpFilter := fmt.Sprintf(`name:eq("%s")`, kpName)

	cmpKPList, err := arubaClient.FromCompute().KeyPairs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &kpFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find key pair in Aruba cloud: %w, keypair_name: '%s'", err, kpName,
		)
	}
	if cmpKPList.IsError() {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find key pair in Aruba cloud: status_code: %d, keypair_name: '%s'",
			cmpKPList.StatusCode, kpName,
		)
	}
	if cmpKPList.Data.Total > 1 {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in key pair list: expected: 1, found: %d, keypair_name: '%s'",
			cmpKPList.Data.Total, kpName,
		)
	}
	if cmpKPList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("key pair not found on CMP, requeuing", "keyPairName", kpName)
		return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	cmpKP := &cmpKPList.Data.Values[0]
	return *(cmpKP.Metadata.ID), cmpKP, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveElasticIPID(
	ctx context.Context,
	arubaClient arubaclient.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, *arubatypes.ElasticIPResponse, ctrl.Result, error) {
	if kubeCS.Spec.ElasticIPReference == nil {
		return "", nil, ctrl.Result{}, nil
	}
	if isDeleting && kubeCS.Status.ElasticIPID != "" {
		return kubeCS.Status.ElasticIPID, nil, ctrl.Result{}, nil
	}

	eipName := kubeCS.Spec.ElasticIPReference.Name
	eipFilter := fmt.Sprintf(`name:eq("%s")`, eipName)

	cmpEipList, err := arubaClient.FromNetwork().ElasticIPs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &eipFilter})
	if err != nil {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find elastic IP in Aruba cloud: %w, eip_name: '%s'", err, eipName,
		)
	}
	if cmpEipList.IsError() {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"failed to find elastic IP in Aruba cloud: status_code: %d, eip_name: '%s'",
			cmpEipList.StatusCode, eipName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToElasticIPList(cmpEipList, eipName, log.FromContext(ctx))
	if cmpEipList.Data.Total > 1 {
		return "", nil, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in elastic IP list: expected: 1, found: %d, eip_name: '%s'",
			cmpEipList.Data.Total, eipName,
		)
	}
	if cmpEipList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("elastic IP not found on CMP, requeuing", "eipName", eipName)
		return "", nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	cmpEip := &cmpEipList.Data.Values[0]
	return *(cmpEip.Metadata.ID), cmpEip, ctrl.Result{}, nil
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeCSHasDeniedChanges(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if !kubeCS.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpCS == nil {
		return false
	}
	return checkCSDeniedChanges(kubeCS, cmpCS) != nil
}

func kubeCSSpecInSyncWithCMP(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		!kubeCSNeedsUpdate(kubeCS, cmpCS)
}

func kubeCSShouldUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		kubeCSNeedsUpdate(kubeCS, cmpCS)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpCloudServerNotExists(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return cmpCS == nil
}

func cmpCloudServerIsFinal(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpCS.Status) == reconciler.CSPResourceStateNatureFinal
}

func cmpCloudServerIsTransitory(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpCS.Status) == reconciler.CSPResourceStateNatureTransitory
}

func cmpCloudServerNotExistsOrTransitory(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil {
		return true
	}
	if cmpCS.Status.State == nil {
		return false
	}
	return reconciler.AssessCSPResourceStateNature(&cmpCS.Status) == reconciler.CSPResourceStateNatureTransitory
}

// cmpCloudServerIsActive returns true when the CMP cloud server is in a final usable state.
// CloudServer may settle into Active, Running, or Stopped after provisioning.
func cmpCloudServerIsActive(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	switch *cmpCS.Status.State {
	case arubatypes.StateActive, arubatypes.StateRunning, arubatypes.StateStopped:
		return true
	default:
		return false
	}
}

func cmpCloudServerIsFailed(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return cmpCS != nil && cmpCS.Status.State != nil && *cmpCS.Status.State == arubatypes.StateFailed
}

// ---------------------------------------------------------------------------
// Kube actions
// ---------------------------------------------------------------------------

func (r *CloudServerReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeCS *v1alpha1.CloudServer, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	prePatches := []func(*v1alpha1.CloudServer){
		func(cs *v1alpha1.CloudServer) {
			if prjID, ok := ctx.Value(projectIDKey).(string); ok && cs.Status.ProjectID == "" {
				cs.Status.ProjectID = prjID
			}
			if vID, ok := ctx.Value(vpcIDKey).(string); ok && cs.Status.VPCID == "" {
				cs.Status.VPCID = vID
			}
			if bvID, ok := ctx.Value(bootVolumeIDKey).(string); ok && cs.Status.BootVolumeID == "" {
				cs.Status.BootVolumeID = bvID
			}
			if kpID, ok := ctx.Value(keyPairIDKey).(string); ok && kpID != "" && cs.Status.KeyPairID == "" {
				cs.Status.KeyPairID = kpID
			}
			if eipID, ok := ctx.Value(elasticIPIDKey).(string); ok && eipID != "" && cs.Status.ElasticIPID == "" {
				cs.Status.ElasticIPID = eipID
			}
			if sIDs, ok := ctx.Value(subnetIDsKey).([]string); ok && len(cs.Status.SubnetIDs) == 0 {
				cs.Status.SubnetIDs = sIDs
			}
			if sgIDs, ok := ctx.Value(securityGroupIDsKey).([]string); ok && len(cs.Status.SecurityGroupIDs) == 0 {
				cs.Status.SecurityGroupIDs = sgIDs
			}
		},
	}
	return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeCS, phase, reason, nil, prePatches...)
}

func (r *CloudServerReconciler) kubeMarkToDelete(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkDeleting(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkDeletingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkDeleted(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkToUpdate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkUpdating(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkToCreate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkCreating(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkCreatingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	cmpID := ""
	if cmpCS != nil && cmpCS.Metadata.ID != nil {
		cmpID = *cmpCS.Metadata.ID
	}
	return reconciler.SetActiveAndSetID(r.Client, ctx, kubeCS, cmpID, nil, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && prjID != "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && vID != "" {
			cs.Status.VPCID = vID
		}
		if bvID, ok := ctx.Value(bootVolumeIDKey).(string); ok && bvID != "" {
			cs.Status.BootVolumeID = bvID
		}
		if kpID, ok := ctx.Value(keyPairIDKey).(string); ok && kpID != "" {
			cs.Status.KeyPairID = kpID
		}
		if eipID, ok := ctx.Value(elasticIPIDKey).(string); ok && eipID != "" {
			cs.Status.ElasticIPID = eipID
		}
		if sIDs, ok := ctx.Value(subnetIDsKey).([]string); ok && len(sIDs) > 0 {
			cs.Status.SubnetIDs = sIDs
		}
		if sgIDs, ok := ctx.Value(securityGroupIDsKey).([]string); ok && len(sgIDs) > 0 {
			cs.Status.SecurityGroupIDs = sgIDs
		}
	})
}

func (r *CloudServerReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeCS, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && cs.Status.ProjectID == "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && cs.Status.VPCID == "" {
			cs.Status.VPCID = vID
		}
	})
}

func (r *CloudServerReconciler) kubeSetFailed(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *CloudServerReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)
	prjID := ctx.Value(projectIDKey).(string)

	resp, err := arubaClient.FromCompute().CloudServers().Delete(ctx, prjID, *cmpCS.Metadata.ID, nil)
	if err != nil {
		return reconciler.CMPTransportError("delete", *cmpCS.Metadata.Name, err)
	}
	return reconciler.CMPCheckResponse("delete", *cmpCS.Metadata.Name, resp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

func (r *CloudServerReconciler) cmpUpdate(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)
	prjID := ctx.Value(projectIDKey).(string)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkCSDeniedChanges(kubeCS, cmpCS); err != nil {
		return err
	}

	request := buildCSUpdateRequest(ctx, kubeCS, cmpCS)
	resp, err := arubaClient.FromCompute().CloudServers().Update(ctx, prjID, *cmpCS.Metadata.ID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("update", kubeCS.Name, err)
	}
	return reconciler.CMPCheckResponse("update", kubeCS.Name, resp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *CloudServerReconciler) cmpCreate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(arubaclient.Client)
	prjID := ctx.Value(projectIDKey).(string)

	request := cmpCSRequestFromKube(ctx, kubeCS)
	resp, err := arubaClient.FromCompute().CloudServers().Create(ctx, prjID, *request, nil)
	if err != nil {
		return reconciler.CMPTransportError("create", kubeCS.Name, err)
	}
	return reconciler.CMPCheckResponse("create", kubeCS.Name, resp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

func checkCSDeniedChanges(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	if cmpCS == nil {
		return nil
	}

	if kubeCS.Spec.Zone != string(cmpCS.Properties.Zone) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'dataCenter' is not allowed"))
	}

	if kubeCS.Spec.FlavorName != string(cmpCS.Properties.Flavor.Name) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'flavorName' is not allowed"))
	}

	// vpcPreset is immutable but has no comparable field in CloudServerPropertiesResult;
	// its enforcement is a CRD-level concern (webhook) and cannot be detected from CMP state.

	return nil
}

func kubeCSNeedsUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeCS.Spec.Tags, cmpCS.Metadata.Tags) {
		return true
	}
	if cmpCS.Metadata.LocationResponse != nil && kubeCS.Spec.Region != string(cmpCS.Metadata.LocationResponse.Value) {
		return true
	}
	return false
}

func buildCSUpdateRequest(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) *arubatypes.CloudServerRequest {
	vpcID := ctx.Value(vpcIDKey).(string)
	prjID := ctx.Value(projectIDKey).(string)
	subnetIDs := ctx.Value(subnetIDsKey).([]string)
	sgIDs := ctx.Value(securityGroupIDsKey).([]string)
	elasticIPID := ctx.Value(elasticIPIDKey).(string)

	tags := make([]string, len(kubeCS.Spec.Tags))
	copy(tags, kubeCS.Spec.Tags)

	subnets := make([]arubatypes.ReferenceResourceCommon, 0, len(subnetIDs))
	for _, sid := range subnetIDs {
		subnets = append(subnets, arubatypes.ReferenceResourceCommon{URI: buildSubnetURI(prjID, vpcID, sid)})
	}

	sgs := make([]arubatypes.ReferenceResourceCommon, 0, len(sgIDs))
	for _, sgid := range sgIDs {
		sgs = append(sgs, arubatypes.ReferenceResourceCommon{URI: buildSecurityGroupURI(prjID, vpcID, sgid)})
	}

	flavorName := cmpCS.Properties.Flavor.Name

	req := &arubatypes.CloudServerRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: *cmpCS.Metadata.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: arubatypes.Region(kubeCS.Spec.Region)},
		},
		Properties: arubatypes.CloudServerPropertiesRequest{
			Zone:           cmpCS.Properties.Zone,
			VPC:            arubatypes.ReferenceResourceCommon{URI: cmpCS.Properties.VPC.URI},
			VPCPreset:      false,
			FlavorName:     &flavorName,
			BootVolume:     arubatypes.ReferenceResourceCommon{URI: cmpCS.Properties.BootVolume.URI},
			KeyPair:        &arubatypes.ReferenceResourceCommon{URI: cmpCS.Properties.KeyPair.URI},
			Subnets:        subnets,
			SecurityGroups: sgs,
		},
	}

	if elasticIPID != "" {
		req.Properties.ElasticIP = &arubatypes.ReferenceResourceCommon{URI: buildElasticIPURI(prjID, elasticIPID)}
	}

	return req
}

func cmpCSRequestFromKube(ctx context.Context, kubeCS *v1alpha1.CloudServer) *arubatypes.CloudServerRequest {
	prjID := ctx.Value(projectIDKey).(string)
	vpcID := ctx.Value(vpcIDKey).(string)
	bootVolumeID := ctx.Value(bootVolumeIDKey).(string)
	subnetIDs := ctx.Value(subnetIDsKey).([]string)
	sgIDs := ctx.Value(securityGroupIDsKey).([]string)
	keyPairID := ctx.Value(keyPairIDKey).(string)
	elasticIPID := ctx.Value(elasticIPIDKey).(string)

	tags := make([]string, len(kubeCS.Spec.Tags))
	copy(tags, kubeCS.Spec.Tags)

	subnets := make([]arubatypes.ReferenceResourceCommon, 0, len(subnetIDs))
	for _, sid := range subnetIDs {
		subnets = append(subnets, arubatypes.ReferenceResourceCommon{URI: buildSubnetURI(prjID, vpcID, sid)})
	}

	sgs := make([]arubatypes.ReferenceResourceCommon, 0, len(sgIDs))
	for _, sgid := range sgIDs {
		sgs = append(sgs, arubatypes.ReferenceResourceCommon{URI: buildSecurityGroupURI(prjID, vpcID, sgid)})
	}

	flavorName := arubatypes.CloudServerFlavor(kubeCS.Spec.FlavorName)

	req := &arubatypes.CloudServerRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeCS.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: arubatypes.Region(kubeCS.Spec.Region)},
		},
		Properties: arubatypes.CloudServerPropertiesRequest{
			Zone:           arubatypes.Zone(kubeCS.Spec.Zone),
			VPC:            arubatypes.ReferenceResourceCommon{URI: buildVpcURI(prjID, vpcID)},
			VPCPreset:      false,
			FlavorName:     &flavorName,
			BootVolume:     arubatypes.ReferenceResourceCommon{URI: buildVolumeURI(prjID, bootVolumeID)},
			Subnets:        subnets,
			SecurityGroups: sgs,
		},
	}

	if keyPairID != "" {
		req.Properties.KeyPair = &arubatypes.ReferenceResourceCommon{URI: buildKeyPairURI(prjID, keyPairID)}
	}
	if elasticIPID != "" {
		req.Properties.ElasticIP = &arubatypes.ReferenceResourceCommon{URI: buildElasticIPURI(prjID, elasticIPID)}
	}

	return req
}

func buildVpcURI(projectID, vpcID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
}

func buildKeyPairURI(projectID, keyPairID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
}

func buildElasticIPURI(projectID, elasticIPID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIPID)
}

func buildSubnetURI(projectID, vpcID, subnetID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/subnets/%s", projectID, vpcID, subnetID)
}

func buildSecurityGroupURI(projectID, vpcID, securityGroupID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s/securityGroups/%s", projectID, vpcID, securityGroupID)
}

func buildVolumeURI(projectID, volumeID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Storage/blockStorages/%s", projectID, volumeID)
}

// ---------------------------------------------------------------------------
// Setup
// ---------------------------------------------------------------------------

// SetupWithManager sets up the controller with the Manager.
func (r *CloudServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CloudServer{}).
		Named("cloudserver").
		Complete(r)
}
