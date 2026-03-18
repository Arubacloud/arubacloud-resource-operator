package reconciler

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Arubacloud/sdk-go/pkg/aruba"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

const (
	ShortRequeueAfter = 1 * time.Second
	LongRequeueAfter  = 20 * time.Second
	// MaxPhaseTimeout defines the maximum time a resource can remain in a non-final phase
	MaxPhaseTimeout = 2 * time.Minute
)

// ResourceReconciler is an interface that must be implemented by all resource reconcilers
type ResourceReconciler interface {
	Object() ResourceObject
	Finalizer() string
	HandleReconcile(ctx context.Context, obj ResourceObject) (ctrl.Result, error)
}

// Reconciler provides base functionality for all resource controllers
type Reconciler struct {
	client.Client
	*runtime.Scheme
	ArubaClient aruba.Client
}

type ResourceObject interface {
	client.Object
	GetResourceStatus() *v1alpha1.ResourceStatus
	GetTenant() string
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
	ClientSecret   string //nolint:gosec // G117: ClientSecret is intentionally storing OAuth secret
	RoleID         string
	RoleSecret     string
	KVMount        string
	HTTPClient     *http.Client
}

// NewReconciler creates a new base reconciler
func NewReconciler(mgr ctrl.Manager, cfg ReconcilerConfig) *Reconciler {
	options := aruba.NewOptions().WithBaseURL(cfg.APIGateway).WithDefaultTokenIssuerURL()

	if cfg.VaultIsEnabled {
		options = options.WithVaultCredentialsRepository(
			cfg.VaultAddress, cfg.KVMount, "test-tenant", cfg.Namespace, cfg.RolePath, cfg.RoleID, cfg.RoleSecret,
		)
	} else {
		options = options.WithClientCredentials(cfg.ClientID, cfg.ClientSecret)
	}

	arubaClient, err := aruba.NewClient(options)
	if err != nil {
		log.Fatalf("failed to create Aruba Client: %v, options: `%v`", err, options)
	}

	return &Reconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		ArubaClient: arubaClient,
	}
}

// Reconcile handles the common reconciliation logic for all resources
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request, resourceReconciler ResourceReconciler) (ctrl.Result, error) {
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
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) getResource(ctx context.Context, req ctrl.Request, obj ResourceObject) (ResourceObject, error) {
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	return obj, nil
}
