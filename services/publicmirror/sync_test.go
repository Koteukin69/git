// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/timeutil"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func allowLocalNetworks(t *testing.T) {
	t.Cleanup(test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true))
	require.NoError(t, migrations_allowlist.Init())
	t.Cleanup(func() { _ = migrations_allowlist.Init() })
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=contributor", "GIT_AUTHOR_EMAIL=contributor@example.com",
		"GIT_COMMITTER_NAME=contributor", "GIT_COMMITTER_EMAIL=contributor@example.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

type syncFixture struct {
	upstream   *fakeUpstream
	repo       *repo_model.Repository
	mirror     *repo_model.PushMirror
	queued     []int64
	featureSHA string
	masterSHA  string
}

// setupSync creates an upstream (a copy of user2/repo1 with an extra commit only reachable from refs/pull/7/head) and
// a public push mirror of user2/repo1 pointing to it.
func setupSync(t *testing.T, flavor string) *syncFixture {
	unittest.PrepareTestEnv(t)
	allowLocalNetworks(t)

	f := &syncFixture{upstream: newFakeUpstream(t, flavor)}
	t.Cleanup(test.MockVariableValue(&addToTaskQueue, func(_ context.Context, pr *issues_model.PullRequest) {
		f.queued = append(f.queued, pr.ID)
	}))

	f.repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, os.MkdirAll(filepath.Dir(f.upstream.barePath()), 0o755))
	runGit(t, "", "clone", "--bare", "--quiet", f.repo.RepoPath(), f.upstream.barePath())
	f.masterSHA = runGit(t, f.upstream.barePath(), "rev-parse", "refs/heads/master")

	work := t.TempDir()
	runGit(t, "", "clone", "--quiet", f.upstream.barePath(), work)
	require.NoError(t, os.WriteFile(filepath.Join(work, "feature.txt"), []byte("feature\n"), 0o644))
	runGit(t, work, "add", "feature.txt")
	runGit(t, work, "commit", "--quiet", "-m", "add feature")
	f.featureSHA = runGit(t, work, "rev-parse", "HEAD")
	runGit(t, work, "push", "--quiet", "origin", "HEAD:refs/pull/7/head")

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	octocat := &User{ID: 101, Login: "octocat"}
	contributor := &User{ID: 202, Login: "contributor"}
	merged := created.Add(time.Hour)
	mergeCommit := f.masterSHA
	up := f.upstream
	up.repoInfo = map[string]any{
		"html_url": up.server.URL + "/owner/repo",
		// GitHub
		"stargazers_count": 42, "subscribers_count": 7, "watchers_count": 42,
		// Forgejo
		"stars_count": 42,
		"forks_count": 5,
	}
	if flavor == "forgejo" {
		up.repoInfo["watchers_count"] = 7
	}
	up.issues = []*Issue{
		{Number: 3, Title: "collides with a local issue", Body: "body of 3", State: "open", User: octocat, CreatedAt: created, UpdatedAt: created},
		{Number: 100, Title: "a bug", Body: "it does not work", State: "open", User: octocat, CreatedAt: created, UpdatedAt: created.Add(time.Minute)},
		{Number: 7, Title: "add a feature", Body: "please merge", State: "open", User: contributor, CreatedAt: created, UpdatedAt: created.Add(2 * time.Minute), PullRequest: &PullRequestRef{}},
		{Number: 8, Title: "merged one", State: "closed", User: contributor, CreatedAt: created, UpdatedAt: created.Add(3 * time.Minute), ClosedAt: &merged, PullRequest: &PullRequestRef{Merged: true, MergedAt: &merged}},
	}
	up.pulls[7] = &PullRequest{Number: 7, Head: PullBranch{Label: "contributor:feature", Ref: "feature", SHA: f.featureSHA}, Base: PullBranch{Ref: "master", SHA: f.masterSHA}}
	up.pulls[8] = &PullRequest{Number: 8, Merged: true, MergedAt: &merged, MergeCommitSHA: &mergeCommit, Head: PullBranch{Label: "contributor:old", Ref: "old", SHA: f.masterSHA}, Base: PullBranch{Ref: "master", SHA: f.masterSHA}}
	up.comments = []*Comment{
		{ID: 9001, Body: "me too", User: contributor, IssueURL: up.server.URL + "/api/v3/repos/owner/repo/issues/100", CreatedAt: created.Add(time.Minute), UpdatedAt: created.Add(time.Minute)},
		{ID: 9002, Body: "LGTM", User: octocat, IssueURL: up.server.URL + "/api/v3/repos/owner/repo/issues/7", CreatedAt: created.Add(2 * time.Minute), UpdatedAt: created.Add(2 * time.Minute)},
		{ID: 9003, Body: "orphan", User: octocat, IssueURL: up.server.URL + "/api/v3/repos/owner/repo/issues/999", CreatedAt: created, UpdatedAt: created},
	}

	// the push mirror, as created from the settings: remote with credentials in the git config
	f.mirror = &repo_model.PushMirror{
		RepoID:        f.repo.ID,
		RemoteName:    "remote_mirror_public",
		RemoteAddress: up.webURL(),
		Interval:      time.Hour,
	}
	require.NoError(t, db.Insert(t.Context(), f.mirror))
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(t.Context(), f.mirror, flavor))
	withCreds := strings.Replace(up.webURL(), "://", "://pusher:secret-token@", 1)
	runGit(t, f.repo.RepoPath(), "remote", "add", f.mirror.RemoteName, withCreds)
	return f
}

