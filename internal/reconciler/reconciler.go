package reconciler

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	arubamt "github.com/Arubacloud/sdk-go/pkg/multitenant"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

const (
	// ShortRequeueAfter is the requeue delay used after transient operations such as
	// setting finalizers, where a quick re-check is expected.
	ShortRequeueAfter = 1 * time.Second
	// LongRequeueAfter is the requeue delay used when polling for slow cloud-side
	// operations (e.g. waiting for a resource to become active).
	LongRequeueAfter = 20 * time.Second
	// MaxPhaseTimeout defines the maximum time a resource can remain in a non-final phase
	MaxPhaseTimeout = 10 * time.Minute
)

type contextKey string

const (
	ArubaClientKey contextKey = "arubaClient"
)

// ResourceReconciler is an interface that must be implemented by all resource reconcilers.
// Each concrete controller embeds or delegates to a Reconciler and provides resource-specific
// behaviour through this interface.
type ResourceReconciler interface {
	// Object returns a zero-value instance of the managed resource type used as a
	// target for API-server GET calls.
	Object() ResourceObject
	// Finalizer returns the finalizer string registered on the managed resource to
	// prevent premature garbage collection before cloud-side cleanup is complete.
	Finalizer() string
	// HandleReconcile contains the resource-specific reconciliation logic, including
	// phase transitions and cloud API interactions. It is called after finalizer
	// registration and before finalizer removal.
	HandleReconcile(ctx context.Context, obj ResourceObject) (ctrl.Result, error)
}

// Reconciler provides base functionality for all resource controllers. It holds the
// Kubernetes client, the runtime scheme, and the Aruba cloud API client shared across
// all concrete controllers.
type Reconciler struct {
	// Client is the controller-runtime client used to read and write Kubernetes objects.
	client.Client
	// Scheme is the runtime scheme used for object type registration.
	*runtime.Scheme
	// ArubaClient is the authenticated Aruba cloud API client.
	config            ReconcilerConfig
	multiTenantClient arubamt.Multitenant

	// ArubaClient aruba.Client
}

// ResourceObject is the constraint that every managed custom resource must satisfy.
// It extends the standard controller-runtime client.Object with Aruba-specific accessors.
type ResourceObject interface {
	client.Object
	// GetResourceStatus returns a pointer to the embedded ResourceStatus, which tracks
	// the current phase and conditions of the cloud resource.
	GetResourceStatus() *v1alpha1.ResourceStatus
	// GetTenant returns the tenant identifier associated with this resource, used to
	// scope Aruba API calls to the correct account.
	GetTenant() string
}

// ReconcilerConfig holds configuration for setting up a Reconciler. Values are
// typically populated from environment variables or a ConfigMap at operator startup.
type ReconcilerConfig struct {
	// APIGateway is the base URL of the Aruba cloud API endpoint.
	APIGateway string
	// VaultIsEnabled controls whether credentials are fetched from HashiCorp Vault
	// instead of being supplied directly as ClientID/ClientSecret.
	VaultIsEnabled bool
	// VaultAddress is the address of the HashiCorp Vault server (used when VaultIsEnabled is true).
	VaultAddress string
	// KeycloakURL is the base URL of the Keycloak instance used for token issuance.
	KeycloakURL string
	// RealmAPI is the Keycloak realm used for authentication.
	RealmAPI string
	// Namespace is the Kubernetes namespace the operator runs in, used for Vault path scoping.
	Namespace string
	// RolePath is the Vault AppRole mount path used to retrieve credentials.
	RolePath string
	// ClientID is the OAuth2 client ID used when VaultIsEnabled is false.
	ClientID string
	// ClientSecret is the OAuth2 client secret used when VaultIsEnabled is false.
	ClientSecret string //nolint:gosec // G117: ClientSecret is intentionally storing OAuth secret
	// RoleID is the Vault AppRole role ID used when VaultIsEnabled is true.
	RoleID string
	// RoleSecret is the Vault AppRole secret ID used when VaultIsEnabled is true.
	RoleSecret string
	// KVMount is the Vault KV secrets engine mount path where credentials are stored.
	KVMount string
	// HTTPClient is an optional custom HTTP client injected for testing or proxy support.
	HTTPClient *http.Client
}

// NewReconcilerForTest creates a Reconciler suitable for unit testing. It accepts a
// pre-populated Multitenant client cache and a Kubernetes client+scheme directly,
// bypassing the ctrl.Manager requirement and real credential configuration.
func NewReconcilerForTest(k8sClient client.Client, scheme *runtime.Scheme, mtClient arubamt.Multitenant) *Reconciler {
	return &Reconciler{
		Client:            k8sClient,
		Scheme:            scheme,
		multiTenantClient: mtClient,
	}
}

// NewReconciler creates a new base Reconciler wired to the given controller-runtime
// Manager and configured via cfg. It initialises the Aruba cloud client, selecting
// either Vault-based or direct client-credential authentication depending on
// cfg.VaultIsEnabled. The process is terminated with log.Fatalf if the Aruba client
// cannot be created, since the operator cannot function without a valid API client.
func NewReconciler(mgr ctrl.Manager, cfg ReconcilerConfig) *Reconciler {
	// arubaClient, err := aruba.NewClient(options)
	// if err != nil {
	// 	log.Fatalf("failed to create Aruba Client: %v, options: `%v`", err, options)
	// }

	return &Reconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		config: cfg,
		// ArubaClient: arubaClient,
		multiTenantClient: arubamt.New(),
	}
}

