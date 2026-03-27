package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

const (
	// AnnotationOwnerReferences is the annotation key storing the custom Aruba owner
	// references as a JSON-marshalled []ArubaOwnerReference. This is the cross-namespace
	// replacement for standard Kubernetes OwnerReferences.
	AnnotationOwnerReferences = "arubacloud.com/owner-references"

	// LabelKeyPrefix is the prefix for ownership labels used in cluster-wide queries.
	// The full label key is LabelKeyPrefix + lowercase(ownerKind), e.g.
	// "arubacloud.com/owner-project".
	LabelKeyPrefix = "arubacloud.com/owner-"
)

// ownerLabelKey returns the ownership label key for the given owner object,
// derived from the owner's GVK kind lowercased (e.g. "arubacloud.com/owner-project").
func ownerLabelKey(scheme *runtime.Scheme, owner client.Object) (string, error) {
	gvks, _, err := scheme.ObjectKinds(owner)
	if err != nil || len(gvks) == 0 {
		return "", fmt.Errorf("cannot determine GVK for owner %T: %w", owner, err)
	}
	return LabelKeyPrefix + strings.ToLower(gvks[0].Kind), nil
}

// setArubaControllerReference sets owner as the controller-owner in the
// arubacloud.com/owner-references annotation on owned. This is the cross-namespace
// replacement for controllerutil.SetControllerReference.
//
// Rules (mirroring K8s OwnerReference semantics):
//   - At most one ArubaOwnerReference may have Controller=true.
//   - If the owner is already present (matched by UID), the entry is updated in place.
//   - If a different controller is already set, an error is returned.
func setArubaControllerReference(scheme *runtime.Scheme, owner client.Object, owned client.Object) error {
	gvks, _, err := scheme.ObjectKinds(owner)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot determine GVK for owner %T: %w", owner, err)
	}
	gvk := gvks[0]

	refs, err := parseArubaOwnerReferences(owned)
	if err != nil {
		return fmt.Errorf("parsing existing owner references: %w", err)
	}

	t := true
	newRef := v1alpha1.ArubaOwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Namespace:          owner.GetNamespace(),
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		Controller:         &t,
		BlockOwnerDeletion: &t,
	}

	for i, ref := range refs {
		if ref.UID == owner.GetUID() {
			refs[i] = newRef
			return marshalArubaOwnerReferences(owned, refs)
		}
		if ref.Controller != nil && *ref.Controller {
			return fmt.Errorf("object %s/%s already has a controller owner %s/%s (kind=%s)",
				owned.GetNamespace(), owned.GetName(), ref.Namespace, ref.Name, ref.Kind)
		}
	}

	refs = append(refs, newRef)
	return marshalArubaOwnerReferences(owned, refs)
}

