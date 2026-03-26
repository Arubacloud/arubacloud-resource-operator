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
	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	cloudServerFinalizerName            = "cloudserver.arubacloud.com/finalizer"
	bootVolumeIDKey          contextKey = "bootVolumeID"
	keyPairIDKey             contextKey = "keyPairID"
	elasticIpIDKey           contextKey = "elasticIpID"
	subnetIDsKey             contextKey = "subnetIDs"
	securityGroupIDsKey      contextKey = "securityGroupIDs"
)

// CloudServerReconciler reconciles a CloudServer object
type CloudServerReconciler struct {
	*reconciler.Reconciler
	ts *TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]
}

// NewCloudServerReconciler creates a new CloudServerReconciler
func NewCloudServerReconciler(baseReconciler *reconciler.Reconciler) *CloudServerReconciler {
	r := &CloudServerReconciler{
		Reconciler: baseReconciler,
	}
	r.ts = r.newTransitionSet()
	return r
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

func (r *CloudServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *CloudServerReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.CloudServer{}
}

func (r *CloudServerReconciler) Finalizer() string {
	return cloudServerFinalizerName
}

//nolint:gocyclo // complexity is intentional to maintain locality of behavior
func (r *CloudServerReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	kubeCS, ok := obj.(*v1alpha1.CloudServer)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.CloudServer")
	}

	logger := log.FromContext(ctx).WithValues("tenant", kubeCS.Spec.Tenant)
	logger.Info("reconciling CloudServer")

	arubaClient, err := r.ArubaClient(kubeCS.Spec.Tenant)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Aruba client: %w", err)
	}

	// Validate required references
	if kubeCS.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid")
	}
	if kubeCS.Spec.VpcReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("vpc reference is not valid")
	}
	if kubeCS.Spec.BootVolumeReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("boot volume reference is not valid")
	}
	if len(kubeCS.Spec.SubnetReferences) == 0 {
		return ctrl.Result{}, fmt.Errorf("at least one subnet reference is required")
	}
	if len(kubeCS.Spec.SecurityGroupReferences) == 0 {
		return ctrl.Result{}, fmt.Errorf("at least one security group reference is required")
	}

	if kubeCS.GetDeletionTimestamp().IsZero() {
		kubeProject := &v1alpha1.Project{}
		if err := resolveOwnerObject(ctx, r.Client, kubeCS.Spec.ProjectReference, kubeCS.Namespace, kubeProject); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("resolving parent project for owner reference: %w", err)
			}
			logger.V(1).Info("parent project not found for owner reference setup, skipping", "projectName", kubeCS.Spec.ProjectReference.Name)
		} else {
			requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kubeProject, kubeCS)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("setting owner reference on cloudserver: %w", err)
			}
			if requeue {
				return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
			}
		}
	}

	isDeleting := !kubeCS.GetDeletionTimestamp().IsZero()
	// WORKAROUND: used to gate the dependency readiness checks in resolveBootVolumeID and resolveSubnetIDs.
	// TODO: Remove once the CMP Infra Team fixes the root cause.
	isCreating := !isDeleting && kubeCS.Status.ResourceID == ""

	// --- Resolve Project ID ---
	prjID, result, err := r.resolveProjectID(ctx, arubaClient, kubeCS, isDeleting)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve VPC ID ---
	vpcID, result, err := r.resolveVpcID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve Boot Volume ID ---
	bootVolumeID, result, err := r.resolveBootVolumeID(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve Subnet IDs ---
	subnetIDs, result, err := r.resolveSubnetIDs(ctx, arubaClient, kubeCS, isDeleting, isCreating, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve Security Group IDs ---
	securityGroupIDs, result, err := r.resolveSecurityGroupIDs(ctx, arubaClient, kubeCS, isDeleting, prjID, vpcID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve KeyPair ID (optional) ---
	keyPairID, result, err := r.resolveKeyPairID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Resolve Elastic IP ID (optional) ---
	elasticIpID, result, err := r.resolveElasticIpID(ctx, arubaClient, kubeCS, isDeleting, prjID)
	if err != nil || result != (ctrl.Result{}) {
		return result, err
	}

	// --- Fetch CMP CloudServer ---
	csName := kubeCS.Name
	csFilter := fmt.Sprintf(`name:eq("%s")`, csName)

	cmpCSList, err := arubaClient.FromCompute().CloudServers().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &csFilter})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find cloud server in Aruba cloud: %w, name: '%s'",
			err, csName,
		)
	}
	if cmpCSList.IsError() && cmpCSList.StatusCode != http.StatusNotFound {
		return ctrl.Result{}, fmt.Errorf(
			"failed to find cloud server in Aruba cloud: status_code: %d, name: '%s'",
			cmpCSList.StatusCode, csName,
		)
	}
	if !cmpCSList.IsError() && (cmpCSList.Data.Total < 0 || cmpCSList.Data.Total > 1) {
		return ctrl.Result{}, fmt.Errorf(
			"inconsistent data in cloud server list: name: '%s', instances: %d",
			csName, cmpCSList.Data.Total,
		)
	}

	var cmpCS *arubatypes.CloudServerResponse
	if cmpCSList.Data != nil && cmpCSList.Data.Total == 1 {
		cmpCS = &cmpCSList.Data.Values[0]
	}
	logger.V(1).Info("CMP CloudServer state", "found", cmpCS != nil, "projectID", prjID, "vpcID", vpcID)

	ctx = context.WithValue(ctx, projectIDKey, prjID)
	ctx = context.WithValue(ctx, vpcIDKey, vpcID)
	ctx = context.WithValue(ctx, bootVolumeIDKey, bootVolumeID)
	ctx = context.WithValue(ctx, subnetIDsKey, subnetIDs)
	ctx = context.WithValue(ctx, securityGroupIDsKey, securityGroupIDs)
	ctx = context.WithValue(ctx, keyPairIDKey, keyPairID)
	ctx = context.WithValue(ctx, elasticIpIDKey, elasticIpID)
	ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)
	ctx = log.IntoContext(ctx, logger)

	return r.ts.Run(ctx, kubeCS, cmpCS)
}

