package gitlabci

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
)

func TestParseProjectPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantOwner string
		wantRepo  string
	}{
		{"group/project", "group/project", "group", "project"},
		{"nested namespace", "group/subgroup/project", "group/subgroup", "project"},
		{"deep nesting", "a/b/c/d", "a/b/c", "d"},
		{"no slash", "project", "", "project"},
		{"personal namespace", "rafaelgherrero/test-telefonistka", "rafaelgherrero", "test-telefonistka"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			owner, repo := parseProjectPath(tt.path)
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestExtractMRNumberFromCommitMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		want    int
	}{
		{
			"standard merge commit",
			"Merge branch 'feature-x' into 'main'\n\nSee merge request group/project!42",
			42,
		},
		{
			"nested namespace",
			"Merge branch 'fix' into 'main'\n\nSee merge request group/sub/project!100",
			100,
		},
		{
			"personal namespace",
			"Merge branch 'feat' into 'main'\n\nSee merge request rafaelgherrero/test-telefonistka!7",
			7,
		},
		{
			"no MR reference",
			"Regular commit message without MR reference",
			0,
		},
		{
			"empty message",
			"",
			0,
		},
		{
			"MR number 1",
			"Merge branch 'x' into 'main'\n\nSee merge request org/repo!1",
			1,
		},
		{
			"large MR number",
			"Merge branch 'y' into 'main'\n\nSee merge request org/repo!9999",
			9999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractMRNumberFromCommitMessage(tt.message)
			if got != tt.want {
				t.Errorf("extractMRNumberFromCommitMessage(%q) = %d, want %d", tt.message, got, tt.want)
			}
		})
	}
}

func TestBuildMergeRequestEvent(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "42")
	t.Setenv("CI_PROJECT_PATH", "group/project")
	t.Setenv("CI_MERGE_REQUEST_TITLE", "Test MR Title")
	t.Setenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", "feature-branch")
	t.Setenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "main")
	t.Setenv("CI_COMMIT_SHA", "abc123def456abc123def456abc123def456abc1")
	t.Setenv("CI_MERGE_REQUEST_DIFF_BASE_SHA", "def456abc123def456abc123def456abc123def4")
	t.Setenv("GITLAB_USER_LOGIN", "testuser")
	t.Setenv("CI_SERVER_URL", "https://gitlab.example.com")
	t.Setenv("CI_PROJECT_ID", "123")
	t.Setenv("CI_MERGE_REQUEST_EVENT_TYPE", "merged_result")

	event, err := buildMergeRequestEvent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Type() != gitprovider.EventTypeMergeRequest {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypeMergeRequest)
	}

	pr := event.(*GitLabCIMergeRequestEvent)

	if pr.MRNumber != 42 {
		t.Errorf("MRNumber = %d, want 42", pr.MRNumber)
	}
	if pr.Owner != "group" {
		t.Errorf("Owner = %q, want %q", pr.Owner, "group")
	}
	if pr.Repo != "project" {
		t.Errorf("Repo = %q, want %q", pr.Repo, "project")
	}
	if pr.Title != "Test MR Title" {
		t.Errorf("Title = %q, want %q", pr.Title, "Test MR Title")
	}
	if pr.SourceBranch != "feature-branch" {
		t.Errorf("SourceBranch = %q, want %q", pr.SourceBranch, "feature-branch")
	}
	if pr.TargetBranch != "main" {
		t.Errorf("TargetBranch = %q, want %q", pr.TargetBranch, "main")
	}
	if pr.Author != "testuser" {
		t.Errorf("Author = %q, want %q", pr.Author, "testuser")
	}
	if pr.MergeStatus != "merged_result" {
		t.Errorf("MergeStatus = %q, want %q", pr.MergeStatus, "merged_result")
	}

	// Verify PullRequest() helper
	pullReq := event.(*GitLabCIMergeRequestEvent).PullRequest()
	if pullReq.Number != 42 {
		t.Errorf("PullRequest().Number = %d, want 42", pullReq.Number)
	}
	if pullReq.HeadRef != "feature-branch" {
		t.Errorf("PullRequest().HeadRef = %q, want %q", pullReq.HeadRef, "feature-branch")
	}

	// Verify Repository() helper
	repo := event.(*GitLabCIMergeRequestEvent).Repository()
	if repo.Owner != "group" {
		t.Errorf("Repository().Owner = %q, want %q", repo.Owner, "group")
	}
	if repo.Name != "project" {
		t.Errorf("Repository().Name = %q, want %q", repo.Name, "project")
	}
}

