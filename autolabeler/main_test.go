package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func newNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func TestReconcile_AddsLabelToUnlabeledNamespace(t *testing.T) {
	ns := newNamespace("test-ns", nil)
	fakeClient := fake.NewClientset(ns)
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	nsInformer := factory.Core().V1().Namespaces()
	nsInformer.Informer()
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	defer close(stopCh)

	err := reconcile(fakeClient, nsInformer.Lister(), "test-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := fakeClient.CoreV1().Namespaces().Get(context.TODO(), "test-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get namespace: %v", err)
	}
	if updated.Labels["team"] != "unassigned" {
		t.Errorf("expected label team=unassigned, got labels: %v", updated.Labels)
	}
}

func TestReconcile_SkipsNamespaceWithExistingLabel(t *testing.T) {
	ns := newNamespace("test-ns", map[string]string{"team": "backend"})
	fakeClient := fake.NewClientset(ns)
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	nsInformer := factory.Core().V1().Namespaces()
	nsInformer.Informer()
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	defer close(stopCh)

	err := reconcile(fakeClient, nsInformer.Lister(), "test-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := fakeClient.CoreV1().Namespaces().Get(context.TODO(), "test-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get namespace: %v", err)
	}
	if updated.Labels["team"] != "backend" {
		t.Errorf("expected label team=backend (unchanged), got labels: %v", updated.Labels)
	}
}

func TestReconcile_SkipsSystemNamespaces(t *testing.T) {
	systemNamespaces := []string{"kube-system", "kube-public", "kube-node-lease", "default"}

	for _, name := range systemNamespaces {
		t.Run(name, func(t *testing.T) {
			ns := newNamespace(name, nil)
			fakeClient := fake.NewClientset(ns)
			factory := informers.NewSharedInformerFactory(fakeClient, 0)
			nsInformer := factory.Core().V1().Namespaces()
			nsInformer.Informer()
			stopCh := make(chan struct{})
			factory.Start(stopCh)
			factory.WaitForCacheSync(stopCh)
			defer close(stopCh)

			err := reconcile(fakeClient, nsInformer.Lister(), name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			updated, err := fakeClient.CoreV1().Namespaces().Get(context.TODO(), name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get namespace: %v", err)
			}
			if _, exists := updated.Labels["team"]; exists {
				t.Errorf("system namespace %s should not have team label, got: %v", name, updated.Labels)
			}
		})
	}
}

func TestReconcile_NonExistentNamespace(t *testing.T) {
	fakeClient := fake.NewClientset()
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	nsInformer := factory.Core().V1().Namespaces()
	nsInformer.Informer()
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	defer close(stopCh)

	err := reconcile(fakeClient, nsInformer.Lister(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for non-existent namespace, got nil")
	}
}

func TestReconcile_PreservesExistingLabels(t *testing.T) {
	ns := newNamespace("test-ns", map[string]string{"env": "production"})
	fakeClient := fake.NewClientset(ns)
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	nsInformer := factory.Core().V1().Namespaces()
	nsInformer.Informer()
	stopCh := make(chan struct{})
	factory.Start(stopCh)
	factory.WaitForCacheSync(stopCh)
	defer close(stopCh)

	err := reconcile(fakeClient, nsInformer.Lister(), "test-ns")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated, err := fakeClient.CoreV1().Namespaces().Get(context.TODO(), "test-ns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get namespace: %v", err)
	}
	if updated.Labels["team"] != "unassigned" {
		t.Errorf("expected team=unassigned, got: %v", updated.Labels)
	}
	if updated.Labels["env"] != "production" {
		t.Errorf("existing label env=production was lost, got: %v", updated.Labels)
	}
}
