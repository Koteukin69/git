// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Fork: single-owner mode, see docs/fork/single-owner.md.

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	singleowner_service "forgejo.org/services/singleowner"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// enableSingleOwner turns single-owner mode on for owner, in this process
// and in the configuration file read by `forgejo serv` (SSH).
func enableSingleOwner(t *testing.T, owner string) func() {
	t.Helper()
	conf, err := os.ReadFile(setting.CustomConf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(setting.CustomConf, append(append([]byte{}, conf...), []byte("\n[single_owner]\nPRIMARY_OWNER = "+owner+"\n")...), 0o600))

	resetSetting := test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, owner)
	require.NoError(t, singleowner_service.Init(db.DefaultContext))
	require.True(t, singleowner.Active())
	return func() {
		singleowner.Reset()
		resetSetting()
		require.NoError(t, os.WriteFile(setting.CustomConf, conf, 0o600))
	}
}

func assertRedirect(t *testing.T, session *TestSession, path, location string) {
	t.Helper()
	req := NewRequest(t, "GET", path)
	var resp *httptest.ResponseRecorder
	if session != nil {
		resp = session.MakeRequest(t, req, http.StatusMovedPermanently)
	} else {
		resp = MakeRequest(t, req, http.StatusMovedPermanently)
	}
	assert.Equal(t, location, resp.Header().Get("Location"), path)
}

func TestSingleOwnerDisabled(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	singleowner.Reset()

	MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", "/user2"), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", "/repo1"), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/user4"), http.StatusNotFound)

	// configured, but the owner does not exist: the instance starts and the mode stays off
	defer test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, "no-such-owner")()
	require.NoError(t, singleowner_service.Init(db.DefaultContext))
	assert.False(t, singleowner.Active())
	MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", "/repo1"), http.StatusNotFound)
}

