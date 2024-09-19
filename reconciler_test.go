package goply

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/lithammer/dedent"
	"github.com/samber/lo"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func requireLive(t *testing.T) {
	if _, ok := os.LookupEnv("TEST_LIVE"); !ok {
		t.Skip("Skipping due to TEST_LIVE not being set")
	}
}

func requireEnvVar(t *testing.T, varName string) string {
	val, ok := os.LookupEnv(varName)
	require.True(t, ok, fmt.Sprintf("%v is required", varName))
	return val
}

func basicSetup(t *testing.T, namespace string) (*Reconciler, *kubernetes.Clientset, func()) {
	t.Helper()

	requireLive(t)
	kubeconfigPath := requireEnvVar(t, "KUBECONFIG")
	kubeconfigBytes, err := os.ReadFile(kubeconfigPath)
	require.NoError(t, err)

	// Build a regular client-go client
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	require.NoError(t, err)
	client, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)

	// Cleanup after ourselves
	cleanup := func() {
		err := client.CoreV1().Namespaces().Delete(context.TODO(), namespace, metav1.DeleteOptions{PropagationPolicy: ptr(metav1.DeletePropagationForeground)})
		if k8serr.IsNotFound(err) {
			return
		}
		require.NoError(t, err)
	}

	// Build a reconciler
	r, err := NewReconciler(&ReconcilerConfig{
		Kubeconfig: string(kubeconfigBytes),
	})
	require.NoError(t, err)

	r.SetLogFunc(func(s string) { log.Trace(s) })

	return r, client, cleanup
}

func TestReconcile(t *testing.T) {
	const ns = "goply-reconcile-test"
	r, client, cleanup := basicSetup(t, ns)
	defer cleanup()

	// Apply some basic yaml
	origYaml := dedent.Dedent(fmt.Sprintf(`
		---
		apiVersion: v1
		kind: Namespace
		metadata:
		  name: %v
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-one
		  namespace: %v
		data:
		  foo: foo1
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-two
		  namespace: %v
		data:
		  bar: bar1
	`, ns, ns, ns))[1:]
	origInventory, err := r.Apply(origYaml, ApplyOpts{})
	require.NoError(t, err)

	// Should have an NS and 2 config maps
	allConfigmaps, err := client.CoreV1().ConfigMaps(ns).List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)
	configNames := lo.Map(allConfigmaps.Items, func(c corev1.ConfigMap, _ int) string { return c.Name })
	sort.Strings(configNames)
	require.Equal(t, []string{"config-one", "config-two", "kube-root-ca.crt"}, configNames)
	// And should see 3 things in the inventory
	require.Equal(t, 3, len(origInventory.Items))

	// Reapply a modified version of the yaml that only has one configmap
	newYaml := dedent.Dedent(fmt.Sprintf(`
		---
		apiVersion: v1
		kind: Namespace
		metadata:
		  name: %v
		---
		apiVersion: v1
		kind: ConfigMap
		metadata:
		  name: config-two
		  namespace: %v
		data:
		  bar: bar1
	`, ns, ns))[1:]
	newInventory, err := r.Reconcile(newYaml, ApplyOpts{}, &origInventory)
	require.NoError(t, err)

	// Should only have one configmap now
	allConfigmaps, err = client.CoreV1().ConfigMaps(ns).List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)
	configNames = lo.Map(allConfigmaps.Items, func(c corev1.ConfigMap, _ int) string { return c.Name })
	sort.Strings(configNames)
	require.Equal(t, []string{"config-two", "kube-root-ca.crt"}, configNames)
	// And should see 2 things in the inventory
	require.Equal(t, 2, len(newInventory.Items))
}

func TestDelete(t *testing.T) {
	const ns = "goply-delete-test"
	r, client, cleanup := basicSetup(t, ns)
	defer cleanup()

	// Apply some basic yaml
	yaml := dedent.Dedent(fmt.Sprintf(`
		---
		apiVersion: v1
		kind: Namespace
		metadata:
		  name: %v
	`, ns))[1:]
	_, err := r.Apply(yaml, ApplyOpts{})
	require.NoError(t, err)

	// Should have a namespace
	_, err = client.CoreV1().Namespaces().Get(context.TODO(), ns, metav1.GetOptions{})
	require.NoError(t, err)

	// Delete that same yaml
	err = r.Delete(yaml, DeleteOpts{})
	require.NoError(t, err)

	// Should not have a namespace
	_, err = client.CoreV1().Namespaces().Get(context.TODO(), ns, metav1.GetOptions{})
	require.Error(t, err)
}
