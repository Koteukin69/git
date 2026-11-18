// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		provider, remote            string
		source, web, api, issue, pr string
		stargazers                  string
	}{
		{
			provider: ProviderGitHub, remote: "https://github.com/Owner/Repo.git",
			source: "github.com/owner/repo", web: "https://github.com/Owner/Repo", api: "https://api.github.com",
			issue: "https://github.com/Owner/Repo/issues/3", pr: "https://github.com/Owner/Repo/pull/3",
			stargazers: "https://github.com/Owner/Repo/stargazers",
		},
		{
			provider: ProviderGitHub, remote: "git@github.com:owner/repo.git",
			source: "github.com/owner/repo", web: "https://github.com/owner/repo", api: "https://api.github.com",
			issue: "https://github.com/owner/repo/issues/3", pr: "https://github.com/owner/repo/pull/3",
			stargazers: "https://github.com/owner/repo/stargazers",
		},
		{
			provider: ProviderGitHub, remote: "ssh://git@github.com:22/owner/repo",
			source: "github.com/owner/repo", web: "https://github.com/owner/repo", api: "https://api.github.com",
			issue: "https://github.com/owner/repo/issues/3", pr: "https://github.com/owner/repo/pull/3",
			stargazers: "https://github.com/owner/repo/stargazers",
		},
		{
			provider: ProviderGitHub, remote: "https://ghe.example.com/owner/repo",
			source: "ghe.example.com/owner/repo", web: "https://ghe.example.com/owner/repo", api: "https://ghe.example.com/api/v3",
			issue: "https://ghe.example.com/owner/repo/issues/3", pr: "https://ghe.example.com/owner/repo/pull/3",
			stargazers: "https://ghe.example.com/owner/repo/stargazers",
		},
		{
			provider: ProviderForgejo, remote: "https://codeberg.org/owner/repo.git",
			source: "codeberg.org/owner/repo", web: "https://codeberg.org/owner/repo", api: "https://codeberg.org/api/v1",
			issue: "https://codeberg.org/owner/repo/issues/3", pr: "https://codeberg.org/owner/repo/pulls/3",
			stargazers: "https://codeberg.org/owner/repo/stars",
		},
		{
			provider: ProviderForgejo, remote: "http://localhost:3000/sub/path/owner/repo",
			source: "localhost:3000/sub/path/owner/repo", web: "http://localhost:3000/sub/path/owner/repo", api: "http://localhost:3000/sub/path/api/v1",
			issue: "http://localhost:3000/sub/path/owner/repo/issues/3", pr: "http://localhost:3000/sub/path/owner/repo/pulls/3",
			stargazers: "http://localhost:3000/sub/path/owner/repo/stars",
		},
	}
	for _, c := range cases {
		t.Run(c.remote, func(t *testing.T) {
			u, err := Parse(c.provider, c.remote)
			require.NoError(t, err)
			assert.Equal(t, c.source, u.Source())
			assert.Equal(t, c.web, u.WebURL())
			assert.Equal(t, c.api, u.APIURL())
			assert.Equal(t, c.issue, u.IssueURL(3, false))
			assert.Equal(t, c.pr, u.IssueURL(3, true))
			assert.Equal(t, c.stargazers, u.StargazersURL())
			assert.Equal(t, c.web+"/fork", u.ForkURL())
			assert.Equal(t, c.web+"/issues/new", u.NewIssueURL())
			assert.Equal(t, c.web+"/compare", u.NewPullURL())
		})
	}

	t.Run("display name", func(t *testing.T) {
		u, err := Parse(ProviderGitHub, "https://github.com/a/b")
		require.NoError(t, err)
		assert.Equal(t, "GitHub", u.DisplayName())
		u, err = Parse(ProviderForgejo, "https://codeberg.org/a/b")
		require.NoError(t, err)
		assert.Equal(t, "codeberg.org", u.DisplayName())
	})

	for _, bad := range []struct{ provider, remote string }{
		{ProviderGitHub, "/local/path/repo.git"},
		{ProviderGitHub, "https://github.com/onlyowner"},
		{ProviderGitHub, "https://github.com/a/b/c"},
		{ProviderGitHub, "file:///tmp/a/b"},
		{"gitlab", "https://gitlab.com/a/b"},
	} {
		_, err := Parse(bad.provider, bad.remote)
		assert.Error(t, err, bad.remote)
	}
}

func TestItemURL(t *testing.T) {
	gh, err := Parse(ProviderGitHub, "https://github.com/o/r")
	require.NoError(t, err)
	fallback := "https://github.com/o/r/issues/5"
	for raw, expected := range map[string]string{
		"https://github.com/o/r/issues/5":     "https://github.com/o/r/issues/5",
		"https://GitHub.com/o/r/issues/5#x":   "https://GitHub.com/o/r/issues/5#x",
		"javascript:alert(document.domain)":   fallback,
		"JavaScript://github.com/%0aalert(1)": fallback,
		"data:text/html,<script>":             fallback,
		"https://evil.example.com/o/r":        fallback,
		"https://user:pw@github.com/o/r":      fallback,
		"//github.com/o/r/issues/5":           fallback,
		"":                                    fallback,
	} {
		assert.Equal(t, expected, gh.ItemURL(raw, 5, false), raw)
	}
	assert.Equal(t, "https://github.com/o/r/pull/5", gh.ItemURL("javascript:x", 5, true))

	fj, err := Parse(ProviderForgejo, "http://localhost:3000/sub/o/r")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:3000/sub/o/r/pulls/2", fj.ItemURL("http://localhost:3000/sub/o/r/pulls/2", 2, true))
	assert.Equal(t, "http://localhost:3000/sub/o/r/pulls/2", fj.ItemURL("http://localhost:4000/o/r/pulls/2", 2, true))
}
