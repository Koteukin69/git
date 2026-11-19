// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations

import (
	"testing"

	migration_tests "forgejo.org/models/gitea_migrations/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_addRepositoryPublicMirrorCounts(t *testing.T) {
	type Repository struct {
		ID         int64 `xorm:"pk autoincr"`
		NumStars   int
		NumForks   int
		NumWatches int
	}
	type PushMirror struct {
		ID             int64 `xorm:"pk autoincr"`
		RepoID         int64
		PublicProvider string `xorm:"VARCHAR(20) NOT NULL DEFAULT ''"`
		PublicStars    int    `xorm:"NOT NULL DEFAULT 0"`
		PublicForks    int    `xorm:"NOT NULL DEFAULT 0"`
		PublicWatchers int    `xorm:"NOT NULL DEFAULT 0"`
	}

	x, deferable := migration_tests.PrepareTestEnv(t, 0, new(Repository), new(PushMirror))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	_, err := x.Insert(
		&Repository{ID: 1, NumStars: 1, NumForks: 2, NumWatches: 3},
		&Repository{ID: 2, NumStars: 4},
		&PushMirror{RepoID: 1, PublicProvider: "github", PublicStars: 100, PublicForks: 20, PublicWatchers: 5},
		&PushMirror{RepoID: 2, PublicStars: 9}, // not a public mirror
	)
	require.NoError(t, err)

	require.NoError(t, addRepositoryPublicMirrorCounts(x))

	type result struct {
		ID               int64
		NumStars         int
		HasPublicMirror  bool
		PublicNumStars   int
		PublicNumForks   int
		PublicNumWatches int
	}
	var repos []result
	require.NoError(t, x.Table("repository").OrderBy("id").Find(&repos))
	assert.Equal(t, []result{
		{ID: 1, NumStars: 1, HasPublicMirror: true, PublicNumStars: 100, PublicNumForks: 20, PublicNumWatches: 5},
		{ID: 2, NumStars: 4},
	}, repos)
}
