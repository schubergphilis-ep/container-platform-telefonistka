package testutils

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	log "github.com/sirupsen/logrus"
)

// MockProvider implements gitprovider.GitProvider for unit tests.
// Populate Directories, Files, and response maps to control behaviour.
// Use Errors map to inject errors into specific method calls.
// Call counts are tracked in the Calls map (thread-safe).
type MockProvider struct {
	// Directories maps "path@ref" → list of FileNodes returned by GetDirectoryContent
	Directories map[string][]*gitprovider.FileNode
	// Files maps "path@ref" → file content returned by GetFileContent
	Files map[string][]byte
	// Errors maps method names to errors that should be returned.
	Errors map[string]error

	// --- Configurable return values ---

	// DefaultBranch returned by GetDefaultBranch (defaults to "main")
	DefaultBranchName string
	// Ref returned by GetRef (defaults to a stub)
	RefResponse *gitprovider.Reference
	// Branch returned by GetBranch; if nil, GetBranch returns an error (branch not found)
	BranchResponse *gitprovider.Reference
	// PR returned by CreatePullRequest
	CreatedPR *gitprovider.PullRequest
	// PR returned by GetPullRequest
	PullRequestResponse *gitprovider.PullRequest
	// CommitFiles returned by ListPullRequestFiles
	PullRequestFiles []*gitprovider.CommitFile
	// Commit returned by CreateCommit
	CommitResponse *gitprovider.Commit
	// DiffResponse returned by CompareCommits; if nil, an empty diff is returned.
	DiffResponse *gitprovider.DiffResult
	// Review returned by ApprovePullRequest
	ApprovalResponse *gitprovider.Review

	// --- Call tracking ---

	mu    sync.Mutex
	Calls map[string]int // method name → call count
	// LastCreatePR stores the most recent CreatePullRequest argument
	LastCreatePR *gitprovider.NewPullRequest
	// LastCommitOpts stores the most recent CreateCommit options
	LastCommitOpts *gitprovider.CommitOptions
	// LastComment stores the most recent CommentOnPullRequest body
	LastComment string
	// DeletedBranches tracks branch names passed to DeleteBranch
	DeletedBranches []string
	// StatusUpdates tracks SetCommitStatus calls as "sha:state"
	StatusUpdates []string
}

func (m *MockProvider) trackCall(method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Calls == nil {
		m.Calls = make(map[string]int)
	}
	m.Calls[method]++
}

// CallCount returns the number of times a method was called.
func (m *MockProvider) CallCount(method string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Calls == nil {
		return 0
	}
	return m.Calls[method]
}

// mockError returns the injected error for a method, or nil if none configured.
func (m *MockProvider) mockError(method string) error {
	if m.Errors == nil {
		return nil
	}
	return m.Errors[method]
}

func (m *MockProvider) Type() gitprovider.ProviderType { return "mock" }

func (m *MockProvider) GetBotIdentity(ctx context.Context) (*gitprovider.User, error) {
	m.trackCall("GetBotIdentity")
	return &gitprovider.User{Login: "telefonistka-bot"}, nil
}

func (m *MockProvider) GetRepository(ctx context.Context, owner, repo string) (*gitprovider.Repository, error) {
	m.trackCall("GetRepository")
	return &gitprovider.Repository{
		Name:          repo,
		FullName:      owner + "/" + repo,
		Owner:         owner,
		DefaultBranch: m.getDefaultBranch(),
	}, nil
}

func (m *MockProvider) getDefaultBranch() string {
	if m.DefaultBranchName != "" {
		return m.DefaultBranchName
	}
	return "main"
}

func (m *MockProvider) GetDefaultBranch(ctx context.Context, owner, repo string) (string, error) {
	m.trackCall("GetDefaultBranch")
	if err := m.mockError("GetDefaultBranch"); err != nil {
		return "", err
	}
	return m.getDefaultBranch(), nil
}

func (m *MockProvider) GetFileContent(ctx context.Context, owner, repo, path, ref string) ([]byte, error) {
	m.trackCall("GetFileContent")
	if err := m.mockError("GetFileContent"); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s@%s", path, ref)
	content, ok := m.Files[key]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", key)
	}
	return content, nil
}

