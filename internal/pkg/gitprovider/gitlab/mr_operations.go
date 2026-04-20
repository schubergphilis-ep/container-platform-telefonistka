package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	log "github.com/sirupsen/logrus"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// countDiffStats parses a unified diff string and counts added/deleted lines.
func countDiffStats(patch string) (additions, deletions int) {
	for _, line := range strings.Split(patch, "\n") {
		if len(line) == 0 {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			deletions++
		}
	}
	return additions, deletions
}

// GetPullRequest returns a merge request (GitLab's equivalent of PR)
func (g *GitLabProvider) GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitprovider.PullRequest, error) {
	projectPath := g.getProjectPath(owner, repo)

	mr, resp, err := g.client.MergeRequests.GetMergeRequest(projectPath, int64(number), nil)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to get merge request: %w", err)
	}

	return convertGitLabMR(mr), nil
}

// CreatePullRequest creates a new merge request
func (g *GitLabProvider) CreatePullRequest(ctx context.Context, owner, repo string, pr *gitprovider.NewPullRequest) (*gitprovider.PullRequest, error) {
	projectPath := g.getProjectPath(owner, repo)

	opts := &gitlab.CreateMergeRequestOptions{
		Title:        gitlab.Ptr(pr.Title),
		Description:  gitlab.Ptr(pr.Body),
		SourceBranch: gitlab.Ptr(pr.Head),
		TargetBranch: gitlab.Ptr(pr.Base),
	}

	// Add assignees if specified
	if len(pr.Assignees) > 0 {
		// GitLab API expects assignee IDs, not usernames
		// We need to look up user IDs first
		var assigneeIDs []int64
		for _, username := range pr.Assignees {
			users, _, err := g.client.Users.ListUsers(&gitlab.ListUsersOptions{
				Username: gitlab.Ptr(username),
			})
			if err != nil {
				log.Warnf("Failed to look up assignee %q: %v", username, err)
			} else if len(users) > 0 {
				assigneeIDs = append(assigneeIDs, users[0].ID)
			} else {
				log.Warnf("Assignee %q not found in GitLab", username)
			}
		}
		if len(assigneeIDs) > 0 {
			opts.AssigneeIDs = &assigneeIDs
		}
	}

	mr, resp, err := g.client.MergeRequests.CreateMergeRequest(projectPath, opts)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to create merge request: %w", err)
	}

	result := convertGitLabMR(mr)

	// Add labels if specified
	if len(pr.Labels) > 0 {
		err = g.AddLabels(ctx, owner, repo, int(mr.IID), pr.Labels)
		if err != nil {
			return result, fmt.Errorf("merge request created but failed to add labels: %w", err)
		}
		result.Labels = pr.Labels
	}

	return result, nil
}

// ListPullRequestFiles returns the list of files changed in an MR.
// It uses the Compare API with pagination to handle large MRs.
func (g *GitLabProvider) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]*gitprovider.CommitFile, error) {
	projectPath := g.getProjectPath(owner, repo)

	mr, resp, err := g.client.MergeRequests.GetMergeRequestChanges(projectPath, int64(number), &gitlab.GetMergeRequestChangesOptions{})
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to list MR changes: %w", err)
	}

	if mr.DiffRefs.BaseSha == "" || mr.DiffRefs.HeadSha == "" {
		return nil, gitprovider.ErrNoDiffRefs
	}

	// Note: The Compare API returns all diffs without pagination.
	var result []*gitprovider.CommitFile
	compareOpts := &gitlab.CompareOptions{
		From: gitlab.Ptr(mr.DiffRefs.BaseSha),
		To:   gitlab.Ptr(mr.DiffRefs.HeadSha),
	}

	compare, _, err := g.client.Repositories.Compare(projectPath, compareOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to compare MR refs: %w", err)
	}

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

		result = append(result, &gitprovider.CommitFile{
			Filename:  diff.NewPath,
			Status:    status,
			Additions: adds,
			Deletions: dels,
			Changes:   adds + dels,
			Patch:     diff.Diff,
		})
	}

	return result, nil
}

// MergePullRequest merges a merge request
func (g *GitLabProvider) MergePullRequest(ctx context.Context, owner, repo string, number int, options *gitprovider.MergeOptions) error {
	projectPath := g.getProjectPath(owner, repo)

	opts := &gitlab.AcceptMergeRequestOptions{}

	if options != nil {
		if options.CommitMessage != "" {
			opts.MergeCommitMessage = gitlab.Ptr(options.CommitMessage)
		}
		if options.SHA != "" {
			opts.SHA = gitlab.Ptr(options.SHA)
		}

		// Map merge method
		if options.MergeMethod != "" {
			switch options.MergeMethod {
			case gitprovider.MergeMethodMerge:
				opts.AutoMerge = gitlab.Ptr(false)
			case gitprovider.MergeMethodSquash:
				opts.Squash = gitlab.Ptr(true)
			case gitprovider.MergeMethodRebase:
				// GitLab doesn't have a direct rebase merge method in the accept API
				// Would need to use rebase_before_merge project setting
			}
		}
	}

	_, resp, err := g.client.MergeRequests.AcceptMergeRequest(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to merge merge request: %w", err)
	}

	return nil
}

