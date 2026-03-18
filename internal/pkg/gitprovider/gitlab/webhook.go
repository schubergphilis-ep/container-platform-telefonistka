package gitlab

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// ParseWebhook parses a GitLab webhook payload
func (g *GitLabProvider) ParseWebhook(req *http.Request, secret []byte) (gitprovider.Event, error) {
	// Read and buffer the body so it can be re-read if needed (e.g. by middleware)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, &gitprovider.WebhookValidationError{
			Message: fmt.Sprintf("failed to read request body: %v", err),
		}
	}
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))

	payload, err := gitlab.ParseWebhook(gitlab.HookEventType(req), body)
	if err != nil {
		return nil, &gitprovider.WebhookValidationError{
			Message: fmt.Sprintf("failed to parse webhook: %v", err),
		}
	}

	// Convert GitLab event to provider-agnostic event
	switch event := payload.(type) {
	case *gitlab.MergeEvent:
		return &GitLabMergeRequestEvent{event: event}, nil
	case *gitlab.PushEvent:
		return &GitLabPushEvent{event: event}, nil
	case *gitlab.MergeCommentEvent:
		return &GitLabNoteEvent{event: event}, nil
	default:
		return &GitLabUnknownEvent{}, nil
	}
}

// ValidateWebhookSignature validates the webhook signature.
// An empty secret is rejected — callers must configure a webhook secret.
func (g *GitLabProvider) ValidateWebhookSignature(req *http.Request, secret []byte) error {
	if len(secret) == 0 {
		return &gitprovider.WebhookValidationError{
			Message: "webhook secret is not configured; refusing to validate",
		}
	}
	// GitLab uses X-Gitlab-Token header with constant-time comparison to prevent timing attacks
	token := req.Header.Get("X-Gitlab-Token")
	if subtle.ConstantTimeCompare([]byte(token), secret) != 1 {
		return &gitprovider.WebhookValidationError{
			Message: "invalid webhook token",
		}
	}
	return nil
}

// GitLabMergeRequestEvent wraps GitLab's MergeEvent
type GitLabMergeRequestEvent struct {
	event *gitlab.MergeEvent
}

func (e *GitLabMergeRequestEvent) Type() gitprovider.EventType {
	return gitprovider.EventTypeMergeRequest
}

func (e *GitLabMergeRequestEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		ID:            e.event.Project.ID,
		Name:          e.event.Project.Name,
		FullName:      e.event.Project.PathWithNamespace,
		Owner:         e.event.Project.Namespace,
		DefaultBranch: e.event.Project.DefaultBranch,
		Private:       e.event.Project.Visibility != gitlab.PublicVisibility,
		HTMLURL:       e.event.Project.WebURL,
		CloneURL:      e.event.Project.GitHTTPURL,
	}
}

func (e *GitLabMergeRequestEvent) Action() string {
	// Map GitLab actions to GitHub-style actions
	switch e.event.ObjectAttributes.Action {
	case "open":
		return "opened"
	case "reopen":
		return "reopened"
	case "update":
		return "synchronize"
	case "merge":
		return "closed"
	case "close":
		return "closed"
	default:
		return e.event.ObjectAttributes.Action
	}
}

func (e *GitLabMergeRequestEvent) PullRequest() *gitprovider.PullRequest {
	merged := e.event.ObjectAttributes.State == "merged"

	// Extract label titles from the webhook event
	var labels []string
	for _, l := range e.event.ObjectAttributes.Labels {
		if l != nil {
			labels = append(labels, l.Title)
		}
	}

	return &gitprovider.PullRequest{
		Number:      int(e.event.ObjectAttributes.IID),
		Title:       e.event.ObjectAttributes.Title,
		Body:        e.event.ObjectAttributes.Description,
		State:       e.event.ObjectAttributes.State,
		Author:      e.event.User.Username,
		HeadRef:     e.event.ObjectAttributes.SourceBranch,
		BaseRef:     e.event.ObjectAttributes.TargetBranch,
		HeadSHA:     e.event.ObjectAttributes.LastCommit.ID,
		BaseSHA:     e.event.ObjectAttributes.OldRev, // Only populated on update events; empty on open/merge. Fetch MR via API for reliable BaseSHA.
		Labels:      labels,
		HTMLURL:     e.event.ObjectAttributes.URL,
		Merged:      merged,
		Mergeable:   true, // GitLab doesn't provide this in webhook
		MergeCommit: e.event.ObjectAttributes.MergeCommitSHA,
	}
}

