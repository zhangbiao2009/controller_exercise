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

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v57/github"
	"golang.org/x/oauth2"
)

// GitHubProvider implements IssueProvider for GitHub
type GitHubProvider struct{}

// NewGitHubProvider creates a new GitHubProvider
func NewGitHubProvider() *GitHubProvider {
	return &GitHubProvider{}
}

// newClient creates an authenticated GitHub client
func (p *GitHubProvider) newClient(ctx context.Context, token string) *github.Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}

// parseRepo splits "owner/repo" into owner and repo parts
func parseRepo(repo string) (owner, repoName string, err error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repo format %q, expected 'owner/repo'", repo)
	}
	return parts[0], parts[1], nil
}

// Create creates a new GitHub issue
func (p *GitHubProvider) Create(ctx context.Context, token string, input CreateIssueInput) (*Issue, error) {
	owner, repo, err := parseRepo(input.Repo)
	if err != nil {
		return nil, err
	}

	client := p.newClient(ctx, token)

	issueRequest := &github.IssueRequest{
		Title: github.String(input.Title),
		Body:  github.String(input.Body),
	}
	if len(input.Labels) > 0 {
		issueRequest.Labels = &input.Labels
	}

	ghIssue, _, err := client.Issues.Create(ctx, owner, repo, issueRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub issue: %w", err)
	}

	return &Issue{
		Number: ghIssue.GetNumber(),
		URL:    ghIssue.GetHTMLURL(),
		State:  ghIssue.GetState(),
		Title:  ghIssue.GetTitle(),
		Body:   ghIssue.GetBody(),
		Labels: extractLabels(ghIssue.Labels),
	}, nil
}

// Get retrieves an existing GitHub issue
func (p *GitHubProvider) Get(ctx context.Context, token string, repoStr string, issueNumber int) (*Issue, error) {
	owner, repo, err := parseRepo(repoStr)
	if err != nil {
		return nil, err
	}

	client := p.newClient(ctx, token)

	ghIssue, _, err := client.Issues.Get(ctx, owner, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub issue: %w", err)
	}

	return &Issue{
		Number: ghIssue.GetNumber(),
		URL:    ghIssue.GetHTMLURL(),
		State:  ghIssue.GetState(),
		Title:  ghIssue.GetTitle(),
		Body:   ghIssue.GetBody(),
		Labels: extractLabels(ghIssue.Labels),
	}, nil
}

// Update updates an existing GitHub issue
func (p *GitHubProvider) Update(ctx context.Context, token string, repoStr string, issueNumber int, input UpdateIssueInput) (*Issue, error) {
	owner, repo, err := parseRepo(repoStr)
	if err != nil {
		return nil, err
	}

	client := p.newClient(ctx, token)

	issueRequest := &github.IssueRequest{}
	if input.Title != "" {
		issueRequest.Title = github.String(input.Title)
	}
	if input.Body != "" {
		issueRequest.Body = github.String(input.Body)
	}
	if input.Labels != nil {
		issueRequest.Labels = &input.Labels
	}

	ghIssue, _, err := client.Issues.Edit(ctx, owner, repo, issueNumber, issueRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to update GitHub issue: %w", err)
	}

	return &Issue{
		Number: ghIssue.GetNumber(),
		URL:    ghIssue.GetHTMLURL(),
		State:  ghIssue.GetState(),
		Title:  ghIssue.GetTitle(),
		Body:   ghIssue.GetBody(),
		Labels: extractLabels(ghIssue.Labels),
	}, nil
}

// Close closes a GitHub issue
func (p *GitHubProvider) Close(ctx context.Context, token string, repoStr string, issueNumber int) error {
	owner, repo, err := parseRepo(repoStr)
	if err != nil {
		return err
	}

	client := p.newClient(ctx, token)

	state := "closed"
	_, _, err = client.Issues.Edit(ctx, owner, repo, issueNumber, &github.IssueRequest{
		State: &state,
	})
	if err != nil {
		return fmt.Errorf("failed to close GitHub issue: %w", err)
	}

	return nil
}

// Reopen reopens a closed GitHub issue
func (p *GitHubProvider) Reopen(ctx context.Context, token string, repoStr string, issueNumber int) error {
	owner, repo, err := parseRepo(repoStr)
	if err != nil {
		return err
	}

	client := p.newClient(ctx, token)

	state := "open"
	_, _, err = client.Issues.Edit(ctx, owner, repo, issueNumber, &github.IssueRequest{
		State: &state,
	})
	if err != nil {
		return fmt.Errorf("failed to reopen GitHub issue: %w", err)
	}

	return nil
}

// extractLabels extracts label names from GitHub label objects
func extractLabels(labels []*github.Label) []string {
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		if label.Name != nil {
			result = append(result, *label.Name)
		}
	}
	return result
}