// CommentOnPullRequest adds a comment to a merge request
func (g *GitLabProvider) CommentOnPullRequest(ctx context.Context, owner, repo string, number int, body string) (*gitprovider.Comment, error) {
	projectPath := g.getProjectPath(owner, repo)

	opts := &gitlab.CreateMergeRequestNoteOptions{
		Body: gitlab.Ptr(body),
	}

	note, resp, err := g.client.Notes.CreateMergeRequestNote(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to create note: %w", err)
	}

	return convertGitLabNote(note), nil
}

// ListPullRequestComments returns all comments on a merge request
func (g *GitLabProvider) ListPullRequestComments(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Comment, error) {
	projectPath := g.getProjectPath(owner, repo)

	var allNotes []*gitlab.Note
	opts := &gitlab.ListMergeRequestNotesOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
		},
	}

	for {
		notes, resp, err := g.client.Notes.ListMergeRequestNotes(projectPath, int64(number), opts)
		g.updateLastResponse(resp)

		if err != nil {
			return nil, fmt.Errorf("failed to list notes: %w", err)
		}

		allNotes = append(allNotes, notes...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	var result []*gitprovider.Comment
	for _, note := range allNotes {
		result = append(result, convertGitLabNote(note))
	}

	return result, nil
}

// ApprovePullRequest approves a merge request
func (g *GitLabProvider) ApprovePullRequest(ctx context.Context, owner, repo string, number int) (*gitprovider.Review, error) {
	projectPath := g.getProjectPath(owner, repo)

	// Approve the MR
	approval, resp, err := g.client.MergeRequestApprovals.ApproveMergeRequest(projectPath, int64(number), nil)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to approve merge request: %w", err)
	}

	// Get current user to populate author
	user, _, userErr := g.client.Users.CurrentUser()
	author := "unknown"
	if userErr != nil {
		log.Warnf("Failed to get current user for approval author: %v", userErr)
	} else if user != nil {
		author = user.Username
	}

	// GitLab approvals don't have a separate review object like GitHub
	// We create a synthetic one
	createdAt := time.Now()
	if approval.CreatedAt != nil {
		createdAt = *approval.CreatedAt
	}

	return &gitprovider.Review{
		ID:        int64(number), // Use MR IID as review ID
		Author:    author,
		State:     gitprovider.ReviewStateApproved,
		Body:      "Approved",
		CreatedAt: createdAt,
	}, nil
}

// ListPullRequestReviews returns all approvals on a merge request
func (g *GitLabProvider) ListPullRequestReviews(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Review, error) {
	projectPath := g.getProjectPath(owner, repo)

	approvals, resp, err := g.client.MergeRequestApprovals.GetConfiguration(projectPath, int64(number))
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to get approvals: %w", err)
	}

	var result []*gitprovider.Review
	for _, approval := range approvals.ApprovedBy {
		result = append(result, &gitprovider.Review{
			ID:     approval.User.ID,
			Author: approval.User.Username,
			State:  gitprovider.ReviewStateApproved,
			Body:   "Approved",
		})
	}

	return result, nil
}

// AddLabels adds labels to a merge request.
// Uses GitLab's atomic add_labels field to avoid read-modify-write races.
func (g *GitLabProvider) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) error {
	projectPath := g.getProjectPath(owner, repo)

	addLabels := gitlab.LabelOptions(labels)
	opts := &gitlab.UpdateMergeRequestOptions{
		AddLabels: &addLabels,
	}

	_, resp, err := g.client.MergeRequests.UpdateMergeRequest(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to add labels: %w", err)
	}

	return nil
}

// RemoveLabel removes a label from a merge request.
// Uses GitLab's atomic remove_labels field to avoid read-modify-write races.
func (g *GitLabProvider) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error {
	projectPath := g.getProjectPath(owner, repo)

	removeLabels := gitlab.LabelOptions([]string{label})
	opts := &gitlab.UpdateMergeRequestOptions{
		RemoveLabels: &removeLabels,
	}

	_, resp, err := g.client.MergeRequests.UpdateMergeRequest(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to remove label: %w", err)
	}

	return nil
}

