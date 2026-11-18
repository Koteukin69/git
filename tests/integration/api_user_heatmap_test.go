// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	activities_model "forgejo.org/models/activities"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func heatmapGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func heatmapCommit(t *testing.T, dir, email, date string) {
	t.Helper()
	heatmapGit(t, dir, []string{
		"GIT_AUTHOR_NAME=user2", "GIT_AUTHOR_EMAIL=" + email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=user2", "GIT_COMMITTER_EMAIL=" + email,
	}, "commit", "--allow-empty", "-m", "commit authored at "+date)
}

func getHeatmap(t *testing.T, token, username string) map[string]int64 {
	t.Helper()
	req := NewRequest(t, "GET", fmt.Sprintf("/api/v1/users/%s/heatmap", username)).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	var heatmap []*activities_model.UserHeatmapData
	DecodeJSON(t, resp, &heatmap)

	days := make(map[string]int64, len(heatmap))
	for _, day := range heatmap {
		days[day.Date] = day.Contributions
	}
	return days
}

func TestUserHeatmap(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		timeutil.MockSet(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC))
		defer timeutil.MockUnset()

		token := getUserToken(t, "user1", auth_model.AccessTokenScopeReadUser)

		// The fixtures only contain actions which GitHub does not count (e.g. closing an issue)
		assert.Empty(t, getHeatmap(t, token, "user2"))

		dir := t.TempDir()
		u.Path = "user2/repo1.git"
		u.User = url.UserPassword("user2", userPassword)
		doGitClone(dir, u)(t)

		// one push containing commits authored over several days
		heatmapCommit(t, dir, "user2@example.com", "2020-10-01T12:00:00+00:00")
		heatmapCommit(t, dir, "user2@example.com", "2020-10-03T23:30:00+03:00") // 20:30 UTC
		heatmapCommit(t, dir, "user2@example.com", "2020-10-03T22:30:00-05:00") // 03:30 UTC on the 4th
		heatmapCommit(t, dir, "someone-else@example.com", "2020-10-05T12:00:00+00:00")
		doGitPushTestRepository(dir, "origin", "master")(t)

		assert.Equal(t, map[string]int64{"2020-10-01": 1, "2020-10-03": 2}, getHeatmap(t, token, "user2"))

		t.Run("other branches do not count", func(t *testing.T) {
			heatmapGit(t, dir, nil, "checkout", "-b", "heatmap-side-branch")
			heatmapCommit(t, dir, "user2@example.com", "2020-10-10T12:00:00+00:00")
			doGitPushTestRepository(dir, "origin", "heatmap-side-branch")(t)

			assert.Equal(t, map[string]int64{"2020-10-01": 1, "2020-10-03": 2}, getHeatmap(t, token, "user2"))
		})

		t.Run("rewritten history is reflected", func(t *testing.T) {
			heatmapGit(t, dir, nil, "checkout", "master")
			heatmapGit(t, dir, nil, "reset", "--hard", "HEAD~3")
			heatmapCommit(t, dir, "user2@example.com", "2020-10-20T12:00:00+00:00")
			doGitPushTestRepository(dir, "origin", "+master")(t)

			assert.Equal(t, map[string]int64{"2020-10-01": 1, "2020-10-20": 1}, getHeatmap(t, token, "user2"))
		})

		t.Run("API keeps a timestamp on the same day", func(t *testing.T) {
			req := NewRequest(t, "GET", "/api/v1/users/user2/heatmap").AddTokenAuth(token)
			resp := MakeRequest(t, req, http.StatusOK)
			var heatmap []*activities_model.UserHeatmapData
			DecodeJSON(t, resp, &heatmap)
			for _, day := range heatmap {
				assert.True(t, strings.HasPrefix(day.Timestamp.AsTimeInLocation(time.UTC).Format(time.RFC3339), day.Date))
			}
		})
	})
}
