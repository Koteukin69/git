// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package contribution

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/git"
	"forgejo.org/modules/log"
	"forgejo.org/modules/sync"

	"xorm.io/builder"
)

// repoPool makes sure that only one sync runs per repository at a time.
var repoPool = sync.NewExclusivePool()

func isEmptyCommitID(id string) bool {
	return id == "" || strings.Trim(id, "0") == ""
}

// shouldTrack reports whether the commits of a repository can count as contributions. Like on GitHub, commits made
// in forks do not count.
func shouldTrack(repo *repo_model.Repository) bool {
	return !repo.IsFork && !repo.IsEmpty && repo.DefaultBranch != ""
}

// FullSync makes the stored contributions of a repository match the commits reachable from its default branch.
func FullSync(ctx context.Context, repo *repo_model.Repository) error {
	key := strconv.FormatInt(repo.ID, 10)
	repoPool.CheckIn(key)
	defer repoPool.CheckOut(key)

	return fullSync(ctx, repo)
}

func fullSync(ctx context.Context, repo *repo_model.Repository) error {
	if !shouldTrack(repo) {
		return activities_model.DeleteCommitContributionsByRepo(ctx, repo.ID)
	}

	branchRef := git.BranchPrefix + repo.DefaultBranch
	if !git.IsBranchExist(ctx, repo.RepoPath(), repo.DefaultBranch) {
		return activities_model.DeleteCommitContributionsByRepo(ctx, repo.ID)
	}

	rows, err := readCommitContributions(ctx, repo.RepoPath(), branchRef)
	if err != nil {
		return err
	}
	return activities_model.ReplaceRepoCommitContributions(ctx, repo.ID, rows)
}

// IncrementalSync records the commits added to the default branch by a push from oldCommitID to newCommitID.
// It falls back to a full sync when the push was not a fast-forward.
func IncrementalSync(ctx context.Context, repo *repo_model.Repository, oldCommitID, newCommitID string) error {
	key := strconv.FormatInt(repo.ID, 10)
	repoPool.CheckIn(key)
	defer repoPool.CheckOut(key)

	if !shouldTrack(repo) || isEmptyCommitID(oldCommitID) || isEmptyCommitID(newCommitID) {
		return fullSync(ctx, repo)
	}

	isAncestor, err := isAncestor(ctx, repo.RepoPath(), oldCommitID, newCommitID)
	if err != nil {
		return err
	}
	if !isAncestor {
		// force push: commits might have disappeared from the default branch
		return fullSync(ctx, repo)
	}

	rows, err := readCommitContributions(ctx, repo.RepoPath(), oldCommitID+".."+newCommitID)
	if err != nil {
		return err
	}
	return activities_model.InsertCommitContributions(ctx, repo.ID, rows)
}

func isAncestor(ctx context.Context, repoPath, ancestor, descendant string) (bool, error) {
	_, _, err := git.NewCommand(ctx, "merge-base", "--is-ancestor").
		AddDynamicArguments(ancestor, descendant).
		RunStdString(&git.RunOpts{Dir: repoPath})
	if err == nil {
		return true, nil
	}
	if git.IsErrorExitCode(err, 1) {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor %s %s: %w", ancestor, descendant, err)
}

// SyncAllRepositories rebuilds the contributions of every repository.
func SyncAllRepositories(ctx context.Context) error {
	return db.Iterate(ctx, builder.NewCond(), func(ctx context.Context, repo *repo_model.Repository) error {
		select {
		case <-ctx.Done():
			return db.ErrCancelledf("before syncing commit contributions of %s", repo.FullName())
		default:
		}
		if err := FullSync(ctx, repo); err != nil {
			log.Error("Unable to sync commit contributions of %-v: %v", repo, err)
		}
		return nil
	})
}