func TestBuildMergeRequestEvent_MissingIID(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "")
	t.Setenv("CI_PROJECT_PATH", "group/project")

	_, err := buildMergeRequestEvent()
	if err == nil {
		t.Error("expected error when CI_MERGE_REQUEST_IID is not set")
	}
}

func TestBuildMergeRequestEvent_InvalidIID(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "not-a-number")
	t.Setenv("CI_PROJECT_PATH", "group/project")

	_, err := buildMergeRequestEvent()
	if err == nil {
		t.Error("expected error when CI_MERGE_REQUEST_IID is not a number")
	}
}

func TestBuildMergeRequestEvent_MissingProjectPath(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "1")
	t.Setenv("CI_PROJECT_PATH", "")

	_, err := buildMergeRequestEvent()
	if err == nil {
		t.Error("expected error when CI_PROJECT_PATH is not set")
	}
}

func TestBuildMergeRequestEvent_DefaultAction(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "5")
	t.Setenv("CI_PROJECT_PATH", "org/repo")
	t.Setenv("CI_MERGE_REQUEST_EVENT_TYPE", "") // not set

	event, err := buildMergeRequestEvent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pr := event.(*GitLabCIMergeRequestEvent)
	if pr.MergeStatus != "opened" {
		t.Errorf("MergeStatus = %q, want %q (default)", pr.MergeStatus, "opened")
	}
}

func TestBuildPushEvent(t *testing.T) {
	t.Setenv("CI_PROJECT_PATH", "group/project")
	t.Setenv("CI_COMMIT_SHA", "abc123def456abc123def456abc123def456abc1")
	t.Setenv("CI_COMMIT_BEFORE_SHA", "000000000000000000000000000000000000000a")
	t.Setenv("CI_COMMIT_REF_NAME", "main")
	t.Setenv("GITLAB_USER_LOGIN", "pusher")
	t.Setenv("CI_SERVER_URL", "https://gitlab.example.com")
	t.Setenv("CI_PROJECT_ID", "99")
	t.Setenv("CI_COMMIT_MESSAGE", "feat: add new feature")

	event, err := buildPushEvent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Type() != gitprovider.EventTypePush {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypePush)
	}

	push := event.(*GitLabCIPushEvent)

	if push.Owner != "group" {
		t.Errorf("Owner = %q, want %q", push.Owner, "group")
	}
	if push.Repo != "project" {
		t.Errorf("Repo = %q, want %q", push.Repo, "project")
	}
	if push.RefName != "main" {
		t.Errorf("RefName = %q, want %q", push.RefName, "main")
	}
	if push.CommitAuthor != "pusher" {
		t.Errorf("CommitAuthor = %q, want %q", push.CommitAuthor, "pusher")
	}
	if push.CommitMessage != "feat: add new feature" {
		t.Errorf("CommitMessage = %q, want %q", push.CommitMessage, "feat: add new feature")
	}

	// Verify interface methods
	if event.(*GitLabCIPushEvent).Ref() != "main" {
		t.Errorf("Ref() = %q, want %q", push.Ref(), "main")
	}
	if event.(*GitLabCIPushEvent).After() != "abc123def456abc123def456abc123def456abc1" {
		t.Errorf("After() = %q", push.After())
	}

	commits := event.(*GitLabCIPushEvent).Commits()
	if len(commits) != 1 {
		t.Errorf("Commits() len = %d, want 1", len(commits))
	}

	// Verify Repository() helper
	repo := event.(*GitLabCIPushEvent).Repository()
	if repo.Owner != "group" {
		t.Errorf("Repository().Owner = %q, want %q", repo.Owner, "group")
	}
}

