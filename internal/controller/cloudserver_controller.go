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

	"github.com/Arubacloud/sdk-go/pkg/aruba"

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

// cloudServerBundle is the vs (drift-validation) type parameter. CloudServer's
// drift rules compare against the K8s dependency objects only, so it embeds just
// the K8s bundle — no CMP-side responses are needed.
type cloudServerBundle struct {
	kubeCloudServerBundle
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
	ivs *reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *kubeCloudServerBundle]
	vs  *reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *cloudServerBundle]
	ts  *reconciler.TransitionSet[*v1alpha1.CloudServer, *aruba.CloudServer]
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
	cmpCS, ctx, result, err := r.fetchCMPDependencies(ctx, kubeCS, arubaClient, isDeleting, isCreating)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// Stage 7: CMP-aware validation (vs only).
	if !isDeleting && cmpCS != nil && kubeBdl.KubeProject != nil {
		if validationErr := r.vs.Run(kubeCS, cmpCS, &cloudServerBundle{
			kubeCloudServerBundle: *kubeBdl,
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
	arubaClient aruba.Client,
	isDeleting bool,
	isCreating bool,
) (cmpCS *aruba.CloudServer, enrichedCtx context.Context, result ctrl.Result, err error) {
	// --- Resolve Project ID ---
	prjID, result, err := r.resolveProjectID(ctx, arubaClient, kubeCS, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve VPC ID ---
	vpcID, result, err := r.resolveVpcID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve Boot Volume ID ---
	bootVolumeID, result, err := r.resolveBootVolumeID(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve Subnet IDs ---
	subnetIDs, result, err := r.resolveSubnetIDs(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve Security Group IDs ---
	securityGroupIDs, result, err := r.resolveSecurityGroupIDs(ctx, arubaClient, kubeCS, isDeleting, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve KeyPair ID (optional) ---
	keyPairID, result, err := r.resolveKeyPairID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Resolve Elastic IP ID (optional) ---
	elasticIPID, result, err := r.resolveElasticIPID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return nil, ctx, result, err
	}

	// --- Fetch CMP CloudServer ---
	csName := kubeCS.Name
	csFilter := fmt.Sprintf(`name:eq("%s")`, csName)

	cmpCSList, err := arubaClient.FromCompute().CloudServers().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(csFilter))
	if err != nil && !isCMPNotFound(err) {
		return nil, ctx, ctrl.Result{}, fmt.Errorf(
			"failed to find cloud server in Aruba cloud: %w, name: '%s'",
			err, csName,
		)
	}
	var cmpServers []*aruba.CloudServer
	if cmpCSList != nil {
		cmpServers = cmpCSList.Items()
	}
	if len(cmpServers) > 1 {
		return nil, ctx, ctrl.Result{}, fmt.Errorf(
			"inconsistent data in cloud server list: name: '%s', instances: %d",
			csName, len(cmpServers),
		)
	}

	if len(cmpServers) == 1 {
		cmpCS = cmpServers[0]
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

	return cmpCS, ctx, ctrl.Result{}, nil
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) newIntentionValidationSet() *reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *kubeCloudServerBundle] {
	ivs := &reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *kubeCloudServerBundle]{}
	// Required references
	ivs.Add("ProjectReferenceRequired", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, _ *kubeCloudServerBundle) error {
		if k.Spec.ProjectReference.Name == "" {
			return fmt.Errorf("project reference is required")
		}
		return nil
	})
	ivs.Add("VPCReferenceRequired", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, _ *kubeCloudServerBundle) error {
		if k.Spec.VPCReference.Name == "" {
			return fmt.Errorf("vpc reference is required")
		}
		return nil
	})
	ivs.Add("BootVolumeReferenceRequired", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, _ *kubeCloudServerBundle) error {
		if k.Spec.BootVolumeReference.Name == "" {
			return fmt.Errorf("boot volume reference is required")
		}
		return nil
	})
	ivs.Add("SubnetReferencesRequired", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, _ *kubeCloudServerBundle) error {
		if len(k.Spec.SubnetReferences) == 0 {
			return fmt.Errorf("at least one subnet reference is required")
		}
		return nil
	})
	ivs.Add("SecurityGroupReferencesRequired", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, _ *kubeCloudServerBundle) error {
		if len(k.Spec.SecurityGroupReferences) == 0 {
			return fmt.Errorf("at least one security group reference is required")
		}
		return nil
	})
	// 1. Tenant must match Project (nil-guarded — Project may not be resolved yet)
	ivs.Add("TenantMustMatchProject", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeProject == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeProject.Spec.Tenant != "" && k.Spec.Tenant != b.KubeProject.Spec.Tenant {
			return fmt.Errorf("tenant mismatch with Project: %q != %q", k.Spec.Tenant, b.KubeProject.Spec.Tenant)
		}
		return nil
	})
	// 2. VPC must match all Subnets (K8s-only)
	ivs.Add("VPCMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
	ivs.Add("VPCMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
	ivs.Add("TenantMustMatchVPC", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match VPC tenant %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	// 12. Tenant must match BootVolume (nil-guarded)
	ivs.Add("TenantMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeBootVolume.Spec.Tenant != "" && k.Spec.Tenant != b.KubeBootVolume.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match BlockStorage tenant %q", k.Spec.Tenant, b.KubeBootVolume.Spec.Tenant)
		}
		return nil
	})
	// 13. Tenant must match KeyPair (nil-guarded, optional)
	ivs.Add("TenantMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeKeyPair.Spec.Tenant != "" && k.Spec.Tenant != b.KubeKeyPair.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match KeyPair tenant %q", k.Spec.Tenant, b.KubeKeyPair.Spec.Tenant)
		}
		return nil
	})
	// 14. Tenant must match ElasticIP (nil-guarded, optional)
	ivs.Add("TenantMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeElasticIP.Spec.Tenant != "" && k.Spec.Tenant != b.KubeElasticIP.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match ElasticIP tenant %q", k.Spec.Tenant, b.KubeElasticIP.Spec.Tenant)
		}
		return nil
	})
	// 15. Tenant must match all Subnets
	ivs.Add("TenantMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
	ivs.Add("TenantMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
	ivs.Add("ProjectMustMatchVPC", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match VPC project reference %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 18. Project reference must match BootVolume (nil-guarded)
	ivs.Add("ProjectMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeBootVolume.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match BlockStorage project reference %q", k.Spec.ProjectReference.Name, b.KubeBootVolume.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 19. Project reference must match KeyPair (nil-guarded, optional)
	ivs.Add("ProjectMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeKeyPair.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match KeyPair project reference %q", k.Spec.ProjectReference.Name, b.KubeKeyPair.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 20. Project reference must match ElasticIP (nil-guarded, optional)
	ivs.Add("ProjectMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeElasticIP.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match ElasticIP project reference %q", k.Spec.ProjectReference.Name, b.KubeElasticIP.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 21. Project reference must match all Subnets
	ivs.Add("ProjectMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
	ivs.Add("ProjectMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
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
func (r *CloudServerReconciler) newValidationSet() *reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *cloudServerBundle] {
	vs := &reconciler.ValidationSet[*v1alpha1.CloudServer, *aruba.CloudServer, *cloudServerBundle]{}
	// 1. Tenant must match Project
	vs.Add("TenantMustMatchProject", reconciler.FieldMustMatch[*v1alpha1.CloudServer, *aruba.CloudServer, *cloudServerBundle](
		"tenant",
		func(k *v1alpha1.CloudServer) string { return k.Spec.Tenant },
		func(b *cloudServerBundle) string { return b.KubeProject.Spec.Tenant },
		"Project",
	))
	// 2. VPC must match all Subnets (K8s-only)
	vs.Add("VPCMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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
	vs.Add("VPCMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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
	vs.Add("TenantMustMatchVPC", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeVpc.Spec.Tenant != "" && k.Spec.Tenant != b.KubeVpc.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match VPC tenant %q", k.Spec.Tenant, b.KubeVpc.Spec.Tenant)
		}
		return nil
	})
	// 12. Tenant must match BootVolume (K8s-side, nil-guarded)
	vs.Add("TenantMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeBootVolume.Spec.Tenant != "" && k.Spec.Tenant != b.KubeBootVolume.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match BlockStorage tenant %q", k.Spec.Tenant, b.KubeBootVolume.Spec.Tenant)
		}
		return nil
	})
	// 13. Tenant must match KeyPair (K8s-side, nil-guarded, optional)
	vs.Add("TenantMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeKeyPair.Spec.Tenant != "" && k.Spec.Tenant != b.KubeKeyPair.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match KeyPair tenant %q", k.Spec.Tenant, b.KubeKeyPair.Spec.Tenant)
		}
		return nil
	})
	// 14. Tenant must match ElasticIP (K8s-side, nil-guarded, optional)
	vs.Add("TenantMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.Tenant != "" && b.KubeElasticIP.Spec.Tenant != "" && k.Spec.Tenant != b.KubeElasticIP.Spec.Tenant {
			return fmt.Errorf("tenant %q does not match ElasticIP tenant %q", k.Spec.Tenant, b.KubeElasticIP.Spec.Tenant)
		}
		return nil
	})
	// 15. Tenant must match all Subnets (K8s-side)
	vs.Add("TenantMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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
	vs.Add("TenantMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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
	vs.Add("ProjectMustMatchVPC", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeVpc == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeVpc.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match VPC project reference %q", k.Spec.ProjectReference.Name, b.KubeVpc.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 18. Project reference must match BootVolume (K8s-side, nil-guarded)
	vs.Add("ProjectMustMatchBootVolume", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeBootVolume == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeBootVolume.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match BlockStorage project reference %q", k.Spec.ProjectReference.Name, b.KubeBootVolume.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 19. Project reference must match KeyPair (K8s-side, nil-guarded, optional)
	vs.Add("ProjectMustMatchKeyPair", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeKeyPair == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeKeyPair.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match KeyPair project reference %q", k.Spec.ProjectReference.Name, b.KubeKeyPair.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 20. Project reference must match ElasticIP (K8s-side, nil-guarded, optional)
	vs.Add("ProjectMustMatchElasticIP", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
		if b.KubeElasticIP == nil {
			return nil
		}
		if k.Spec.ProjectReference.Name != b.KubeElasticIP.Spec.ProjectReference.Name {
			return fmt.Errorf("project reference %q does not match ElasticIP project reference %q", k.Spec.ProjectReference.Name, b.KubeElasticIP.Spec.ProjectReference.Name)
		}
		return nil
	})
	// 21. Project reference must match all Subnets (K8s-side)
	vs.Add("ProjectMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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
	vs.Add("ProjectMustMatchAllSecurityGroups", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *cloudServerBundle) error {
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

func (r *CloudServerReconciler) newTransitionSet() *reconciler.TransitionSet[*v1alpha1.CloudServer, *aruba.CloudServer] {
	ts := &reconciler.TransitionSet[*v1alpha1.CloudServer, *aruba.CloudServer]{
		DefaultRequeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		DefaultRequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "PhaseTimedOut",
		KCondition:     reconciler.KubePhaseTimedOut[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *aruba.CloudServer],
		KAction:        r.kubeSetFailedOnTimeout,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 1. ValidationFailedAndDeleting — unblock deletion for resources stuck in any *ValidationFailed state
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "ValidationFailedAndDeleting",
		KCondition:     reconciler.KubeAnyValidationFailedAndDeleting[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *aruba.CloudServer],
		KAction:        reconciler.KubeResetValidationFailedForDeletion[*v1alpha1.CloudServer, *aruba.CloudServer](r.Client),
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueAndPropagateError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 2. PendingAndDeleting — resource deleted while still in Pending; skip CMP entirely
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:       "PendingAndDeleting",
		KCondition: reconciler.KubePendingAndDeleting[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition: reconciler.AlwaysTrue[*v1alpha1.CloudServer, *aruba.CloudServer],
		KAction:    reconciler.KubeDeleteFromPending[*v1alpha1.CloudServer, *aruba.CloudServer](r.Client),
		Requeue:    reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 3. ShouldBeDeleted
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "ShouldBeDeleted",
		KCondition:     reconciler.KubeShouldDelete[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 4. ShouldDeleteTimedOut
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "ShouldDeleteTimedOut",
		KCondition:     reconciler.KubeShouldDeleteTimedOut[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *aruba.CloudServer],
		KAction:        r.kubeMarkToDelete,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 5. ShouldBeDeletedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:              "ShouldBeDeletedOnCMP",
		KCondition:        reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:        cmpCloudServerIsFinal,
		AAction:           r.cmpDelete,
		KActionOnASuccess: r.kubeMarkDeleting,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *aruba.CloudServer](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 6. DeletionOnCMPNotNeeded
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "DeletionOnCMPNotNeeded",
		KCondition:     reconciler.KubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 7. WaitingDeletionOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "WaitingDeletionOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 8. DeletionConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "DeletionConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeletingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 9. DeletionAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "DeletionAccomplished",
		KCondition:     reconciler.KubeDeletionAccomplished[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkDeleted,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 10. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:       "HasDeniedChanges",
		KCondition: kubeCSHasDeniedChanges,
		ACondition: cmpCloudServerIsFinal,
		KAction: func(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) error {
			return fmt.Errorf("cloud server update rejected: %w", checkCSDeniedChanges(kubeCS, cmpCS))
		},
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.LongRequeueAndIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 11. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "SpecAlreadyInSyncWithCMP",
		KCondition:     kubeCSSpecInSyncWithCMP,
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 12. ShouldBeUpdated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "ShouldBeUpdated",
		KCondition:     kubeCSShouldUpdate,
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkToUpdate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 13. ShouldBeUpdatedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:              "ShouldBeUpdatedOnCMP",
		KCondition:        reconciler.KubeShouldBeUpdatedOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:        cmpCloudServerIsFinal,
		AAction:           r.cmpUpdate,
		KActionOnASuccess: r.kubeMarkUpdating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *aruba.CloudServer](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 14. WaitingUpdateOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "WaitingUpdateOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 15. UpdateConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "UpdateConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsFinal,
		KAction:        r.kubeMarkUpdatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 16. UpdateAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "UpdateAccomplished",
		KCondition:     reconciler.KubeUpdateAccomplished[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 17. ShouldBeCreated
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "ShouldBeCreated",
		KCondition:     reconciler.KubeIsFirstReconciliation[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerNotExists,
		KAction:        r.kubeMarkToCreate,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 18. ShouldBeCreatedInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:              "ShouldBeCreatedInCMP",
		KCondition:        reconciler.KubeShouldBeCreatedOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:        cmpCloudServerNotExists,
		AAction:           r.cmpCreate,
		KActionOnASuccess: r.kubeMarkCreating,
		KActionOnAError:   reconciler.KubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *aruba.CloudServer](r.Client),
		Requeue:           reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError:    reconciler.SmartRequeueOnError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 19. WaitingCreationInCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "WaitingCreationInCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerNotExistsOrTransitory,
		Requeue:        reconciler.LongRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 20. CreationConfirmedOnCMP
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "CreationConfirmedOnCMP",
		KCondition:     reconciler.KubeWaitingCreationInCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeMarkCreatingDone,
		Requeue:        reconciler.ShortRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 21. CreationAccomplished
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "CreationAccomplished",
		KCondition:     reconciler.KubeIsCreatedOnCMP[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsActive,
		KAction:        r.kubeSetActiveAndSetID,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	// 22. IsInError
	ts.Add(&reconciler.AbstractTransition[*v1alpha1.CloudServer, *aruba.CloudServer]{
		Name:           "IsInError",
		KCondition:     reconciler.AlwaysTrue[*v1alpha1.CloudServer, *aruba.CloudServer],
		ACondition:     cmpCloudServerIsFailed,
		KAction:        r.kubeSetFailed,
		Requeue:        reconciler.NoRequeue[*v1alpha1.CloudServer, *aruba.CloudServer],
		RequeueOnError: reconciler.NoRequeueButIgnoreError[*v1alpha1.CloudServer, *aruba.CloudServer],
	})

	return ts
}

// ---------------------------------------------------------------------------
// CMP resolve helpers
// ---------------------------------------------------------------------------

func (r *CloudServerReconciler) resolveProjectID(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
) (string, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.ProjectID != "" {
		return kubeCS.Status.ProjectID, ctrl.Result{}, nil
	}

	projectName := kubeCS.Spec.ProjectReference.Name
	prjFilter := fmt.Sprintf(`name:eq("%s")`, projectName)

	cmpProjectList, err := arubaClient.FromProject().List(ctx, aruba.WithFilter(prjFilter))
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find project in Aruba cloud: %w, project_name: '%s'", err, projectName,
		)
	}
	cmpProjects := cmpProjectList.Items()
	if len(cmpProjects) == 0 && kubeCS.Status.ProjectID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: project not found but already recorded: project_name: '%s'", projectName,
		)
	}
	if len(cmpProjects) > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in project list: expected: 1, found: %d, project_name: '%s'",
			len(cmpProjects), projectName,
		)
	}
	if len(cmpProjects) == 0 {
		log.FromContext(ctx).V(1).Info("parent project not found on CMP, requeuing", "projectName", projectName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	prjID := cmpProjects[0].ID()
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
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.VPCID != "" {
		return kubeCS.Status.VPCID, ctrl.Result{}, nil
	}

	vpcName := kubeCS.Spec.VPCReference.Name
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)

	cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(vpcFilter))
	if err != nil && !isCMPNotFound(err) {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: %w, vpc_name: '%s'", err, vpcName,
		)
	}
	// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
	var cmpVpcs []*aruba.VPC
	if cmpVpcList != nil {
		cmpVpcs = filterByName(cmpVpcList.Items(), vpcName, func(v *aruba.VPC) string { return v.Name() })
	}
	if len(cmpVpcs) == 0 && kubeCS.Status.VPCID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: vpc not found but already recorded: vpc_name: '%s'", vpcName,
		)
	}
	if len(cmpVpcs) > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s'",
			len(cmpVpcs), vpcName,
		)
	}
	if len(cmpVpcs) == 0 {
		log.FromContext(ctx).V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	vpcID := cmpVpcs[0].ID()
	if kubeCS.Status.VPCID != "" && kubeCS.Status.VPCID != vpcID {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id: recorded: '%s', found: '%s', vpc_name: '%s'",
			kubeCS.Status.VPCID, vpcID, vpcName,
		)
	}
	return vpcID, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveBootVolumeID(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	// WORKAROUND: isCreating gates the readiness check below.
	// TODO: remove once CMP Infra Team fixes the root cause.
	isCreating bool,
	prjID string,
) (string, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.BootVolumeID != "" {
		return kubeCS.Status.BootVolumeID, ctrl.Result{}, nil
	}

	volName := kubeCS.Spec.BootVolumeReference.Name
	volFilter := fmt.Sprintf(`name:eq("%s")`, volName)

	cmpVolList, err := arubaClient.FromStorage().Volumes().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(volFilter))
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find boot volume in Aruba cloud: %w, volume_name: '%s'", err, volName,
		)
	}
	cmpVols := cmpVolList.Items()
	if len(cmpVols) == 0 && kubeCS.Status.BootVolumeID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: boot volume not found but already recorded: volume_name: '%s'", volName,
		)
	}
	if len(cmpVols) > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in volume list: expected: 1, found: %d, volume_name: '%s'",
			len(cmpVols), volName,
		)
	}
	if len(cmpVols) == 0 {
		log.FromContext(ctx).V(1).Info("boot volume not found on CMP, requeuing", "volumeName", volName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	cmpVol := cmpVols[0]

	// WORKAROUND: CMP bug causes CloudServer creation to stall when the boot volume is not yet
	// ready. Wait for the volume to reach a final CMP state before proceeding with the create.
	// TODO: Remove this block once the CMP Infra Team fixes the root cause.
	if isCreating && !reconciler.IsFinalState(cmpVol.State()) {
		log.FromContext(ctx).V(1).Info("boot volume not ready on CMP, requeuing", "volumeName", volName, "cmpState", string(cmpVol.State()))
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	volID := cmpVol.ID()
	if kubeCS.Status.BootVolumeID != "" && kubeCS.Status.BootVolumeID != volID {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent boot volume id: recorded: '%s', found: '%s', volume_name: '%s'",
			kubeCS.Status.BootVolumeID, volID, volName,
		)
	}
	return volID, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveSubnetIDs(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	// WORKAROUND: isCreating gates the readiness check below.
	// TODO: remove once CMP Infra Team fixes the root cause.
	isCreating bool,
	prjID, vpcID string,
) ([]string, ctrl.Result, error) {
	if isDeleting && len(kubeCS.Status.SubnetIDs) > 0 {
		return kubeCS.Status.SubnetIDs, ctrl.Result{}, nil
	}

	ids := make([]string, 0, len(kubeCS.Spec.SubnetReferences))
	for _, ref := range kubeCS.Spec.SubnetReferences {
		cmpList, err := arubaClient.FromNetwork().Subnets().List(ctx, aruba.VPCRef(prjID, vpcID), aruba.WithFilter(fmt.Sprintf(`name:eq("%s")`, ref.Name)))
		if err != nil && !isCMPNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find subnet in Aruba cloud: %w, subnet_name: '%s'", err, ref.Name,
			)
		}
		// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
		var cmpSubnets []*aruba.Subnet
		if cmpList != nil {
			cmpSubnets = filterByName(cmpList.Items(), ref.Name, func(s *aruba.Subnet) string { return s.Name() })
		}
		if len(cmpSubnets) > 1 {
			return nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in subnet list: expected: 1, found: %d, subnet_name: '%s'",
				len(cmpSubnets), ref.Name,
			)
		}
		if len(cmpSubnets) == 0 {
			log.FromContext(ctx).V(1).Info("subnet not found on CMP, requeuing", "subnetName", ref.Name)
			return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		cmpSubnet := cmpSubnets[0]

		// WORKAROUND: CMP bug causes CloudServer creation to stall when a subnet is not yet
		// ready. Wait for the subnet to reach a final CMP state before proceeding with the create.
		// TODO: Remove this block once the CMP Infra Team fixes the root cause.
		if isCreating && !reconciler.IsFinalState(cmpSubnet.State()) {
			log.FromContext(ctx).V(1).Info("subnet not ready on CMP, requeuing", "subnetName", ref.Name, "cmpState", string(cmpSubnet.State()))
			return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		ids = append(ids, cmpSubnet.ID())
	}
	return ids, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveSecurityGroupIDs(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID, vpcID string,
) ([]string, ctrl.Result, error) {
	if isDeleting && len(kubeCS.Status.SecurityGroupIDs) > 0 {
		return kubeCS.Status.SecurityGroupIDs, ctrl.Result{}, nil
	}

	ids := make([]string, 0, len(kubeCS.Spec.SecurityGroupReferences))
	for _, ref := range kubeCS.Spec.SecurityGroupReferences {
		cmpList, err := arubaClient.FromNetwork().SecurityGroups().List(ctx, aruba.VPCRef(prjID, vpcID), aruba.WithFilter(fmt.Sprintf(`name:eq("%s")`, ref.Name)))
		if err != nil && !isCMPNotFound(err) {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: %w, sg_name: '%s'", err, ref.Name,
			)
		}
		// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
		var cmpSGs []*aruba.SecurityGroup
		if cmpList != nil {
			cmpSGs = filterByName(cmpList.Items(), ref.Name, func(s *aruba.SecurityGroup) string { return s.Name() })
		}
		if len(cmpSGs) > 1 {
			return nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, found: %d, sg_name: '%s'",
				len(cmpSGs), ref.Name,
			)
		}
		if len(cmpSGs) == 0 {
			log.FromContext(ctx).V(1).Info("security group not found on CMP, requeuing", "sgName", ref.Name)
			return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		ids = append(ids, cmpSGs[0].ID())
	}
	return ids, ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveKeyPairID(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, ctrl.Result, error) {
	if kubeCS.Spec.KeyPairReference.Name == "" {
		return "", ctrl.Result{}, nil
	}
	if isDeleting && kubeCS.Status.KeyPairID != "" {
		return kubeCS.Status.KeyPairID, ctrl.Result{}, nil
	}

	kpName := kubeCS.Spec.KeyPairReference.Name
	kpFilter := fmt.Sprintf(`name:eq("%s")`, kpName)

	cmpKPList, err := arubaClient.FromCompute().KeyPairs().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(kpFilter))
	if err != nil && !isCMPNotFound(err) {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find key pair in Aruba cloud: %w, keypair_name: '%s'", err, kpName,
		)
	}
	var cmpKPs []*aruba.KeyPair
	if cmpKPList != nil {
		cmpKPs = cmpKPList.Items()
	}
	if len(cmpKPs) > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in key pair list: expected: 1, found: %d, keypair_name: '%s'",
			len(cmpKPs), kpName,
		)
	}
	if len(cmpKPs) == 0 {
		log.FromContext(ctx).V(1).Info("key pair not found on CMP, requeuing", "keyPairName", kpName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	return cmpKPs[0].ID(), ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveElasticIPID(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, ctrl.Result, error) {
	if kubeCS.Spec.ElasticIPReference == nil {
		return "", ctrl.Result{}, nil
	}
	if isDeleting && kubeCS.Status.ElasticIPID != "" {
		return kubeCS.Status.ElasticIPID, ctrl.Result{}, nil
	}

	eipName := kubeCS.Spec.ElasticIPReference.Name
	eipFilter := fmt.Sprintf(`name:eq("%s")`, eipName)

	cmpEipList, err := arubaClient.FromNetwork().ElasticIPs().List(ctx, aruba.URI("/projects/"+prjID), aruba.WithFilter(eipFilter))
	if err != nil && !isCMPNotFound(err) {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find elastic IP in Aruba cloud: %w, eip_name: '%s'", err, eipName,
		)
	}
	// Client-side name filter workaround (issue https://jira.aruba.it/browse/DEV-66643).
	var cmpEips []*aruba.ElasticIP
	if cmpEipList != nil {
		cmpEips = filterByName(cmpEipList.Items(), eipName, func(e *aruba.ElasticIP) string { return e.Name() })
	}
	if len(cmpEips) > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in elastic IP list: expected: 1, found: %d, eip_name: '%s'",
			len(cmpEips), eipName,
		)
	}
	if len(cmpEips) == 0 {
		log.FromContext(ctx).V(1).Info("elastic IP not found on CMP, requeuing", "eipName", eipName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	return cmpEips[0].ID(), ctrl.Result{}, nil
}

// ---------------------------------------------------------------------------
// Kube conditions
// ---------------------------------------------------------------------------

func kubeCSHasDeniedChanges(kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	if !kubeCS.DeletionTimestamp.IsZero() {
		return false
	}
	if cmpCS == nil {
		return false
	}
	return checkCSDeniedChanges(kubeCS, cmpCS) != nil
}

func kubeCSSpecInSyncWithCMP(kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		!kubeCSNeedsUpdate(kubeCS, cmpCS)
}

func kubeCSShouldUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return reconciler.KubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		kubeCSNeedsUpdate(kubeCS, cmpCS)
}

