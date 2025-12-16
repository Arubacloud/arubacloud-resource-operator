package reconciler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	apiError "k8s.io/apimachinery/pkg/api/errors"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
	arubaClient "github.com/Arubacloud/arubacloud-resource-operator/internal/client"
	"github.com/Arubacloud/arubacloud-resource-operator/internal/util"
)

const (
	requeueAfter = 20 * time.Second
	// maxPhaseTimeout defines the maximum time a resource can remain in a non-final phase
	maxPhaseTimeout = 5 * time.Minute
)

// ResourceReconciler is an interface that must be implemented by all resource reconcilers
type ResourceReconciler interface {
	Init(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
	Creating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
	Provisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
	Updating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
	Created(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
	Deleting(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error)
}

// Reconciler provides base functionality for all resource controllers
type Reconciler struct {
	client.Client
	*runtime.Scheme
	*arubaClient.HelperClient
	*arubaClient.AppRoleClient
	TokenManager   arubaClient.ITokenManager
	VaultIsEnabled bool
}

// ReconcilerConfig holds configuration for setting up Reconciler
type ReconcilerConfig struct {
	APIGateway     string
	VaultIsEnabled bool
	VaultAddress   string
	KeycloakURL    string
	RealmAPI       string
	Namespace      string
	RolePath       string
	ClientID       string
	ClientSecret   string
	RoleID         string
	RoleSecret     string
	KVMount        string
	HTTPClient     *http.Client
}

// NewReconciler creates a new base reconciler
func NewReconciler(mgr ctrl.Manager, cfg ReconcilerConfig) *Reconciler {
	var vaultAuth *arubaClient.AppRoleClient
	helperClientInstance := arubaClient.NewHelperClient(mgr.GetClient(), cfg.HTTPClient, cfg.APIGateway)

	if cfg.VaultIsEnabled {
		vaultClient := arubaClient.VaultClient(cfg.VaultAddress)
		var err error
		vaultAuth, err = arubaClient.NewAppRoleClient(cfg.Namespace, cfg.RolePath, cfg.RoleID, cfg.RoleSecret, cfg.KVMount, vaultClient)
		if err != nil {
			ctrl.Log.Error(err, "failed to init vault client: %v")
			os.Exit(1)
		}
		defer vaultAuth.Close()
		ctrl.Log.V(1).Info("Vault integration is enabled; Vault client initialized")
	}

	oauthClient := arubaClient.NewTokenManager(cfg.KeycloakURL, cfg.RealmAPI, "", "", nil)

	if !cfg.VaultIsEnabled {
		ctrl.Log.V(1).Info("Vault integration is disabled; using static Keycloak client credentials")
		oauthClient.SetClientIdAndSecret(cfg.ClientID, cfg.ClientSecret)
	}

	return &Reconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		HelperClient:   helperClientInstance,
		AppRoleClient:  vaultAuth,
		TokenManager:   oauthClient,
		VaultIsEnabled: cfg.VaultIsEnabled,
	}
}

// Reconcile handles the common reconciliation logic for all resources
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
	obj client.Object,
	status *v1alpha1.ResourceStatus,
	resourceReconciler ResourceReconciler,
	tenant *string,
) (ctrl.Result, error) {
	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if apiError.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if tenant == nil || *tenant == "" {
		if r.VaultIsEnabled {
			errMsg := "Tenant ID is not specified in the resource spec"
			ctrl.Log.Error(fmt.Errorf("%s", errMsg), "Cannot proceed without Tenant ID when Vault integration is enabled", "Resource", req.NamespacedName)
			return ctrl.Result{}, fmt.Errorf("%s", errMsg)
		} else {
			ctrl.Log.V(1).Info("Vault integration is disabled; proceeding without Tenant ID")
		}
	}

	ctrl.Log.V(1).Info("Setting tenant in Aruba client", "TenantID", tenant)
	if err := r.Authenticate(ctx, *tenant); err != nil {
		ctrl.Log.Error(err, "Failed to authenticate Aruba client", "tenantID", tenant)
		return ctrl.Result{}, err
	}

	isPhaseTimeout, phaseTimeoutResult, phaseTimeoutError := r.HandlePhaseTimeout(ctx, obj, status)
	if isPhaseTimeout {
		return phaseTimeoutResult, phaseTimeoutError
	}

	shouldBeDeleted, handleDeletionResult, handleDeletionError := r.HandleToDelete(ctx, obj, status)
	if shouldBeDeleted {
		return handleDeletionResult, handleDeletionError
	}

	var reconcileResult ctrl.Result
	var reconcileError error

	switch status.Phase {
	case "":
		reconcileResult, reconcileError = resourceReconciler.Init(ctx, obj, status)
	case v1alpha1.ResourcePhaseCreating:
		reconcileResult, reconcileError = resourceReconciler.Creating(ctx, obj, status)
	case v1alpha1.ResourcePhaseProvisioning:
		reconcileResult, reconcileError = resourceReconciler.Provisioning(ctx, obj, status)
	case v1alpha1.ResourcePhaseUpdating:
		reconcileResult, reconcileError = resourceReconciler.Updating(ctx, obj, status)
	case v1alpha1.ResourcePhaseCreated:
		reconcileResult, reconcileError = resourceReconciler.Created(ctx, obj, status)
	case v1alpha1.ResourcePhaseDeleting:
		reconcileResult, reconcileError = resourceReconciler.Deleting(ctx, obj, status)
	case v1alpha1.ResourcePhaseDeleted:
		// Resource is already deleted, nothing to do
		reconcileResult, reconcileError = ctrl.Result{}, nil
	case v1alpha1.ResourcePhaseFailed:
		// Resource is in failed state, nothing to do unless spec changes
		reconcileResult, reconcileError = ctrl.Result{}, nil
	}

	return reconcileResult, reconcileError
}