func TestSingleOwnerWeb(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer enableSingleOwner(t, "user2")()

	t.Run("ShortURLs", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		for _, p := range []string{
			"/repo1",
			"/repo1/issues",
			"/repo1/issues/1",
			"/repo1/pulls",
			"/repo1/src/branch/master",
			"/repo1/src/branch/master/README.md",
			"/repo1/raw/branch/master/README.md",
			"/repo1/commits/branch/master",
			"/repo1/releases",
			"/repo1/wiki",
			"/repo1/compare",
			"/repo1/activity",
			"/repo1.rss",
		} {
			MakeRequest(t, NewRequest(t, "GET", p), http.StatusOK)
		}
		loginUser(t, "user2").MakeRequest(t, NewRequest(t, "GET", "/repo1/settings"), http.StatusOK)

		resp := MakeRequest(t, NewRequest(t, "GET", "/repo1"), http.StatusOK)
		body := resp.Body.String()
		assert.Contains(t, body, `href="/repo1/issues"`)
		assert.Contains(t, body, setting.AppURL+"repo1.git")
		signedIn := loginUser(t, "user2").MakeRequest(t, NewRequest(t, "GET", "/repo1"), http.StatusOK).Body.String()
		assert.Contains(t, signedIn, fmt.Sprintf("ssh://git@%s:%d/repo1.git", setting.SSH.Domain, setting.SSH.Port))
		assert.NotContains(t, signedIn, "user2/repo1.git")
		for _, m := range regexp.MustCompile(`.{0,80}(href="/user2/repo1|user2/repo1\.git).{0,40}`).FindAllString(body, -1) {
			assert.Fail(t, "long repository link", m)
		}
	})

	t.Run("RedirectLongURLs", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertRedirect(t, nil, "/user2/repo1", "/repo1")
		assertRedirect(t, nil, "/user2/repo1/issues?state=closed&page=2", "/repo1/issues?state=closed&page=2")
		assertRedirect(t, nil, "/user2/repo1/src/branch/master/README.md", "/repo1/src/branch/master/README.md")
		assertRedirect(t, nil, "/USER2/repo1/releases", "/repo1/releases")
		assertRedirect(t, nil, "/user2", "/")
		assertRedirect(t, nil, "/user2?tab=stars", "/?tab=stars")

		// owner-level pages, git, go-get and non-GET requests are served in place
		MakeRequest(t, NewRequest(t, "GET", "/user2/-/projects"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/user2/repo1.git/info/refs"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/info/refs"), http.StatusOK)
		resp := MakeRequest(t, NewRequest(t, "GET", "/user2/repo1?go-get=1"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), `name="go-import"`)
		session := loginUser(t, "user2")
		resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/issues/1/watch", map[string]string{"watch": "1"}), NoExpectedStatus)
		assert.NotEqual(t, http.StatusMovedPermanently, resp.Code)

		// unknown repositories are not redirected
		MakeRequest(t, NewRequest(t, "GET", "/user2/no-such-repo"), http.StatusNotFound)
	})

	t.Run("Home", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		assert.True(t, strings.HasPrefix(strings.TrimSpace(doc.Find(".profile-avatar-name .username").Text()), "user2"))
		assert.Positive(t, doc.Find(`a[href="/repo1"]`).Length())

		session := loginUser(t, "user4")
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
		doc = NewHTMLParser(t, resp.Body)
		assert.True(t, strings.HasPrefix(strings.TrimSpace(doc.Find(".profile-avatar-name .username").Text()), "user2"))
		assert.Equal(t, "/-/single-owner/dashboard", doc.Find("#navbar-logo").AttrOr("href", ""))
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/dashboard"), http.StatusOK)
		assert.Contains(t, NewHTMLParser(t, resp.Body).Find("title").Text(), "Dashboard")
	})

	t.Run("SystemRoutesAndOtherUsers", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		MakeRequest(t, NewRequest(t, "GET", "/explore/repos"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/user/login"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/user4"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/org3"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/org3/repo3"), NoExpectedStatus)
		MakeRequest(t, NewRequest(t, "GET", "/user5/repo4"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/repo4"), http.StatusNotFound)

		// the conflict fallback only serves existing, non-reserved users and organizations
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/user4"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/org3"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/user5/repo4"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/api/v1/version"), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/admin"), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/explore/repos"), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, "GET", "/-/single-owner/profile/no-such-user"), http.StatusNotFound)
	})

	t.Run("API", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1"), http.StatusOK)
		var repo api.Repository
		DecodeJSON(t, resp, &repo)
		assert.Equal(t, "user2/repo1", repo.FullName)
		assert.Equal(t, setting.AppURL+"repo1", repo.HTMLURL)
		assert.Equal(t, setting.AppURL+"repo1.git", repo.CloneURL)
		assert.NotContains(t, repo.SSHURL, "user2")

		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/issues/1"), http.StatusOK)
		var issue api.Issue
		DecodeJSON(t, resp, &issue)
		assert.Equal(t, setting.AppURL+"repo1/issues/1", issue.HTMLURL)

		// other owners keep the long form
		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user5/repo4"), http.StatusOK)
		DecodeJSON(t, resp, &repo)
		assert.Equal(t, setting.AppURL+"user5/repo4", repo.HTMLURL)

	})

	t.Run("Markup", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2")
		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo1/markup", map[string]string{
			"mode":    "comment",
			"context": setting.AppSubURL + "/repo1",
			"text":    "#2 and user2/repo1#1 and user5/repo4#1 and user2/repo1@65f1bf27bc3bf70f64657658635e66094edbcb4d",
		}), http.StatusOK)
		body := resp.Body.String()
		assert.Contains(t, body, `href="`+setting.AppURL+`repo1/issues/2"`)
		assert.Contains(t, body, `href="`+setting.AppURL+`repo1/issues/1"`)
		assert.Contains(t, body, `href="`+setting.AppURL+`user5/repo4/issues/1"`)
		assert.Contains(t, body, `href="`+setting.AppURL+`repo1/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d"`)
	})

	t.Run("GoGet", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		u, _ := url.Parse(setting.AppURL)
		resp := MakeRequest(t, NewRequest(t, "GET", "/repo1/sub/pkg?go-get=1"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), fmt.Sprintf(`content="%s/repo1 git %srepo1.git"`, u.Host, setting.AppURL))
		resp = MakeRequest(t, NewRequest(t, "GET", "/user2/repo1?go-get=1"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), fmt.Sprintf(`content="%s/user2/repo1 git %srepo1.git"`, u.Host, setting.AppURL))
	})

	t.Run("Permissions", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		// repo2 is private
		MakeRequest(t, NewRequest(t, "GET", "/repo2"), http.StatusNotFound)
		user4 := loginUser(t, "user4")
		user4.MakeRequest(t, NewRequest(t, "GET", "/repo2"), http.StatusNotFound)
		user4.MakeRequest(t, NewRequest(t, "GET", "/repo2/settings"), http.StatusNotFound)

		user2 := loginUser(t, "user2")
		user2.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo2/settings/collaboration", map[string]string{
			"collaborator": "user4",
		}), http.StatusSeeOther)
		user4.MakeRequest(t, NewRequest(t, "GET", "/repo2"), http.StatusOK)
		user4.MakeRequest(t, NewRequest(t, "GET", "/repo2/issues"), http.StatusOK)
		user4.MakeRequest(t, NewRequest(t, "GET", "/repo2/settings"), http.StatusNotFound)
	})

	t.Run("TokenAuthOnShortURLs", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository)
		// status of the long URL with the mode disabled, i.e. upstream behaviour
		upstream := func(req *RequestWrapper) int {
			id, name := singleowner.OwnerID(), singleowner.OwnerName()
			singleowner.Reset()
			defer singleowner.SetOwner(id, name)
			return MakeRequest(t, req, NoExpectedStatus).Code
		}
		for _, p := range []string{
			"/repo2/raw/branch/master/Home.md",
			"/repo2/archive/master.zip",
			"/repo2.git/info/refs?service=git-upload-pack",
		} {
			long := strings.Replace(p, "/repo2", "/user2/repo2", 1)
			assert.NotEqual(t, http.StatusOK, MakeRequest(t, NewRequest(t, "GET", p), NoExpectedStatus).Code, "anonymous: %s", p)
			assert.Equal(t, upstream(NewRequest(t, "GET", long).AddTokenAuth(token)), MakeRequest(t, NewRequest(t, "GET", p).AddTokenAuth(token), NoExpectedStatus).Code, "token: %s", p)
			assert.Equal(t, upstream(NewRequest(t, "GET", long).AddBasicAuth("user2")), MakeRequest(t, NewRequest(t, "GET", p).AddBasicAuth("user2"), NoExpectedStatus).Code, "basic: %s", p)
		}
		MakeRequest(t, NewRequest(t, "GET", "/repo2/raw/branch/master/Home.md").AddTokenAuth(token), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/repo2.git/info/refs?service=git-upload-pack").AddTokenAuth(token), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", "/repo2.git/info/refs?service=git-upload-pack").AddBasicAuth("user2"), http.StatusOK)
	})

	t.Run("RenamedRepository", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2")
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo16/settings", map[string]string{
			"action":    "update",
			"repo_name": "repo16-renamed",
		}), http.StatusSeeOther)
		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: 2, Name: "repo16-renamed"})

		assertRedirect(t, session, "/repo16/issues?state=all", "/repo16-renamed/issues?state=all")
		assertRedirect(t, session, "/user2/repo16/issues", "/repo16/issues")
		session.MakeRequest(t, NewRequest(t, "GET", "/repo16-renamed"), http.StatusOK)
	})

	t.Run("RenamedOwner", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		// renaming another user does not affect the mode
		session := loginUser(t, "user1")
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/admin/users/5/edit", map[string]string{
			"user_name":  "user5-renamed",
			"login_type": "0-0",
			"email":      "user5@example.com",
		}), NoExpectedStatus)
		assert.True(t, singleowner.IsOwner("user2"))
	})
}

