// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo_test

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryPublicMirrorCounts(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	firstRepoID := func(t *testing.T, orderBy db.SearchOrderBy) int64 {
		t.Helper()
		repos, _, err := repo_model.SearchRepository(ctx, &repo_model.SearchRepoOptions{
			ListOptions: db.ListOptions{Page: 1, PageSize: 1},
			OrderBy:     orderBy,
			Private:     false,
		})
		require.NoError(t, err)
		require.Len(t, repos, 1)
		return repos[0].ID
	}
	assertCounts := func(t *testing.T, has bool, stars, forks, watches int) {
		t.Helper()
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		assert.Equal(t, has, repo.HasPublicMirror)
		assert.Equal(t, stars, repo.DisplayNumStars())
		assert.Equal(t, forks, repo.DisplayNumForks())
		assert.Equal(t, watches, repo.DisplayNumWatches())
	}

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	localStars, localForks, localWatches := repo.NumStars, repo.NumForks, repo.NumWatches
	assertCounts(t, false, localStars, localForks, localWatches)
	assert.NotEqual(t, int64(1), firstRepoID(t, db.SearchOrderByStarsReverse))
	assert.NotEqual(t, int64(1), firstRepoID(t, db.SearchOrderByForksReverse))

	m := &repo_model.PushMirror{RepoID: 1, RemoteName: "public", RemoteAddress: "https://github.com/owner/repo"}
	require.NoError(t, db.Insert(ctx, m))
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, m, repo_model.PublicMirrorProviderGitHub))
	assertCounts(t, true, 0, 0, 0)

	// the sync stores the upstream counts on the repository, the local ones are kept
	m.PublicStars, m.PublicForks, m.PublicWatchers = 1234, 56, 7
	require.NoError(t, repo_model.UpdatePushMirrorPublicStatus(ctx, m))
	assertCounts(t, true, 1234, 56, 7)
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.Equal(t, localStars, repo.NumStars)
	assert.Equal(t, localForks, repo.NumForks)
	assert.Equal(t, localWatches, repo.NumWatches)

	// and they are used for sorting
	assert.Equal(t, int64(1), firstRepoID(t, db.SearchOrderByStarsReverse))
	assert.Equal(t, int64(1), firstRepoID(t, db.SearchOrderByForksReverse))
	assert.NotEqual(t, int64(1), firstRepoID(t, db.SearchOrderByStars))

	// disabling the public mirror goes back to the local counts
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, m, repo_model.PublicMirrorProviderNone))
	assertCounts(t, false, localStars, localForks, localWatches)
	assert.NotEqual(t, int64(1), firstRepoID(t, db.SearchOrderByStarsReverse))

	// enabling it again restores the last synced counts
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, m, repo_model.PublicMirrorProviderGitHub))
	assertCounts(t, true, 1234, 56, 7)

	// deleting another push mirror keeps them, deleting the public mirror clears them
	other := &repo_model.PushMirror{RepoID: 1, RemoteName: "other", RemoteAddress: "https://example.com/a/b"}
	require.NoError(t, db.Insert(ctx, other))
	require.NoError(t, repo_model.DeletePushMirrors(ctx, repo_model.PushMirrorOptions{ID: other.ID, RepoID: 1}))
	assertCounts(t, true, 1234, 56, 7)
	require.NoError(t, repo_model.DeletePushMirrors(ctx, repo_model.PushMirrorOptions{ID: m.ID, RepoID: 1}))
	assertCounts(t, false, localStars, localForks, localWatches)
}
