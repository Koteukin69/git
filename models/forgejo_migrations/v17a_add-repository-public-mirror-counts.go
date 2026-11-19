// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations

import (
	"code.forgejo.org/xorm/xorm"
)

func init() {
	registerMigration(&Migration{
		Description: "add the counts of the public mirror to repositories",
		Upgrade:     addRepositoryPublicMirrorCounts,
	})
}

func addRepositoryPublicMirrorCounts(x *xorm.Engine) error {
	type Repository struct {
		HasPublicMirror  bool `xorm:"INDEX NOT NULL DEFAULT false"`
		PublicNumStars   int  `xorm:"NOT NULL DEFAULT 0"`
		PublicNumForks   int  `xorm:"NOT NULL DEFAULT 0"`
		PublicNumWatches int  `xorm:"NOT NULL DEFAULT 0"`
	}
	if _, err := x.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true}, new(Repository)); err != nil {
		return err
	}

	// copy the counts of the existing public mirrors, a repository has at most one
	type PushMirror struct {
		ID             int64
		RepoID         int64
		PublicProvider string
		PublicStars    int
		PublicForks    int
		PublicWatchers int
	}
	mirrors := make([]*PushMirror, 0, 10)
	if err := x.Table("push_mirror").Where("public_provider <> ?", "").OrderBy("id").Find(&mirrors); err != nil {
		return err
	}
	done := make(map[int64]bool, len(mirrors))
	for _, m := range mirrors {
		if done[m.RepoID] {
			continue
		}
		done[m.RepoID] = true
		if _, err := x.Table("repository").Where("id = ?", m.RepoID).Update(map[string]any{
			"has_public_mirror":  true,
			"public_num_stars":   m.PublicStars,
			"public_num_forks":   m.PublicForks,
			"public_num_watches": m.PublicWatchers,
		}); err != nil {
			return err
		}
	}
	return nil
}