// ---------------------------------------------------------------------------
// CMP conditions
// ---------------------------------------------------------------------------

func cmpCloudServerNotExists(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return cmpCS == nil
}

func cmpCloudServerIsFinal(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return cmpCS != nil && reconciler.IsFinalState(cmpCS.State())
}

func cmpCloudServerIsTransitory(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return cmpCS != nil && cmpCS.State().IsTransitory()
}

func cmpCloudServerNotExistsOrTransitory(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return cmpCS == nil || cmpCS.State().IsTransitory()
}

// cmpCloudServerIsActive returns true when the CMP cloud server is in a final usable state.
// CloudServer may settle into Active, Running, or Stopped after provisioning.
func cmpCloudServerIsActive(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	if cmpCS == nil {
		return false
	}
	switch cmpCS.State() {
	case aruba.StateActive, aruba.StateRunning, aruba.StateStopped:
		return true
	}
	return false
}

func cmpCloudServerIsFailed(_ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	return cmpCS != nil && cmpCS.State().IsFailure()
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

func (r *CloudServerReconciler) kubeMarkToDelete(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkDeleting(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkDeletingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleting, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkDeleted(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseDeleted, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkToUpdate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkUpdating(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkUpdatingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseUpdating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeMarkToCreate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil)
}

func (r *CloudServerReconciler) kubeMarkCreating(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronizing, nil)
}

func (r *CloudServerReconciler) kubeMarkCreatingDone(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonSynchronized, nil)
}

func (r *CloudServerReconciler) kubeSetActiveAndSetID(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) error {
	cmpID := ""
	if cmpCS != nil {
		cmpID = cmpCS.ID()
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

func (r *CloudServerReconciler) kubeSetFailedOnTimeout(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return reconciler.SetFailedOnTimeout(r.Client, ctx, kubeCS, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && cs.Status.ProjectID == "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && cs.Status.VPCID == "" {
			cs.Status.VPCID = vID
		}
	})
}

func (r *CloudServerReconciler) kubeSetFailed(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// ---------------------------------------------------------------------------
// CMP actions
// ---------------------------------------------------------------------------

func (r *CloudServerReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	err := arubaClient.FromCompute().CloudServers().Delete(ctx, cmpCS)
	return reconciler.CMPErrorFromResult("delete", cmpCS.Name(), err, http.StatusNotFound)
}

func (r *CloudServerReconciler) cmpUpdate(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkCSDeniedChanges(kubeCS, cmpCS); err != nil {
		return err
	}

	prjID := ctx.Value(projectIDKey).(string)
	elasticIPID := ctx.Value(elasticIPIDKey).(string)

	// The fetched wrapper already carries the immutable fields (VPC, boot volume,
	// key pair, zone, flavor, subnets from the response). Mutate the mutable ones
	// (tags, region) and re-attach the security groups — fromResponse does not
	// hydrate securityGroupRefs, so toRequest would otherwise send an empty set.
	cmpCS.RetaggedAs(kubeCS.Spec.Tags...).
		InRegion(aruba.Region(kubeCS.Spec.Region)).
		WithoutVPCPreset().
		WithSecurityGroups(securityGroupRefs(ctx)...)
	if elasticIPID != "" {
		cmpCS.WithElasticIP(aruba.URI(buildElasticIPURI(prjID, elasticIPID)))
	}

	_, err := arubaClient.FromCompute().CloudServers().Update(ctx, cmpCS)
	return reconciler.CMPErrorFromResult("update", kubeCS.Name, err)
}

func (r *CloudServerReconciler) cmpCreate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *aruba.CloudServer) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	prjID := ctx.Value(projectIDKey).(string)
	vpcID := ctx.Value(vpcIDKey).(string)
	bootVolumeID := ctx.Value(bootVolumeIDKey).(string)
	keyPairID := ctx.Value(keyPairIDKey).(string)
	elasticIPID := ctx.Value(elasticIPIDKey).(string)

	server := aruba.NewCloudServer().
		InProject(aruba.URI("/projects/" + prjID)).
		Named(kubeCS.Name).
		Tagged(kubeCS.Spec.Tags...).
		InRegion(aruba.Region(kubeCS.Spec.Region)).
		InZone(aruba.Zone(kubeCS.Spec.Zone)).
		OfFlavor(aruba.CloudServerFlavor(kubeCS.Spec.FlavorName)).
		WithoutVPCPreset().
		WithVPC(aruba.URI(buildVpcURI(prjID, vpcID))).
		BootingFrom(aruba.URI(buildVolumeURI(prjID, bootVolumeID))).
		OnSubnets(subnetRefs(ctx)...).
		WithSecurityGroups(securityGroupRefs(ctx)...)
	if keyPairID != "" {
		server.UsingKeyPair(aruba.URI(buildKeyPairURI(prjID, keyPairID)))
	}
	if elasticIPID != "" {
		server.WithElasticIP(aruba.URI(buildElasticIPURI(prjID, elasticIPID)))
	}

	_, err := arubaClient.FromCompute().CloudServers().Create(ctx, server)
	return reconciler.CMPErrorFromResult("create", kubeCS.Name, err)
}