func (f *syncFixture) sync(t *testing.T) *repo_model.PushMirror {
	t.Helper()
	require.NoError(t, Sync(t.Context(), f.mirror.ID))
	return unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: f.mirror.ID})
}

func (f *syncFixture) issue(t *testing.T, number int64) *issues_model.Issue {
	t.Helper()
	m, err := repo_model.GetPublicMirrorIssue(t.Context(), f.repo.ID, f.upstream.upstreamSource(), number)
	require.NoError(t, err)
	require.NotNil(t, m, "upstream #%d is not synced", number)
	issue, err := issues_model.GetIssueByID(t.Context(), m.IssueID)
	require.NoError(t, err)
	return issue
}

func (f *fakeUpstream) upstreamSource() string {
	return strings.TrimPrefix(f.server.URL, "http://") + "/owner/repo"
}

func TestSyncPublicMirror(t *testing.T) {
	for _, flavor := range []string{repo_model.PublicMirrorProviderGitHub, repo_model.PublicMirrorProviderForgejo} {
		t.Run(flavor, func(t *testing.T) {
			testSyncPublicMirror(t, flavor)
		})
	}
}

func testSyncPublicMirror(t *testing.T, flavor string) {
	f := setupSync(t, flavor)
	ctx := t.Context()

	localIssue3 := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: f.repo.ID, Index: 3})
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: f.repo.ID, Index: 100})
	issuesBefore := unittest.GetCount(t, &issues_model.Issue{RepoID: f.repo.ID})
	branchesBefore := runGit(t, f.repo.RepoPath(), "for-each-ref", "refs/heads/")

	m := f.sync(t)
	assert.Empty(t, m.PublicLastError)
	assert.Equal(t, 42, m.PublicStars)
	assert.Equal(t, 5, m.PublicForks)
	assert.Equal(t, 7, m.PublicWatchers)
	assert.NotZero(t, m.PublicSyncedUnix)
	assert.NotZero(t, m.PublicLastSyncUnix)

	t.Run("credentials of the mirror are used for the API", func(t *testing.T) {
		defer f.upstream.lock()()
		for _, h := range f.upstream.authHeaders {
			assert.Equal(t, "token secret-token", h)
		}
	})

	t.Run("issues", func(t *testing.T) {
		assert.Equal(t, issuesBefore+4, unittest.GetCount(t, &issues_model.Issue{RepoID: f.repo.ID}))

		bug := f.issue(t, 100)
		assert.EqualValues(t, 100, bug.Index, "a free upstream number is kept")
		assert.Equal(t, "a bug", bug.Title)
		assert.Equal(t, "it does not work", bug.Content)
		assert.EqualValues(t, user_model.GhostUserID, bug.PosterID)
		assert.Equal(t, "octocat", bug.OriginalAuthor)
		assert.EqualValues(t, 101, bug.OriginalAuthorID)
		assert.False(t, bug.IsClosed)
		assert.False(t, bug.IsPull)

		collided := f.issue(t, 3)
		assert.NotEqual(t, localIssue3.ID, collided.ID)
		assert.NotEqual(t, int64(3), collided.Index, "a used number gets the next free index")
		assert.Equal(t, "collides with a local issue", collided.Title)

		// local numbering continues after the synced issues
		next, err := db.GetNextResourceIndex(ctx, "issue_index", f.repo.ID)
		require.NoError(t, err)
		assert.Greater(t, next, int64(100))
	})

	t.Run("pull requests", func(t *testing.T) {
		issue := f.issue(t, 7)
		require.True(t, issue.IsPull)
		pr, err := issues_model.GetPullRequestByIssueID(ctx, issue.ID)
		require.NoError(t, err)
		assert.Equal(t, issues_model.PullRequestFlowAGit, pr.Flow)
		assert.Equal(t, "contributor:feature", pr.HeadBranch)
		assert.Equal(t, "master", pr.BaseBranch)
		assert.Equal(t, f.repo.ID, pr.HeadRepoID)
		assert.False(t, pr.HasMerged)
		assert.Equal(t, f.masterSHA, pr.MergeBase)
		assert.Contains(t, f.queued, pr.ID, "the mergeability of open pull requests is checked")
		assert.Equal(t, f.featureSHA, runGit(t, f.repo.RepoPath(), "rev-parse", pr.GetGitRefName()),
			"the head is fetched into refs/pull/<index>/head")
		assert.Equal(t, branchesBefore, runGit(t, f.repo.RepoPath(), "for-each-ref", "refs/heads/"), "no branch is created, it would be pushed back")

		mergedIssue := f.issue(t, 8)
		merged, err := issues_model.GetPullRequestByIssueID(ctx, mergedIssue.ID)
		require.NoError(t, err)
		assert.True(t, merged.HasMerged)
		assert.True(t, mergedIssue.IsClosed)
		assert.Equal(t, f.masterSHA, merged.MergedCommitID)
		assert.NotContains(t, f.queued, merged.ID)
	})

	t.Run("comments", func(t *testing.T) {
		bug := f.issue(t, 100)
		assert.Equal(t, 1, bug.NumComments)
		comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: bug.ID, Type: issues_model.CommentTypeComment})
		assert.Equal(t, "me too", comment.Content)
		assert.Equal(t, "contributor", comment.OriginalAuthor)
		assert.EqualValues(t, user_model.GhostUserID, comment.PosterID)

		pull := f.issue(t, 7)
		unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: pull.ID, Content: "LGTM"})
		unittest.AssertNotExistsBean(t, &issues_model.Comment{Content: "orphan"})
	})

	t.Run("second sync is incremental and idempotent", func(t *testing.T) {
		issues := unittest.GetCount(t, &issues_model.Issue{RepoID: f.repo.ID})
		comments := unittest.GetCount(t, &issues_model.Comment{})
		f.queued = nil
		m := f.sync(t)
		assert.Empty(t, m.PublicLastError)
		assert.Equal(t, issues, unittest.GetCount(t, &issues_model.Issue{RepoID: f.repo.ID}))
		assert.Equal(t, comments, unittest.GetCount(t, &issues_model.Comment{}))
		defer f.upstream.lock()()
		assert.NotEmpty(t, f.upstream.sinceParams[len(f.upstream.sinceParams)-1], "the second sync only asks for updated items")
	})

	t.Run("updates are synced", func(t *testing.T) {
		later := time.Now().Add(time.Minute)
		unlock := f.upstream.lock()
		bug := f.upstream.issues[1]
		bug.State, bug.Title, bug.UpdatedAt, bug.ClosedAt = "closed", "a bug (fixed)", later, &later
		f.upstream.comments[0].Body, f.upstream.comments[0].UpdatedAt = "me too, edited", later
		f.upstream.comments = append(f.upstream.comments, &Comment{ID: 9004, Body: "new comment", User: &User{ID: 101, Login: "octocat"}, HTMLURL: "https://example.com/owner/repo/issues/100#issuecomment-9004", CreatedAt: later, UpdatedAt: later})
		f.upstream.repoInfo["stargazers_count"], f.upstream.repoInfo["stars_count"] = 43, 43
		unlock()

		m := f.sync(t)
		assert.Empty(t, m.PublicLastError)
		assert.Equal(t, 43, m.PublicStars)
		issue := f.issue(t, 100)
		assert.True(t, issue.IsClosed)
		assert.Equal(t, "a bug (fixed)", issue.Title)
		assert.EqualValues(t, later.Unix(), issue.ClosedUnix)
		assert.Equal(t, 2, issue.NumComments)
		unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: issue.ID, Content: "me too, edited"})
		unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: issue.ID, Content: "new comment"})
	})

	t.Run("new head of a pull request", func(t *testing.T) {
		work := t.TempDir()
		runGit(t, "", "clone", "--quiet", f.upstream.barePath(), work)
		runGit(t, work, "fetch", "--quiet", "origin", "refs/pull/7/head")
		runGit(t, work, "checkout", "--quiet", "FETCH_HEAD")
		require.NoError(t, os.WriteFile(filepath.Join(work, "feature.txt"), []byte("feature v2\n"), 0o644))
		runGit(t, work, "commit", "--quiet", "-am", "improve feature")
		newSHA := runGit(t, work, "rev-parse", "HEAD")
		runGit(t, work, "push", "--quiet", "--force", "origin", "HEAD:refs/pull/7/head")

		later := time.Now().Add(2 * time.Minute)
		unlock := f.upstream.lock()
		f.upstream.issues[2].UpdatedAt = later
		f.upstream.pulls[7].Head.SHA = newSHA
		unlock()

		f.queued = nil
		f.sync(t)
		pr, err := issues_model.GetPullRequestByIssueID(ctx, f.issue(t, 7).ID)
		require.NoError(t, err)
		assert.Equal(t, newSHA, runGit(t, f.repo.RepoPath(), "rev-parse", pr.GetGitRefName()))
		assert.Contains(t, f.queued, pr.ID)
	})

	t.Run("a pull request merged locally is never reopened", func(t *testing.T) {
		issue := f.issue(t, 7)
		pr, err := issues_model.GetPullRequestByIssueID(ctx, issue.ID)
		require.NoError(t, err)
		pr.HasMerged = true
		require.NoError(t, pr.UpdateCols(ctx, "has_merged"))
		issue.IsClosed = true
		_, err = db.GetEngine(ctx).ID(issue.ID).Cols("is_closed").Update(issue)
		require.NoError(t, err)

		unlock := f.upstream.lock()
		f.upstream.issues[2].UpdatedAt = time.Now().Add(3 * time.Minute)
		f.upstream.issues[2].Title = "add a feature (renamed)"
		unlock()

		f.sync(t)
		issue = f.issue(t, 7)
		assert.True(t, issue.IsClosed)
		assert.Equal(t, "add a feature (renamed)", issue.Title)
	})

	t.Run("a deleted local issue is synced again", func(t *testing.T) {
		issue := f.issue(t, 100)
		_, err := db.GetEngine(ctx).ID(issue.ID).Delete(new(issues_model.Issue))
		require.NoError(t, err)

		unlock := f.upstream.lock()
		f.upstream.issues[1].UpdatedAt = time.Now().Add(4 * time.Minute)
		unlock()

		f.sync(t)
		again := f.issue(t, 100)
		assert.NotEqual(t, issue.ID, again.ID)
		assert.Equal(t, "a bug (fixed)", again.Title)
	})

	t.Run("errors are saved and the cursor is kept", func(t *testing.T) {
		before := unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: f.mirror.ID})
		unlock := f.upstream.lock()
		f.upstream.rateLimited = true
		unlock()
		defer func() {
			unlock := f.upstream.lock()
			f.upstream.rateLimited = false
			unlock()
		}()

		require.ErrorIs(t, Sync(ctx, f.mirror.ID), ErrRateLimited)
		m := unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: f.mirror.ID})
		assert.Contains(t, m.PublicLastError, "rate limit")
		assert.Equal(t, before.PublicSyncedUnix, m.PublicSyncedUnix)
	})

	t.Run("disabled public mirror is not synced", func(t *testing.T) {
		require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, f.mirror, repo_model.PublicMirrorProviderNone))
		unlock := f.upstream.lock()
		n := len(f.upstream.requests)
		unlock()
		require.NoError(t, Sync(ctx, f.mirror.ID))
		defer f.upstream.lock()()
		assert.Len(t, f.upstream.requests, n)
	})
}