func TestBuildPushEvent_MissingProjectPath(t *testing.T) {
	t.Setenv("CI_PROJECT_PATH", "")

	_, err := buildPushEvent()
	if err == nil {
		t.Error("expected error when CI_PROJECT_PATH is not set")
	}
}

func TestBuildEventFromEnvironment_NotGitLabCI(t *testing.T) {
	t.Setenv("GITLAB_CI", "")

	_, err := BuildEventFromEnvironment()
	if err == nil {
		t.Error("expected error when not running in GitLab CI")
	}
}

func TestBuildEventFromEnvironment_MergeRequest(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PIPELINE_SOURCE", "merge_request_event")
	t.Setenv("CI_MERGE_REQUEST_IID", "10")
	t.Setenv("CI_PROJECT_PATH", "org/repo")
	t.Setenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", "feature")
	t.Setenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "main")
	t.Setenv("CI_COMMIT_SHA", "abc123def456abc123def456abc123def456abc1")

	event, err := BuildEventFromEnvironment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Type() != gitprovider.EventTypeMergeRequest {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypeMergeRequest)
	}
}

func TestBuildEventFromEnvironment_Push(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PIPELINE_SOURCE", "push")
	t.Setenv("CI_PROJECT_PATH", "org/repo")
	t.Setenv("CI_COMMIT_SHA", "abc123def456abc123def456abc123def456abc1")
	t.Setenv("CI_COMMIT_BEFORE_SHA", "0000000000000000000000000000000000000000")
	t.Setenv("CI_COMMIT_REF_NAME", "main")

	event, err := BuildEventFromEnvironment()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Type() != gitprovider.EventTypePush {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypePush)
	}
}

func TestBuildEventFromEnvironment_UnsupportedSource(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PIPELINE_SOURCE", "schedule")

	_, err := BuildEventFromEnvironment()
	if err == nil {
		t.Error("expected error for unsupported pipeline source")
	}
}

// --- Additional comprehensive tests ---

func TestShortSHA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty string", "", ""},
		{"short string less than 8", "abc", "abc"},
		{"exactly 8 characters", "abcdef12", "abcdef12"},
		{"long SHA", "abc123def456abc123def456abc123def456abc1", "abc123de"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shortSHA(tt.input)
			if got != tt.want {
				t.Errorf("shortSHA(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWriteEventFile_HappyPath(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PIPELINE_SOURCE", "push")
	t.Setenv("CI_PROJECT_PATH", "org/repo")
	t.Setenv("CI_COMMIT_SHA", "abc123def456abc123def456abc123def456abc1")
	t.Setenv("CI_COMMIT_BEFORE_SHA", "0000000000000000000000000000000000000000")
	t.Setenv("CI_COMMIT_REF_NAME", "main")
	t.Setenv("GITLAB_USER_LOGIN", "writer")
	t.Setenv("CI_SERVER_URL", "https://gitlab.example.com")
	t.Setenv("CI_PROJECT_ID", "55")
	t.Setenv("CI_COMMIT_MESSAGE", "test commit")

	tmpFile, err := os.CreateTemp(t.TempDir(), "event-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	err = WriteEventFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("WriteEventFile() returned error: %v", err)
	}

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to read event file: %v", err)
	}

	// Verify it is valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("event file is not valid JSON: %v", err)
	}

	// Verify key fields are present
	if parsed["ProjectPath"] != "org/repo" {
		t.Errorf("ProjectPath = %v, want %q", parsed["ProjectPath"], "org/repo")
	}
	if parsed["Owner"] != "org" {
		t.Errorf("Owner = %v, want %q", parsed["Owner"], "org")
	}
	if parsed["Repo"] != "repo" {
		t.Errorf("Repo = %v, want %q", parsed["Repo"], "repo")
	}
}

func TestWriteEventFile_NotInGitLabCI(t *testing.T) {
	t.Setenv("GITLAB_CI", "")

	err := WriteEventFile("/tmp/should-not-exist.json")
	if err == nil {
		t.Error("expected error when not in GitLab CI")
	}
}

