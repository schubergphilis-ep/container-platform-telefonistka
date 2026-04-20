package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// GetRef returns a Git reference
func (g *GitLabProvider) GetRef(ctx context.Context, owner, repo, ref string) (*gitprovider.Reference, error) {
	projectPath := g.getProjectPath(owner, repo)

	// Strip "refs/heads/" prefix if present
	ref = strings.TrimPrefix(ref, "refs/heads/")

	branch, resp, err := g.client.Branches.GetBranch(projectPath, ref)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to get ref: %w", err)
	}

	return &gitprovider.Reference{
		Ref: "refs/heads/" + branch.Name,
		SHA: branch.Commit.ID,
	}, nil
}

// CreateRef creates a new Git reference (branch)
func (g *GitLabProvider) CreateRef(ctx context.Context, owner, repo, ref, sha string) (*gitprovider.Reference, error) {
	projectPath := g.getProjectPath(owner, repo)

	// Strip "refs/heads/" prefix if present
	ref = strings.TrimPrefix(ref, "refs/heads/")

	opts := &gitlab.CreateBranchOptions{
		Branch: gitlab.Ptr(ref),
		Ref:    gitlab.Ptr(sha),
	}

	branch, resp, err := g.client.Branches.CreateBranch(projectPath, opts)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to create ref: %w", err)
	}

	return &gitprovider.Reference{
		Ref: "refs/heads/" + branch.Name,
		SHA: branch.Commit.ID,
	}, nil
}

// UpdateRef updates a Git reference to point at a new SHA.
//
// GitLab's REST API does not provide an atomic "update branch pointer" operation.
// For force updates we delete the branch and recreate it at the target SHA.
// This is non-atomic: between delete and create the branch briefly does not exist.
// Callers should be aware that concurrent watchers (CI pipelines, webhooks) may
// observe the deletion as a separate event.
//
// Non-force updates are not supported — use CreateCommit with CommitActions to
// advance a branch by adding commits.
func (g *GitLabProvider) UpdateRef(ctx context.Context, owner, repo, ref, sha string, force bool) (*gitprovider.Reference, error) {
	if !force {
		return nil, &gitprovider.ProviderNotSupportedError{
			Provider:  gitprovider.ProviderTypeGitLab,
			Operation: "UpdateRef (non-force)",
			Message:   "non-force ref update is not supported by the GitLab provider; use CreateCommit with CommitActions to advance a branch",
		}
	}

	projectPath := g.getProjectPath(owner, repo)

	// Strip "refs/heads/" prefix if present
	ref = strings.TrimPrefix(ref, "refs/heads/")

	// Check if branch already points at the target SHA to avoid unnecessary delete+create
	branch, resp, err := g.client.Branches.GetBranch(projectPath, ref)
	g.updateLastResponse(resp)
	if err == nil && branch != nil && branch.Commit.ID == sha {
		return &gitprovider.Reference{
			Ref: "refs/heads/" + branch.Name,
			SHA: branch.Commit.ID,
		}, nil
	}

	// Delete and recreate the branch at the target SHA.
	// WARNING: This is not atomic. If CreateRef fails after DeleteBranch,
	// we attempt to restore the branch at the old SHA.
	oldSHA := branch.Commit.ID

	_, err = g.client.Branches.DeleteBranch(projectPath, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to delete branch %q for force update: %w", ref, err)
	}

	newRef, err := g.CreateRef(ctx, owner, repo, ref, sha)
	if err != nil {
		// Attempt rollback: recreate branch at old SHA
		if _, rollbackErr := g.CreateRef(ctx, owner, repo, ref, oldSHA); rollbackErr != nil {
			return nil, fmt.Errorf("failed to recreate branch %q at %s AND failed to rollback to %s: %v (original error: %w)", ref, sha, oldSHA, rollbackErr, err)
		}
		return nil, fmt.Errorf("failed to recreate branch %q at %s (rolled back to %s): %w", ref, sha, oldSHA, err)
	}

	return newRef, nil
}

// DeleteRef deletes a Git reference (branch)
func (g *GitLabProvider) DeleteRef(ctx context.Context, owner, repo, ref string) error {
	projectPath := g.getProjectPath(owner, repo)

	// Strip "refs/heads/" prefix if present
	ref = strings.TrimPrefix(ref, "refs/heads/")

	resp, err := g.client.Branches.DeleteBranch(projectPath, ref)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to delete ref: %w", err)
	}

	return nil
}