func TestSetPushMirrorPublicProvider(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	a := &repo_model.PushMirror{RepoID: 1, RemoteName: "a", RemoteAddress: "https://github.com/a/b"}
	b := &repo_model.PushMirror{RepoID: 1, RemoteName: "b", RemoteAddress: "https://codeberg.org/a/b"}
	other := &repo_model.PushMirror{RepoID: 2, RemoteName: "c", RemoteAddress: "https://github.com/c/d"}
	require.NoError(t, db.Insert(ctx, a, b, other))
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, other, repo_model.PublicMirrorProviderGitHub))

	got, err := repo_model.GetPublicPushMirror(ctx, 1)
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, a, repo_model.PublicMirrorProviderGitHub))
	got, err = repo_model.GetPublicPushMirror(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, a.ID, got.ID)
	assert.Equal(t, "https://github.com/a/b", got.PublicUpstream().WebURL())

	// only one public mirror per repository
	a.PublicSyncedUnix = timeutil.TimeStampNow()
	require.NoError(t, repo_model.SetPushMirrorPublicProvider(ctx, b, repo_model.PublicMirrorProviderForgejo))
	got, err = repo_model.GetPublicPushMirror(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, b.ID, got.ID)
	assert.Empty(t, unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: a.ID}).PublicProvider)
	// other repositories are not affected
	assert.Equal(t, repo_model.PublicMirrorProviderGitHub, unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: other.ID}).PublicProvider)

	mirrors, err := repo_model.GetPublicPushMirrorsToSync(ctx)
	require.NoError(t, err)
	ids := []int64{}
	for _, m := range mirrors {
		ids = append(ids, m.ID)
	}
	assert.ElementsMatch(t, []int64{b.ID, other.ID}, ids)
}