func TestGitLabCIMergeRequestEvent_InterfaceCompliance(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "develop")

	event := &GitLabCIMergeRequestEvent{
		MRNumber:     99,
		ProjectPath:  "myorg/myteam/myrepo",
		Owner:        "myorg/myteam",
		Repo:         "myrepo",
		Title:        "Add feature X",
		SourceBranch: "feat-x",
		TargetBranch: "develop",
		HeadSHA:      "aaaa1111bbbb2222cccc3333dddd4444eeee5555",
		BaseSHA:      "ffff6666aaaa7777bbbb8888cccc9999dddd0000",
		Author:       "janedoe",
		ServerURL:    "https://gitlab.mycompany.com",
		ProjectID:    "456",
		MergeStatus:  "merged_result",
	}

	// Verify interface satisfaction at compile time
	var _ gitprovider.PullRequestEvent = event

	// Type()
	if event.Type() != gitprovider.EventTypeMergeRequest {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypeMergeRequest)
	}

	// Action()
	if event.Action() != "merged_result" {
		t.Errorf("Action() = %q, want %q", event.Action(), "merged_result")
	}

	// Sender()
	if event.Sender() != "janedoe" {
		t.Errorf("Sender() = %q, want %q", event.Sender(), "janedoe")
	}

	// PullRequest()
	pr := event.PullRequest()
	if pr.Number != 99 {
		t.Errorf("PullRequest().Number = %d, want 99", pr.Number)
	}
	if pr.Title != "Add feature X" {
		t.Errorf("PullRequest().Title = %q, want %q", pr.Title, "Add feature X")
	}
	if pr.HeadRef != "feat-x" {
		t.Errorf("PullRequest().HeadRef = %q, want %q", pr.HeadRef, "feat-x")
	}
	if pr.BaseRef != "develop" {
		t.Errorf("PullRequest().BaseRef = %q, want %q", pr.BaseRef, "develop")
	}
	if pr.HeadSHA != "aaaa1111bbbb2222cccc3333dddd4444eeee5555" {
		t.Errorf("PullRequest().HeadSHA = %q, want correct SHA", pr.HeadSHA)
	}
	if pr.BaseSHA != "ffff6666aaaa7777bbbb8888cccc9999dddd0000" {
		t.Errorf("PullRequest().BaseSHA = %q, want correct SHA", pr.BaseSHA)
	}
	if pr.Author != "janedoe" {
		t.Errorf("PullRequest().Author = %q, want %q", pr.Author, "janedoe")
	}
	if pr.State != "open" {
		t.Errorf("PullRequest().State = %q, want %q", pr.State, "open")
	}
	wantPRURL := "https://gitlab.mycompany.com/myorg/myteam/myrepo/-/merge_requests/99"
	if pr.HTMLURL != wantPRURL {
		t.Errorf("PullRequest().HTMLURL = %q, want %q", pr.HTMLURL, wantPRURL)
	}

	// Repository() with CI_DEFAULT_BRANCH set
	repo := event.Repository()
	if repo.Name != "myrepo" {
		t.Errorf("Repository().Name = %q, want %q", repo.Name, "myrepo")
	}
	if repo.FullName != "myorg/myteam/myrepo" {
		t.Errorf("Repository().FullName = %q, want %q", repo.FullName, "myorg/myteam/myrepo")
	}
	if repo.Owner != "myorg/myteam" {
		t.Errorf("Repository().Owner = %q, want %q", repo.Owner, "myorg/myteam")
	}
	if repo.DefaultBranch != "develop" {
		t.Errorf("Repository().DefaultBranch = %q, want %q", repo.DefaultBranch, "develop")
	}
	wantHTMLURL := "https://gitlab.mycompany.com/myorg/myteam/myrepo"
	if repo.HTMLURL != wantHTMLURL {
		t.Errorf("Repository().HTMLURL = %q, want %q", repo.HTMLURL, wantHTMLURL)
	}
}

func TestGitLabCIMergeRequestEvent_RepositoryDefaultBranchFallback(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "")

	event := &GitLabCIMergeRequestEvent{
		ProjectPath: "org/repo",
		Owner:       "org",
		Repo:        "repo",
		ServerURL:   "https://gitlab.com",
	}

	repo := event.Repository()
	if repo.DefaultBranch != "main" {
		t.Errorf("Repository().DefaultBranch = %q, want %q (fallback)", repo.DefaultBranch, "main")
	}
}