// --- Dependency resolution helpers ---

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
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, ctrl.Result, error) {
	if isDeleting && kubeCS.Status.VpcID != "" {
		return kubeCS.Status.VpcID, ctrl.Result{}, nil
	}

	vpcName := kubeCS.Spec.VpcReference.Name
	vpcFilter := fmt.Sprintf(`name:eq("%s")`, vpcName)

	cmpVpcList, err := arubaClient.FromNetwork().VPCs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &vpcFilter})
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: %w, vpc_name: '%s'", err, vpcName,
		)
	}
	if cmpVpcList.IsError() {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find vpc in Aruba cloud: status_code: %d, vpc_name: '%s'",
			cmpVpcList.StatusCode, vpcName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToVPCList(cmpVpcList, vpcName, log.FromContext(ctx))
	if cmpVpcList.Data.Total == 0 && kubeCS.Status.VpcID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: vpc not found but already recorded: vpc_name: '%s'", vpcName,
		)
	}
	if cmpVpcList.Data.Total > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in vpc list: expected: 1, found: %d, vpc_name: '%s'",
			cmpVpcList.Data.Total, vpcName,
		)
	}
	if cmpVpcList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("parent vpc not found on CMP, requeuing", "vpcName", vpcName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	vpcID := *(cmpVpcList.Data.Values[0].Metadata.ID)
	if kubeCS.Status.VpcID != "" && kubeCS.Status.VpcID != vpcID {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent vpc id: recorded: '%s', found: '%s', vpc_name: '%s'",
			kubeCS.Status.VpcID, vpcID, vpcName,
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

	cmpVolList, err := arubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &volFilter})
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find boot volume in Aruba cloud: %w, volume_name: '%s'", err, volName,
		)
	}
	if cmpVolList.IsError() {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find boot volume in Aruba cloud: status_code: %d, volume_name: '%s'",
			cmpVolList.StatusCode, volName,
		)
	}
	if cmpVolList.Data.Total == 0 && kubeCS.Status.BootVolumeID != "" {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data: boot volume not found but already recorded: volume_name: '%s'", volName,
		)
	}
	if cmpVolList.Data.Total > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in volume list: expected: 1, found: %d, volume_name: '%s'",
			cmpVolList.Data.Total, volName,
		)
	}
	if cmpVolList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("boot volume not found on CMP, requeuing", "volumeName", volName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}

	// WORKAROUND: CMP bug causes CloudServer creation to stall when the boot volume is not yet
	// ready. Wait for the volume to reach a final CMP state before proceeding with the create.
	// TODO: Remove this block once the CMP Infra Team fixes the root cause.
	if isCreating {
		stateNature := AssesCSPResourceStateNature(&cmpVolList.Data.Values[0].Status)
		if stateNature != CSPResourceStateNatureFinal {
			state := "<nil>"
			if cmpVolList.Data.Values[0].Status.State != nil {
				state = *cmpVolList.Data.Values[0].Status.State
			}
			log.FromContext(ctx).V(1).Info("boot volume not ready on CMP, requeuing", "volumeName", volName, "cmpState", state)
			return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
	}

	volID := *(cmpVolList.Data.Values[0].Metadata.ID)
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
		filter := fmt.Sprintf(`name:eq("%s")`, ref.Name)
		cmpList, err := arubaClient.FromNetwork().Subnets().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &filter})
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find subnet in Aruba cloud: %w, subnet_name: '%s'", err, ref.Name,
			)
		}
		if cmpList.IsError() {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find subnet in Aruba cloud: status_code: %d, subnet_name: '%s'",
				cmpList.StatusCode, ref.Name,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSubnetList(cmpList, ref.Name, log.FromContext(ctx))
		if cmpList.Data.Total > 1 {
			return nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in subnet list: expected: 1, found: %d, subnet_name: '%s'",
				cmpList.Data.Total, ref.Name,
			)
		}
		if cmpList.Data.Total == 0 {
			log.FromContext(ctx).V(1).Info("subnet not found on CMP, requeuing", "subnetName", ref.Name)
			return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}

		// WORKAROUND: CMP bug causes CloudServer creation to stall when a subnet is not yet
		// ready. Wait for the subnet to reach a final CMP state before proceeding with the create.
		// TODO: Remove this block once the CMP Infra Team fixes the root cause.
		if isCreating {
			stateNature := AssesCSPResourceStateNature(&cmpList.Data.Values[0].Status)
			if stateNature != CSPResourceStateNatureFinal {
				state := "<nil>"
				if cmpList.Data.Values[0].Status.State != nil {
					state = *cmpList.Data.Values[0].Status.State
				}
				log.FromContext(ctx).V(1).Info("subnet not ready on CMP, requeuing", "subnetName", ref.Name, "cmpState", state)
				return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
			}
		}

		ids = append(ids, *(cmpList.Data.Values[0].Metadata.ID))
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
		filter := fmt.Sprintf(`name:eq("%s")`, ref.Name)
		cmpList, err := arubaClient.FromNetwork().SecurityGroups().List(ctx, prjID, vpcID, &arubatypes.RequestParameters{Filter: &filter})
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: %w, sg_name: '%s'", err, ref.Name,
			)
		}
		if cmpList.IsError() {
			return nil, ctrl.Result{}, fmt.Errorf(
				"failed to find security group in Aruba cloud: status_code: %d, sg_name: '%s'",
				cmpList.StatusCode, ref.Name,
			)
		}
		// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
		applyNameFilterToSecurityGroupList(cmpList, ref.Name, log.FromContext(ctx))
		if cmpList.Data.Total > 1 {
			return nil, ctrl.Result{}, fmt.Errorf(
				"inconsistent data in security group list: expected: 1, found: %d, sg_name: '%s'",
				cmpList.Data.Total, ref.Name,
			)
		}
		if cmpList.Data.Total == 0 {
			log.FromContext(ctx).V(1).Info("security group not found on CMP, requeuing", "sgName", ref.Name)
			return nil, ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
		}
		ids = append(ids, *(cmpList.Data.Values[0].Metadata.ID))
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

	cmpKPList, err := arubaClient.FromCompute().KeyPairs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &kpFilter})
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find key pair in Aruba cloud: %w, keypair_name: '%s'", err, kpName,
		)
	}
	if cmpKPList.IsError() {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find key pair in Aruba cloud: status_code: %d, keypair_name: '%s'",
			cmpKPList.StatusCode, kpName,
		)
	}
	if cmpKPList.Data.Total > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in key pair list: expected: 1, found: %d, keypair_name: '%s'",
			cmpKPList.Data.Total, kpName,
		)
	}
	if cmpKPList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("key pair not found on CMP, requeuing", "keyPairName", kpName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	return *(cmpKPList.Data.Values[0].Metadata.ID), ctrl.Result{}, nil
}