// GetCommit returns a Git commit
func (g *GitLabProvider) GetCommit(ctx context.Context, owner, repo, sha string) (*gitprovider.Commit, error) {
	projectPath := g.getProjectPath(owner, repo)

	commit, resp, err := g.client.Commits.GetCommit(projectPath, sha, nil)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	var commitDate time.Time
	if commit.AuthoredDate != nil {
		commitDate = *commit.AuthoredDate
	}

	return &gitprovider.Commit{
		SHA:       commit.ID,
		Message:   commit.Message,
		Author:    commit.AuthorName,
		Date:      commitDate,
		Parents:   commit.ParentIDs,
		HTMLURL:   commit.WebURL,
		Committer: commit.CommitterName,
	}, nil
}

// CreateCommit creates a new Git commit using GitLab's Commits API with actions
// This is the CRITICAL method that solves the tree API challenge
func (g *GitLabProvider) CreateCommit(ctx context.Context, owner, repo string, opts *gitprovider.CommitOptions) (*gitprovider.Commit, error) {
	projectPath := g.getProjectPath(owner, repo)

	var actions []*gitlab.CommitActionOptions

	// GitLab uses CommitActions — the TreeEntries (GitHub-style tree API) is not supported.
	if len(opts.CommitActions) == 0 {
		return nil, fmt.Errorf("CommitActions must be provided for GitLab commits (tree API is not supported)")
	}

	for _, action := range opts.CommitActions {
		glAction := &gitlab.CommitActionOptions{
			Action:   gitlab.Ptr(gitlab.FileActionValue(action.Action)),
			FilePath: gitlab.Ptr(action.FilePath),
		}

		if action.Action != "delete" {
			glAction.Content = gitlab.Ptr(action.Content)
		}

		if action.Encoding != "" {
			glAction.Encoding = gitlab.Ptr(action.Encoding)
		}

		if action.PreviousPath != "" {
			glAction.PreviousPath = gitlab.Ptr(action.PreviousPath)
		}

		actions = append(actions, glAction)
	}

	branch := opts.Branch
	if branch == "" {
		return nil, fmt.Errorf("branch must be specified for GitLab commits")
	}

	commitOpts := &gitlab.CreateCommitOptions{
		Branch:        gitlab.Ptr(branch),
		CommitMessage: gitlab.Ptr(opts.Message),
		Actions:       actions,
	}

	if opts.Author != nil {
		commitOpts.AuthorName = gitlab.Ptr(opts.Author.Name)
		commitOpts.AuthorEmail = gitlab.Ptr(opts.Author.Email)
	}

	commit, resp, err := g.client.Commits.CreateCommit(projectPath, commitOpts)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to create commit: %w", err)
	}

	var createdCommitDate time.Time
	if commit.AuthoredDate != nil {
		createdCommitDate = *commit.AuthoredDate
	}

	return &gitprovider.Commit{
		SHA:       commit.ID,
		Message:   commit.Message,
		Author:    commit.AuthorName,
		Date:      createdCommitDate,
		Parents:   commit.ParentIDs,
		HTMLURL:   commit.WebURL,
		Committer: commit.CommitterName,
	}, nil
}

// CompareCommits compares two commits
func (g *GitLabProvider) CompareCommits(ctx context.Context, owner, repo, base, head string) (*gitprovider.DiffResult, error) {
	projectPath := g.getProjectPath(owner, repo)

	compare, resp, err := g.client.Repositories.Compare(projectPath, &gitlab.CompareOptions{
		From: gitlab.Ptr(base),
		To:   gitlab.Ptr(head),
	})
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to compare commits: %w", err)
	}

	var files []*gitprovider.CommitFile
	for _, diff := range compare.Diffs {
		status := "modified"
		if diff.NewFile {
			status = "added"
		} else if diff.DeletedFile {
			status = "removed"
		} else if diff.RenamedFile {
			status = "renamed"
		}

		adds, dels := countDiffStats(diff.Diff)

		files = append(files, &gitprovider.CommitFile{
			Filename:  diff.NewPath,
			Status:    status,
			Additions: adds,
			Deletions: dels,
			Changes:   adds + dels,
			Patch:     diff.Diff,
		})
	}

	return &gitprovider.DiffResult{
		FromSHA: base,
		ToSHA:   head,
		Files:   files,
		Changes: len(files),
	}, nil
}

// CreateBranch creates a new branch
func (g *GitLabProvider) CreateBranch(ctx context.Context, owner, repo, branch, sha string) (*gitprovider.Reference, error) {
	return g.CreateRef(ctx, owner, repo, branch, sha)
}

// GetBranch returns a branch
func (g *GitLabProvider) GetBranch(ctx context.Context, owner, repo, branch string) (*gitprovider.Reference, error) {
	return g.GetRef(ctx, owner, repo, branch)
}

// DeleteBranch deletes a branch
func (g *GitLabProvider) DeleteBranch(ctx context.Context, owner, repo, branch string) error {
	return g.DeleteRef(ctx, owner, repo, branch)
}
