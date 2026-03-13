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
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)

const (
	blockStorageFinalizerName = "blockstorage.arubacloud.com/finalizer"
)

var (
	errBlockStorageNotFound = errors.New("blockstorage not found")
)

// BlockStorageReconciler reconciles a BlockStorage object
type BlockStorageReconciler struct {
	*reconciler.Reconciler
}

// NewBlockStorageReconciler creates a new BlockStorageReconciler
func NewBlockStorageReconciler(baseReconciler *reconciler.Reconciler) *BlockStorageReconciler {
	return &BlockStorageReconciler{
		Reconciler: baseReconciler,
	}
}

// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=arubacloud.com,resources=blockstorages/finalizers,verbs=update
// +kubebuilder:rbac:groups=arubacloud.com,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

func (r *BlockStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.Reconciler.Reconcile(ctx, req, r)
}

func (r *BlockStorageReconciler) Object() reconciler.ResourceObject {
	return &v1alpha1.BlockStorage{}
}

func (r *BlockStorageReconciler) Finalizer() string {
	return blockStorageFinalizerName
}

func (r *BlockStorageReconciler) HandleReconcile(ctx context.Context, obj reconciler.ResourceObject) (ctrl.Result, error) {
	// 1 - Convert-back the generic resource to the concrete type
	k8sBs, ok := obj.(*v1alpha1.BlockStorage)
	if !ok {
		return ctrl.Result{}, errors.New("obj is not a *v1alpha1.BlockStorage") // TODO: better error handling
	}

	//		--------------------------------
	//		CHECK PROJECT REFERENCE
	//		--------------------------------
	//

	// 1.1 - Check if the project reference is valid
	if k8sBs.Spec.ProjectReference.Name == "" {
		return ctrl.Result{}, fmt.Errorf("project reference is not valid") // TODO: better error handling
	}

	// 1.2 - Check if the project reference exists

	// 2 - Create the Aruba search parameters to retrieve the desired resource
	// from Aruba API
	bsName, prjName := k8sBs.Name, k8sBs.Spec.ProjectReference.Name
	bsFilter := fmt.Sprintf(`name:eq("%s")`, bsName)
	prjFilter := fmt.Sprintf(`name:eq("%s")`, prjName)

	// 3 - Chech if the desired project exists in Aruba CMP
	prjResp, err := r.ArubaClient.FromProject().List(ctx, &arubatypes.RequestParameters{Filter: &prjFilter})
	// 3.1 - In case we have some technical issue, so we propagate the error
	if err != nil {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find project in Aruba cloud: %w, project_name: '%s', project_filter: '%s'",
			err, prjName, prjFilter,
		)
	}
	// 3.2 - In case we have some server or business issue, so we propagate
	// the error
	if prjResp.IsError() {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find project in Aruba cloud: status_code: %d, project_name: '%s', project_filter: '%s'",
			prjResp.StatusCode, prjName, prjFilter,
		)
	}
	// 3.3 - In case the project was not found but the object still not have a
	// project id on its status, so we consider that the project is still being
	// created and we requeue the reconciliation
	if prjResp.Data.Total == 0 && k8sBs.Status.ProjectID != "" {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"inconsistent data in project list: expected: 1, project not found: project_name: '%s', project_filter: '%s'", prjName, prjFilter,
		)
	}

	// 3.4 - In case we find more then a single project, so we consider as an
	// inconsistency
	if prjResp.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"inconsistent data in project list: expected: 1, found: %d, project_name: '%s', project_filter: '%s'",
			prjResp.Data.Total, prjName, prjFilter,
		)
	}

	prjID := *(prjResp.Data.Values[0].Metadata.ID)

	// 3.5 - In case the id of the project retrieved using the project name on
	// the object project reference differs from the project id present in the
	// object status, we consider that the user wants to change the reference
	// project of the block storage and then we block this not allowed
	// operation by returning an error
	if k8sBs.Status.ProjectID != "" && k8sBs.Status.ProjectID != prjID {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"inconsistent project id in blockstorage: blockstorage_name: '%s', blockstorage_project_id: '%s', project_name: '%s', project_id: '%s'",
			bsName, k8sBs.Status.ProjectID, prjName, prjID,
		)
	}

	//		-------------------------------------
	//		CHECK IF BLOCK STORAGE EXISTS OR NOT
	//		-------------------------------------
	//

	// 4 - Chech if the desired block storage exists in Aruba CMP
	bsResp, err := r.ArubaClient.FromStorage().Volumes().List(ctx, prjID, &arubatypes.RequestParameters{Filter: &bsFilter})
	// 4.1 - In case we have some technical issue, so we propagate the error
	if err != nil {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find blockstorage in Aruba cloud: %w, blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s'",
			err, bsName, bsFilter, prjName,
		)
	}
	// 4.2 - In case we have some server or business issue, so we propagate
	// the error
	if bsResp.IsError() {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"failed to find blockstorage in Aruba cloud: status_code: %d, blockstorage_name: '%s', project_name: '%s'",
			bsResp.StatusCode, bsName, prjName,
		)
	}

	// 4.3 - In case we find more then a single block storage or a bizarre
	// negative number, so we consider as an inconsistency
	if bsResp.Data.Total < 0 || bsResp.Data.Total > 1 {
		return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
			"inconsistent data in blockstorage list: blockstorage_name: '%s', blockstorage_filter: '%s', project_name: '%s', instances: %d",
			bsName, bsFilter, prjName, len(bsResp.Data.Values),
		)
	}

	// In case we do not find the resource on the Aruba CMP
	// we need to understand if we are in the "new", "deleted" or "deleting" path
	// checking k8s resource status phase and then we need to react accordingly
	if bsResp.Data.Total == 0 { // BLOCK STORAGE NOT FOUND

		//		-----------------------------------------------------------------------
		//		A) BLOCK STORAGE K8S IS IN DELETED and already deleted in Aruba CMP
		//		B) BLOCK STORAGE K8S IS IN DELETING and already deleted in Aruba CMP
		//		C) BLOCK STORAGE CAN BE NEW SO SHOULD BE CREATED
		//		-------------------------------------------------------------------------

		// CASE A) BLOCK STORAGE CAN BE DELETED IN K8S and already deleted in Aruba CMP
		if k8sBs.Status.Phase == v1alpha1.ResourcePhaseDeleted {
			// In case we do not find the resource on the Aruba CMP but the resource is already marked as "deleted" on its status, we consider that the resource has been already deleted on the Aruba CMP and then we can proceed with the finalizer removal by marking the resource as "deleted" on its status
			return ctrl.Result{}, nil
		}

		// CASE B) BLOCK STORAGE CAN BE DELETING in K8S and already deleted in Aruba CMP
		if k8sBs.Status.Phase == v1alpha1.ResourcePhaseDeleting {
			// In case we do not find the resource on the Aruba CMP but the resource is already marked as "deleting" on its status, we consider that the resource has been already deleted on the Aruba CMP and then we can proceed with the finalizer removal by marking the resource as "deleted" on its status
			k8sBsCopy := k8sBs.DeepCopy()
			k8sBsCopy.Status.Phase = v1alpha1.ResourcePhaseDeleted
			if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as 'deleted': %w", err) // TODO: better error handling
			}

			// This branch MUST return
			return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
		}

		// CASE C) BLOCK STORAGE CAN BE NEW SO SHOULD BE CREATED
		bsCreateResp, err := r.ArubaClient.FromStorage().Volumes().Create(ctx, prjID, *blockStorageRequestFromK8s(k8sBs), nil)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create blockstorage in Aruba CMP: %w", err) // TODO: better error handling
		}

		k8sBsCopy := k8sBs.DeepCopy()

		var status v1alpha1.ResourcePhase

		switch bsCreateResp.StatusCode {
		case http.StatusOK, http.StatusCreated, http.StatusAccepted:
			status = v1alpha1.ResourcePhaseCreating
		case http.StatusBadRequest:
			status = v1alpha1.ResourcePhaseFailed
		default:
			return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, fmt.Errorf( // TODO: better error handling
				"failed to create blockstorage in Aruba CMP: status_code: %d, blockstorage_name: '%s', project_name: '%s'",
				bsCreateResp.StatusCode, bsName, prjName,
			)
		}

		k8sBsCopy.Status.ProjectID = prjID
		k8sBsCopy.Status.Phase = status
		k8sBsCopy.Status.ResourceID = *bsCreateResp.Data.Metadata.ID
		if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as %s: %w", status, err) // TODO: better error handling
		}

		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	// The resource present on the Aruba Cloud
	// CASE A) resource is still in creating, updating, deleting, provisioning, disabling, enabling (TRANSIENT STATES)
	// CASE B) resource would be deleted
	// CASE C) resource would be to update
	// CASE D) restore is active, failed or disabled (FINAL STATES)

	arubaBS := &bsResp.Data.Values[0]

	//CASE A)
	if AssesCSPResourceStateNature(&arubaBS.Status) == CSPResourceStateNatureTransitory { // DELETING IN ARUBA CMP FOR EXAMPLE
		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	// CASE B) resource would be deleted
	if !k8sBs.GetDeletionTimestamp().IsZero() { // CASE A)

		if k8sBs.Status.Phase != v1alpha1.ResourcePhaseDeleting { // CASE A)
			k8sBsCopy := k8sBs.DeepCopy()
			k8sBsCopy.Status.Phase = v1alpha1.ResourcePhaseDeleting
			if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as 'deleting': %w", err) // TODO: better error handling
			}

			bsResp, err := r.ArubaClient.FromStorage().Volumes().Delete(ctx, prjID, *arubaBS.Metadata.ID, nil)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf( // TODO: better error handling
					"failed to delete blockstorage in Aruba CMP: %w, blockstorage_name: '%s', project_name: '%s'",
					err, bsName, prjName)

			}

			switch bsResp.StatusCode {
			case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
				// Do nothing, we can consider the delete request as successful

			case http.StatusBadRequest:
				return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil // TODO: better error handling, we can consider to requeue the request in order to retry the delete operation

			default:
				return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, fmt.Errorf( // TODO: better error handling
					"failed to delete blockstorage in Aruba CMP: status_code: %d, blockstorage_name: '%s', project_name: '%s'",
					bsResp.StatusCode, bsName, prjName)
			}
		}

		// This branch MUST return
		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	request, mustUpdate, err := convertAndCheckForUpdate(k8sBs, arubaBS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to convert and check blockstorage: %w", err) // TODO: better error handling
	}

	if mustUpdate { // CASE C)

		if request == nil {
			return ctrl.Result{}, fmt.Errorf("update required but block storage request is nil, blockstorage_name: '%s', project_name: '%s'", bsName, prjName)
		}
		// 5.3.1 - Set the k8s resource as updating if it is not yet
		if k8sBs.Status.Phase != v1alpha1.ResourcePhaseUpdating {
			k8sBsCopy := k8sBs.DeepCopy()
			k8sBsCopy.Status.Phase = v1alpha1.ResourcePhaseUpdating
			if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as 'updating': %w", err) // TODO: better error handling
			}
		}

		// BLOCK STORAGE CAN BE ACTIVE, FAILED OR DISABLED

		if (arubaBS.Status.State != nil && *arubaBS.Status.State == CSPResourceStateActive) ||
			(arubaBS.Status.State != nil && *arubaBS.Status.State == CSPResourceStateInUse) ||
			(arubaBS.Status.State != nil && *arubaBS.Status.State == CSPResourceStateNotUsed) {

			// 5.3.2 - Request the resource update to the Arube CMP
			updateResp, err := r.ArubaClient.FromStorage().Volumes().Update(ctx, prjID, *arubaBS.Metadata.ID, *request, nil)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed update blockstorage in Aruba CMP: %w, blockstorage_name: '%s', project_name: '%s'", err, bsName, prjName)
			}
			if updateResp != nil && updateResp.IsError() {
				errDetail := ""
				if updateResp.Error != nil {
					var status int32
					title, detail := "", ""
					if updateResp.Error.Status != nil {
						status = *updateResp.Error.Status
					}
					if updateResp.Error.Title != nil {
						title = *updateResp.Error.Title
					}
					if updateResp.Error.Detail != nil {
						detail = *updateResp.Error.Detail
					}
					errDetail = fmt.Sprintf("status_code: '%d', title: '%s', detail: '%s'", status, title, detail)
				}
				return ctrl.Result{}, fmt.Errorf("failed update blockstorage in Aruba CMP: %s, blockstorage_name: '%s', project_name: '%s'", errDetail, bsName, prjName)
			}

		}

		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	if k8sBs.Status.Phase == v1alpha1.ResourcePhaseUpdating && *arubaBS.Status.State == CSPResourceStateUpdating { //UPDATING NOT FINISHED YET
		return ctrl.Result{RequeueAfter: reconciler.RequeueAfter}, nil
	}

	if k8sBs.Status.Phase == v1alpha1.ResourcePhaseUpdating {
		var status v1alpha1.ResourcePhase

		switch *arubaBS.Status.State {
		case CSPResourceStateActive,
			CSPResourceStateNotUsed,
			CSPResourceStateInUse,
			CSPResourceStateUsed,
			CSPResourceStateStopped,
			CSPResourceStateRunning,
			CSPResourceStateDisabled:
			status = v1alpha1.ResourcePhaseActive
		case CSPResourceStateFailed:
			status = v1alpha1.ResourcePhaseFailed
		default:
			// In case of an unexpected state, keep the current phase
			status = k8sBs.Status.Phase
		}

		k8sBsCopy := k8sBs.DeepCopy()
		k8sBsCopy.Status.Phase = status
		if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as '%s': %w", status, err) // TODO: better error handling
		}

		// This branch MUST return
		return ctrl.Result{}, nil
	}

	// cmp resource can be failed, creaed or creating

	k8sBsCopy := k8sBs.DeepCopy()

	if k8sBsCopy.Status.Phase == v1alpha1.ResourcePhaseActive {
		return ctrl.Result{}, nil
	}

	if k8sBsCopy.Status.Phase == v1alpha1.ResourcePhaseCreating {
		switch *bsResp.Data.Values[0].Status.State {
		case CSPResourceStateFailed:
			k8sBsCopy.Status.Phase = v1alpha1.ResourcePhaseFailed
		case CSPResourceStateActive, CSPResourceStateInUse, CSPResourceStateNotUsed:
			k8sBsCopy.Status.Phase = v1alpha1.ResourcePhaseActive
		}

		if err := r.Client.Status().Patch(ctx, k8sBsCopy, client.MergeFrom(k8sBs)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed set blockstorage state as 'created': %w", err) // TODO: better error handling
		}
	}

	return ctrl.Result{}, fmt.Errorf("Cannot be reached code: blockstorage_name: '%s', project_name: '%s'", bsName, prjName) // TODO: better error handling
}