// ArubaClient returns an authenticated Aruba cloud API client scoped to the tenant associated
// with the given resource. It first checks if a client for the tenant already exists in
// the multi-tenant client cache, and if not, it creates a new client using the Reconciler's
// configuration (either Vault-based or direct credentials) and adds it to the cache for
// future use. Errors during client creation are returned to the caller for handling.
func (r *Reconciler) ArubaClient(tenant string) (aruba.Client, error) {
	c, ok := r.multiTenantClient.Get(tenant)
	if ok {
		return c, nil
	}

	options := aruba.NewOptions().WithBaseURL(r.config.APIGateway).WithDefaultTokenIssuerURL()

	if r.config.VaultIsEnabled {
		options = options.WithVaultCredentialsRepository(
			r.config.VaultAddress, r.config.KVMount, tenant, r.config.Namespace, r.config.RolePath, r.config.RoleID, r.config.RoleSecret,
		)
	} else {
		ctrl.Log.Info("vault disabled, using direct credentials", "tenant", tenant)
		options = options.WithClientCredentials(r.config.ClientID, r.config.ClientSecret)
	}

	arubaClient, err := aruba.NewClient(options)
	if err != nil {
		return nil, fmt.Errorf("failed to create Aruba Client: %w, options: `%v`", err, options)
	}

	r.multiTenantClient.Add(tenant, arubaClient)

	return arubaClient, nil
}

// Reconcile implements the shared three-step reconciliation loop used by all resource
// controllers:
//
//  1. Finalizer registration — adds the resource-specific finalizer when the object is
//     not being deleted, then requeues so the next iteration starts with the finalizer
//     already present.
//  2. Resource-specific reconciliation — delegates to resourceReconciler.HandleReconcile
//     which drives phase transitions and interacts with the Aruba cloud API.
//  3. Finalizer removal — once HandleReconcile reports the resource has reached the
//     Deleted phase, the finalizer is stripped so Kubernetes can garbage-collect the object.
//
// Both finalizer operations are wrapped in retry.RetryOnConflict to handle optimistic
// concurrency conflicts from concurrent writes.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request, resourceReconciler ResourceReconciler) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	var result ctrl.Result

	// 1 - Make sure that the finalizers are in place when resource is not
	//     being deleteing
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := r.getResource(ctx, req, resourceReconciler.Object())
		if obj == nil || err != nil {
			return err
		}

		if obj.GetDeletionTimestamp().IsZero() && !slices.Contains(obj.GetFinalizers(), resourceReconciler.Finalizer()) {
			obj.SetFinalizers(append(obj.GetFinalizers(), resourceReconciler.Finalizer()))
			if err := r.Update(ctx, obj); err != nil {
				// The reconcile loop ends in error when it's not possible to set
				// the finalizar
				return err
			}

			logger.V(2).Info("finalizer added", "finalizer", resourceReconciler.Finalizer())
			// The reconciliation is requeued after the finalizer have been set
			result = ctrl.Result{RequeueAfter: 1 * time.Second}
			return nil // TODO: better RequeueAfter management
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	} else if !result.IsZero() {
		return result, nil
	}

	// 2 - Call the specific resource reconciler to handle the details of the
	//     reconciliation and the phase drifting
	obj, err := r.getResource(ctx, req, resourceReconciler.Object())
	if obj == nil || err != nil {
		return ctrl.Result{}, err
	}

	result, err = resourceReconciler.HandleReconcile(ctx, obj)
	if err != nil {
		logger.Error(err, "reconcile failed")
		return ctrl.Result{}, err
	} else if !result.IsZero() {
		return result, nil
	}

	// 3 - Handle de deletion case by removing the finalizer
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := r.getResource(ctx, req, resourceReconciler.Object())
		if obj == nil || err != nil {
			return err
		}

		status := obj.GetResourceStatus()

		if !obj.GetDeletionTimestamp().IsZero() &&
			status != nil &&
			status.Phase == v1alpha1.ResourcePhaseDeleted &&
			slices.Contains(obj.GetFinalizers(), resourceReconciler.Finalizer()) {
			obj.SetFinalizers(slices.DeleteFunc(obj.GetFinalizers(), func(v string) bool {
				return strings.EqualFold(v, resourceReconciler.Finalizer())
			}))
			if err := r.Update(ctx, obj); err != nil {
				return err
			}
			logger.V(2).Info("finalizer removed, resource deleted", "finalizer", resourceReconciler.Finalizer())
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getResource fetches the resource identified by req into obj. It returns (nil, nil)
// when the object is not found (i.e. it has already been deleted from the API server),
// allowing callers to treat a missing object as a no-op rather than an error.
func (r *Reconciler) getResource(ctx context.Context, req ctrl.Request, obj ResourceObject) (ResourceObject, error) {
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.FromContext(ctx).V(2).Info("resource not found, skipping", "resource", req.NamespacedName)
			return nil, nil
		}
		return nil, err
	}

	return obj, nil
}
