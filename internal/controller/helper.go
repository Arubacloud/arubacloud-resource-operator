package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"

	apiError "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/util"

	"gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/api/v1alpha1"
	arubaClient "gitlab.aruba.it/ingegneria/seca/operators/aruba-operator/internal/client"
)

// HelperReconciler provides base functionality for all Aruba controllers
type HelperReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	HelperClient *arubaClient.HelperClient
	PhaseManager *util.PhaseManager
	// VaultAppRole to use for Vault API calls (required)
	VaultAppRole *arubaClient.AppRoleClient
	OauthClient  *arubaClient.TokenManager
}

// HelperReconcilerConfig holds configuration for setting up HelperReconciler
type HelperReconcilerConfig struct {
	APIGateway   string
	VaultAddress string
	KeycloakURL  string
	RealmAPI     string
	Namespace    string
	RolePath     string
	RoleID       string
	RoleSecret   string
	KVMount      string
	// HTTPClient to use for API calls (optional)
	HTTPClient *http.Client
}

// NewHelperReconciler creates a new base reconciler
func NewHelperReconciler(mgr ctrl.Manager, cfg HelperReconcilerConfig) *HelperReconciler {
	helperClientInstance := arubaClient.NewHelperClient(mgr.GetClient(), cfg.HTTPClient, cfg.APIGateway)

	vaultClient := arubaClient.VaultClient(cfg.VaultAddress)
	vaultAuth, err := arubaClient.NewAppRoleClient(cfg.Namespace, cfg.RolePath, cfg.RoleID, cfg.RoleSecret, cfg.KVMount, vaultClient)
	if err != nil {
		ctrl.Log.Error(err, "failed to init vault client: %v")
		os.Exit(1)
	}

	oauthClient := arubaClient.NewTokenManager(cfg.KeycloakURL, cfg.RealmAPI, "", "", nil)

	defer vaultAuth.Close()
	return &HelperReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		HelperClient: helperClientInstance,
		VaultAppRole: vaultAuth,
		OauthClient:  oauthClient,
	}
}

// ArubaReconciler is an interface that must be implemented by all Aruba resource reconcilers
type ArubaReconciler interface {
	Init(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
	Creating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
	Provisioning(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
	Updating(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
	Created(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
	Deleting(ctx context.Context, pm *util.PhaseManager) (ctrl.Result, error)
}

// CommonReconcile handles the common reconciliation logic for all Aruba resources
func (r *HelperReconciler) CommonReconcile(
	ctx context.Context,
	req ctrl.Request,
	obj client.Object,
	status *v1alpha1.ArubaResourceStatus,
	tenant *string,
	reconciler ArubaReconciler,
) (ctrl.Result, error) {
	err := r.Client.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if apiError.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if tenant == nil || *tenant == "" {
		errMsg := "Tenant ID is not specified in the resource spec"
		ctrl.Log.Error(fmt.Errorf("%s", errMsg), "Cannot proceed without Tenant ID", "Resource", req.NamespacedName)
		return ctrl.Result{}, fmt.Errorf("%s", errMsg)
	}

	ctrl.Log.V(1).Info("Setting tenant in Aruba client", "TenantID", tenant)
	if err := r.Authenticate(*tenant); err != nil {
		ctrl.Log.Error(err, "Failed to authenticate Aruba client", "tenantID", tenant)
		return ctrl.Result{}, err
	}

	pm := &util.PhaseManager{
		Client: r.Client,
		Object: obj,
		Status: status,
	}

	isPhaseTimeout, phaseTimeoutResult, phaseTimeoutError := pm.HandlePhaseTimeout(ctx)
	if isPhaseTimeout {
		return phaseTimeoutResult, phaseTimeoutError
	}

	shouldBeDeleted, handleDeletionResult, handleDeletionError := pm.HandleToDelete(ctx)
	if shouldBeDeleted {
		return handleDeletionResult, handleDeletionError
	}

	var reconcileResult ctrl.Result
	var reconcileError error

	switch status.Phase {
	case "":
		reconcileResult, reconcileError = reconciler.Init(ctx, pm)
	case v1alpha1.ArubaResourcePhaseCreating:
		reconcileResult, reconcileError = reconciler.Creating(ctx, pm)
	case v1alpha1.ArubaResourcePhaseProvisioning:
		reconcileResult, reconcileError = reconciler.Provisioning(ctx, pm)
	case v1alpha1.ArubaResourcePhaseUpdating:
		reconcileResult, reconcileError = reconciler.Updating(ctx, pm)
	case v1alpha1.ArubaResourcePhaseCreated:
		reconcileResult, reconcileError = reconciler.Created(ctx, pm)
	case v1alpha1.ArubaResourcePhaseDeleting:
		reconcileResult, reconcileError = reconciler.Deleting(ctx, pm)
	}

	return reconcileResult, reconcileError
}

func (r *HelperReconciler) GetArubaObject(ctx context.Context, key client.ObjectKey, obj client.Object) (client.Object, error) {
	phaseLogger := ctrl.Log.WithValues("Phase", "Common checks")

	if err := r.Client.Get(ctx, key, obj); err != nil {
		if !apiError.IsNotFound(err) {
			phaseLogger.Error(err, "cannot get aruba object")
			return nil, err
		}
		if apiError.IsNotFound(err) {
			phaseLogger.Info("aruba object not found, nothing to do")
			return nil, nil
		}
	}

	return obj, nil
}

// Authenticate Verify if the client is authenticated
func (c *HelperReconciler) Authenticate(tenantId string) error {
	if c.Client == nil {
		return fmt.Errorf("client configuration not loaded")
	}

	token := c.OauthClient.GetActiveToken(tenantId)
	if token != "" {
		c.HelperClient.SetAPIToken(token)
		return nil
	}

	apiKeyData, err := c.VaultAppRole.GetSecret(tenantId)
	if err != nil {
		ctrl.Log.Error(err, "Failed to get API key from Vault", "TenantID", tenantId)
		return err
	}

	ctrl.Log.V(1).Info("Retrieved API key from Vault", "secretData", apiKeyData)
	clientId, _ := apiKeyData["client-id"].(string)
	ctrl.Log.V(1).Info("Authenticating Aruba client", "ClientID", clientId)
	clientSecret, _ := apiKeyData["client-secret"].(string)
	ctrl.Log.V(1).Info("Authenticating Aruba client", "ClientSecret", clientSecret)

	c.OauthClient.SetClientIdAndSecret(clientId, clientSecret)

	token, err = c.OauthClient.GetAccessToken(false, tenantId)

	if err != nil {
		return err
	}

	c.HelperClient.SetAPIToken(token)
	return nil
}

func (r *HelperReconciler) getProjectID(ctx context.Context, name string, namespace string) (string, error) {
	arubaProject := &v1alpha1.ArubaProject{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaProject)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaProject %s/%s: %w",
			namespace, name, err)
	}

	if arubaProject.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaProject %s/%s does not have a project ID yet",
			namespace, name)
	}

	return arubaProject.Status.ResourceID, nil
}