func blockStorageRequestFromK8s(k8sBs *v1alpha1.BlockStorage) *arubatypes.BlockStorageRequest {
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: k8sBs.Name,
				Tags: k8sBs.Spec.Tags,
			},
			Location: arubatypes.LocationRequest(k8sBs.Spec.Location),
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        int(k8sBs.Spec.SizeGb),
			BillingPeriod: k8sBs.Spec.BillingPeriod,
			Zone:          &k8sBs.Spec.DataCenter,
			Bootable:      &k8sBs.Spec.Bootable,
			Image:         &k8sBs.Spec.Image,
			Type:          arubatypes.BlockStorageType(k8sBs.Spec.Type),
		},
	}
}

func convertAndCheckForUpdate(
	k8sObj *v1alpha1.BlockStorage,
	arubaObj *arubatypes.BlockStorageResponse,
) (*arubatypes.BlockStorageRequest, bool, error) {
	request := blockStorageRequestFromResponse(arubaObj)
	if request == nil {
		return nil, false, fmt.Errorf("block storage request from response is nil")
	}

	//
	// Not allowed cases
	//
	// An error is returned to block the reconciliation if a single not
	// allowed condition is found

	errs := []error{}

	if k8sObj.Spec.Bootable != *request.Properties.Bootable {
		errs = append(errs, fmt.Errorf("%w: change the 'bootable' is not allowed", ErrNotAllowedChange))
	}

	if k8sObj.Spec.Image != *request.Properties.Image {
		errs = append(errs, fmt.Errorf("%w: change the 'image' is not allowed", ErrNotAllowedChange))
	}

	if k8sObj.Spec.Type != string(request.Properties.Type) {
		errs = append(errs, fmt.Errorf("%w: change the 'type' is not allowed", ErrNotAllowedChange))
	}

	if k8sObj.Spec.Location.Value != request.Metadata.Location.Value {
		errs = append(errs, fmt.Errorf("%w: change the 'location' is not allowed", ErrNotAllowedChange))
	}

	if len(errs) > 0 {
		return nil, false, errors.Join(errs...)
	}

	//
	// Updating cases
	//
	// We allow the reconciliation to continue when we find the first valid
	// case

	if k8sObj.Spec.BillingPeriod != request.Properties.BillingPeriod ||
		k8sObj.Spec.Bootable != *request.Properties.Bootable ||
		k8sObj.Spec.DataCenter != *request.Properties.Zone ||
		k8sObj.Spec.SizeGb != int32(request.Properties.SizeGB) ||
		!blockStorageTagsAreEquals(k8sObj, request) {
		request.Properties.BillingPeriod = k8sObj.Spec.BillingPeriod
		bootable := k8sObj.Spec.Bootable
		request.Properties.Bootable = &bootable
		zone := k8sObj.Spec.DataCenter
		request.Properties.Zone = &zone
		request.Properties.SizeGB = int(k8sObj.Spec.SizeGb)
		tags := make([]string, len(k8sObj.Spec.Tags))
		copy(tags, k8sObj.Spec.Tags)
		request.Metadata.Tags = tags
		return request, true, nil
	}

	// If we do not find any allowed updating condition, so we signal the
	// caller to not proceed the reconciliation
	return nil, false, nil
}

