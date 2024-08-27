package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// objectMetaToName converts an ObjectMeta to a NamespacedName.
func objectMetaToName(meta metav1.ObjectMeta) types.NamespacedName {
	return types.NamespacedName{Namespace: meta.Namespace, Name: meta.Name}
}

// upsertResource upserts a resource in the cluster.
// It creates the resource if it doesn't exist, otherwise it updates it.
func upsertResource(ctx context.Context, c client.Client, resource client.Object) error {
	if err := c.Get(ctx, types.NamespacedName{
		Name:      resource.GetName(),
		Namespace: resource.GetNamespace(),
	}, resource); err != nil {
		if apierrors.IsNotFound(err) {
			return c.Create(ctx, resource)
		}
		return err
	}
	return c.Update(ctx, resource)
}

func deleteResource(ctx context.Context, c client.Client, resource client.Object) error {
	name := types.NamespacedName{
		Name:      resource.GetName(),
		Namespace: resource.GetNamespace(),
	}
	if err := c.Get(ctx, name, resource); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error getting resource %s for deletion: %w", name, err)
	}
	return c.Delete(ctx, resource)
}
