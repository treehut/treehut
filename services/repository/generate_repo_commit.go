// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/util"
)

func generateRepoCommit(ctx context.Context, repo, templateRepo, generateRepo *repo_model.Repository, tmpDir string) error {
	commitTimeStr := time.Now().Format(time.RFC3339)
	authorSig := repo.Owner.NewGitSig()

	// Because this may call hooks we should pass in the environment
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME="+authorSig.Name,
		"GIT_AUTHOR_EMAIL="+authorSig.Email,
		"GIT_AUTHOR_DATE="+commitTimeStr,
		"GIT_COMMITTER_NAME="+authorSig.Name,
		"GIT_COMMITTER_EMAIL="+authorSig.Email,
		"GIT_COMMITTER_DATE="+commitTimeStr,
	)

	// Clone to temporary path and do the init commit.
	templateRepoPath := templateRepo.RepoPath()
	if err := git.Clone(ctx, templateRepoPath, tmpDir, git.CloneRepoOptions{
		Depth:  1,
		Branch: templateRepo.DefaultBranch,
	}); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}

	if err := util.RemoveAll(path.Join(tmpDir, ".git")); err != nil {
		return fmt.Errorf("remove git dir: %w", err)
	}

	// Variable expansion
	gt, err := checkGiteaTemplate(tmpDir)
	if err != nil {
		return fmt.Errorf("checkGiteaTemplate: %w", err)
	}

	if gt != nil {
		// All file access should be done through `root` to avoid file traversal attacks, especially with symlinks
		root, err := os.OpenRoot(tmpDir)
		if err != nil {
			return fmt.Errorf("open root: %w", err)
		}
		defer root.Close()

		if err := root.Remove(gt.Path); err != nil {
			return fmt.Errorf("remove .giteatemplate: %w", err)
		}

		// Avoid walking tree if there are no globs
		if len(gt.Globs()) > 0 {
			tmpDirSlash := strings.TrimSuffix(filepath.ToSlash(tmpDir), "/") + "/"
			if err := filepath.WalkDir(tmpDirSlash, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}

				if d.IsDir() {
					return nil
				}

				base := strings.TrimPrefix(filepath.ToSlash(path), tmpDirSlash)
				for _, g := range gt.Globs() {
					if g.Match(base) {
						// `path` will be an absolute filepath from `WalkDir`, but `os.Root` requires all accesses are
						// relative file paths from the root -- use `relPath` from here out.
						relPath, err := filepath.Rel(tmpDir, path)
						if err != nil {
							return err
						}

						content, err := root.ReadFile(relPath)
						if err != nil {
							return err
						}

						if err := root.WriteFile(relPath,
							[]byte(generateExpansion(string(content), templateRepo, generateRepo, false)),
							0o644); err != nil {
							return err
						}

						substPath := generateExpansion(relPath, templateRepo, generateRepo, true)

						// Create parent subdirectories if needed or continue silently if it exists
						if err := root.MkdirAll(filepath.Dir(substPath), 0o755); err != nil {
							return err
						}

						// Substitute filename variables
						if err := root.Rename(relPath, substPath); err != nil {
							return err
						}

						break
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}

		// Before template expansion, .git was removed so that a fresh repo can be initialized; remove it again in case
		// some template variable usage has conflicted with this directory and impacts git operations.
		if err := root.RemoveAll(".git"); err != nil {
			return fmt.Errorf("unable to remove .git folder")
		}
	}

	if err := git.InitRepository(ctx, tmpDir, false, templateRepo.ObjectFormatName); err != nil {
		return err
	}

	repoPath := repo.RepoPath()
	if stdout, _, err := git.NewCommand(ctx, "remote", "add", "origin").AddDynamicArguments(repoPath).
		SetDescription(fmt.Sprintf("generateRepoCommit (git remote add): %s to %s", templateRepoPath, tmpDir)).
		RunStdString(&git.RunOpts{Dir: tmpDir, Env: env}); err != nil {
		log.Error("Unable to add %v as remote origin to temporary repo to %s: stdout %s\nError: %v", repo, tmpDir, stdout, err)
		return fmt.Errorf("git remote add: %w", err)
	}

	// set default branch based on whether it's specified in the newly generated repo or not
	defaultBranch := repo.DefaultBranch
	if strings.TrimSpace(defaultBranch) == "" {
		defaultBranch = templateRepo.DefaultBranch
	}

	return initRepoCommit(ctx, tmpDir, repo, repo.Owner, defaultBranch)
}

func checkGiteaTemplate(tmpDir string) (*GiteaTemplate, error) {
	configDirs := []string{".forgejo", ".gitea"}
	var templateFilePath string

	// All file access should be done through `root` to avoid file traversal attacks, especially with symlinks
	root, err := os.OpenRoot(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}
	defer root.Close()

	for _, dir := range configDirs {
		candidatePath := filepath.Join(dir, "template")
		if _, err := root.Stat(candidatePath); err == nil {
			templateFilePath = candidatePath
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	if templateFilePath == "" {
		return nil, nil
	}

	content, err := root.ReadFile(templateFilePath)
	if err != nil {
		return nil, err
	}

	return &GiteaTemplate{
		Path:    templateFilePath,
		Content: content,
	}, nil
}