func blockStorageRequestFromResponse(response *arubatypes.BlockStorageResponse) *arubatypes.BlockStorageRequest {
	if response == nil {
		return nil
	}
	name := ""
	if response.Metadata.Name != nil {
		name = *response.Metadata.Name
	}
	tags := make([]string, len(response.Metadata.Tags))
	copy(tags, response.Metadata.Tags)
	location := arubatypes.LocationRequest{Value: ""}
	if response.Metadata.LocationResponse != nil {
		location.Value = response.Metadata.LocationResponse.Value
	}
	zone := response.Properties.Zone
	return &arubatypes.BlockStorageRequest{
		Metadata: arubatypes.RegionalResourceMetadataRequest{
			ResourceMetadataRequest: arubatypes.ResourceMetadataRequest{
				Name: name,
				Tags: tags,
			},
			Location: location,
		},
		Properties: arubatypes.BlockStoragePropertiesRequest{
			SizeGB:        response.Properties.SizeGB,
			BillingPeriod: response.Properties.BillingPeriod,
			Zone:          &zone,
			Type:          response.Properties.Type,
			Bootable:      response.Properties.Bootable,
			Image:         response.Properties.Image,
		},
	}
}

func blockStorageTagsAreEquals(k8sObj *v1alpha1.BlockStorage, request *arubatypes.BlockStorageRequest) bool {
	// TODO: generalize this function
	if request == nil {
		return false
	}
	if len(k8sObj.Spec.Tags) != len(request.Metadata.Tags) {
		return false
	}

	slices.Sort(k8sObj.Spec.Tags)
	slices.Sort(request.Metadata.Tags)

	for i, tag := range k8sObj.Spec.Tags {
		if tag != request.Metadata.Tags[i] {
			return false
		}
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *BlockStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.BlockStorage{}).
		Named("blockstorage").
		Complete(r)
}
