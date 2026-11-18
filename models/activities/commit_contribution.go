// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package activities

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/modules/container"
	"forgejo.org/modules/timeutil"

	"code.forgejo.org/xorm/xorm/schemas"
)

// CommitContribution records that a commit, reachable from the default branch of a repository, was authored (or
// co-authored) by the given email. It is used to compute the contribution heatmap the way GitHub does: commits are
// counted by their author date, in the time zone recorded in the commit.
type CommitContribution struct {
	ID             int64              `xorm:"pk autoincr"`
	RepoID         int64              `xorm:"UNIQUE(s) NOT NULL"`
	CommitSHA      string             `xorm:"VARCHAR(64) UNIQUE(s) NOT NULL"`
	Email          string             `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"` // lower-cased
	AuthorUnix     timeutil.TimeStamp `xorm:"NOT NULL"`
	AuthorTZOffset int                `xorm:"NOT NULL DEFAULT 0"` // seconds east of UTC
}

func init() {
	db.RegisterModel(new(CommitContribution))
}

// TableIndices implements xorm's TableIndices interface.
func (c *CommitContribution) TableIndices() []*schemas.Index {
	emailIndex := schemas.NewIndex("email_author_unix", schemas.IndexType)
	emailIndex.AddColumn("email", "author_unix")
	repoIndex := schemas.NewIndex("repo_id_author_unix", schemas.IndexType)
	repoIndex.AddColumn("repo_id", "author_unix")
	return []*schemas.Index{emailIndex, repoIndex}
}

func (c *CommitContribution) key() string {
	return c.CommitSHA + "\x00" + c.Email
}

const commitContributionBatchSize = 100

func insertCommitContributions(ctx context.Context, rows []*CommitContribution) error {
	for start := 0; start < len(rows); start += commitContributionBatchSize {
		end := min(start+commitContributionBatchSize, len(rows))
		if err := db.Insert(ctx, rows[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// InsertCommitContributions inserts the rows of a repository which do not exist yet.
func InsertCommitContributions(ctx context.Context, repoID int64, rows []*CommitContribution) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		existing, err := existingCommitContributionKeys(ctx, repoID)
		if err != nil {
			return err
		}
		missing := make([]*CommitContribution, 0, len(rows))
		for _, row := range rows {
			row.RepoID = repoID
			if existing.Add(row.key()) {
				missing = append(missing, row)
			}
		}
		return insertCommitContributions(ctx, missing)
	})
}

// ReplaceRepoCommitContributions makes the stored rows of a repository identical to the given ones.
func ReplaceRepoCommitContributions(ctx context.Context, repoID int64, rows []*CommitContribution) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		existing := make(map[string]int64)
		if err := db.GetEngine(ctx).Table("commit_contribution").Where("repo_id = ?", repoID).
			Iterate(new(CommitContribution), func(_ int, bean any) error {
				c := bean.(*CommitContribution)
				existing[c.key()] = c.ID
				return nil
			}); err != nil {
			return err
		}

		wanted := make(container.Set[string], len(rows))
		missing := make([]*CommitContribution, 0)
		for _, row := range rows {
			row.RepoID = repoID
			key := row.key()
			if !wanted.Add(key) {
				continue
			}
			if _, ok := existing[key]; !ok {
				missing = append(missing, row)
			}
		}

		obsolete := make([]int64, 0)
		for key, id := range existing {
			if !wanted.Contains(key) {
				obsolete = append(obsolete, id)
			}
		}
		for start := 0; start < len(obsolete); start += commitContributionBatchSize {
			end := min(start+commitContributionBatchSize, len(obsolete))
			if _, err := db.GetEngine(ctx).In("id", obsolete[start:end]).Delete(new(CommitContribution)); err != nil {
				return err
			}
		}

		return insertCommitContributions(ctx, missing)
	})
}

// DeleteCommitContributionsByRepo removes all rows of a repository.
func DeleteCommitContributionsByRepo(ctx context.Context, repoID int64) error {
	_, err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Delete(new(CommitContribution))
	return err
}

func existingCommitContributionKeys(ctx context.Context, repoID int64) (container.Set[string], error) {
	existing := make(container.Set[string])
	err := db.GetEngine(ctx).Table("commit_contribution").Where("repo_id = ?", repoID).
		Cols("commit_sha", "email").
		Iterate(new(CommitContribution), func(_ int, bean any) error {
			existing.Add(bean.(*CommitContribution).key())
			return nil
		})
	return existing, err
}