// parseArubaOwnerReferences reads and JSON-parses the owner-references annotation.
// Returns nil, nil when the annotation is absent or empty.
func parseArubaOwnerReferences(obj client.Object) ([]v1alpha1.ArubaOwnerReference, error) {
	raw := obj.GetAnnotations()[AnnotationOwnerReferences]
	if raw == "" {
		return nil, nil
	}
	var refs []v1alpha1.ArubaOwnerReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// marshalArubaOwnerReferences serialises refs and writes the owner-references annotation.
func marshalArubaOwnerReferences(obj client.Object, refs []v1alpha1.ArubaOwnerReference) error {
	data, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[AnnotationOwnerReferences] = string(data)
	obj.SetAnnotations(annotations)
	return nil
}

// hasArubaOwnerReference reports whether obj's owner-references annotation contains
// an entry with the given ownerUID.
func hasArubaOwnerReference(obj client.Object, ownerUID types.UID) bool {
	refs, err := parseArubaOwnerReferences(obj)
	if err != nil {
		return false
	}
	for _, ref := range refs {
		if ref.UID == ownerUID {
			return true
		}
	}
	return false
}

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

// ensureOwnerReference idempotently sets owner as the controller owner of owned by
// writing both the arubacloud.com/owner-references annotation and the
// arubacloud.com/owner-<kind> label. Standard Kubernetes OwnerReferences are not
// used, enabling cross-namespace ownership.
//
// It returns (true, nil) when a patch was applied and the caller should requeue
// with ShortRequeueAfter. It returns (false, nil) when both layers were already
// present. It returns (false, err) on Kubernetes API write failures.
func ensureOwnerReference(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner client.Object,
	owned client.Object,
) (bool, error) {
	logger := log.FromContext(ctx)

	labelKey, err := ownerLabelKey(scheme, owner)
	if err != nil {
		return false, err
	}
	labelValue := string(owner.GetUID())

	var needsRequeue bool
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := owned.DeepCopyObject().(client.Object)
		if err := c.Get(ctx, client.ObjectKeyFromObject(owned), fresh); err != nil {
			return err
		}

		hasLabel := fresh.GetLabels()[labelKey] == labelValue
		hasAnnotation := hasArubaOwnerReference(fresh, owner.GetUID())

		if hasLabel && hasAnnotation {
			return nil
		}

		logger.V(1).Info("setting ownership metadata",
			"owner", fmt.Sprintf("%s/%s", owner.GetNamespace(), owner.GetName()),
			"owned", fmt.Sprintf("%s/%s", owned.GetNamespace(), owned.GetName()))

		objPatch := fresh.DeepCopyObject().(client.Object)

		if !hasAnnotation {
			if err := setArubaControllerReference(scheme, owner, objPatch); err != nil {
				return fmt.Errorf("setting aruba owner reference: %w", err)
			}
		}

		if !hasLabel {
			labels := objPatch.GetLabels()
			if labels == nil {
				labels = make(map[string]string)
			}
			labels[labelKey] = labelValue
			objPatch.SetLabels(labels)
		}

		needsRequeue = true
		return c.Patch(ctx, objPatch, client.MergeFrom(fresh))
	})

	return needsRequeue, err
}

// hasOwnedChildren reports whether any objects in the provided child lists are owned
// by owner, identified by a cluster-wide label query using the ownership label.
func hasOwnedChildren(
	ctx context.Context,
	c client.Client,
	owner client.Object,
	ownerLabelKey string,
	childLists ...client.ObjectList,
) (bool, error) {
	ownerUID := string(owner.GetUID())
	labelSelector := client.MatchingLabels{ownerLabelKey: ownerUID}

	for _, list := range childLists {
		if err := c.List(ctx, list, labelSelector); err != nil {
			return false, fmt.Errorf("listing owned children by label: %w", err)
		}
		items, err := apimeta.ExtractList(list)
		if err != nil {
			return false, fmt.Errorf("extracting list items: %w", err)
		}
		if len(items) > 0 {
			return true, nil
		}
	}

	return false, nil
}

// deleteOwnedChildren issues a Delete for every child in the provided lists that is
// owned by owner (matched via cluster-wide label query) and not already being deleted.
// Idempotent: children with a non-zero DeletionTimestamp are skipped; NotFound is ignored.
func deleteOwnedChildren(
	ctx context.Context,
	c client.Client,
	owner client.Object,
	ownerLabelKey string,
	childLists ...client.ObjectList,
) error {
	ownerUID := string(owner.GetUID())
	labelSelector := client.MatchingLabels{ownerLabelKey: ownerUID}

	for _, list := range childLists {
		if err := c.List(ctx, list, labelSelector); err != nil {
			return fmt.Errorf("listing owned children by label: %w", err)
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
			if !obj.GetDeletionTimestamp().IsZero() {
				continue
			}
			if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting owned child %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
			}
		}
	}

	return nil
}

// childToParentMapFunc returns a handler.MapFunc that enqueues the parent resource
// by reading the child's spec reference via extractRef. Works for both same-namespace
// and cross-namespace parent-child relationships, replacing Owns()-based watches.
func childToParentMapFunc(
	extractRef func(client.Object) *v1alpha1.ResourceReference,
) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		ref := extractRef(obj)
		if ref == nil {
			return nil
		}
		ns := ref.Namespace
		if ns == "" {
			ns = obj.GetNamespace()
		}
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{Name: ref.Name, Namespace: ns},
		}}
	}
}