func TestGitLabCIPushEvent_InterfaceCompliance(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "master")

	event := &GitLabCIPushEvent{
		ProjectPath:   "team/project",
		Owner:         "team",
		Repo:          "project",
		CommitSHA:     "1111222233334444555566667777888899990000",
		BeforeSHA:     "0000999988887777666655554444333322221111",
		RefName:       "master",
		CommitAuthor:  "deployer",
		ServerURL:     "https://gitlab.example.com",
		ProjectID:     "77",
		CommitMessage: "fix: resolve nil pointer",
	}

	// Verify interface satisfaction at compile time
	var _ gitprovider.PushEvent = event

	// Type()
	if event.Type() != gitprovider.EventTypePush {
		t.Errorf("Type() = %v, want %v", event.Type(), gitprovider.EventTypePush)
	}

	// Ref()
	if event.Ref() != "master" {
		t.Errorf("Ref() = %q, want %q", event.Ref(), "master")
	}

	// Before()
	if event.Before() != "0000999988887777666655554444333322221111" {
		t.Errorf("Before() = %q, want correct SHA", event.Before())
	}

	// After()
	if event.After() != "1111222233334444555566667777888899990000" {
		t.Errorf("After() = %q, want correct SHA", event.After())
	}

	// Sender()
	if event.Sender() != "deployer" {
		t.Errorf("Sender() = %q, want %q", event.Sender(), "deployer")
	}

	// Commits()
	commits := event.Commits()
	if len(commits) != 1 {
		t.Fatalf("Commits() len = %d, want 1", len(commits))
	}
	if commits[0].SHA != "1111222233334444555566667777888899990000" {
		t.Errorf("Commits()[0].SHA = %q, want correct SHA", commits[0].SHA)
	}
	if commits[0].Author != "deployer" {
		t.Errorf("Commits()[0].Author = %q, want %q", commits[0].Author, "deployer")
	}
	if commits[0].Message != "Commit from CI pipeline" {
		t.Errorf("Commits()[0].Message = %q, want %q", commits[0].Message, "Commit from CI pipeline")
	}

	// Repository() with CI_DEFAULT_BRANCH set
	repo := event.Repository()
	if repo.Name != "project" {
		t.Errorf("Repository().Name = %q, want %q", repo.Name, "project")
	}
	if repo.FullName != "team/project" {
		t.Errorf("Repository().FullName = %q, want %q", repo.FullName, "team/project")
	}
	if repo.Owner != "team" {
		t.Errorf("Repository().Owner = %q, want %q", repo.Owner, "team")
	}
	if repo.DefaultBranch != "master" {
		t.Errorf("Repository().DefaultBranch = %q, want %q", repo.DefaultBranch, "master")
	}
	wantHTMLURL := "https://gitlab.example.com/team/project"
	if repo.HTMLURL != wantHTMLURL {
		t.Errorf("Repository().HTMLURL = %q, want %q", repo.HTMLURL, wantHTMLURL)
	}
}

func TestGitLabCIPushEvent_RepositoryDefaultBranchFallback(t *testing.T) {
	t.Setenv("CI_DEFAULT_BRANCH", "")

	event := &GitLabCIPushEvent{
		ProjectPath: "org/repo",
		Owner:       "org",
		Repo:        "repo",
		ServerURL:   "https://gitlab.com",
	}

	repo := event.Repository()
	if repo.DefaultBranch != "main" {
		t.Errorf("Repository().DefaultBranch = %q, want %q (fallback)", repo.DefaultBranch, "main")
	}
}

func TestBuildEventFromEnvironment_EmptyPipelineSource(t *testing.T) {
	t.Setenv("GITLAB_CI", "true")
	t.Setenv("CI_PIPELINE_SOURCE", "")

	_, err := BuildEventFromEnvironment()
	if err == nil {
		t.Error("expected error when CI_PIPELINE_SOURCE is empty")
	}
	if err != nil && !strings.Contains(err.Error(), "unsupported pipeline source") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unsupported pipeline source")
	}
}