// HandlePhaseTimeout transitions the resource to failed state due to timeout
func (r *Reconciler) HandlePhaseTimeout(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (bool, ctrl.Result, error) {
	isTimeout := false

	if status.PhaseStartTime == nil {
		return isTimeout, ctrl.Result{}, nil
	}

	transitioningPhases := []v1alpha1.ResourcePhase{
		v1alpha1.ResourcePhaseCreating,
		v1alpha1.ResourcePhaseProvisioning,
		v1alpha1.ResourcePhaseUpdating,
		v1alpha1.ResourcePhaseDeleting,
	}

	if !slices.Contains(transitioningPhases, status.Phase) {
		return isTimeout, ctrl.Result{}, nil
	}

	elapsed := time.Since(status.PhaseStartTime.Time)
	isTimeout = elapsed > maxPhaseTimeout

	if !isTimeout {
		return isTimeout, ctrl.Result{}, nil
	}

	phaseLogger := ctrl.Log.WithValues("Phase", status.Phase, "Kind", obj.GetObjectKind().GroupVersionKind().Kind, "Name", obj.GetName())
	message := fmt.Sprintf("Reconciliation took too much time (timeout: %+v)", maxPhaseTimeout)
	phaseLogger.Info(message)

	nextCtrlResult, err := r.Next(
		ctx,
		obj,
		status,
		v1alpha1.ResourcePhaseFailed,
		metav1.ConditionFalse,
		"ReconciliationTimeout",
		message,
		false,
	)

	return isTimeout, nextCtrlResult, err
}

// HandleToDelete checks if resource should transition to deleting phase
func (r *Reconciler) HandleToDelete(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (bool, ctrl.Result, error) {
	shouldBeDeleted := status.Phase != v1alpha1.ResourcePhaseDeleting &&
		status.Phase != v1alpha1.ResourcePhaseFailed &&
		!obj.GetDeletionTimestamp().IsZero()

	if !shouldBeDeleted {
		return shouldBeDeleted, ctrl.Result{}, nil
	}

	nextCtrlResult, err := r.Next(
		ctx,
		obj,
		status,
		v1alpha1.ResourcePhaseDeleting,
		metav1.ConditionFalse,
		"ToBeDeleted",
		"deletion timestamp detected",
		true,
	)
	return shouldBeDeleted, nextCtrlResult, err
}

// Next transitions to the next phase with message and condition updates
func (r *Reconciler) Next(
	ctx context.Context,
	obj client.Object,
	resStatus *v1alpha1.ResourceStatus,
	nextPhase v1alpha1.ResourcePhase,
	condStatus metav1.ConditionStatus,
	reason, message string,
	requeue bool,
) (ctrl.Result, error) {
	currentPhase := resStatus.Phase
	if currentPhase == "" {
		currentPhase = "Initializing"
	}

	phaseLogger := ctrl.Log.WithValues("Phase", currentPhase, "NextPhase", nextPhase, "Kind", obj.GetObjectKind().GroupVersionKind().Kind, "Name", obj.GetName())
	// Debouncing logic: if this is a retry (requeue=true) with the same phase, check timing
	if requeue && currentPhase == nextPhase && resStatus.PhaseStartTime != nil {
		timeSincePhaseStart := time.Since(resStatus.PhaseStartTime.Time)

		intervalsElapsed := int(timeSincePhaseStart / requeueAfter)
		nextInterval := time.Duration(intervalsElapsed+1) * requeueAfter
		timeToNextInterval := nextInterval - timeSincePhaseStart

		phaseLogger.Info("Reconcile Debounce",
			"reason", reason,
			"message", message,
			"timeSincePhaseStart", timeSincePhaseStart,
			"timeToNextInterval", timeToNextInterval,
			"intervalsElapsed", intervalsElapsed,
			"requeueAfter", requeueAfter)

		if timeToNextInterval > 0 && timeToNextInterval < requeueAfter {
			return ctrl.Result{RequeueAfter: timeToNextInterval}, nil
		}
	}

	// Update phase start time ONLY if phase is changing or not set
	if resStatus.PhaseStartTime == nil || currentPhase != nextPhase {
		now := metav1.Now()
		resStatus.PhaseStartTime = &now
	}
	resStatus.Phase = nextPhase
	resStatus.Message = message
	resStatus.ObservedGeneration = obj.GetGeneration()
	resStatus.Conditions = util.UpdateConditions(resStatus.Conditions, v1alpha1.ConditionTypeSynchronized, condStatus, reason, message)

	if err := r.Client.Status().Update(ctx, obj); err != nil {
		phaseLogger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	phaseLogger.Info(message)
	return ctrl.Result{Requeue: requeue, RequeueAfter: requeueAfter}, nil
}

// NextToFailedOnApiError handles API errors with proper 4xx/5xx logic and condition management
func (r *Reconciler) NextToFailedOnApiError(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, err error) (ctrl.Result, error) {
	var apiErr *arubaClient.ApiError
	if errors.As(err, &apiErr) {
		statusCode := apiErr.Status
		message := apiErr.Error()

		// Handle notReady/invalidStatus errors during transitioning phases - should retry
		if apiErr.IsInvalidStatus() {
			return r.Next(
				ctx,
				obj,
				status,
				status.Phase,
				metav1.ConditionFalse,
				"ResourceNotReady",
				fmt.Sprintf("Remote resource is not ready, will retry: %s", message),
				true,
			)
		}

		// Handle other 4xx errors (client errors) - fail immediately
		if statusCode >= 400 && statusCode < 500 {
			return r.Next(
				ctx,
				obj,
				status,
				v1alpha1.ResourcePhaseFailed,
				metav1.ConditionFalse,
				"ClientError",
				fmt.Sprintf("Client error (HTTP %d): %s", statusCode, message),
				false,
			)
		}

		// Handle 5xx errors (server errors) - should retry
		if statusCode >= 500 {
			return r.Next(
				ctx,
				obj,
				status,
				status.Phase,
				metav1.ConditionFalse,
				"ServerError",
				fmt.Sprintf("Server error (HTTP %d): %s - will retry", statusCode, message),
				true,
			)
		}
	}

	// Unknown error, treat as retriable
	return r.NextToFailedOnReconcileError(ctx, obj, status, err)
}

// NextToFailedOnReconcileError handles generic reconcile errors
func (r *Reconciler) NextToFailedOnReconcileError(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, err error) (ctrl.Result, error) {
	return r.Next(
		ctx,
		obj,
		status,
		status.Phase,
		metav1.ConditionFalse,
		"ReconcileError",
		fmt.Sprintf("Reconcile error encountered, will retry: %s", err.Error()),
		true,
	)
}

// InitializeResource handles the initialization phase with finalizer management
func (r *Reconciler) InitializeResource(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, finalizerName string) (ctrl.Result, error) {
	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(obj, finalizerName) {
		controllerutil.AddFinalizer(obj, finalizerName)
		err := r.Update(ctx, obj)
		if err != nil {
			return r.NextToFailedOnApiError(ctx, obj, status, err)
		}
	}

	return r.Next(ctx, obj, status, v1alpha1.ResourcePhaseCreating, metav1.ConditionFalse, "Initialized", "Resource initialized successfully", true)
}

// HandleDeletion handles the deletion phase with finalizer removal
func (r *Reconciler) HandleDeletion(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, finalizerName string, deleteFunc func(context.Context) error) (ctrl.Result, error) {
	err := deleteFunc(ctx)
	if err != nil {
		return r.NextToFailedOnApiError(ctx, obj, status, err)
	}

	// Remove finalizer to allow Kubernetes to delete the resource
	if controllerutil.ContainsFinalizer(obj, finalizerName) {
		controllerutil.RemoveFinalizer(obj, finalizerName)
		err := r.Update(ctx, obj)
		if err != nil {
			return r.NextToFailedOnApiError(ctx, obj, status, err)
		}
	}

	return ctrl.Result{}, nil
}

// HandleCreating handles the resource creation phase
func (r *Reconciler) HandleCreating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, createFunc func(context.Context) (string, string, error)) (ctrl.Result, error) {
	resourceID, state, err := createFunc(ctx)
	if err != nil {
		return r.NextToFailedOnApiError(ctx, obj, status, err)
	}

	// Update status with resource ID
	status.ResourceID = resourceID

	if state == "InCreation" || state == "Provisioning" {
		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseProvisioning,
			metav1.ConditionFalse,
			"Provisioning",
			"Resource is being provisioned",
			true,
		)
	}

	return r.Next(
		ctx,
		obj,
		status,
		v1alpha1.ResourcePhaseCreated,
		metav1.ConditionTrue,
		"Created",
		"Resource created successfully",
		true,
	)
}

// HandleUpdating handles the resource update phase
func (r *Reconciler) HandleUpdating(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, updateFunc func(context.Context) error) (ctrl.Result, error) {
	err := updateFunc(ctx)
	if err != nil {
		return r.NextToFailedOnApiError(ctx, obj, status, err)
	}

	return r.Next(
		ctx,
		obj,
		status,
		v1alpha1.ResourcePhaseCreated,
		metav1.ConditionTrue,
		"Updated",
		"Resource updated successfully",
		true,
	)
}

// HandleProvisioning handles the provisioning state check with configurable state transitions
func (r *Reconciler) HandleProvisioning(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus, getStatusFunc func(context.Context) (string, error)) (ctrl.Result, error) {
	state, err := getStatusFunc(ctx)
	if err != nil {
		return r.NextToFailedOnApiError(ctx, obj, status, err)
	}

	message := ""
	switch state {
	case "Available", "Active", "NotUsed", "Used":
		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseCreated,
			metav1.ConditionTrue,
			"Created",
			"Resource created successfully",
			true,
		)
	case "Failed", "Error":
		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseFailed,
			metav1.ConditionTrue,
			"ProvisioningFailed",
			message,
			false,
		)
	default:
		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseProvisioning,
			metav1.ConditionTrue,
			"Provisioning",
			message,
			true,
		)
	}
}

