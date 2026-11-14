// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo_test

import (
	"testing"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fork: single-owner mode, see docs/fork/single-owner.md.
func TestSingleOwnerRepoLinks(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.AppURL, "https://git.example.com/")()
	defer test.MockVariableValue(&setting.AppSubURL, "")()
	defer test.MockVariableValue(&setting.SSH, setting.SSH)()
	defer test.MockVariableValue(&setting.Repository, setting.Repository)()
	defer singleowner.Reset()
	setting.SSH.User = "git"
	setting.SSH.Domain = "git.example.com"

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1}) // user2/repo1
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3}) // org3/repo3
	sha := "65f1bf27bc3bf70f64657658635e66094edbcb4d"

	type expected struct {
		link, html, commit, compare, https, ssh, sshPort, sshCompat string
	}
	check := func(t *testing.T, repo *repo_model.Repository, e expected) {
		t.Helper()
		setting.SSH.Port = 22
		setting.Repository.UseCompatSSHURI = false
		assert.Equal(t, e.link, repo.Link())
		assert.Equal(t, e.html, repo.HTMLURL())
		assert.Equal(t, e.commit, repo.CommitLink(sha))
		assert.Equal(t, e.compare, repo.ComposeCompareURL("a", "b"))
		assert.Equal(t, e.https, repo.CloneLink().HTTPS)
		assert.Equal(t, e.ssh, repo.CloneLink().SSH)
		setting.Repository.UseCompatSSHURI = true
		assert.Equal(t, e.sshCompat, repo.CloneLink().SSH)
		setting.SSH.Port = 2222
		assert.Equal(t, e.sshPort, repo.CloneLink().SSH)
		// API paths never change
		assert.Equal(t, "https://git.example.com/api/v1/repos/"+repo.OwnerName+"/"+repo.Name, repo.APIURL())
	}

	long1 := expected{
		link:      "/user2/repo1",
		html:      "https://git.example.com/user2/repo1",
		commit:    "/user2/repo1/commit/" + sha,
		compare:   "user2/repo1/compare/a...b",
		https:     "https://git.example.com/user2/repo1.git",
		ssh:       "git@git.example.com:user2/repo1.git",
		sshCompat: "ssh://git@git.example.com/user2/repo1.git",
		sshPort:   "ssh://git@git.example.com:2222/user2/repo1.git",
	}
	long3 := expected{
		link:      "/org3/repo3",
		html:      "https://git.example.com/org3/repo3",
		commit:    "/org3/repo3/commit/" + sha,
		compare:   "org3/repo3/compare/a...b",
		https:     "https://git.example.com/org3/repo3.git",
		ssh:       "git@git.example.com:org3/repo3.git",
		sshCompat: "ssh://git@git.example.com/org3/repo3.git",
		sshPort:   "ssh://git@git.example.com:2222/org3/repo3.git",
	}

	t.Run("Disabled", func(t *testing.T) {
		singleowner.Reset()
		check(t, repo1, long1)
		check(t, repo3, long3)
	})

	t.Run("Enabled", func(t *testing.T) {
		singleowner.SetOwner(2, "user2")
		check(t, repo1, expected{
			link:      "/repo1",
			html:      "https://git.example.com/repo1",
			commit:    "/repo1/commit/" + sha,
			compare:   "repo1/compare/a...b",
			https:     "https://git.example.com/repo1.git",
			ssh:       "git@git.example.com:repo1.git",
			sshCompat: "ssh://git@git.example.com/repo1.git",
			sshPort:   "ssh://git@git.example.com:2222/repo1.git",
		})
		// other owners are not affected
		check(t, repo3, long3)
	})

	t.Run("EnabledForOrganization", func(t *testing.T) {
		singleowner.SetOwner(3, "org3")
		check(t, repo1, long1)
		assert.Equal(t, "/repo3", repo3.Link())
		assert.Equal(t, "https://git.example.com/repo3", repo3.HTMLURL())
	})
}

func TestCheckSingleOwnerRepoName(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, "")()
	defer singleowner.Reset()
	ctx := db.DefaultContext

	// disabled: everything goes
	for _, name := range []string{"explore", "user4", "anything"} {
		require.NoError(t, repo_model.CheckSingleOwnerRepoName(ctx, 2, name))
	}

	check := func(t *testing.T) {
		t.Helper()
		for _, name := range []string{"explore", "api", "admin", "assets", "user", "-", "foo.keys", "foo.png", "user4", "User4", "org3"} {
			err := repo_model.CheckSingleOwnerRepoName(ctx, 2, name)
			assert.True(t, db.IsErrNameReserved(err), "%s: %v", name, err)
		}
		require.NoError(t, repo_model.CheckSingleOwnerRepoName(ctx, 2, "brand-new"))
		// other owners are not affected
		require.NoError(t, repo_model.CheckSingleOwnerRepoName(ctx, 4, "explore"))
		require.NoError(t, repo_model.CheckSingleOwnerRepoName(ctx, 4, "user4"))
	}

	// configured, runtime state not resolved (e.g. CLI): resolved from the database
	setting.SingleOwner.PrimaryOwner = "user2"
	t.Run("Configured", check)

	// configured and resolved by the web process
	singleowner.SetOwner(2, "user2")
	t.Run("Active", check)

	// configured owner does not exist: mode disabled
	singleowner.Reset()
	setting.SingleOwner.PrimaryOwner = "nobody-here"
	require.NoError(t, repo_model.CheckSingleOwnerRepoName(ctx, 2, "explore"))
}
