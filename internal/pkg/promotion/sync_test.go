package promotion

import (
	"context"
	"testing"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/testutils"
)

// TestGenerateSyncCommitActions_DriftIsIncluded verifies that when there is drift
// between source and target directories (files with different content), the sync
// actions include updates for BOTH the newly changed file AND the drifted file.
func TestGenerateSyncCommitActions_DriftIsIncluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Setup: source has values.yaml (changed by MR) and deployment.yaml (unchanged)
	// Target has values.yaml (drifted - different content) and deployment.yaml (same content)
	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"envs/dev/comp1@main": {
				{Name: "values.yaml", Path: "envs/dev/comp1/values.yaml", Type: "file", SHA: "sha_src_values"},
				{Name: "deployment.yaml", Path: "envs/dev/comp1/deployment.yaml", Type: "file", SHA: "sha_same_deploy"},
			},
			"envs/staging/comp1@main": {
				{Name: "values.yaml", Path: "envs/staging/comp1/values.yaml", Type: "file", SHA: "sha_tgt_values_DRIFTED"},
				{Name: "deployment.yaml", Path: "envs/staging/comp1/deployment.yaml", Type: "file", SHA: "sha_same_deploy"},
			},
		},
		Files: map[string][]byte{
			"envs/dev/comp1/values.yaml@main": []byte("image: app:v2\n"), // new version from MR
		},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"envs/dev/comp1", "envs/staging/comp1", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expect exactly 1 action: update values.yaml (drift fix with new content).
	// deployment.yaml should be skipped because SHAs match.
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(actions), actions)
	}

	action := actions[0]
	if action.Action != "update" {
		t.Errorf("expected action 'update', got '%s'", action.Action)
	}
	if action.FilePath != "envs/staging/comp1/values.yaml" {
		t.Errorf("expected file path 'envs/staging/comp1/values.yaml', got '%s'", action.FilePath)
	}
}

// TestGenerateSyncCommitActions_NewFileCreated verifies that files present in source
// but not in target are created.
func TestGenerateSyncCommitActions_NewFileCreated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "existing.yaml", Path: "src/existing.yaml", Type: "file", SHA: "sha1"},
				{Name: "new-file.yaml", Path: "src/new-file.yaml", Type: "file", SHA: "sha_new"},
			},
			"tgt@main": {
				{Name: "existing.yaml", Path: "tgt/existing.yaml", Type: "file", SHA: "sha1"}, // same SHA
			},
		},
		Files: map[string][]byte{
			"src/new-file.yaml@main": []byte("new content\n"),
		},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(actions) != 1 {
		t.Fatalf("expected 1 action (create new file), got %d", len(actions))
	}
	if actions[0].Action != "create" {
		t.Errorf("expected 'create' action, got '%s'", actions[0].Action)
	}
	if actions[0].FilePath != "tgt/new-file.yaml" {
		t.Errorf("expected file path 'tgt/new-file.yaml', got '%s'", actions[0].FilePath)
	}
}

// TestGenerateSyncCommitActions_DeletedFileRemoved verifies that files present in target
// but not in source are deleted.
func TestGenerateSyncCommitActions_DeletedFileRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "keep.yaml", Path: "src/keep.yaml", Type: "file", SHA: "sha1"},
			},
			"tgt@main": {
				{Name: "keep.yaml", Path: "tgt/keep.yaml", Type: "file", SHA: "sha1"}, // same SHA
				{Name: "remove-me.yaml", Path: "tgt/remove-me.yaml", Type: "file", SHA: "sha_old"},
			},
		},
		Files: map[string][]byte{},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(actions) != 1 {
		t.Fatalf("expected 1 action (delete), got %d", len(actions))
	}
	if actions[0].Action != "delete" {
		t.Errorf("expected 'delete' action, got '%s'", actions[0].Action)
	}
	if actions[0].FilePath != "tgt/remove-me.yaml" {
		t.Errorf("expected file path 'tgt/remove-me.yaml', got '%s'", actions[0].FilePath)
	}
}

// TestGenerateSyncCommitActions_NoActionsWhenInSync verifies that no actions
// are generated when source and target are identical.
func TestGenerateSyncCommitActions_NoActionsWhenInSync(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "a.yaml", Path: "src/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "b.yaml", Path: "src/b.yaml", Type: "file", SHA: "sha2"},
			},
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "b.yaml", Path: "tgt/b.yaml", Type: "file", SHA: "sha2"},
			},
		},
		Files: map[string][]byte{},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(actions) != 0 {
		t.Fatalf("expected 0 actions when in sync, got %d: %+v", len(actions), actions)
	}
}