func (r *CloudServerReconciler) resolveElasticIpID(
	ctx context.Context,
	arubaClient aruba.Client,
	kubeCS *v1alpha1.CloudServer,
	isDeleting bool,
	prjID string,
) (string, ctrl.Result, error) {
	if kubeCS.Spec.ElasticIpReference == nil {
		return "", ctrl.Result{}, nil
	}
	if isDeleting && kubeCS.Status.ElasticIpID != "" {
		return kubeCS.Status.ElasticIpID, ctrl.Result{}, nil
	}

	eipName := kubeCS.Spec.ElasticIpReference.Name
	eipFilter := fmt.Sprintf(`name:eq("%s")`, eipName)

	cmpEipList, err := arubaClient.FromNetwork().ElasticIPs().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &eipFilter})
	if err != nil {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find elastic IP in Aruba cloud: %w, eip_name: '%s'", err, eipName,
		)
	}
	if cmpEipList.IsError() {
		return "", ctrl.Result{}, fmt.Errorf(
			"failed to find elastic IP in Aruba cloud: status_code: %d, eip_name: '%s'",
			cmpEipList.StatusCode, eipName,
		)
	}
	// TODO: Remove once CMP API name:eq() filter is fixed (issue https://jira.aruba.it/browse/DEV-66643).
	applyNameFilterToElasticIPList(cmpEipList, eipName, log.FromContext(ctx))
	if cmpEipList.Data.Total > 1 {
		return "", ctrl.Result{}, fmt.Errorf(
			"inconsistent data in elastic IP list: expected: 1, found: %d, eip_name: '%s'",
			cmpEipList.Data.Total, eipName,
		)
	}
	if cmpEipList.Data.Total == 0 {
		log.FromContext(ctx).V(1).Info("elastic IP not found on CMP, requeuing", "eipName", eipName)
		return "", ctrl.Result{RequeueAfter: reconciler.LongRequeueAfter}, nil
	}
	return *(cmpEipList.Data.Values[0].Metadata.ID), ctrl.Result{}, nil
}

