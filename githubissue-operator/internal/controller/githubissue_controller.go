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
	"fmt"
	"slices"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	issuesv1 "github.com/zhangbiao2009/controller_exercise/githubissue-operator/api/v1"
	"github.com/zhangbiao2009/controller_exercise/githubissue-operator/pkg/providers"
)

const githubIssueFinalizer = "issues.github.example.com/cleanup"

// GitHubIssueReconciler reconciles a GitHubIssue object
type GitHubIssueReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	IssueProvider providers.IssueProvider
}

//+kubebuilder:rbac:groups=issues.github.example.com,resources=githubissues,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=issues.github.example.com,resources=githubissues/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=issues.github.example.com,resources=githubissues/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the GitHubIssue object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *GitHubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the GitHubIssue instance
	var issue issuesv1.GitHubIssue
	if err := r.Get(ctx, req.NamespacedName, &issue); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			logger.Info("GitHubIssue resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "unable to fetch GitHubIssue")
		return ctrl.Result{}, err
	}

	// 2. Get GitHub token from Secret
	var secret corev1.Secret
	secretKey := types.NamespacedName{
		Name:      issue.Spec.TokenSecretRef,
		Namespace: issue.Namespace, // Assuming the secret is in the same namespace as the GitHubIssue
	}
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		logger.Error(err, "unable to fetch Secret for GitHub token")
		return ctrl.Result{}, err
	}
	tokenBytes, exists := secret.Data["token"]
	if !exists {
		logger.Error(fmt.Errorf("token key not found in secret"), "invalid Secret data")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	token := string(tokenBytes)

	// 3. Handle deletion (check if CR is being deleted)
	if !issue.DeletionTimestamp.IsZero() {
		// CR is being deleted
		if controllerutil.ContainsFinalizer(&issue, githubIssueFinalizer) {
			// Finalizer exists, must cleanup first

			// Close the remote issue if it was created
			if issue.Status.IssueNumber > 0 {
				logger.Info("closing remote issue before deletion", "issueNumber", issue.Status.IssueNumber)
				if err := r.IssueProvider.Close(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber); err != nil {
					logger.Error(err, "failed to close remote issue")
					// Retry later - don't remove finalizer until cleanup succeeds
					return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
				}
			}

			// Cleanup done, remove finalizer to allow deletion
			controllerutil.RemoveFinalizer(&issue, githubIssueFinalizer)
			if err := r.Update(ctx, &issue); err != nil {
				logger.Error(err, "failed to remove finalizer")
				return ctrl.Result{}, err
			}
			logger.Info("finalizer removed, CR can be deleted")
		}
		// Finalizer removed or never existed, let Kubernetes delete
		return ctrl.Result{}, nil
	}

	// 4. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&issue, githubIssueFinalizer) {
		controllerutil.AddFinalizer(&issue, githubIssueFinalizer)
		if err := r.Update(ctx, &issue); err != nil {
			logger.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
		// Requeue to re-fetch the updated object before proceeding.
		// The Update call above changes the resourceVersion, so continuing
		// with the stale local copy could cause conflict errors on subsequent writes.
		return ctrl.Result{Requeue: true}, nil
	}

	// 5. Create issue if status.IssueNumber == 0
	if issue.Status.IssueNumber == 0 {
		logger.Info("creating remote issue", "repo", issue.Spec.Repo, "title", issue.Spec.Title)
		createdIssue, err := r.IssueProvider.Create(ctx, token, providers.CreateIssueInput{
			Repo:   issue.Spec.Repo,
			Title:  issue.Spec.Title,
			Body:   issue.Spec.Body,
			Labels: issue.Spec.Labels,
		})
		if err != nil {
			logger.Error(err, "failed to create remote issue")
			return ctrl.Result{}, err
		}

		// Update status with created issue details
		issue.Status.IssueNumber = createdIssue.Number
		issue.Status.IssueURL = createdIssue.URL
		issue.Status.State = createdIssue.State
		if err := r.Status().Update(ctx, &issue); err != nil {
			logger.Error(err, "failed to update GitHubIssue status after creation")
			return ctrl.Result{}, err
		}
		logger.Info("remote issue created successfully", "issueNumber", createdIssue.Number)
	} else {
		// 6. Sync: K8s spec is the source of truth — enforce desired state on GitHub
		logger.Info("syncing remote issue", "issueNumber", issue.Status.IssueNumber)

		// Get current state of the remote issue
		currentIssue, err := r.IssueProvider.Get(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber)
		if err != nil {
			logger.Error(err, "failed to get remote issue for syncing")
			return ctrl.Result{}, err
		}

		// Reopen the issue if someone closed it on GitHub — K8s says it should exist and be open
		if currentIssue.State == "closed" {
			logger.Info("remote issue was closed externally, reopening to match desired state", "issueNumber", issue.Status.IssueNumber)
			if err := r.IssueProvider.Reopen(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber); err != nil {
				logger.Error(err, "failed to reopen remote issue")
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			currentIssue.State = "open"
		}

		// Push spec to GitHub if title/body/labels have drifted
		if currentIssue.Title != issue.Spec.Title || currentIssue.Body != issue.Spec.Body || !labelsMatch(currentIssue.Labels, issue.Spec.Labels) {
			logger.Info("updating remote issue to match spec", "issueNumber", issue.Status.IssueNumber)
			_, err := r.IssueProvider.Update(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber, providers.UpdateIssueInput{
				Title:  issue.Spec.Title,
				Body:   issue.Spec.Body,
				Labels: issue.Spec.Labels,
			})
			if err != nil {
				logger.Error(err, "failed to update remote issue")
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
			logger.Info("remote issue updated successfully", "issueNumber", issue.Status.IssueNumber)
		} else {
			logger.Info("remote issue is already in sync with spec", "issueNumber", issue.Status.IssueNumber)
		}

		// Update status to reflect enforced state
		if issue.Status.State != currentIssue.State {
			issue.Status.State = currentIssue.State
			if err := r.Status().Update(ctx, &issue); err != nil {
				logger.Error(err, "failed to update GitHubIssue status after sync")
				return ctrl.Result{}, err
			}
		}
	}

	// Requeue after 5 minutes to periodically detect and correct
	// any drift on the remote GitHub issue (e.g., someone closed or edited it).
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil

}

// labelsMatch checks if two label slices contain the same elements (order-independent)
func labelsMatch(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)
	sort.Strings(aCopy)
	sort.Strings(bCopy)
	return slices.Equal(aCopy, bCopy)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitHubIssueReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&issuesv1.GitHubIssue{}).
		Complete(r)
}