func TestSingleOwnerNameConflicts(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer enableSingleOwner(t, "user2")()

	assertReserved := func(t *testing.T, resp *httptest.ResponseRecorder) {
		t.Helper()
		msg := NewHTMLParser(t, resp.Body).Find(".ui.negative.message").Text()
		assert.Contains(t, strings.ToLower(msg), "reserved", "%q", msg)
	}

	t.Run("CreateRepository", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2")
		for _, name := range []string{"explore", "api", "user4", "org3"} {
			resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo/create", map[string]string{
				"uid":       "2",
				"repo_name": name,
			}), http.StatusOK)
			assertReserved(t, resp)
			unittest.AssertNotExistsBean(t, &repo_model.Repository{OwnerID: 2, LowerName: name})
		}
		// other owners are not restricted
		session = loginUser(t, "user4")
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo/create", map[string]string{
			"uid":       "4",
			"repo_name": "explore",
		}), http.StatusSeeOther)

		// API
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		req := NewRequestWithJSON(t, "POST", "/api/v1/user/repos", &api.CreateRepoOption{Name: "user4"}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusUnprocessableEntity)
	})

	t.Run("RenameRepository", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2")
		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/repo1/settings", map[string]string{
			"action":    "update",
			"repo_name": "user4",
		}), http.StatusOK)
		assertReserved(t, resp)
		unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerID: 2, Name: "repo1"})
	})

	t.Run("CreateUser", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user1")
		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/admin/users/new", map[string]string{
			"login_type": "0-0",
			"user_name":  "repo1",
			"email":      "repo1@example.com",
			"password":   "Password-1234",
		}), http.StatusOK)
		assertReserved(t, resp)
		unittest.AssertNotExistsBean(t, &user_model.User{LowerName: "repo1"})

		// API
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeWriteAdmin)
		req := NewRequestWithJSON(t, "POST", "/api/v1/admin/users", &api.CreateUserOption{
			Username: "repo1", Email: "repo1@example.com", Password: "Password-1234",
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusUnprocessableEntity)

		// sign-up
		defer test.MockVariableValue(&setting.Service.DisableRegistration, false)()
		resp = MakeRequest(t, NewRequestWithValues(t, "POST", "/user/sign_up", map[string]string{
			"user_name": "repo1",
			"email":     "repo1@example.com",
			"password":  "Password-1234",
			"retype":    "Password-1234",
		}), http.StatusOK)
		assertReserved(t, resp)
		unittest.AssertNotExistsBean(t, &user_model.User{LowerName: "repo1"})
	})

	t.Run("RenameOrganization", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session := loginUser(t, "user2") // owner of org3
		resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/org/org3/settings", map[string]string{
			"name":       "repo1",
			"email":      "org3@example.com",
			"visibility": "0",
		}), http.StatusOK)
		assertReserved(t, resp)
		unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3, Name: "org3"})
	})
}