func (r *HelperReconciler) getElasticIpID(ctx context.Context, name string, namespace string) (string, error) {
	arubaElasticIp := &v1alpha1.ArubaNetworkElasticIp{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaElasticIp)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaElasticIp %s/%s: %w",
			namespace, name, err)
	}

	if arubaElasticIp.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaElasticIp %s/%s does not have an elastic IP ID yet",
			namespace, name)
	}

	return arubaElasticIp.Status.ResourceID, nil
}

func (r *HelperReconciler) getSubnetID(ctx context.Context, name string, namespace string) (string, error) {
	arubaSubnet := &v1alpha1.ArubaSubnet{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaSubnet)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaSubnet %s/%s: %w",
			namespace, name, err)
	}

	if arubaSubnet.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaSubnet %s/%s does not have a subnet ID yet",
			namespace, name)
	}

	return arubaSubnet.Status.ResourceID, nil
}

func (r *HelperReconciler) getSecurityGroupID(ctx context.Context, name string, namespace string) (string, error) {
	arubaSecurityGroup := &v1alpha1.ArubaSecurityGroup{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaSecurityGroup)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaSecurityGroup %s/%s: %w",
			namespace, name, err)
	}

	if arubaSecurityGroup.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaSecurityGroup %s/%s does not have a security group ID yet",
			namespace, name)
	}

	return arubaSecurityGroup.Status.ResourceID, nil
}

func (r *HelperReconciler) getBlockStorageID(ctx context.Context, name string, namespace string) (string, error) {
	arubaBlockStorage := &v1alpha1.ArubaBlockStorage{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaBlockStorage)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaBlockStorage %s/%s: %w",
			namespace, name, err)
	}

	if arubaBlockStorage.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaBlockStorage %s/%s does not have a volume ID yet",
			namespace, name)
	}

	return arubaBlockStorage.Status.ResourceID, nil
}

func (r *HelperReconciler) getVpcID(ctx context.Context, name string, namespace string) (string, error) {
	arubaVpc := &v1alpha1.ArubaVpc{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaVpc)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaVpc %s/%s: %w",
			namespace, name, err)
	}

	if arubaVpc.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaVpc %s/%s does not have a VPC ID yet",
			namespace, name)
	}

	return arubaVpc.Status.ResourceID, nil
}

func (r *HelperReconciler) getKeyPairID(ctx context.Context, name string, namespace string) (string, error) {
	arubaKeyPair := &v1alpha1.ArubaKeyPair{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, arubaKeyPair)
	if err != nil {
		return "", fmt.Errorf("failed to get referenced ArubaKeyPair %s/%s: %w",
			namespace, name, err)
	}

	if arubaKeyPair.Status.ResourceID == "" {
		return "", fmt.Errorf("referenced ArubaKeyPair %s/%s does not have a key pair ID yet",
			namespace, name)
	}

	return arubaKeyPair.Status.ResourceID, nil
}
