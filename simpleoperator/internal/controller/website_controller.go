/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sitesv1 "github.com/zhangbiao2009/controller_exercise/simpleoperator/api/v1"
)

// WebsiteReconciler reconciles a Website object
type WebsiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=sites.davidweb.com,resources=websites,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=sites.davidweb.com,resources=websites/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=sites.davidweb.com,resources=websites/finalizers,verbs=update

//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Website object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *WebsiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// 1. Fetch the Website CR
	website := &sitesv1.Website{}
	if err := r.Get(ctx, req.NamespacedName, website); err != nil {
		if errors.IsNotFound(err) {
			// CR deleted, nothing to do (owned resources auto-deleted)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 2. Create/Update Deployment
	if err := r.reconcileDeployment(ctx, website); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Create/Update Service
	if err := r.reconcileService(ctx, website); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Update Status
	if err := r.updateStatus(ctx, website); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *WebsiteReconciler) reconcileDeployment(ctx context.Context, website *sitesv1.Website) error {
	log := log.FromContext(ctx)

	// Define the desired Deployment
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      website.Name,
			Namespace: website.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &website.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": website.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": website.Name},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{
						Name:  "git-sync",
						Image: "registry.k8s.io/git-sync/git-sync:v4.2.1",
						Args:  []string{"--repo=" + website.Spec.GitURL, "--root=/data", "--one-time"},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "web-content",
							MountPath: "/data",
						}},
					}},
					Containers: []corev1.Container{{
						Name:  "nginx",
						Image: "nginx:alpine",
						Ports: []corev1.ContainerPort{{ContainerPort: 80}},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "web-content",
							MountPath: "/usr/share/nginx/html",
						}},
					}},
					Volumes: []corev1.Volume{{
						Name: "web-content",
						VolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
					}},
				},
			},
		},
	}

	// Set OwnerReference - THIS IS KEY FOR GARBAGE COLLECTION
	if err := ctrl.SetControllerReference(website, dep, r.Scheme); err != nil {
		return err
	}

	// Check if Deployment exists
	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}, found)

	if err != nil && errors.IsNotFound(err) {
		// Create new Deployment
		log.Info("Creating Deployment", "name", dep.Name)
		return r.Create(ctx, dep)
	} else if err != nil {
		return err
	}

	// Update existing Deployment if spec changed
	found.Spec.Replicas = dep.Spec.Replicas
	return r.Update(ctx, found)
}

func (r *WebsiteReconciler) reconcileService(ctx context.Context, website *sitesv1.Website) error {
	log := log.FromContext(ctx)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      website.Name,
			Namespace: website.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": website.Name},
			Ports: []corev1.ServicePort{{
				Port:       80,
				TargetPort: intstr.FromInt(80),
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	// Set OwnerReference
	if err := ctrl.SetControllerReference(website, svc, r.Scheme); err != nil {
		return err
	}

	found := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: svc.Namespace}, found)

	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Service", "name", svc.Name)
		return r.Create(ctx, svc)
	}

	return err
}

func (r *WebsiteReconciler) updateStatus(ctx context.Context, website *sitesv1.Website) error {
	// Get the Deployment to check replicas
	dep := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: website.Name, Namespace: website.Namespace}, dep)
	if err != nil {
		return err
	}

	// Update status fields
	website.Status.AvailableReplicas = dep.Status.AvailableReplicas
	if dep.Status.AvailableReplicas > 0 {
		website.Status.Phase = "Running"
	} else {
		website.Status.Phase = "Pending"
	}

	// Use Status().Update() for status subresource
	return r.Status().Update(ctx, website)
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebsiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sitesv1.Website{}).
		Owns(&appsv1.Deployment{}). // Watch Deployments we own
		Owns(&corev1.Service{}).    // Watch Services we own
		Complete(r)
}