func (m *MockProvider) GetDirectoryContent(ctx context.Context, owner, repo, path, ref string) ([]*gitprovider.FileNode, error) {
	m.trackCall("GetDirectoryContent")
	if err := m.mockError("GetDirectoryContent"); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s@%s", path, ref)
	nodes, ok := m.Directories[key]
	if !ok {
		return nil, fmt.Errorf("directory not found: %s", key)
	}
	return nodes, nil
}

func (m *MockProvider) GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitprovider.PullRequest, error) {
	m.trackCall("GetPullRequest")
	if err := m.mockError("GetPullRequest"); err != nil {
		return nil, err
	}
	if m.PullRequestResponse != nil {
		return m.PullRequestResponse, nil
	}
	return &gitprovider.PullRequest{Number: number, State: "open"}, nil
}

func (m *MockProvider) CreatePullRequest(ctx context.Context, owner, repo string, pr *gitprovider.NewPullRequest) (*gitprovider.PullRequest, error) {
	m.trackCall("CreatePullRequest")
	if err := m.mockError("CreatePullRequest"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.LastCreatePR = pr
	m.mu.Unlock()
	if m.CreatedPR != nil {
		return m.CreatedPR, nil
	}
	return &gitprovider.PullRequest{
		Number:  100,
		Title:   pr.Title,
		Body:    pr.Body,
		HeadRef: pr.Head,
		BaseRef: pr.Base,
		Labels:  pr.Labels,
		State:   "open",
		HTMLURL: fmt.Sprintf("https://example.com/%s/%s/pull/100", owner, repo),
	}, nil
}

func (m *MockProvider) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]*gitprovider.CommitFile, error) {
	m.trackCall("ListPullRequestFiles")
	if err := m.mockError("ListPullRequestFiles"); err != nil {
		return nil, err
	}
	return m.PullRequestFiles, nil
}

func (m *MockProvider) MergePullRequest(ctx context.Context, owner, repo string, number int, options *gitprovider.MergeOptions) error {
	m.trackCall("MergePullRequest")
	return m.mockError("MergePullRequest")
}

func (m *MockProvider) CommentOnPullRequest(ctx context.Context, owner, repo string, number int, body string) (*gitprovider.Comment, error) {
	m.trackCall("CommentOnPullRequest")
	m.mu.Lock()
	m.LastComment = body
	m.mu.Unlock()
	return &gitprovider.Comment{ID: 1, Body: body}, nil
}

func (m *MockProvider) ListPullRequestComments(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Comment, error) {
	m.trackCall("ListPullRequestComments")
	return nil, nil
}

func (m *MockProvider) ApprovePullRequest(ctx context.Context, owner, repo string, number int) (*gitprovider.Review, error) {
	m.trackCall("ApprovePullRequest")
	if err := m.mockError("ApprovePullRequest"); err != nil {
		return nil, err
	}
	if m.ApprovalResponse != nil {
		return m.ApprovalResponse, nil
	}
	return &gitprovider.Review{ID: 1, State: gitprovider.ReviewStateApproved}, nil
}

func (m *MockProvider) ListPullRequestReviews(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Review, error) {
	m.trackCall("ListPullRequestReviews")
	return nil, nil
}

func (m *MockProvider) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) error {
	m.trackCall("AddLabels")
	return nil
}

func (m *MockProvider) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error {
	m.trackCall("RemoveLabel")
	return nil
}

func (m *MockProvider) GetLabels(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Label, error) {
	m.trackCall("GetLabels")
	return nil, nil
}

func (m *MockProvider) AddAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	m.trackCall("AddAssignees")
	return nil
}

func (m *MockProvider) RemoveAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	m.trackCall("RemoveAssignees")
	return nil
}

func (m *MockProvider) GetRef(ctx context.Context, owner, repo, ref string) (*gitprovider.Reference, error) {
	m.trackCall("GetRef")
	if err := m.mockError("GetRef"); err != nil {
		return nil, err
	}
	if m.RefResponse != nil {
		return m.RefResponse, nil
	}
	return &gitprovider.Reference{Ref: ref, SHA: "abc123def456"}, nil
}