// ---------------------------------------------------------------------------
// Other helpers
// ---------------------------------------------------------------------------

// subnetRefs builds the subnet URI references from the resolved IDs stashed in ctx.
func subnetRefs(ctx context.Context) []aruba.Ref {
	prjID := ctx.Value(projectIDKey).(string)
	vpcID := ctx.Value(vpcIDKey).(string)
	subnetIDs := ctx.Value(subnetIDsKey).([]string)
	refs := make([]aruba.Ref, 0, len(subnetIDs))
	for _, sid := range subnetIDs {
		refs = append(refs, aruba.URI(buildSubnetURI(prjID, vpcID, sid)))
	}
	return refs
}

// securityGroupRefs builds the security-group URI references from the resolved IDs stashed in ctx.
func securityGroupRefs(ctx context.Context) []aruba.Ref {
	prjID := ctx.Value(projectIDKey).(string)
	vpcID := ctx.Value(vpcIDKey).(string)
	sgIDs := ctx.Value(securityGroupIDsKey).([]string)
	refs := make([]aruba.Ref, 0, len(sgIDs))
	for _, sgid := range sgIDs {
		refs = append(refs, aruba.URI(buildSecurityGroupURI(prjID, vpcID, sgid)))
	}
	return refs
}

func checkCSDeniedChanges(kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) error {
	if cmpCS == nil {
		return nil
	}

	if kubeCS.Spec.Zone != string(cmpCS.Zone()) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'dataCenter' is not allowed"))
	}

	if kubeCS.Spec.FlavorName != string(cmpCS.Flavor()) {
		return fmt.Errorf("%w: %w", reconciler.ErrNotAllowedChanges, errors.New("change the 'flavorName' is not allowed"))
	}

	// vpcPreset is immutable but has no comparable getter on the CloudServer wrapper;
	// its enforcement is a CRD-level concern (webhook) and cannot be detected from CMP state.

	return nil
}

func kubeCSNeedsUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *aruba.CloudServer) bool {
	if cmpCS == nil {
		return false
	}
	if !reconciler.TagsAreEqual(kubeCS.Spec.Tags, cmpCS.Tags()) {
		return true
	}
	return kubeCS.Spec.Region != string(cmpCS.Region())
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