// --- TransitionSet builder ---

func (r *CloudServerReconciler) newTransitionSet() *TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse] {
	ts := &TransitionSet[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		defaultRequeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		defaultRequeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	}

	// 0. PhaseTimedOut — safety net: fail if stuck in a transitory phase too long
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "PhaseTimedOut",
		kCondition:     kubePhaseTimedOut[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		kAction:        r.kubeSetFailedOnTimeout,
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 1. ShouldBeDeleted
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "ShouldBeDeleted",
		kCondition:     kubeShouldDelete[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsFinal,
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 2. ShouldDeleteTimedOut
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "ShouldDeleteTimedOut",
		kCondition:     kubeShouldDeleteTimedOut[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		kAction:        r.kubeMarkToDelete,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 3. ShouldBeDeletedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:              "ShouldBeDeletedOnCMP",
		kCondition:        kubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:        cmpCloudServerIsFinal,
		aAction:           r.cmpDelete,
		kActionOnASuccess: r.kubeMarkDeleting,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 4. DeletionOnCMPNotNeeded
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "DeletionOnCMPNotNeeded",
		kCondition:     kubeShouldBeDeletedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 5. WaitingDeletionOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "WaitingDeletionOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsTransitory,
		requeue:        LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 6. DeletionConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "DeletionConfirmedOnCMP",
		kCondition:     kubeWaitingDeletionOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerNotExists,
		kAction:        r.kubeMarkDeletingDone,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 7. DeletionAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "DeletionAccomplished",
		kCondition:     kubeDeletionAccomplished[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerNotExists,
		kAction:        r.kubeMarkDeleted,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 8. HasDeniedChanges — surface immutable field violations before attempting update
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:       "HasDeniedChanges",
		kCondition: kubeCSHasDeniedChanges,
		aCondition: cmpCloudServerIsFinal,
		kAction: func(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
			return fmt.Errorf("cloud server update rejected: %w", checkCSDeniedChanges(kubeCS, cmpCS))
		},
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: LongRequeueAndIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 9. SpecAlreadyInSyncWithCMP — generation changed but spec identical to CMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "SpecAlreadyInSyncWithCMP",
		kCondition:     kubeCSSpecInSyncWithCMP,
		aCondition:     cmpCloudServerIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 10. ShouldBeUpdated
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "ShouldBeUpdated",
		kCondition:     kubeCSShouldUpdate,
		aCondition:     cmpCloudServerIsFinal,
		kAction:        r.kubeMarkToUpdate,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 11. ShouldBeUpdatedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:              "ShouldBeUpdatedOnCMP",
		kCondition:        kubeShouldBeUpdatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:        cmpCloudServerIsFinal,
		aAction:           r.cmpUpdate,
		kActionOnASuccess: r.kubeMarkUpdating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 12. WaitingUpdateOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "WaitingUpdateOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsTransitory,
		requeue:        LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 13. UpdateConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "UpdateConfirmedOnCMP",
		kCondition:     kubeWaitingUpdateOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsFinal,
		kAction:        r.kubeMarkUpdatingDone,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 14. UpdateAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "UpdateAccomplished",
		kCondition:     kubeUpdateAccomplished[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 15. ShouldBeCreated
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "ShouldBeCreated",
		kCondition:     kubeIsFirstReconciliation[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerNotExists,
		kAction:        r.kubeMarkToCreate,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 16. ShouldBeCreatedInCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:              "ShouldBeCreatedInCMP",
		kCondition:        kubeShouldBeCreatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:        cmpCloudServerNotExists,
		aAction:           r.cmpCreate,
		kActionOnASuccess: r.kubeMarkCreating,
		kActionOnAError:   kubeSetErrorMessageOnCMPError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse](r.Client),
		requeue:           ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError:    SmartRequeueOnError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 17. WaitingCreationInCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "WaitingCreationInCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerNotExistsOrTransitory,
		requeue:        LongRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 18. CreationConfirmedOnCMP
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "CreationConfirmedOnCMP",
		kCondition:     kubeWaitingCreationInCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsActive,
		kAction:        r.kubeMarkCreatingDone,
		requeue:        ShortRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 19. CreationAccomplished
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "CreationAccomplished",
		kCondition:     kubeIsCreatedOnCMP[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsActive,
		kAction:        r.kubeSetActiveAndSetID,
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	// 20. IsInError
	ts.Add(&AbstractTransition[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse]{
		name:           "IsInError",
		kCondition:     AlwaysTrue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		aCondition:     cmpCloudServerIsFailed,
		kAction:        r.kubeSetFailed,
		requeue:        NoRequeue[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
		requeueOnError: NoRequeueButIgnoreError[*v1alpha1.CloudServer, *arubatypes.CloudServerResponse],
	})

	return ts
}

// --- Resource-specific condition functions ---

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
	return kubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		!kubeCSNeedsUpdate(kubeCS, cmpCS)
}

func kubeCSShouldUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return kubeActiveAndGenerationChanged(kubeCS, cmpCS) &&
		checkCSDeniedChanges(kubeCS, cmpCS) == nil &&
		kubeCSNeedsUpdate(kubeCS, cmpCS)
}

func cmpCloudServerNotExists(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return cmpCS == nil
}

func cmpCloudServerIsFinal(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpCS.Status) == CSPResourceStateNatureFinal
}

func cmpCloudServerIsTransitory(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpCS.Status) == CSPResourceStateNatureTransitory
}

func cmpCloudServerNotExistsOrTransitory(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil {
		return true
	}
	if cmpCS.Status.State == nil {
		return false
	}
	return AssesCSPResourceStateNature(&cmpCS.Status) == CSPResourceStateNatureTransitory
}

// cmpCloudServerIsActive returns true when the CMP cloud server is in a final usable state.
// CloudServer may settle into Active, Running, or Stopped after provisioning.
func cmpCloudServerIsActive(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil || cmpCS.Status.State == nil {
		return false
	}
	switch *cmpCS.Status.State {
	case CSPResourceStateActive, CSPResourceStateRunning, CSPResourceStateStopped:
		return true
	}
	return false
}

func cmpCloudServerIsFailed(_ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	return cmpCS != nil && cmpCS.Status.State != nil && *cmpCS.Status.State == CSPResourceStateFailed
}

// --- Kube action methods ---

func (r *CloudServerReconciler) kubeSetPhaseAndCondition(ctx context.Context, kubeCS *v1alpha1.CloudServer, phase v1alpha1.ResourcePhase, reason string, _ error) error {
	return setPhaseAndCondition(r.Client, ctx, kubeCS, phase, reason, nil, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && cs.Status.ProjectID == "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && cs.Status.VpcID == "" {
			cs.Status.VpcID = vID
		}
		if bvID, ok := ctx.Value(bootVolumeIDKey).(string); ok && cs.Status.BootVolumeID == "" {
			cs.Status.BootVolumeID = bvID
		}
		if kpID, ok := ctx.Value(keyPairIDKey).(string); ok && kpID != "" && cs.Status.KeyPairID == "" {
			cs.Status.KeyPairID = kpID
		}
		if eipID, ok := ctx.Value(elasticIpIDKey).(string); ok && eipID != "" && cs.Status.ElasticIpID == "" {
			cs.Status.ElasticIpID = eipID
		}
		if sIDs, ok := ctx.Value(subnetIDsKey).([]string); ok && len(cs.Status.SubnetIDs) == 0 {
			cs.Status.SubnetIDs = sIDs
		}
		if sgIDs, ok := ctx.Value(securityGroupIDsKey).([]string); ok && len(cs.Status.SecurityGroupIDs) == 0 {
			cs.Status.SecurityGroupIDs = sgIDs
		}
	})
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
	return setActiveAndSetID(r.Client, ctx, kubeCS, cmpID, nil, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && prjID != "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && vID != "" {
			cs.Status.VpcID = vID
		}
		if bvID, ok := ctx.Value(bootVolumeIDKey).(string); ok && bvID != "" {
			cs.Status.BootVolumeID = bvID
		}
		if kpID, ok := ctx.Value(keyPairIDKey).(string); ok && kpID != "" {
			cs.Status.KeyPairID = kpID
		}
		if eipID, ok := ctx.Value(elasticIpIDKey).(string); ok && eipID != "" {
			cs.Status.ElasticIpID = eipID
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
	return setFailedOnTimeout(r.Client, ctx, kubeCS, func(cs *v1alpha1.CloudServer) {
		if prjID, ok := ctx.Value(projectIDKey).(string); ok && cs.Status.ProjectID == "" {
			cs.Status.ProjectID = prjID
		}
		if vID, ok := ctx.Value(vpcIDKey).(string); ok && cs.Status.VpcID == "" {
			cs.Status.VpcID = vID
		}
	})
}

func (r *CloudServerReconciler) kubeSetFailed(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	return r.kubeSetPhaseAndCondition(ctx, kubeCS, v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonSynchronized, nil)
}

// --- CMP action methods ---

func (r *CloudServerReconciler) cmpCreate(ctx context.Context, kubeCS *v1alpha1.CloudServer, _ *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	prjID := ctx.Value(projectIDKey).(string)

	request := cmpCSRequestFromKube(ctx, kubeCS)
	resp, err := arubaClient.FromCompute().CloudServers().Create(ctx, prjID, *request, nil)
	if err != nil {
		return cmpTransportError("create", kubeCS.Name, err)
	}
	return cmpCheckResponse("create", kubeCS.Name, resp,
		http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

func (r *CloudServerReconciler) cmpUpdate(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	prjID := ctx.Value(projectIDKey).(string)

	// Guard: should have been caught by HasDeniedChanges, but double-check.
	if err := checkCSDeniedChanges(kubeCS, cmpCS); err != nil {
		return err
	}

	request := buildCSUpdateRequest(ctx, kubeCS, cmpCS)
	resp, err := arubaClient.FromCompute().CloudServers().Update(ctx, prjID, *cmpCS.Metadata.ID, *request, nil)
	if err != nil {
		return cmpTransportError("update", kubeCS.Name, err)
	}
	return cmpCheckResponse("update", kubeCS.Name, resp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent)
}

func (r *CloudServerReconciler) cmpDelete(ctx context.Context, _ *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
	prjID := ctx.Value(projectIDKey).(string)

	resp, err := arubaClient.FromCompute().CloudServers().Delete(ctx, prjID, *cmpCS.Metadata.ID, nil)
	if err != nil {
		return cmpTransportError("delete", *cmpCS.Metadata.Name, err)
	}
	return cmpCheckResponse("delete", *cmpCS.Metadata.Name, resp,
		http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound)
}

// --- Helper functions ---

func checkCSDeniedChanges(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) error {
	if cmpCS == nil {
		return nil
	}

	if kubeCS.Spec.DataCenter != cmpCS.Properties.Zone {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'dataCenter' is not allowed"))
	}

	if kubeCS.Spec.FlavorName != cmpCS.Properties.Flavor.Name {
		return fmt.Errorf("%w: %w", ErrNotAllowedChanges, errors.New("change the 'flavorName' is not allowed"))
	}

	// vpcPreset is immutable but has no comparable field in CloudServerPropertiesResult;
	// its enforcement is a CRD-level concern (webhook) and cannot be detected from CMP state.

	return nil
}

func kubeCSNeedsUpdate(kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) bool {
	if cmpCS == nil {
		return false
	}
	if !tagsAreEqual(kubeCS.Spec.Tags, cmpCS.Metadata.Tags) {
		return true
	}
	if cmpCS.Metadata.LocationResponse != nil && kubeCS.Spec.Location.Value != cmpCS.Metadata.LocationResponse.Value {
		return true
	}
	return false
}

func buildCSUpdateRequest(ctx context.Context, kubeCS *v1alpha1.CloudServer, cmpCS *arubatypes.CloudServerResponse) *arubatypes.CloudServerRequest {
	vpcID := ctx.Value(vpcIDKey).(string)
	prjID := ctx.Value(projectIDKey).(string)
	subnetIDs := ctx.Value(subnetIDsKey).([]string)
	sgIDs := ctx.Value(securityGroupIDsKey).([]string)
	elasticIpID := ctx.Value(elasticIpIDKey).(string)

	tags := make([]string, len(kubeCS.Spec.Tags))
	copy(tags, kubeCS.Spec.Tags)

	subnets := make([]arubatypes.ReferenceResource, 0, len(subnetIDs))
	for _, sid := range subnetIDs {
		subnets = append(subnets, arubatypes.ReferenceResource{URI: buildSubnetURI(prjID, vpcID, sid)})
	}

	sgs := make([]arubatypes.ReferenceResource, 0, len(sgIDs))
	for _, sgid := range sgIDs {
		sgs = append(sgs, arubatypes.ReferenceResource{URI: buildSecurityGroupURI(prjID, vpcID, sgid)})
	}

	flavorName := cmpCS.Properties.Flavor.Name

	req := &arubatypes.CloudServerRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: *cmpCS.Metadata.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeCS.Spec.Location.Value},
		},
		Properties: arubatypes.CloudServerPropertiesRequest{
			Zone:           cmpCS.Properties.Zone,
			VPC:            arubatypes.ReferenceResource{URI: cmpCS.Properties.VPC.URI},
			VPCPreset:      kubeCS.Spec.VpcPreset,
			FlavorName:     &flavorName,
			BootVolume:     arubatypes.ReferenceResource{URI: cmpCS.Properties.BootVolume.URI},
			KeyPair:        arubatypes.ReferenceResource{URI: cmpCS.Properties.KeyPair.URI},
			Subnets:        subnets,
			SecurityGroups: sgs,
		},
	}

	if elasticIpID != "" {
		req.Properties.ElasticIP = arubatypes.ReferenceResource{URI: buildElasticIpURI(prjID, elasticIpID)}
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
	elasticIpID := ctx.Value(elasticIpIDKey).(string)

	tags := make([]string, len(kubeCS.Spec.Tags))
	copy(tags, kubeCS.Spec.Tags)

	subnets := make([]arubatypes.ReferenceResource, 0, len(subnetIDs))
	for _, sid := range subnetIDs {
		subnets = append(subnets, arubatypes.ReferenceResource{URI: buildSubnetURI(prjID, vpcID, sid)})
	}

	sgs := make([]arubatypes.ReferenceResource, 0, len(sgIDs))
	for _, sgid := range sgIDs {
		sgs = append(sgs, arubatypes.ReferenceResource{URI: buildSecurityGroupURI(prjID, vpcID, sgid)})
	}

	flavorName := kubeCS.Spec.FlavorName

	req := &arubatypes.CloudServerRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: kubeCS.Name,
				Tags: tags,
			},
			Location: arubatypes.LocationRequest{Value: kubeCS.Spec.Location.Value},
		},
		Properties: arubatypes.CloudServerPropertiesRequest{
			Zone:           kubeCS.Spec.DataCenter,
			VPC:            arubatypes.ReferenceResource{URI: buildVpcURI(prjID, vpcID)},
			VPCPreset:      kubeCS.Spec.VpcPreset,
			FlavorName:     &flavorName,
			BootVolume:     arubatypes.ReferenceResource{URI: buildVolumeURI(prjID, bootVolumeID)},
			Subnets:        subnets,
			SecurityGroups: sgs,
		},
	}

	if keyPairID != "" {
		req.Properties.KeyPair = arubatypes.ReferenceResource{URI: buildKeyPairURI(prjID, keyPairID)}
	}
	if elasticIpID != "" {
		req.Properties.ElasticIP = arubatypes.ReferenceResource{URI: buildElasticIpURI(prjID, elasticIpID)}
	}

	return req
}

// URI builder helpers

func buildVpcURI(projectID, vpcID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/vpcs/%s", projectID, vpcID)
}

func buildKeyPairURI(projectID, keyPairID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Compute/keyPairs/%s", projectID, keyPairID)
}

func buildElasticIpURI(projectID, elasticIpID string) string {
	return fmt.Sprintf("/projects/%s/providers/Aruba.Network/elasticIps/%s", projectID, elasticIpID)
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

// SetupWithManager sets up the controller with the Manager.
func (r *CloudServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CloudServer{}).
		Named("cloudserver").
		Complete(r)
}