func TestBuildMergeRequestEvent_NestedGroups(t *testing.T) {
	t.Setenv("CI_MERGE_REQUEST_IID", "7")
	t.Setenv("CI_PROJECT_PATH", "org/team/subteam/project")
	t.Setenv("CI_MERGE_REQUEST_TITLE", "Nested MR")
	t.Setenv("CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", "feat")
	t.Setenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME", "main")
	t.Setenv("CI_COMMIT_SHA", "aabbccdd11223344aabbccdd11223344aabbccdd")
	t.Setenv("CI_MERGE_REQUEST_DIFF_BASE_SHA", "")
	t.Setenv("GITLAB_USER_LOGIN", "nested-user")
	t.Setenv("CI_SERVER_URL", "https://gitlab.example.com")
	t.Setenv("CI_PROJECT_ID", "200")
	t.Setenv("CI_MERGE_REQUEST_EVENT_TYPE", "")

	event, err := buildMergeRequestEvent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mr := event.(*GitLabCIMergeRequestEvent)

	if mr.Owner != "org/team/subteam" {
		t.Errorf("Owner = %q, want %q", mr.Owner, "org/team/subteam")
	}
	if mr.Repo != "project" {
		t.Errorf("Repo = %q, want %q", mr.Repo, "project")
	}
	if mr.ProjectPath != "org/team/subteam/project" {
		t.Errorf("ProjectPath = %q, want %q", mr.ProjectPath, "org/team/subteam/project")
	}
	if mr.MRNumber != 7 {
		t.Errorf("MRNumber = %d, want 7", mr.MRNumber)
	}
	if mr.MergeStatus != "opened" {
		t.Errorf("MergeStatus = %q, want %q (default)", mr.MergeStatus, "opened")
	}
}

func TestBuildPushEvent_AllOptionalFieldsEmpty(t *testing.T) {
	t.Setenv("CI_PROJECT_PATH", "minimal/repo")
	t.Setenv("CI_COMMIT_SHA", "")
	t.Setenv("CI_COMMIT_BEFORE_SHA", "")
	t.Setenv("CI_COMMIT_REF_NAME", "")
	t.Setenv("GITLAB_USER_LOGIN", "")
	t.Setenv("CI_SERVER_URL", "")
	t.Setenv("CI_PROJECT_ID", "")
	t.Setenv("CI_COMMIT_MESSAGE", "")

	event, err := buildPushEvent()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	push := event.(*GitLabCIPushEvent)

	if push.Owner != "minimal" {
		t.Errorf("Owner = %q, want %q", push.Owner, "minimal")
	}
	if push.Repo != "repo" {
		t.Errorf("Repo = %q, want %q", push.Repo, "repo")
	}
	if push.CommitSHA != "" {
		t.Errorf("CommitSHA = %q, want empty", push.CommitSHA)
	}
	if push.BeforeSHA != "" {
		t.Errorf("BeforeSHA = %q, want empty", push.BeforeSHA)
	}
	if push.RefName != "" {
		t.Errorf("RefName = %q, want empty", push.RefName)
	}
	if push.CommitAuthor != "" {
		t.Errorf("CommitAuthor = %q, want empty", push.CommitAuthor)
	}
	if push.ServerURL != "" {
		t.Errorf("ServerURL = %q, want empty", push.ServerURL)
	}

	// Verify interface methods still work with empty values
	if push.Ref() != "" {
		t.Errorf("Ref() = %q, want empty", push.Ref())
	}
	if push.Before() != "" {
		t.Errorf("Before() = %q, want empty", push.Before())
	}
	if push.After() != "" {
		t.Errorf("After() = %q, want empty", push.After())
	}
	if push.Sender() != "" {
		t.Errorf("Sender() = %q, want empty", push.Sender())
	}

	commits := push.Commits()
	if len(commits) != 1 {
		t.Fatalf("Commits() len = %d, want 1", len(commits))
	}
	if commits[0].SHA != "" {
		t.Errorf("Commits()[0].SHA = %q, want empty", commits[0].SHA)
	}
	if commits[0].Author != "" {
		t.Errorf("Commits()[0].Author = %q, want empty", commits[0].Author)
	}
}