func (m *MockProvider) CreateRef(ctx context.Context, owner, repo, ref, sha string) (*gitprovider.Reference, error) {
	m.trackCall("CreateRef")
	return &gitprovider.Reference{Ref: ref, SHA: sha}, nil
}

func (m *MockProvider) UpdateRef(ctx context.Context, owner, repo, ref, sha string, force bool) (*gitprovider.Reference, error) {
	m.trackCall("UpdateRef")
	return &gitprovider.Reference{Ref: ref, SHA: sha}, nil
}

func (m *MockProvider) DeleteRef(ctx context.Context, owner, repo, ref string) error {
	m.trackCall("DeleteRef")
	return nil
}

func (m *MockProvider) GetCommit(ctx context.Context, owner, repo, sha string) (*gitprovider.Commit, error) {
	m.trackCall("GetCommit")
	return &gitprovider.Commit{SHA: sha, Message: "mock commit"}, nil
}

func (m *MockProvider) CreateCommit(ctx context.Context, owner, repo string, opts *gitprovider.CommitOptions) (*gitprovider.Commit, error) {
	m.trackCall("CreateCommit")
	if err := m.mockError("CreateCommit"); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.LastCommitOpts = opts
	m.mu.Unlock()
	if m.CommitResponse != nil {
		return m.CommitResponse, nil
	}
	return &gitprovider.Commit{SHA: "newcommitsha", Message: opts.Message}, nil
}

func (m *MockProvider) CompareCommits(ctx context.Context, owner, repo, base, head string) (*gitprovider.DiffResult, error) {
	m.trackCall("CompareCommits")
	if err := m.mockError("CompareCommits"); err != nil {
		return nil, err
	}
	if m.DiffResponse != nil {
		return m.DiffResponse, nil
	}
	return &gitprovider.DiffResult{FromSHA: base, ToSHA: head}, nil
}

func (m *MockProvider) CreateBranch(ctx context.Context, owner, repo, branch, sha string) (*gitprovider.Reference, error) {
	m.trackCall("CreateBranch")
	if err := m.mockError("CreateBranch"); err != nil {
		return nil, err
	}
	return &gitprovider.Reference{Ref: "refs/heads/" + branch, SHA: sha}, nil
}

func (m *MockProvider) GetBranch(ctx context.Context, owner, repo, branch string) (*gitprovider.Reference, error) {
	m.trackCall("GetBranch")
	if err := m.mockError("GetBranch"); err != nil {
		return nil, err
	}
	if m.BranchResponse != nil {
		return m.BranchResponse, nil
	}
	// Default: branch does not exist
	return nil, fmt.Errorf("branch not found: %s", branch)
}

func (m *MockProvider) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	m.trackCall("DeleteBranch")
	if err := m.mockError("DeleteBranch"); err != nil {
		return err
	}
	m.mu.Lock()
	m.DeletedBranches = append(m.DeletedBranches, branch)
	m.mu.Unlock()
	return nil
}

func (m *MockProvider) SetCommitStatus(ctx context.Context, owner, repo, sha string, status *gitprovider.Status) error {
	m.trackCall("SetCommitStatus")
	m.mu.Lock()
	m.StatusUpdates = append(m.StatusUpdates, sha+":"+status.State)
	m.mu.Unlock()
	return nil
}

func (m *MockProvider) GetCommitStatus(ctx context.Context, owner, repo, sha string) ([]*gitprovider.Status, error) {
	m.trackCall("GetCommitStatus")
	return nil, nil
}

func (m *MockProvider) GetCombinedStatus(ctx context.Context, owner, repo, sha string) (string, error) {
	m.trackCall("GetCombinedStatus")
	return "success", nil
}

func (m *MockProvider) ParseWebhook(req *http.Request, secret []byte) (gitprovider.Event, error) {
	return nil, nil
}

func (m *MockProvider) ValidateWebhookSignature(req *http.Request, secret []byte) error { return nil }
func (m *MockProvider) SupportsGraphQL() bool                                           { return false }
func (m *MockProvider) GetAPIResponse() *gitprovider.APIResponse                        { return nil }

// TestLogger returns a logrus entry suitable for test use.
func TestLogger() *log.Entry {
	return log.WithFields(log.Fields{"test": true})
}
