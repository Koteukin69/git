// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/json"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
	publicmirror_service "forgejo.org/services/publicmirror"
	"forgejo.org/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeGitHub serves the GitHub (Enterprise) API endpoints used by the public mirror sync, for owner/repo.
func newFakeGitHub(t *testing.T, stars, forks int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/repos/owner/repo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "http://" + r.Host + "/owner/repo", "stargazers_count": stars, "forks_count": forks, "subscribers_count": 1,
			})
		case "/api/v3/repos/owner/repo/issues", "/api/v3/repos/owner/repo/issues/comments":
			_, _ = w.Write([]byte("[]"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// assertPublicMirrorCounts checks the stars and forks counts of user2/repo1 everywhere they are shown outside of the
// repository header: lists, sorting, the API and the admin list.
func assertPublicMirrorCounts(t *testing.T, session *TestSession, stars, forks, watchers int) {
	t.Helper()

	for _, link := range []string{"/explore/repos?q=repo1", "/user2?q=repo1"} {
		resp := session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		assert.True(t, strings.HasPrefix(doc.Find(`a[href="/user2/repo1/stars"]`).AttrOr("aria-label", ""), strconv.Itoa(stars)+" "), link)
		assert.True(t, strings.HasPrefix(doc.Find(`a[href="/user2/repo1/forks"]`).AttrOr("aria-label", ""), strconv.Itoa(forks)+" "), link)
	}

	// sorting by stars or forks uses the same counts
	for _, sort := range []string{"moststars", "mostforks"} {
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/explore/repos?sort="+sort), http.StatusOK)
		first := NewHTMLParser(t, resp.Body).Find(".flex-item").First().Find(".flex-item-title a.name").Last().AttrOr("href", "")
		if stars > 1 && forks > 1 {
			assert.Equal(t, "/user2/repo1", first, sort)
		} else {
			assert.NotEqual(t, "/user2/repo1", first, sort)
		}
	}

	var apiRepo struct {
		Stars    int `json:"stars_count"`
		Forks    int `json:"forks_count"`
		Watchers int `json:"watchers_count"`
	}
	resp := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1"), http.StatusOK)
	DecodeJSON(t, resp, &apiRepo)
	assert.Equal(t, stars, apiRepo.Stars)
	assert.Equal(t, forks, apiRepo.Forks)
	assert.Equal(t, watchers, apiRepo.Watchers)
}

func TestPublicPushMirror(t *testing.T) {
	defer test.MockVariableValue(&setting.Mirror.Enabled, true)()
	defer test.MockVariableValue(&setting.Migrations.AllowLocalNetworks, true)()
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, migrations_allowlist.Init())
	defer func() { _ = migrations_allowlist.Init() }()

	upstream := newFakeGitHub(t, 1234, 56)
	upstreamRepo := upstream.URL + "/owner/repo"
	session := loginUser(t, "user2")
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	assertNotRedirectedHome := func(t *testing.T) {
		t.Helper()
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		doc.AssertElement(t, "#public-mirror-star", false)
		doc.AssertElement(t, "#public-mirror-fork", false)
		doc.AssertElement(t, "#public-mirror-flag", false)
		doc.AssertElement(t, "form[action='/user2/repo1/action/star']", true)
	}

	t.Run("without public mirror nothing changes", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertNotRedirectedHome(t)
		session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/new"), http.StatusOK)
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/1"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		doc.AssertElement(t, "#comment-form", true)
		doc.AssertElement(t, "#public-mirror-comment", false)
	})

	t.Run("invalid settings are refused", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		for _, provider := range []string{"gitlab", "github"} {
			address := "https://example.com/a/b/c"
			if provider == "gitlab" {
				address = "https://example.com/a/b"
			}
			resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
				"action":               "push-mirror-add",
				"push_mirror_address":  address,
				"push_mirror_interval": "0",
				"push_mirror_public":   provider,
			}), http.StatusOK)
			assert.Contains(t, resp.Body.String(), "To be used as public mirror")
		}
		unittest.AssertNotExistsBean(t, &repo_model.PushMirror{RepoID: repo.ID})
	})

	var mirror *repo_model.PushMirror
	t.Run("enable in the push mirror settings", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":               "push-mirror-add",
			"push_mirror_address":  upstreamRepo + ".git",
			"push_mirror_username": "pusher",
			"push_mirror_password": "token",
			"push_mirror_interval": "0",
			"push_mirror_public":   "github",
		}), http.StatusSeeOther)
		mirror = unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{RepoID: repo.ID})
		assert.Equal(t, repo_model.PublicMirrorProviderGitHub, mirror.PublicProvider)

		require.NoError(t, publicmirror_service.Sync(t.Context(), mirror.ID))
		mirror = unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: mirror.ID})
		assert.Empty(t, mirror.PublicLastError)
		assert.Equal(t, 1234, mirror.PublicStars)
		assert.Equal(t, 56, mirror.PublicForks)

		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/settings"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		assert.Equal(t, upstreamRepo, doc.Find(".push-mirror-public a").AttrOr("href", ""))
	})

	localRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	t.Run("the counts of the public mirror are used everywhere", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		assertPublicMirrorCounts(t, session, 1234, 56, 1)
		// the local counts are kept, the "check repo stats" cron task recomputes them
		assert.Equal(t, 0, localRepo.NumStars)
		assert.Equal(t, 0, localRepo.NumForks)

		admin := loginUser(t, "user1")
		resp := admin.MakeRequest(t, NewRequest(t, "GET", "/admin/repos?q=repo1"), http.StatusOK)
		row := NewHTMLParser(t, resp.Body).Find(`table tbody tr:has(a[href="/user2/repo1"]) td`)
		cells := []string{}
		row.Each(func(_ int, td *goquery.Selection) { cells = append(cells, strings.TrimSpace(td.Text())) })
		assert.Contains(t, strings.Join(cells, "|"), "|1|1234|56|")
	})

	t.Run("header links to the public mirror", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		resp := MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		star := doc.Find("#public-mirror-star a")
		assert.Equal(t, upstreamRepo, star.First().AttrOr("href", ""))
		assert.Equal(t, upstreamRepo+"/stargazers", star.Last().AttrOr("href", ""))
		assert.Equal(t, "1.2k", strings.TrimSpace(star.Last().Text()))
		fork := doc.Find("#public-mirror-fork a")
		assert.Equal(t, upstreamRepo+"/fork", fork.First().AttrOr("href", ""))
		assert.Equal(t, "56", strings.TrimSpace(fork.Last().Text()))
		doc.AssertElement(t, "#public-mirror-flag", true)
		doc.AssertElement(t, "form[action='/user2/repo1/action/star']", false)
	})

	t.Run("actions redirect to the public mirror", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		starsBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).NumStars

		// a star given before the public mirror was enabled can still be removed
		require.NoError(t, repo_model.StarRepo(db.DefaultContext, 2, repo.ID, true))
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#public-mirror-local-unstar", true)
		session.MakeRequest(t, NewRequest(t, "POST", "/user2/repo1/action/unstar"), http.StatusOK)
		assert.False(t, repo_model.IsStaring(db.DefaultContext, 2, repo.ID))
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#public-mirror-local-unstar", false)

		resp = session.MakeRequest(t, NewRequest(t, "POST", "/user2/repo1/action/star"), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo, resp.Header().Get("Location"))
		assert.Equal(t, starsBefore, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).NumStars)

		req := NewRequest(t, "POST", "/user2/repo1/action/star")
		req.Header.Set("HX-Request", "true")
		resp = session.MakeRequest(t, req, http.StatusNoContent)
		assert.Equal(t, upstreamRepo, resp.Header().Get("HX-Redirect"))

		user4 := loginUser(t, "user4")
		resp = user4.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/fork"), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/fork", resp.Header().Get("Location"))
		resp = user4.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/fork", map[string]string{
			"uid": "4", "repo_name": "fork-of-repo1",
		}), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/fork", resp.Header().Get("Location"))
		unittest.AssertNotExistsBean(t, &repo_model.Repository{OwnerID: 4, Name: "fork-of-repo1"})

		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/new?title=Hello+world&body=see+%23"), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/issues/new?body=see+%23&title=Hello+world", resp.Header().Get("Location"))
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/new/choose"), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/issues/new", resp.Header().Get("Location"))

		issuesBefore := unittest.GetCount(t, &issues_model.Issue{RepoID: repo.ID})
		resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/issues/new", map[string]string{
			"title": "should not be created",
		}), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/issues/new", resp.Header().Get("Location"))
		resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/compare/master...branch2", map[string]string{
			"title": "should not be created",
		}), http.StatusSeeOther)
		assert.Equal(t, upstreamRepo+"/compare", resp.Header().Get("Location"))
		assert.Equal(t, issuesBefore, unittest.GetCount(t, &issues_model.Issue{RepoID: repo.ID}))

		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues"), http.StatusOK)
		assert.Equal(t, upstreamRepo+"/issues/new", NewHTMLParser(t, resp.Body).Find(".issue-list-new").AttrOr("href", ""))
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls"), http.StatusOK)
		assert.Equal(t, upstreamRepo+"/compare", NewHTMLParser(t, resp.Body).Find(".new-pr-button").AttrOr("href", ""))
	})

	t.Run("synced issues are discussed on the public mirror", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		synced := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 1})
		require.NoError(t, repo_model.SavePublicMirrorIssue(db.DefaultContext, &repo_model.PublicMirrorIssue{
			RepoID: repo.ID, Source: mirror.PublicUpstream().Source(), RemoteNumber: 77, IssueID: synced.ID,
			// a hostile value (e.g. stored by an older version): never rendered as is
			RemoteURL: "javascript:alert(document.domain)",
		}))

		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/1"), http.StatusOK)
		doc := NewHTMLParser(t, resp.Body)
		doc.AssertElement(t, "#comment-form", false)
		assert.Equal(t, upstreamRepo+"/issues/77", doc.Find("#public-mirror-comment a.button").AttrOr("href", ""))
		assert.Equal(t, upstreamRepo+"/issues/77", doc.Find("#public-mirror-label").AttrOr("href", ""))
		assert.Contains(t, doc.Find("#public-mirror-label").Text(), "#77")
		assert.Equal(t, upstreamRepo+"/issues/77", doc.Find("#public-mirror-comment span a").AttrOr("href", ""))
		assert.NotContains(t, resp.Body.String(), "javascript:alert")

		commentsBefore := unittest.GetCount(t, &issues_model.Comment{IssueID: synced.ID})
		resp = session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/issues/1/comments", map[string]string{
			"content": "should not be created",
		}), http.StatusOK)
		var redirect struct {
			Redirect string `json:"redirect"`
		}
		DecodeJSON(t, resp, &redirect)
		assert.Equal(t, upstreamRepo+"/issues/77", redirect.Redirect)
		assert.Equal(t, commentsBefore, unittest.GetCount(t, &issues_model.Comment{IssueID: synced.ID}))

		// every tab of a synced pull request shows where it comes from
		syncedPull := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 3})
		require.NoError(t, repo_model.SavePublicMirrorIssue(db.DefaultContext, &repo_model.PublicMirrorIssue{
			RepoID: repo.ID, Source: mirror.PublicUpstream().Source(), RemoteNumber: 78, IssueID: syncedPull.ID, IsPull: true,
		}))
		for _, tab := range []string{"", "/commits", "/files"} {
			resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/3"+tab), http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			assert.Equal(t, upstreamRepo+"/pull/78", doc.Find("#public-mirror-label").AttrOr("href", ""), tab)
		}

		// local issues still work as usual
		local := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: 4})
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/4"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#comment-form", true)
		// and so do local pull requests
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/2"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#comment-form", true)
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/issues/4/comments", map[string]string{
			"content": "a local comment",
		}), http.StatusOK)
		unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: local.ID, Content: "a local comment"})
	})

	t.Run("disable in the push mirror settings", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":               "push-mirror-update",
			"push_mirror_id":       strconv.FormatInt(mirror.ID, 10),
			"push_mirror_interval": "0",
			"push_mirror_public":   "",
		}), http.StatusSeeOther)
		assert.Empty(t, unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: mirror.ID}).PublicProvider)
		assertPublicMirrorCounts(t, session, localRepo.NumStars, localRepo.NumForks, localRepo.NumWatches)

		assertNotRedirectedHome(t)
		session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/new"), http.StatusOK)
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/1"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#comment-form", true)

		// and can be enabled again on an existing mirror
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":               "push-mirror-update",
			"push_mirror_id":       strconv.FormatInt(mirror.ID, 10),
			"push_mirror_interval": "0",
			"push_mirror_public":   "github",
		}), http.StatusSeeOther)
		assert.Equal(t, repo_model.PublicMirrorProviderGitHub, unittest.AssertExistsAndLoadBean(t, &repo_model.PushMirror{ID: mirror.ID}).PublicProvider)
	})

	t.Run("global mirror switch disables the redirects", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		defer test.MockVariableValue(&setting.Mirror.Enabled, false)()
		resp := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK)
		NewHTMLParser(t, resp.Body).AssertElement(t, "#public-mirror-star", false)
		session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/issues/new"), http.StatusOK)
	})
}
