// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"testing"
	"time"

	"forgejo.org/modules/publicmirror"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommentIssueNumber(t *testing.T) {
	for _, c := range []struct {
		comment Comment
		number  int64
	}{
		{Comment{IssueURL: "https://api.github.com/repos/o/r/issues/12"}, 12},
		{Comment{PullRequestURL: "https://codeberg.org/o/r/pulls/7"}, 7},
		{Comment{HTMLURL: "https://codeberg.org/o/r/issues/5#issuecomment-99"}, 5},
		{Comment{IssueURL: "", PullRequestURL: "", HTMLURL: "https://example.com/o/r/pulls/3/"}, 3},
		{Comment{IssueURL: "https://example.com/o/r/issues/abc"}, 0},
		{Comment{}, 0},
	} {
		assert.Equal(t, c.number, c.comment.IssueNumber(), "%+v", c.comment)
	}
}

func TestClientPagination(t *testing.T) {
	allowLocalNetworks(t)
	for _, sendLink := range []bool{true, false} {
		up := newFakeUpstream(t, "forgejo")
		up.sendLink = sendLink
		created := time.Now()
		for i := int64(1); i <= 5; i++ {
			up.issues = append(up.issues, &Issue{Number: i, State: "open", CreatedAt: created, UpdatedAt: created})
		}
		upstream, err := publicmirror.Parse(publicmirror.ProviderForgejo, up.webURL())
		require.NoError(t, err)

		var numbers []int64
		require.NoError(t, NewClient(upstream, "").ListIssues(t.Context(), time.Time{}, func(items []*Issue) error {
			for _, i := range items {
				numbers = append(numbers, i.Number)
			}
			return nil
		}))
		assert.Equal(t, []int64{1, 2, 3, 4, 5}, numbers, "sendLink=%v", sendLink)
		for _, h := range up.authHeaders {
			assert.Empty(t, h, "no token, anonymous requests")
		}
	}
}

func TestClientRepoCounters(t *testing.T) {
	allowLocalNetworks(t)
	up := newFakeUpstream(t, "forgejo")
	up.repoInfo = map[string]any{"html_url": "https://codeberg.org/o/r", "stars_count": 3, "watchers_count": 2, "forks_count": 1}
	upstream, err := publicmirror.Parse(publicmirror.ProviderForgejo, up.webURL())
	require.NoError(t, err)
	info, err := NewClient(upstream, "tok").GetRepo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, &RepoInfo{HTMLURL: "https://codeberg.org/o/r", Stars: 3, Watchers: 2, Forks: 1}, info)
	assert.Equal(t, []string{"token tok"}, up.authHeaders)
}

func TestClientRespectsMigrationBlockList(t *testing.T) {
	// the upstream runs on the loopback interface, which is not allowed by default
	up := newFakeUpstream(t, "forgejo")
	up.repoInfo = map[string]any{}
	t.Cleanup(test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, false))
	require.NoError(t, migrations_allowlist.Init())

	upstream, err := publicmirror.Parse(publicmirror.ProviderForgejo, up.webURL())
	require.NoError(t, err)
	_, err = NewClient(upstream, "").GetRepo(t.Context())
	require.Error(t, err)
	assert.Empty(t, up.requests)
}
