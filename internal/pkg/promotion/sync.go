package promotion

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/schubergphilis/container-platform-telefonistka/internal/pkg/gitprovider"
	log "github.com/sirupsen/logrus"
)

// ListFilesRecursive lists all files (not dirs) recursively under a path using GetDirectoryContent.
func ListFilesRecursive(
	ctx context.Context,
	provider gitprovider.GitProvider,
	owner, repo, path, ref string,
) ([]*gitprovider.FileNode, error) {
	entries, err := provider.GetDirectoryContent(ctx, owner, repo, path, ref)
	if err != nil {
		return nil, err
	}

	var files []*gitprovider.FileNode
	for _, entry := range entries {
		switch entry.Type {
		case "file":
			files = append(files, entry)
		case "dir":
			subFiles, err := ListFilesRecursive(ctx, provider, owner, repo, entry.Path, ref)
			if err != nil {
				return nil, err
			}
			files = append(files, subFiles...)
		}
	}
	return files, nil
}

// trimRelativePath safely removes a base directory prefix from a full file path,
// ensuring the prefix is treated as a directory boundary (not a string prefix).
func trimRelativePath(fullPath, basePath string) string {
	base := strings.TrimSuffix(basePath, "/") + "/"
	rel := strings.TrimPrefix(fullPath, base)
	// If TrimPrefix didn't remove anything, fall back to the old behavior
	if rel == fullPath {
		rel = strings.TrimPrefix(fullPath, basePath)
		rel = strings.TrimPrefix(rel, "/")
	}
	return rel
}

// GenerateSyncCommitActions creates CommitActions to sync files from source to target directory.
// It handles create/update and delete operations.
// blockList contains glob patterns (doublestar syntax) for files that should be skipped during sync.
// Files with identical SHAs in source and target are skipped (already in sync).
func GenerateSyncCommitActions(
	ctx context.Context,
	provider gitprovider.GitProvider,
	owner, repo, sourcePath, targetPath, ref string,
	blockList []string,
	prLogger *log.Entry,
) ([]*gitprovider.CommitAction, error) {
	sourceFiles, err := ListFilesRecursive(ctx, provider, owner, repo, sourcePath, ref)
	if err != nil {
		prLogger.Infof("Source directory %s not found, assuming deletion PR", sourcePath)
		return GenerateDeleteActions(ctx, provider, owner, repo, targetPath, ref, prLogger)
	}

	targetFiles, err := ListFilesRecursive(ctx, provider, owner, repo, targetPath, ref)
	if err != nil {
		// Target directory not found is expected for first-time promotions (all creates, no updates).
		prLogger.Debugf("Target directory %s not found or not accessible, treating as empty: %v", targetPath, err)
		targetFiles = nil
	}

	// Build a map of target relative paths → SHA for efficient comparison.
	// Using SHA comparison avoids fetching content for files that are already identical
	// and prevents sending no-op "update" actions to the Git provider.
	targetFileInfo := make(map[string]string) // relativePath → SHA
	for _, file := range targetFiles {
		relativePath := trimRelativePath(file.Path, targetPath)
		targetFileInfo[relativePath] = file.SHA
	}

	sourceRelativePaths := make(map[string]struct{})
	var actions []*gitprovider.CommitAction

	for _, file := range sourceFiles {
		relativePath := trimRelativePath(file.Path, sourcePath)
		sourceRelativePaths[relativePath] = struct{}{}

		if IsFileBlocked(relativePath, blockList) {
			prLogger.Debugf("Skipping blocked file %s (matched blockList pattern)", relativePath)
			continue
		}

		targetFilePath := strings.TrimSuffix(targetPath, "/") + "/" + relativePath

		targetSHA, existsInTarget := targetFileInfo[relativePath]
		if existsInTarget && targetSHA != "" && file.SHA != "" && targetSHA == file.SHA {
			// File SHAs match — already in sync, skip.
			continue
		}

		// SHAs differ or file is new — fetch content and create action.
		content, err := provider.GetFileContent(ctx, owner, repo, file.Path, ref)
		if err != nil {
			prLogger.Errorf("Failed to get file content for %s: %v", file.Path, err)
			return nil, err
		}

		encodedContent := base64.StdEncoding.EncodeToString(content)

		action := "create"
		if existsInTarget {
			action = "update"
		}

		actions = append(actions, &gitprovider.CommitAction{
			Action:   action,
			FilePath: targetFilePath,
			Content:  encodedContent,
			Encoding: "base64",
		})
	}

	for _, file := range targetFiles {
		relativePath := trimRelativePath(file.Path, targetPath)
		if _, exists := sourceRelativePaths[relativePath]; !exists {
			if IsFileBlocked(relativePath, blockList) {
				prLogger.Debugf("Skipping deletion of blocked file %s (matched blockList pattern)", relativePath)
				continue
			}
			prLogger.Debugf("%s not found in source %s, marking for deletion", relativePath, sourcePath)
			actions = append(actions, &gitprovider.CommitAction{
				Action:   "delete",
				FilePath: file.Path,
			})
		}
	}

	return actions, nil
}

// GenerateDeleteActions creates delete CommitActions for all files under a path.
func GenerateDeleteActions(
	ctx context.Context,
	provider gitprovider.GitProvider,
	owner, repo, path, ref string,
	prLogger *log.Entry,
) ([]*gitprovider.CommitAction, error) {
	files, err := ListFilesRecursive(ctx, provider, owner, repo, path, ref)
	if err != nil {
		prLogger.Infof("Target directory %s also not found, nothing to delete", path)
		return nil, nil
	}

	var actions []*gitprovider.CommitAction
	for _, file := range files {
		actions = append(actions, &gitprovider.CommitAction{
			Action:   "delete",
			FilePath: file.Path,
		})
	}
	return actions, nil
}
