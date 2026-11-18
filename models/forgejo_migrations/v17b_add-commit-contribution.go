// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations

import (
	"code.forgejo.org/xorm/xorm"
	"code.forgejo.org/xorm/xorm/schemas"
)

func init() {
	registerMigration(&Migration{
		Description: "add commit_contribution table for the GitHub-style contribution heatmap",
		Upgrade:     addCommitContribution,
	})
}

type v17bCommitContribution struct {
	ID             int64  `xorm:"pk autoincr"`
	RepoID         int64  `xorm:"UNIQUE(s) NOT NULL"`
	CommitSHA      string `xorm:"VARCHAR(64) UNIQUE(s) NOT NULL"`
	Email          string `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"`
	AuthorUnix     int64  `xorm:"NOT NULL"`
	AuthorTZOffset int    `xorm:"NOT NULL DEFAULT 0"`
}

func (c *v17bCommitContribution) TableName() string {
	return "commit_contribution"
}

func (c *v17bCommitContribution) TableIndices() []*schemas.Index {
	emailIndex := schemas.NewIndex("email_author_unix", schemas.IndexType)
	emailIndex.AddColumn("email", "author_unix")
	repoIndex := schemas.NewIndex("repo_id_author_unix", schemas.IndexType)
	repoIndex.AddColumn("repo_id", "author_unix")
	return []*schemas.Index{emailIndex, repoIndex}
}

func addCommitContribution(x *xorm.Engine) error {
	_, err := x.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true}, new(v17bCommitContribution))
	return err
}
