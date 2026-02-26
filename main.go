package main

import (
	"fmt"
	"path/filepath"
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
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

	// Must call .Informer() to register it with the factory before Start
	nsInformer.Informer()

	// Start the factory and wait for cache sync
	stopCh := make(chan struct{})
	factory.Start(stopCh)

	fmt.Println("Waiting for cache sync...")
	synced := factory.WaitForCacheSync(stopCh)
	for t, ok := range synced {
		fmt.Printf("  %v synced: %v\n", t, ok)
	}

	// List all namespaces from the cache
	namespaces, err := nsInformer.Lister().List(labels.Everything())
	if err != nil {
		panic(err)
	}

	fmt.Printf("Found %d namespaces:\n", len(namespaces))
	for _, ns := range namespaces {
		fmt.Printf("  Namespace: %s, Labels: %v\n", ns.Name, ns.Labels)
	}
}