func TestSingleOwnerGit(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, u *url.URL) {
		defer enableSingleOwner(t, "user2")()
		ctx := NewAPITestContext(t, "user2", "so-pushed", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		t.Run("HTTPS", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			short := *u
			short.Path = "repo1.git"
			short.User = url.UserPassword("user2", userPassword)
			t.Run("CloneShort", doGitClone(t.TempDir(), &short))

			long := short
			long.Path = "user2/repo1.git"
			t.Run("CloneLong", doGitClone(t.TempDir(), &long))

			// private repository with password and token
			private := short
			private.Path = "repo2.git"
			clone := func(u *url.URL) error {
				return git.CloneWithArgs(t.Context(), nil, u.String(), t.TempDir(), git.CloneRepoOptions{})
			}
			require.NoError(t, clone(&private), "private repository with password")
			private.User = url.UserPassword("user2", ctx.Token)
			require.NoError(t, clone(&private), "private repository with token")
			private.User = url.UserPassword("user4", userPassword)
			t.Run("ClonePrivateDenied", doGitCloneFail(&private))

			// push to the short URL, including LFS
			t.Run("CreateRepo", doAPICreateRepository(ctx, nil, git.Sha1ObjectFormat))
			dstPath := t.TempDir()
			push := short
			push.Path = "so-pushed.git"
			t.Run("Clone", doGitClone(dstPath, &push))
			standardCommitAndPushTest(t, dstPath)
			lfsCommitAndPushTest(t, dstPath)

			// push to the long URL keeps working
			push.Path = "user2/so-pushed.git"
			t.Run("SetLongRemote", doGitSetRemoteURL(dstPath, "origin", &push))
			standardCommitAndPushTest(t, dstPath)
		})

		t.Run("Collaborator", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			collaborator := *u
			collaborator.Path = "so-pushed.git"
			collaborator.User = url.UserPassword("user4", userPassword)
			dstPath := t.TempDir()
			t.Run("CloneDenied", doGitCloneFail(&collaborator))
			t.Run("AddReadCollaborator", doAPIAddCollaborator(ctx, "user4", perm.AccessModeRead))
			t.Run("Clone", doGitClone(dstPath, &collaborator))
			t.Run("AddCommits", doGitAddSomeCommits(dstPath, "master"))
			t.Run("PushDenied", doGitPushTestRepositoryFail(dstPath, "origin", "master"))
			t.Run("AddWriteCollaborator", doAPIAddCollaborator(ctx, "user4", perm.AccessModeWrite))
			t.Run("PushAllowed", doGitPushTestRepository(dstPath, "origin", "master"))
		})
		t.Run("SSH", func(t *testing.T) {
			defer tests.PrintCurrentTest(t)()
			withKeyFile(t, "single-owner-key", func(keyFile string) {
				t.Run("CreateUserKey", doAPICreateUserKey(ctx, "single-owner-key", keyFile))

				dstPath := t.TempDir()
				t.Run("CloneShort", doGitClone(dstPath, createSSHUrl("so-pushed.git", u)))
				standardCommitAndPushTest(t, dstPath)
				t.Run("CloneLong", doGitClone(t.TempDir(), createSSHUrl("user2/so-pushed.git", u)))

				// app.ini still names the old owner after a rename: short SSH URLs keep working
				t.Run("AfterOwnerRename", func(t *testing.T) {
					owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
					require.NoError(t, user_service.AdminRenameUser(db.DefaultContext, owner, "user2-renamed"))
					assert.True(t, singleowner.IsOwner("user2-renamed"))
					assert.Equal(t, "user2", setting.SingleOwner.PrimaryOwner)
					t.Run("CloneShort", doGitClone(t.TempDir(), createSSHUrl("so-pushed.git", u)))
					t.Run("CloneLongNewName", doGitClone(t.TempDir(), createSSHUrl("user2-renamed/so-pushed.git", u)))
				})
			})
		})

	})
}