// CheckForUpdates checks if resource needs update based on generation
func (r *Reconciler) CheckForUpdates(ctx context.Context, obj client.Object, status *v1alpha1.ResourceStatus) (ctrl.Result, error) {
	phaseLogger := ctrl.Log.WithValues("Phase", status.Phase, "Kind", obj.GetObjectKind().GroupVersionKind().Kind, "Name", obj.GetName())

	// Check if resource needs update
	if status.ObservedGeneration != obj.GetGeneration() {
		phaseLogger.Info("resource needs update - generation mismatch detected",
			"generation", obj.GetGeneration(),
			"observedGeneration", status.ObservedGeneration)

		return r.Next(
			ctx,
			obj,
			status,
			v1alpha1.ResourcePhaseUpdating,
			metav1.ConditionFalse,
			"Updating",
			"Resource update initiated",
			true,
		)
	}

	phaseLogger.Info("resource is up to date")
	return ctrl.Result{}, nil
}

// Authenticate authenticates the client with the given tenant
func (r *Reconciler) Authenticate(ctx context.Context, tenantId string) error {
	if r.Client == nil {
		return fmt.Errorf("client configuration not loaded")
	}

	token := r.TokenManager.GetActiveToken(tenantId)
	if token != "" {
		r.SetAPIToken(token)
		return nil
	}

	if r.VaultIsEnabled {
		apiKeyData, err := r.GetSecret(ctx, tenantId)
		if err != nil {
			ctrl.Log.Error(err, "Failed to get API key from Vault", "TenantID", tenantId)
			return err
		}

		ctrl.Log.V(1).Info("Retrieved API key from Vault", "secretData", apiKeyData)
		clientId, _ := apiKeyData["client-id"].(string)
		ctrl.Log.V(1).Info("Authenticating Aruba client", "ClientID", clientId)
		clientSecret, _ := apiKeyData["client-secret"].(string)
		ctrl.Log.V(1).Info("Authenticating Aruba client", "ClientSecret", clientSecret)

		r.TokenManager.SetClientIdAndSecret(clientId, clientSecret)
	}

	token, err := r.TokenManager.GetAccessToken(false, tenantId)

	if err != nil {
		return err
	}

	r.SetAPIToken(token)
	return nil
}

