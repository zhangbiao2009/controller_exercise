package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/client-go/util/workqueue"
)

func getClientset() (*kubernetes.Clientset, error) {
	// Try in-cluster config first (works when running inside a pod)
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig (for local development)
		kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	return kubernetes.NewForConfig(config)
}

func main() {
	clientset, err := getClientset()
	if err != nil {
		panic(err)
	}

	// Create the factory (resync every 30 seconds)
	factory := informers.NewSharedInformerFactory(clientset, 30*time.Second)
	// Get the Namespace informer from the factory
	nsInformer := factory.Core().V1().Namespaces()

	// Create a rate-limiting workqueue
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())

	// Register event handlers on the informer before factory.Start()
	nsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err == nil {
				queue.Add(key)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err == nil {
				queue.Add(key)
			}
		},
	})

	// Start the factory and wait for cache sync
	stopCh := make(chan struct{})
	factory.Start(stopCh)

	fmt.Println("Waiting for cache sync...")
	synced := factory.WaitForCacheSync(stopCh)
	for t, ok := range synced {
		fmt.Printf("  %v synced: %v\n", t, ok)
	}

	// Worker loop — process items from the queue
	fmt.Println("Starting worker...")
	for {
		// Get the next key from the queue (blocks until one is available)
		key, shutdown := queue.Get()
		if shutdown {
			fmt.Println("Queue shut down")
			return
		}

		// Process the key
		err := reconcile(clientset, nsInformer.Lister(), key.(string))
		if err != nil {
			fmt.Printf("Error reconciling %s: %v, requeuing\n", key, err)
			queue.AddRateLimited(key) // requeue with backoff
		} else {
			queue.Forget(key) // clear rate limiter tracking
		}

		// Tell the queue this item is done processing
		queue.Done(key)
	}
}

func reconcile(clientset kubernetes.Interface, lister corev1listers.NamespaceLister, key string) error {
	ns, err := lister.Get(key)
	if err != nil {
		return err // will be requeued
	}

	// Skip system namespaces
	switch ns.Name {
	case "kube-system", "kube-public", "kube-node-lease", "default":
		return nil
	}

	// Check if "team" label exists
	if _, exists := ns.Labels["team"]; exists {
		return nil // already labeled, nothing to do
	}

	// Patch the namespace to add the label
	fmt.Printf("Labeling namespace %s with team=unassigned\n", ns.Name)
	patch := []byte(`{"metadata":{"labels":{"team":"unassigned"}}}`)
	_, err = clientset.CoreV1().Namespaces().Patch(
		context.TODO(),
		ns.Name,
		types.MergePatchType,
		patch,
		metav1.PatchOptions{},
	)
	return err
}
