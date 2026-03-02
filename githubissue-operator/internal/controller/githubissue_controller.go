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

// Reconcile ensures the remote GitHub issue matches the desired state in the GitHubIssue CR.
func (r *GitHubIssueReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the GitHubIssue instance
	var issue issuesv1.GitHubIssue
	if err := r.Get(ctx, req.NamespacedName, &issue); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("GitHubIssue resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 2. Get GitHub token (needed for all provider operations, including deletion cleanup)
	token, err := r.getToken(ctx, &issue)
	if err != nil {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// 3. Handle deletion
	if !issue.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &issue, token)
	}

	// 4. Ensure finalizer is present before any external mutations
	if added, result, err := r.ensureFinalizer(ctx, &issue); added {
		return result, err
	}

	// 5. Create or sync the remote issue
	if issue.Status.IssueNumber == 0 {
		if err := r.createRemoteIssue(ctx, &issue, token); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if result, err := r.syncRemoteIssue(ctx, &issue, token); err != nil {
			return result, err
		}
	}

	// 6. Periodic resync to detect and correct drift
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// ---------------------------------------------------------------------------
// Helper methods — one per reconciliation phase
// ---------------------------------------------------------------------------

// getToken reads the GitHub API token from the Secret referenced by the CR.
func (r *GitHubIssueReconciler) getToken(ctx context.Context, issue *issuesv1.GitHubIssue) (string, error) {
	var secret corev1.Secret
	key := types.NamespacedName{
		Name:      issue.Spec.TokenSecretRef,
		Namespace: issue.Namespace,
	}
	if err := r.Get(ctx, key, &secret); err != nil {
		return "", fmt.Errorf("unable to fetch Secret %s: %w", key, err)
	}
	tokenBytes, exists := secret.Data["token"]
	if !exists {
		return "", fmt.Errorf("key \"token\" not found in Secret %s", key)
	}
	return string(tokenBytes), nil
}

// handleDeletion closes the remote issue (if it exists) and removes the finalizer
// so Kubernetes can complete the deletion.
func (r *GitHubIssueReconciler) handleDeletion(ctx context.Context, issue *issuesv1.GitHubIssue, token string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(issue, githubIssueFinalizer) {
		return ctrl.Result{}, nil
	}

	// Close the remote issue if it was created
	if issue.Status.IssueNumber > 0 {
		logger.Info("closing remote issue before deletion", "issueNumber", issue.Status.IssueNumber)
		if err := r.IssueProvider.Close(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber); err != nil {
			logger.Error(err, "failed to close remote issue")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Remove finalizer to unblock deletion
	controllerutil.RemoveFinalizer(issue, githubIssueFinalizer)
	if err := r.Update(ctx, issue); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	logger.Info("finalizer removed, CR can be deleted")
	return ctrl.Result{}, nil
}

// ensureFinalizer adds the cleanup finalizer if it is not already present.
// Returns (true, result, err) when the finalizer was just added (caller should return immediately
// to requeue and re-fetch the updated object).
func (r *GitHubIssueReconciler) ensureFinalizer(ctx context.Context, issue *issuesv1.GitHubIssue) (bool, ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(issue, githubIssueFinalizer) {
		return false, ctrl.Result{}, nil
	}
	controllerutil.AddFinalizer(issue, githubIssueFinalizer)
	if err := r.Update(ctx, issue); err != nil {
		return true, ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
	}
	// Requeue to re-fetch the updated object (resourceVersion changed).
	return true, ctrl.Result{Requeue: true}, nil
}

// createRemoteIssue creates a new GitHub issue and records its details in status.
func (r *GitHubIssueReconciler) createRemoteIssue(ctx context.Context, issue *issuesv1.GitHubIssue, token string) error {
	logger := log.FromContext(ctx)
	logger.Info("creating remote issue", "repo", issue.Spec.Repo, "title", issue.Spec.Title)

	created, err := r.IssueProvider.Create(ctx, token, providers.CreateIssueInput{
		Repo:   issue.Spec.Repo,
		Title:  issue.Spec.Title,
		Body:   issue.Spec.Body,
		Labels: issue.Spec.Labels,
	})
	if err != nil {
		return fmt.Errorf("failed to create remote issue: %w", err)
	}

	issue.Status.IssueNumber = created.Number
	issue.Status.IssueURL = created.URL
	issue.Status.State = created.State
	if err := r.Status().Update(ctx, issue); err != nil {
		return fmt.Errorf("failed to update status after creation: %w", err)
	}
	logger.Info("remote issue created", "issueNumber", created.Number)
	return nil
}

// syncRemoteIssue enforces the desired state (spec) onto the existing GitHub issue.
// It reopens the issue if closed externally and pushes any title/body/labels drift.
func (r *GitHubIssueReconciler) syncRemoteIssue(ctx context.Context, issue *issuesv1.GitHubIssue, token string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("syncing remote issue", "issueNumber", issue.Status.IssueNumber)

	current, err := r.IssueProvider.Get(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get remote issue: %w", err)
	}

	// Reopen if someone closed it on GitHub
	if current.State == "closed" {
		logger.Info("reopening externally-closed issue", "issueNumber", issue.Status.IssueNumber)
		if err := r.IssueProvider.Reopen(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber); err != nil {
			logger.Error(err, "failed to reopen remote issue")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		current.State = "open"
	}

	// Push spec to GitHub if title/body/labels have drifted
	if r.specDrifted(issue, current) {
		logger.Info("updating remote issue to match spec", "issueNumber", issue.Status.IssueNumber)
		if _, err := r.IssueProvider.Update(ctx, token, issue.Spec.Repo, issue.Status.IssueNumber, providers.UpdateIssueInput{
			Title:  issue.Spec.Title,
			Body:   issue.Spec.Body,
			Labels: issue.Spec.Labels,
		}); err != nil {
			logger.Error(err, "failed to update remote issue")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		logger.Info("remote issue updated")
	}

	// Sync status back
	if issue.Status.State != current.State {
		issue.Status.State = current.State
		if err := r.Status().Update(ctx, issue); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update status after sync: %w", err)
		}
	}
	return ctrl.Result{}, nil
}

// specDrifted reports whether the remote issue differs from the desired spec.
func (r *GitHubIssueReconciler) specDrifted(issue *issuesv1.GitHubIssue, remote *providers.Issue) bool {
	return remote.Title != issue.Spec.Title ||
		remote.Body != issue.Spec.Body ||
		!labelsMatch(remote.Labels, issue.Spec.Labels)
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