// Helper methods for getting resource references
func (r *Reconciler) GetProjectID(ctx context.Context, name string, namespace string) (string, error) {
	project := &v1alpha1.Project{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, project)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced Project %s/%s: %w",
			namespace, name, err)
	}

	if project.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced Project %s/%s does not have a project ID yet",
			namespace, name)
	}

	return project.Status.ResourceID, nil
}

func (r *Reconciler) GetElasticIpID(ctx context.Context, name string, namespace string) (string, error) {
	elasticIp := &v1alpha1.ElasticIp{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, elasticIp)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ElasticIp %s/%s: %w",
			namespace, name, err)
	}

	if elasticIp.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ElasticIp %s/%s does not have an elastic IP ID yet",
			namespace, name)
	}

	return elasticIp.Status.ResourceID, nil
}

func (r *Reconciler) GetSubnetID(ctx context.Context, name string, namespace string) (string, error) {
	subnet := &v1alpha1.Subnet{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, subnet)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced Subnet %s/%s: %w",
			namespace, name, err)
	}

	if subnet.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced Subnet %s/%s does not have a subnet ID yet",
			namespace, name)
	}

	return subnet.Status.ResourceID, nil
}

func (r *Reconciler) GetSecurityGroupID(ctx context.Context, name string, namespace string) (string, error) {
	securityGroup := &v1alpha1.SecurityGroup{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, securityGroup)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced SecurityGroup %s/%s: %w",
			namespace, name, err)
	}

	if securityGroup.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced SecurityGroup %s/%s does not have a security group ID yet",
			namespace, name)
	}

	return securityGroup.Status.ResourceID, nil
}

func (r *Reconciler) GetBlockStorageID(ctx context.Context, name string, namespace string) (string, error) {
	blockStorage := &v1alpha1.BlockStorage{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, blockStorage)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced BlockStorage %s/%s: %w",
			namespace, name, err)
	}

	if blockStorage.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced BlockStorage %s/%s does not have a volume ID yet",
			namespace, name)
	}

	return blockStorage.Status.ResourceID, nil
}

func (r *Reconciler) GetVpcID(ctx context.Context, name string, namespace string) (string, error) {
	vpc := &v1alpha1.Vpc{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, vpc)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced Vpc %s/%s: %w",
			namespace, name, err)
	}

	if vpc.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced Vpc %s/%s does not have a VPC ID yet",
			namespace, name)
	}

	return vpc.Status.ResourceID, nil
}

func (r *Reconciler) GetKeyPairID(ctx context.Context, name string, namespace string) (string, error) {
	keyPair := &v1alpha1.KeyPair{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, keyPair)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced KeyPair %s/%s: %w",
			namespace, name, err)
	}

	if keyPair.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced KeyPair %s/%s does not have a key pair ID yet",
			namespace, name)
	}

	return keyPair.Status.ResourceID, nil
}
