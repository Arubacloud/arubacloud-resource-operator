package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// resolveOwnerObject fetches the parent Kubernetes object identified by ref into target.
// If ref.Namespace is empty, childNamespace is used as the lookup namespace.
// Returns a NotFound error (apierrors.IsNotFound) if the parent does not exist.
func resolveOwnerObject(
	ctx context.Context,
	c client.Client,
	ref v1alpha1.ResourceReference,
	childNamespace string,
	target client.Object,
) error {
	ns := ref.Namespace
	if ns == "" {
		ns = childNamespace
	}
	return c.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: ns}, target)
}

// ensureOwnerReference idempotently sets owner as the controller owner of owned.
// It returns (true, nil) when the OwnerReference was newly set and the caller should
// requeue with ShortRequeueAfter to let the API server index propagate.
// It returns (false, nil) when the reference was already present or could not be set
// (e.g. cross-namespace reference — logged as a warning, not an error).
// It returns (false, err) on Kubernetes API write failures.
func ensureOwnerReference(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	owned client.Object,
) (bool, error) {
	logger := log.FromContext(ctx)

	var needsRequeue bool
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := owned.DeepCopyObject().(client.Object)
		if err := c.Get(ctx, client.ObjectKeyFromObject(owned), fresh); err != nil {
			return err
		}

		// Already owned by this owner — idempotent.
		for _, ref := range fresh.GetOwnerReferences() {
			if ref.UID == owner.GetUID() {
				return nil
			}
		}

		objPatch := fresh.DeepCopyObject().(client.Object)
		if err := controllerutil.SetControllerReference(owner, objPatch, scheme); err != nil {
			// Cross-namespace or other structural constraint — skip silently.
			logger.Info("skipping owner reference setup",
				"owner", owner.GetName(), "owned", owned.GetName(), "reason", err.Error())
			return nil
		}

		needsRequeue = true
		return c.Patch(ctx, objPatch, client.MergeFrom(fresh))
	})

	return needsRequeue, err
}

// hasOwnedChildren reports whether any objects in the provided child lists are owned
// by owner (matched via OwnerReference UID). It lists all objects in owner's namespace
// for each list type and filters client-side by UID.
func hasOwnedChildren(ctx context.Context, c client.Client, owner client.Object, childLists ...client.ObjectList) (bool, error) {
	ownerUID := owner.GetUID()
	namespace := owner.GetNamespace()

	for _, list := range childLists {
		if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return false, fmt.Errorf("listing owned children: %w", err)
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return false, fmt.Errorf("extracting list items: %w", err)
		}
		for _, item := range items {
			obj, ok := item.(client.Object)
			if !ok {
				continue
			}
			for _, ref := range obj.GetOwnerReferences() {
				if ref.UID == ownerUID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// deleteOwnedChildren issues a Delete for every child in the provided lists that is
// owned by owner (matched via OwnerReference UID) and not already being deleted.
// Idempotent: children with a non-zero DeletionTimestamp are skipped; NotFound is ignored.
// Call this from the WaitingChildrenDeletion action so that the Kubernetes GC does not
// have to cascade-delete them (the GC only does so after the owner is fully removed from
// etcd, which cannot happen while the owner still has a finalizer).
func deleteOwnedChildren(ctx context.Context, c client.Client, owner client.Object, childLists ...client.ObjectList) error {
	ownerUID := owner.GetUID()
	namespace := owner.GetNamespace()

	for _, list := range childLists {
		if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
			return fmt.Errorf("listing owned children: %w", err)
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return fmt.Errorf("extracting list items: %w", err)
		}
		for _, item := range items {
			obj, ok := item.(client.Object)
			if !ok {
				continue
			}
			for _, ref := range obj.GetOwnerReferences() {
				if ref.UID != ownerUID {
					continue
				}
				if !obj.GetDeletionTimestamp().IsZero() {
					break // already deleting — nothing to do
				}
				if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("deleting owned child %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
				}
				break
			}
		}
	}
	return nil
}