func (e *GitLabMergeRequestEvent) Sender() string {
	return e.event.User.Username
}

// GetGitLabEvent returns the underlying GitLab event (for backward compatibility)
func (e *GitLabMergeRequestEvent) GetGitLabEvent() *gitlab.MergeEvent {
	return e.event
}

// GitLabPushEvent wraps GitLab's PushEvent
type GitLabPushEvent struct {
	event *gitlab.PushEvent
}

func (e *GitLabPushEvent) Type() gitprovider.EventType {
	return gitprovider.EventTypePush
}

func (e *GitLabPushEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		ID:            e.event.ProjectID,
		Name:          e.event.Project.Name,
		FullName:      e.event.Project.PathWithNamespace,
		Owner:         e.event.Project.Namespace,
		DefaultBranch: e.event.Project.DefaultBranch,
		Private:       e.event.Project.Visibility != gitlab.PublicVisibility,
		HTMLURL:       e.event.Project.WebURL,
		CloneURL:      e.event.Project.GitHTTPURL,
	}
}

func (e *GitLabPushEvent) Ref() string {
	return e.event.Ref
}

func (e *GitLabPushEvent) Before() string {
	return e.event.Before
}

func (e *GitLabPushEvent) After() string {
	return e.event.After
}

func (e *GitLabPushEvent) Commits() []gitprovider.Commit {
	var commits []gitprovider.Commit
	for _, c := range e.event.Commits {
		commits = append(commits, gitprovider.Commit{
			SHA:     c.ID,
			Message: c.Message,
			Author:  c.Author.Name,
			HTMLURL: c.URL,
		})
	}
	return commits
}

func (e *GitLabPushEvent) Sender() string {
	return e.event.UserUsername
}

// GetGitLabEvent returns the underlying GitLab event (for backward compatibility)
func (e *GitLabPushEvent) GetGitLabEvent() *gitlab.PushEvent {
	return e.event
}

// GitLabNoteEvent wraps GitLab's MergeCommentEvent
type GitLabNoteEvent struct {
	event *gitlab.MergeCommentEvent
}

func (e *GitLabNoteEvent) Type() gitprovider.EventType {
	return gitprovider.EventTypeNote
}

func (e *GitLabNoteEvent) Repository() *gitprovider.Repository {
	return &gitprovider.Repository{
		ID:            e.event.ProjectID,
		Name:          e.event.Project.Name,
		FullName:      e.event.Project.PathWithNamespace,
		Owner:         e.event.Project.Namespace,
		DefaultBranch: e.event.Project.DefaultBranch,
		Private:       e.event.Project.Visibility != gitlab.PublicVisibility,
		HTMLURL:       e.event.Project.WebURL,
		CloneURL:      e.event.Project.GitHTTPURL,
	}
}

func (e *GitLabNoteEvent) Action() string {
	// GitLab doesn't have action in note events, just creation
	return "created"
}

func (e *GitLabNoteEvent) Issue() *gitprovider.PullRequest {
	return &gitprovider.PullRequest{
		Number:  int(e.event.MergeRequest.IID),
		Title:   e.event.MergeRequest.Title,
		Body:    e.event.MergeRequest.Description,
		State:   e.event.MergeRequest.State,
		Author:  e.event.User.Username,
		HeadRef: e.event.MergeRequest.SourceBranch,
		BaseRef: e.event.MergeRequest.TargetBranch,
		HTMLURL: e.event.MergeRequest.URL,
	}
}

func (e *GitLabNoteEvent) Comment() *gitprovider.Comment {
	return &gitprovider.Comment{
		ID:      e.event.ObjectAttributes.ID,
		Body:    e.event.ObjectAttributes.Note,
		Author:  e.event.User.Username,
		HTMLURL: e.event.ObjectAttributes.URL,
	}
}

func (e *GitLabNoteEvent) Sender() string {
	return e.event.User.Username
}

// GetGitLabEvent returns the underlying GitLab event (for backward compatibility)
func (e *GitLabNoteEvent) GetGitLabEvent() *gitlab.MergeCommentEvent {
	return e.event
}

// GitLabUnknownEvent represents an unknown event type
type GitLabUnknownEvent struct{}

func (e *GitLabUnknownEvent) Type() gitprovider.EventType {
	return gitprovider.EventTypeUnknown
}

func (e *GitLabUnknownEvent) Repository() *gitprovider.Repository {
	return nil
}
