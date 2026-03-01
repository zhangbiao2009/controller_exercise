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

package providers

import "context"

// Issue represents a remote issue from any provider
type Issue struct {
	// Number is the issue number/ID
	Number int
	// URL is the web URL to view the issue
	URL string
	// State is the current state: "open" or "closed"
	State string
	// Title is the issue title
	Title string
	// Body is the issue description
	Body string
	// Labels are the labels applied to the issue
	Labels []string
}

// CreateIssueInput contains the data needed to create an issue
type CreateIssueInput struct {
	// Repo in format "owner/repo"
	Repo string
	// Title of the issue
	Title string
	// Body/description of the issue
	Body string
	// Labels to apply
	Labels []string
}

// UpdateIssueInput contains the data needed to update an issue
type UpdateIssueInput struct {
	// Title of the issue (optional, empty means no change)
	Title string
	// Body/description of the issue (optional, empty means no change)
	Body string
	// Labels to apply (nil means no change, empty slice clears labels)
	Labels []string
}

// IssueProvider defines the interface for managing remote issues
type IssueProvider interface {
	// Create creates a new issue and returns the created issue details
	Create(ctx context.Context, token string, input CreateIssueInput) (*Issue, error)

	// Get retrieves an existing issue by repo and issue number
	Get(ctx context.Context, token string, repo string, issueNumber int) (*Issue, error)

	// Update updates an existing issue
	Update(ctx context.Context, token string, repo string, issueNumber int, input UpdateIssueInput) (*Issue, error)

	// Close closes an issue
	Close(ctx context.Context, token string, repo string, issueNumber int) error

	// Reopen reopens a closed issue
	Reopen(ctx context.Context, token string, repo string, issueNumber int) error
}
