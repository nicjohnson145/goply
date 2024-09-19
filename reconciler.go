package goply

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fluxcd/pkg/ssa"
	ssautils "github.com/fluxcd/pkg/ssa/utils"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/fluxcd/pkg/ssa/normalize"
	"github.com/fluxcd/cli-utils/pkg/kstatus/polling"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultTimeout = 5 * time.Minute
)

func ptr[T any](x T) *T {
	return &x
}

type ApplyOpts struct {
	WaitTimeout *time.Duration
	SkipWait    bool
}

type DeleteOpts struct {
	WaitTimeout *time.Duration
	SkipWait    bool
}

func NewReconciler(kubeconfig string) (*Reconciler, error) {
	mgr, err := newResourceManager(kubeconfig)
	if err != nil {
		return nil, err
	}

	return &Reconciler{
		mgr: mgr,
	}, nil
}

func newResourceManager(kubeconf string) (*ssa.ResourceManager, error) {
	restConfig, err := clientcmd.RESTConfigFromKubeConfig([]byte(kubeconf))
	if err != nil {
		return nil, fmt.Errorf("error getting rest config: %w", err)
	}

	client, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("error building controller runtime client: %w", err)
	}

	dc, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("error creating discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))

	poller := polling.NewStatusPoller(client, mapper, polling.Options{})

	mgr := ssa.NewResourceManager(client, poller, ssa.Owner{
		Field: "goply",
		Group: "goply",
	})

	return mgr, nil
}

func getResourceStages(yaml string) ([]*unstructured.Unstructured, []*unstructured.Unstructured, error) {
	stageOne := []*unstructured.Unstructured{}
	stageTwo := []*unstructured.Unstructured{}

	allObjects, err := GetObjects(yaml)
	if err != nil {
		return stageOne, stageTwo, fmt.Errorf("error decoding yaml to unstructured: %w", err)
	}

	if err := normalize.UnstructuredList(allObjects); err != nil {
		return stageOne, stageTwo, fmt.Errorf("error setting defaults: %w", err)
	}

	for _, obj := range allObjects {
		if ssautils.IsClusterDefinition(obj) {
			stageOne = append(stageOne, obj)
		} else {
			stageTwo = append(stageTwo, obj)
		}
	}

	return stageOne, stageTwo, nil
}

func GetObjects(yaml string) ([]*unstructured.Unstructured, error) {
	allObjects, err := ssautils.ReadObjects(strings.NewReader(yaml))
	if err != nil {
		return []*unstructured.Unstructured{}, fmt.Errorf("error decoding yaml to unstructured: %w", err)
	}

	return allObjects, nil
}

type Reconciler struct {
	mgr     *ssa.ResourceManager
	logFunc func(string)
}

func (r *Reconciler) SetLogFunc(f func(string)) {
	r.logFunc = f
}

func (r *Reconciler) log(msg string) {
	if r.logFunc == nil {
		return
	}
	r.logFunc(msg)
}

func (r *Reconciler) Apply(yaml string, opts ApplyOpts) (Inventory, error) {
	return r.Reconcile(yaml, opts, nil)
}

func (r *Reconciler) Reconcile(yaml string, opts ApplyOpts, previousInventory *Inventory) (Inventory, error) {
	if opts.WaitTimeout == nil {
		opts.WaitTimeout = ptr(DefaultTimeout)
	}

	stageOne, stageTwo, err := getResourceStages(yaml)
	if err != nil {
		return Inventory{}, fmt.Errorf("error getting resource stages: %w", err)
	}

	inventory := Inventory{}
	inventory.Items = append(
		inventory.Items,
		lo.Map(stageOne, func(u *unstructured.Unstructured, _ int) InventoryItem {
			return toInventoryItem(u)
		})...,
	)
	inventory.Items = append(
		inventory.Items,
		lo.Map(stageTwo, func(u *unstructured.Unstructured, _ int) InventoryItem {
			return toInventoryItem(u)
		})...,
	)

	r.log("beginning apply of stage one resources")
	_, err = r.mgr.ApplyAll(context.TODO(), stageOne, ssa.ApplyOptions{})
	if err != nil {
		return Inventory{}, fmt.Errorf("error applying stage one resources: %w", err)
	}

	// Can't skip the stage1 wait, because it's got the NS and CRD objects, so if we don't wait for
	// those to show up, stage2 will probably fail
	r.log("waiting for stage one resources to reconcile")
	err = r.mgr.Wait(stageOne, ssa.WaitOptions{
		Interval: 2 * time.Second,
		Timeout:  30 * time.Second,
	})
	if err != nil {
		return Inventory{}, fmt.Errorf("timed out waiting for objects to reconcile")
	}

	r.log("beginning apply of stage two resources")
	_, err = r.mgr.ApplyAll(context.TODO(), stageTwo, ssa.ApplyOptions{})
	if err != nil {
		return Inventory{}, fmt.Errorf("error applying stage two resources: %w", err)
	}

	if !opts.SkipWait {
		r.log("waiting for stage two resources to reconcile")
		err = r.mgr.Wait(stageTwo, ssa.WaitOptions{
			Interval: 2 * time.Second,
			Timeout:  *opts.WaitTimeout,
		})
		if err != nil {
			return Inventory{}, fmt.Errorf("timed out waiting for objects to reconcile")
		}
	}

	if previousInventory != nil {
		if err := r.removeItems(*previousInventory, inventory, opts); err != nil {
			return Inventory{}, fmt.Errorf("error pruning items: %w", err)
		}
	}

	return inventory, nil
}

func (r *Reconciler) removeItems(previousInventory Inventory, newInventory Inventory, opts ApplyOpts) error {
	toRemove := previousInventory.ItemsToRemove(newInventory)
	if len(toRemove) == 0 {
		return nil
	}

	r.log("pruning resources")
	return r.delete(toRemove, DeleteOpts{WaitTimeout: opts.WaitTimeout, SkipWait: opts.SkipWait})
}

func (r *Reconciler) delete(items []*unstructured.Unstructured, opts DeleteOpts) error {
	if opts.WaitTimeout == nil {
		opts.WaitTimeout = ptr(DefaultTimeout)
	}

	r.log("beginning delete of resources")
	_, err := r.mgr.DeleteAll(context.TODO(), items, ssa.DeleteOptions{PropagationPolicy: metav1.DeletePropagationForeground})
	if err != nil {
		return fmt.Errorf("error during deletion: %w", err)
	}

	if !opts.SkipWait {
		r.log("waiting for resources to terminate")
		err = r.mgr.WaitForTermination(items, ssa.WaitOptions{
			Interval: 2 * time.Second,
			Timeout:  *opts.WaitTimeout,
		})
	}

	return nil
}

func (r *Reconciler) Delete(yaml string, opts DeleteOpts) error {
	allObjects, err := GetObjects(yaml)
	if err != nil {
		return fmt.Errorf("error decoding yaml to unstructured: %w", err)
	}

	return r.delete(allObjects, opts)
}
