// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities_test

import (
	"testing"
	"time"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func heatmapDay(date string, contributions int64) *activities_model.UserHeatmapData {
	day, _ := time.Parse("2006-01-02", date)
	return &activities_model.UserHeatmapData{
		Timestamp:     timeutil.TimeStamp(day.Add(12 * time.Hour).Unix()),
		Date:          date,
		Contributions: contributions,
	}
}

func commitAt(repoID int64, sha, email string, at time.Time) *activities_model.CommitContribution {
	_, offset := at.Zone()
	return &activities_model.CommitContribution{
		RepoID:         repoID,
		CommitSHA:      sha,
		Email:          email,
		AuthorUnix:     timeutil.TimeStamp(at.Unix()),
		AuthorTZOffset: offset,
	}
}

func TestGetUserHeatmapDataByUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	timeutil.MockSet(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
	defer timeutil.MockUnset()
	defer test.MockVariableValue(&setting.DefaultUILocation, time.UTC)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	moscow := time.FixedZone("", 3*3600)
	newYork := time.FixedZone("", -5*3600)

	// repo 1: public, owned by user 2 / repo 2: private, owned by user 2 / repo 11: public fork
	require.NoError(t, db.Insert(db.DefaultContext, []*activities_model.CommitContribution{
		// 20:30 UTC, but 23:30 on the 20th in the commit's time zone
		commitAt(1, "a", "user2@example.com", time.Date(2020, 10, 20, 23, 30, 0, 0, moscow)),
		// 03:30 UTC on the 21st, but 22:30 on the 20th in the commit's time zone
		commitAt(1, "b", "user2@example.com", time.Date(2020, 10, 20, 22, 30, 0, 0, newYork)),
		// co-authored by another email of the same user: counted once
		commitAt(1, "b", user2.GetPlaceholderEmail(), time.Date(2020, 10, 20, 22, 30, 0, 0, newYork)),
		// unverified email
		commitAt(1, "c", "user2-2@example.com", time.Date(2020, 10, 22, 12, 0, 0, 0, time.UTC)),
		// private repository
		commitAt(2, "d", "user2@example.com", time.Date(2020, 11, 1, 12, 0, 0, 0, time.UTC)),
		// fork
		commitAt(11, "e", "user2@example.com", time.Date(2020, 11, 2, 12, 0, 0, 0, time.UTC)),
		// too old
		commitAt(1, "f", "user2@example.com", time.Date(2019, 6, 1, 12, 0, 0, 0, time.UTC)),
		// somebody else
		commitAt(1, "g", "user4@example.com", time.Date(2020, 11, 3, 12, 0, 0, 0, time.UTC)),
	}))
	_, err := db.GetEngine(db.DefaultContext).NoAutoTime().Insert([]*activities_model.Action{
		{
			UserID: 2, ActUserID: 2, RepoID: 1, OpType: activities_model.ActionCreateIssue,
			CreatedUnix: timeutil.TimeStamp(time.Date(2020, 12, 1, 10, 0, 0, 0, time.UTC).Unix()),
		},
		{
			// a plain comment does not count
			UserID: 2, ActUserID: 2, RepoID: 1, OpType: activities_model.ActionCommentIssue,
			CreatedUnix: timeutil.TimeStamp(time.Date(2020, 12, 2, 10, 0, 0, 0, time.UTC).Unix()),
		},
		{
			// issues opened in forks do not count
			UserID: 2, ActUserID: 2, RepoID: 11, OpType: activities_model.ActionCreateIssue,
			CreatedUnix: timeutil.TimeStamp(time.Date(2020, 12, 3, 10, 0, 0, 0, time.UTC).Unix()),
		},
	})
	require.NoError(t, err)

	public := []*activities_model.UserHeatmapData{heatmapDay("2020-10-20", 2), heatmapDay("2020-12-01", 1)}
	all := []*activities_model.UserHeatmapData{heatmapDay("2020-10-20", 2), heatmapDay("2020-11-01", 1), heatmapDay("2020-12-01", 1)}

	testCases := []struct {
		desc     string
		userID   int64
		doerID   int64
		expected []*activities_model.UserHeatmapData
	}{
		{"self sees commits in private repo", 2, 2, all},
		{"admin sees commits in private repo", 2, 1, all},
		{"other user does not see commits in private repo", 2, 3, public},
		{"nobody does not see commits in private repo", 2, 0, public},
		{"closing an issue does not count", 16, 16, []*activities_model.UserHeatmapData{}},
		{"repository creations are grouped by day", 10, 10, []*activities_model.UserHeatmapData{heatmapDay("2020-10-18", 3)}},
		{"test cutoff within", 40, 40, []*activities_model.UserHeatmapData{heatmapDay("2019-12-27", 1)}},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: tc.userID})
			var doer *user_model.User
			if tc.doerID != 0 {
				doer = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: tc.doerID})
			}

			heatmap, err := activities_model.GetUserHeatmapDataByUser(db.DefaultContext, user, doer)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, heatmap)
		})
	}
}

func TestReplaceRepoCommitContributions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := db.DefaultContext
	at := time.Date(2020, 10, 20, 12, 0, 0, 0, time.UTC)

	require.NoError(t, activities_model.ReplaceRepoCommitContributions(ctx, 1, []*activities_model.CommitContribution{
		commitAt(0, "a", "one@example.com", at),
		commitAt(0, "b", "one@example.com", at),
		commitAt(0, "b", "two@example.com", at),
	}))
	unittest.AssertCount(t, &activities_model.CommitContribution{RepoID: 1}, 3)

	// "a" was removed by a force push, "c" was added
	require.NoError(t, activities_model.ReplaceRepoCommitContributions(ctx, 1, []*activities_model.CommitContribution{
		commitAt(0, "b", "one@example.com", at),
		commitAt(0, "b", "two@example.com", at),
		commitAt(0, "c", "one@example.com", at),
	}))
	unittest.AssertCount(t, &activities_model.CommitContribution{RepoID: 1}, 3)
	unittest.AssertNotExistsBean(t, &activities_model.CommitContribution{RepoID: 1, CommitSHA: "a"})
	unittest.AssertExistsAndLoadBean(t, &activities_model.CommitContribution{RepoID: 1, CommitSHA: "c"})

	// incremental inserts ignore existing rows
	require.NoError(t, activities_model.InsertCommitContributions(ctx, 1, []*activities_model.CommitContribution{
		commitAt(0, "c", "one@example.com", at),
		commitAt(0, "d", "one@example.com", at),
	}))
	unittest.AssertCount(t, &activities_model.CommitContribution{RepoID: 1}, 4)

	require.NoError(t, activities_model.DeleteCommitContributionsByRepo(ctx, 1))
	unittest.AssertCount(t, &activities_model.CommitContribution{RepoID: 1}, 0)
}
