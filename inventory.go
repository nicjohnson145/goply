package goply

import (
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/cli-utils/pkg/object"
)

type Inventory struct {
	Items []InventoryItem
}

func (i Inventory) ItemsToRemove(newInv Inventory) []*unstructured.Unstructured {
	newSet := newSet(lo.Map(newInv.Items, func(i InventoryItem, _ int) string { return i.ID() })...)

	toRemove := []*unstructured.Unstructured{}

	for _, i := range i.Items {
		if !newSet.Contains(i.ID()) {
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   i.ObjMetadata.GroupKind.Group,
				Kind:    i.ObjMetadata.GroupKind.Kind,
				Version: i.GroupVersion,
			})
			obj.SetName(i.Name)
			obj.SetNamespace(i.Namespace)

			toRemove = append(toRemove, obj)
		}
	}

	return toRemove
}

type InventoryItem struct {
	object.ObjMetadata
	GroupVersion string
}

func (i InventoryItem) ID() string {
	return i.ObjMetadata.String()
}

func toInventoryItem(obj *unstructured.Unstructured) InventoryItem {
	return InventoryItem{
		ObjMetadata:  object.UnstructuredToObjMetadata(obj),
		GroupVersion: obj.GroupVersionKind().Version,
	}
}
