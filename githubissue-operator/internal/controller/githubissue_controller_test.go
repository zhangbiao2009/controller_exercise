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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	issuesv1 "github.com/zhangbiao2009/controller_exercise/githubissue-operator/api/v1"
	"github.com/zhangbiao2009/controller_exercise/githubissue-operator/pkg/providers"
)

var _ = Describe("GitHubIssue Controller", func() {
	const (
		resourceName = "test-issue"
		namespace    = "default"
		secretName   = "github-token"
		repo         = "owner/repo"
		token        = "fake-token"
	)

	ctx := context.Background()

	namespacedName := types.NamespacedName{
		Name:      resourceName,
		Namespace: namespace,
	}

	var mockProvider *providers.MockProvider
	var reconciler *GitHubIssueReconciler

	BeforeEach(func() {
		mockProvider = providers.NewMockProvider()

		// Build a fresh fake client for each test for isolation
		k8sClient = fake.NewClientBuilder().
			WithScheme(testScheme).
			WithStatusSubresource(&issuesv1.GitHubIssue{}).
			Build()

		reconciler = &GitHubIssueReconciler{
			Client:        k8sClient,
			Scheme:        testScheme,
			IssueProvider: mockProvider,
		}

		// Create the Secret with a token
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"token": []byte(token),
			},
		}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret)
		if apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		}
	})

	AfterEach(func() {
		// Clean up GitHubIssue if it exists
		issue := &issuesv1.GitHubIssue{}
		err := k8sClient.Get(ctx, namespacedName, issue)
		if err == nil {
			// Remove finalizer so deletion can proceed
			if controllerutil.ContainsFinalizer(issue, githubIssueFinalizer) {
				controllerutil.RemoveFinalizer(issue, githubIssueFinalizer)
				Expect(k8sClient.Update(ctx, issue)).To(Succeed())
			}
			Expect(k8sClient.Delete(ctx, issue)).To(Succeed())
		}
	})

	createGitHubIssue := func() {
		issue := &issuesv1.GitHubIssue{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: namespace,
			},
			Spec: issuesv1.GitHubIssueSpec{
				Repo:           repo,
				Title:          "Test Issue",
				Body:           "This is a test issue",
				Labels:         []string{"bug"},
				TokenSecretRef: secretName,
			},
		}
		Expect(k8sClient.Create(ctx, issue)).To(Succeed())
	}

	Context("When creating a new GitHubIssue", func() {
		It("should add a finalizer on first reconcile", func() {
			createGitHubIssue()

			// First reconcile: adds finalizer and requeues
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue(), "should requeue after adding finalizer")

			// Verify finalizer was added
			var issue issuesv1.GitHubIssue
			Expect(k8sClient.Get(ctx, namespacedName, &issue)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&issue, githubIssueFinalizer)).To(BeTrue())
		})

		It("should create a remote issue and update status", func() {
			createGitHubIssue()

			// First reconcile: adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile: creates the remote issue
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter.Minutes()).To(Equal(5.0))

			// Verify mock was called
			Expect(mockProvider.CreateCalled).To(Equal(1))

			// Verify status was updated
			var issue issuesv1.GitHubIssue
			Expect(k8sClient.Get(ctx, namespacedName, &issue)).To(Succeed())
			Expect(issue.Status.IssueNumber).To(Equal(1))
			Expect(issue.Status.IssueURL).To(Equal("https://github.com/owner/repo/issues/1"))
			Expect(issue.Status.State).To(Equal("open"))
		})
	})

	Context("When syncing an existing GitHubIssue", func() {
		It("should update remote issue when spec drifts", func() {
			createGitHubIssue()

			// Reconcile twice: add finalizer + create issue
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Update the spec (change title)
			var issue issuesv1.GitHubIssue
			Expect(k8sClient.Get(ctx, namespacedName, &issue)).To(Succeed())
			issue.Spec.Title = "Updated Title"
			Expect(k8sClient.Update(ctx, &issue)).To(Succeed())

			// Third reconcile: should sync and update remote
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter.Minutes()).To(Equal(5.0))

			// Verify update was called on the provider
			Expect(mockProvider.UpdateCalled).To(Equal(1))
		})

		It("should reopen a closed issue", func() {
			createGitHubIssue()

			// Reconcile twice: add finalizer + create issue
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Simulate someone closing the issue on GitHub
			remoteIssue := mockProvider.GetIssue(repo, 1)
			Expect(remoteIssue).NotTo(BeNil())
			remoteIssue.State = "closed"

			// Reconcile: should detect closed state and reopen
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Verify issue was reopened
			remoteIssue = mockProvider.GetIssue(repo, 1)
			Expect(remoteIssue.State).To(Equal("open"))
		})

		It("should not update remote when spec is in sync", func() {
			createGitHubIssue()

			// Reconcile twice: add finalizer + create issue
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Third reconcile: no changes needed
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Update should not have been called
			Expect(mockProvider.UpdateCalled).To(Equal(0))
		})
	})

	Context("When deleting a GitHubIssue", func() {
		It("should close the remote issue and remove finalizer", func() {
			createGitHubIssue()

			// Reconcile twice: add finalizer + create issue
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Delete the CR
			var issue issuesv1.GitHubIssue
			Expect(k8sClient.Get(ctx, namespacedName, &issue)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &issue)).To(Succeed())

			// Reconcile: should close remote issue and remove finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Verify close was called
			Expect(mockProvider.CloseCalled).To(Equal(1))

			// Verify the remote issue is closed
			remoteIssue := mockProvider.GetIssue(repo, 1)
			Expect(remoteIssue.State).To(Equal("closed"))

			// Verify finalizer was removed (object should be gone or have no finalizer)
			err = k8sClient.Get(ctx, namespacedName, &issue)
			if err == nil {
				Expect(controllerutil.ContainsFinalizer(&issue, githubIssueFinalizer)).To(BeFalse())
			} else {
				Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be deleted")
			}
		})
	})

	Context("When the Secret is missing", func() {
		It("should return an error", func() {
			// Create GitHubIssue pointing to a non-existent secret
			issue := &issuesv1.GitHubIssue{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: issuesv1.GitHubIssueSpec{
					Repo:           repo,
					Title:          "Test Issue",
					TokenSecretRef: "nonexistent-secret",
				},
			}
			Expect(k8sClient.Create(ctx, issue)).To(Succeed())

			// Reconcile should fail because secret doesn't exist
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("When the CR does not exist", func() {
		It("should not return an error", func() {
			// Reconcile a non-existent resource
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: namespace},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})
})