// TestGenerateSyncCommitActions_BlockListSkipsFiles verifies that blocked files
// are not synced or deleted.
func TestGenerateSyncCommitActions_BlockListSkipsFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "values.yaml", Path: "src/values.yaml", Type: "file", SHA: "sha_new"},
				{Name: "secret.yaml", Path: "src/secret.yaml", Type: "file", SHA: "sha_secret_new"},
			},
			"tgt@main": {
				{Name: "values.yaml", Path: "tgt/values.yaml", Type: "file", SHA: "sha_old"},
				{Name: "secret.yaml", Path: "tgt/secret.yaml", Type: "file", SHA: "sha_secret_old"},
			},
		},
		Files: map[string][]byte{
			"src/values.yaml@main": []byte("updated values\n"),
		},
	}

	blockList := []string{"secret.yaml"}
	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", blockList, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only values.yaml should be synced, secret.yaml is blocked
	if len(actions) != 1 {
		t.Fatalf("expected 1 action (only non-blocked), got %d", len(actions))
	}
	if actions[0].FilePath != "tgt/values.yaml" {
		t.Errorf("expected 'tgt/values.yaml', got '%s'", actions[0].FilePath)
	}
}

// TestGenerateSyncCommitActions_FallbackWhenSHAEmpty verifies that when SHA values
// are empty (provider doesn't populate them), files are synced by fetching content.
func TestGenerateSyncCommitActions_FallbackWhenSHAEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "file.yaml", Path: "src/file.yaml", Type: "file", SHA: ""}, // empty SHA
			},
			"tgt@main": {
				{Name: "file.yaml", Path: "tgt/file.yaml", Type: "file", SHA: ""}, // empty SHA
			},
		},
		Files: map[string][]byte{
			"src/file.yaml@main": []byte("content\n"),
		},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With empty SHAs, the file should still be synced (falls back to content fetch)
	if len(actions) != 1 {
		t.Fatalf("expected 1 action when SHAs are empty, got %d", len(actions))
	}
	if actions[0].Action != "update" {
		t.Errorf("expected 'update' action, got '%s'", actions[0].Action)
	}
}

// TestGenerateSyncCommitActions_RecursiveDirectories verifies that subdirectories
// are traversed recursively.
func TestGenerateSyncCommitActions_RecursiveDirectories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			"src@main": {
				{Name: "top.yaml", Path: "src/top.yaml", Type: "file", SHA: "sha_top"},
				{Name: "subdir", Path: "src/subdir", Type: "dir"},
			},
			"src/subdir@main": {
				{Name: "nested.yaml", Path: "src/subdir/nested.yaml", Type: "file", SHA: "sha_nested_new"},
			},
			"tgt@main": {
				{Name: "top.yaml", Path: "tgt/top.yaml", Type: "file", SHA: "sha_top"}, // same
				{Name: "subdir", Path: "tgt/subdir", Type: "dir"},
			},
			"tgt/subdir@main": {
				{Name: "nested.yaml", Path: "tgt/subdir/nested.yaml", Type: "file", SHA: "sha_nested_old"}, // drifted
			},
		},
		Files: map[string][]byte{
			"src/subdir/nested.yaml@main": []byte("fixed content\n"),
		},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only nested.yaml should have an action (drift fix); top.yaml SHAs match
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if actions[0].FilePath != "tgt/subdir/nested.yaml" {
		t.Errorf("expected 'tgt/subdir/nested.yaml', got '%s'", actions[0].FilePath)
	}
	if actions[0].Action != "update" {
		t.Errorf("expected 'update', got '%s'", actions[0].Action)
	}
}

// TestGenerateSyncCommitActions_SourceDeletionPR verifies behavior when source
// directory doesn't exist (deletion scenario).
func TestGenerateSyncCommitActions_SourceDeletionPR(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	provider := &testutils.MockProvider{
		Directories: map[string][]*gitprovider.FileNode{
			// source does NOT exist
			"tgt@main": {
				{Name: "a.yaml", Path: "tgt/a.yaml", Type: "file", SHA: "sha1"},
				{Name: "b.yaml", Path: "tgt/b.yaml", Type: "file", SHA: "sha2"},
			},
		},
		Files: map[string][]byte{},
	}

	actions, err := GenerateSyncCommitActions(ctx, provider, "owner", "repo",
		"src", "tgt", "main", nil, testutils.TestLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both target files should be deleted since source doesn't exist
	if len(actions) != 2 {
		t.Fatalf("expected 2 delete actions, got %d", len(actions))
	}
	for _, a := range actions {
		if a.Action != "delete" {
			t.Errorf("expected 'delete' action, got '%s'", a.Action)
		}
	}
}
