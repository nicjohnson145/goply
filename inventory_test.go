package goply

import (
	"testing"

	"github.com/lithammer/dedent"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func inventoryFromYaml(t *testing.T, yaml string) Inventory {
	t.Helper()
	objs, err := getObjects(yaml)
	require.NoError(t, err)

	return Inventory{
		Items: lo.Map(objs, func(o *unstructured.Unstructured, _ int) InventoryItem { return toInventoryItem(o) }),
	}
}

func TestInventoryDiff(t *testing.T) {
	twoMaps := inventoryFromYaml(t, dedent.Dedent(`
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-one
		  namespace: goply-test
		data:
		  foo: foo1
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-two
		  namespace: goply-test
		data:
		  bar: bar1
	`)[1:])
	oneMap := inventoryFromYaml(t, dedent.Dedent(`
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-two
		  namespace: goply-test
		data:
		  bar: bar1
	`)[1:])

	toRemove := twoMaps.ItemsToRemove(oneMap)
	toRemoveNames := lo.Map(toRemove, func(u *unstructured.Unstructured, _ int) string { return u.GetName() })
	require.Equal(t, []string{"config-one"}, toRemoveNames)
}
