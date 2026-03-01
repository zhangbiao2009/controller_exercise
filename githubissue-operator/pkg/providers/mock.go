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
	"sync"
)

// MockProvider implements IssueProvider for testing
type MockProvider struct {
	mu           sync.RWMutex
	issues       map[string]*Issue // key: "repo#number"
	nextNumber   int
	CreateFunc   func(ctx context.Context, token string, input CreateIssueInput) (*Issue, error)
	GetFunc      func(ctx context.Context, token string, repo string, issueNumber int) (*Issue, error)
	UpdateFunc   func(ctx context.Context, token string, repo string, issueNumber int, input UpdateIssueInput) (*Issue, error)
	CloseFunc    func(ctx context.Context, token string, repo string, issueNumber int) error
	CreateCalled int
	GetCalled    int
	UpdateCalled int
	CloseCalled  int
}

// NewMockProvider creates a new MockProvider
func NewMockProvider() *MockProvider {
	return &MockProvider{
		issues:     make(map[string]*Issue),
		nextNumber: 1,
	}
}

func issueKey(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// Create creates a mock issue
func (m *MockProvider) Create(ctx context.Context, token string, input CreateIssueInput) (*Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CreateCalled++

	if m.CreateFunc != nil {
		return m.CreateFunc(ctx, token, input)
	}

	issue := &Issue{
		Number: m.nextNumber,
		URL:    fmt.Sprintf("https://github.com/%s/issues/%d", input.Repo, m.nextNumber),
		State:  "open",
		Title:  input.Title,
		Body:   input.Body,
		Labels: input.Labels,
	}
	m.issues[issueKey(input.Repo, m.nextNumber)] = issue
	m.nextNumber++

	return issue, nil
}

// Get retrieves a mock issue
func (m *MockProvider) Get(ctx context.Context, token string, repo string, issueNumber int) (*Issue, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.GetCalled++

	if m.GetFunc != nil {
		return m.GetFunc(ctx, token, repo, issueNumber)
	}

	issue, ok := m.issues[issueKey(repo, issueNumber)]
	if !ok {
		return nil, fmt.Errorf("issue not found: %s#%d", repo, issueNumber)
	}
	return issue, nil
}

// Update updates a mock issue
func (m *MockProvider) Update(ctx context.Context, token string, repo string, issueNumber int, input UpdateIssueInput) (*Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateCalled++

	if m.UpdateFunc != nil {
		return m.UpdateFunc(ctx, token, repo, issueNumber, input)
	}

	key := issueKey(repo, issueNumber)
	issue, ok := m.issues[key]
	if !ok {
		return nil, fmt.Errorf("issue not found: %s#%d", repo, issueNumber)
	}

	if input.Title != "" {
		issue.Title = input.Title
	}
	if input.Body != "" {
		issue.Body = input.Body
	}
	if input.Labels != nil {
		issue.Labels = input.Labels
	}

	return issue, nil
}

// Close closes a mock issue
func (m *MockProvider) Close(ctx context.Context, token string, repo string, issueNumber int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CloseCalled++

	if m.CloseFunc != nil {
		return m.CloseFunc(ctx, token, repo, issueNumber)
	}

	key := issueKey(repo, issueNumber)
	issue, ok := m.issues[key]
	if !ok {
		return fmt.Errorf("issue not found: %s#%d", repo, issueNumber)
	}

	issue.State = "closed"
	return nil
}

// Reopen reopens a mock issue
func (m *MockProvider) Reopen(ctx context.Context, token string, repo string, issueNumber int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := issueKey(repo, issueNumber)
	issue, ok := m.issues[key]
	if !ok {
		return fmt.Errorf("issue not found: %s#%d", repo, issueNumber)
	}

	issue.State = "open"
	return nil
}

// Reset clears all mock state
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issues = make(map[string]*Issue)
	m.nextNumber = 1
	m.CreateCalled = 0
	m.GetCalled = 0
	m.UpdateCalled = 0
	m.CloseCalled = 0
}

// GetIssue returns a stored issue for inspection in tests
func (m *MockProvider) GetIssue(repo string, number int) *Issue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.issues[issueKey(repo, number)]
}