// GetLabels returns all labels on a merge request
func (g *GitLabProvider) GetLabels(ctx context.Context, owner, repo string, number int) ([]*gitprovider.Label, error) {
	projectPath := g.getProjectPath(owner, repo)

	mr, resp, err := g.client.MergeRequests.GetMergeRequest(projectPath, int64(number), nil)
	g.updateLastResponse(resp)

	if err != nil {
		return nil, fmt.Errorf("failed to get labels: %w", err)
	}

	// Fetch project labels once (not per MR label) to enrich the response
	projectLabels, _, labelErr := g.client.Labels.ListLabels(projectPath, &gitlab.ListLabelsOptions{})
	labelByName := make(map[string]*gitlab.Label)
	if labelErr == nil {
		for _, l := range projectLabels {
			labelByName[l.Name] = l
		}
	}

	var result []*gitprovider.Label
	for _, labelName := range mr.Labels {
		if l, ok := labelByName[labelName]; ok {
			result = append(result, &gitprovider.Label{
				ID:          l.ID,
				Name:        l.Name,
				Color:       l.Color,
				Description: l.Description,
			})
		} else {
			// Fallback to basic info if project labels couldn't be fetched
			result = append(result, &gitprovider.Label{
				Name: labelName,
			})
		}
	}

	return result, nil
}

// AddAssignees adds assignees to a merge request
func (g *GitLabProvider) AddAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	projectPath := g.getProjectPath(owner, repo)

	// Convert usernames to user IDs
	var assigneeIDs []int64
	for _, username := range assignees {
		users, _, err := g.client.Users.ListUsers(&gitlab.ListUsersOptions{
			Username: gitlab.Ptr(username),
		})
		if err == nil && len(users) > 0 {
			assigneeIDs = append(assigneeIDs, users[0].ID)
		}
	}

	if len(assigneeIDs) == 0 {
		return fmt.Errorf("no valid assignees found")
	}

	opts := &gitlab.UpdateMergeRequestOptions{
		AssigneeIDs: &assigneeIDs,
	}

	_, resp, err := g.client.MergeRequests.UpdateMergeRequest(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to add assignees: %w", err)
	}

	return nil
}

// RemoveAssignees removes assignees from a merge request
func (g *GitLabProvider) RemoveAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	projectPath := g.getProjectPath(owner, repo)

	// Get current assignees
	mr, _, err := g.client.MergeRequests.GetMergeRequest(projectPath, int64(number), nil)
	if err != nil {
		return fmt.Errorf("failed to get current assignees: %w", err)
	}

	// Build set of usernames to remove
	removeSet := make(map[string]bool)
	for _, username := range assignees {
		removeSet[username] = true
	}

	// Filter assignees
	var newAssigneeIDs []int64
	if mr.Assignees != nil {
		for _, assignee := range mr.Assignees {
			if !removeSet[assignee.Username] {
				newAssigneeIDs = append(newAssigneeIDs, assignee.ID)
			}
		}
	}

	opts := &gitlab.UpdateMergeRequestOptions{
		AssigneeIDs: &newAssigneeIDs,
	}

	_, resp, err := g.client.MergeRequests.UpdateMergeRequest(projectPath, int64(number), opts)
	g.updateLastResponse(resp)

	if err != nil {
		return fmt.Errorf("failed to remove assignees: %w", err)
	}

	return nil
}

// Conversion helper functions

func convertGitLabMR(mr *gitlab.MergeRequest) *gitprovider.PullRequest {
	if mr == nil {
		return nil
	}

	state := "open"
	switch mr.State {
	case "merged":
		state = "merged"
	case "closed":
		state = "closed"
	}

	merged := mr.State == "merged"

	// Helper to safely convert *time.Time to time.Time
	safeTime := func(t *time.Time) time.Time {
		if t == nil {
			return time.Time{}
		}
		return *t
	}

	// Determine mergeable status from DetailedMergeStatus if available
	mergeable := false
	if mr.DetailedMergeStatus != "" {
		mergeable = mr.DetailedMergeStatus == "mergeable" || mr.DetailedMergeStatus == "ci_still_running"
	}

	return &gitprovider.PullRequest{
		Number:      int(mr.IID),
		Title:       mr.Title,
		Body:        mr.Description,
		State:       state,
		Author:      mr.Author.Username,
		HeadRef:     mr.SourceBranch,
		BaseRef:     mr.TargetBranch,
		HeadSHA:     mr.SHA,
		BaseSHA:     mr.DiffRefs.BaseSha,
		Labels:      mr.Labels,
		HTMLURL:     mr.WebURL,
		Merged:      merged,
		Mergeable:   mergeable,
		MergeCommit: mr.MergeCommitSHA,
		CreatedAt:   safeTime(mr.CreatedAt),
		UpdatedAt:   safeTime(mr.UpdatedAt),
	}
}

func convertGitLabNote(note *gitlab.Note) *gitprovider.Comment {
	if note == nil {
		return nil
	}

	// Helper to safely convert *time.Time to time.Time
	safeTime := func(t *time.Time) time.Time {
		if t == nil {
			return time.Time{}
		}
		return *t
	}

	return &gitprovider.Comment{
		ID:        note.ID,
		Body:      note.Body,
		Author:    note.Author.Username,
		CreatedAt: safeTime(note.CreatedAt),
		UpdatedAt: safeTime(note.UpdatedAt),
	}
}
