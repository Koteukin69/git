// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"forgejo.org/modules/json"

	"github.com/stretchr/testify/require"
)

// fakeUpstream is a minimal GitHub (or Forgejo/Gitea) server: the REST endpoints used by the sync, and git over
// smart HTTP (through git http-backend) for fetching pull request heads.
type fakeUpstream struct {
	t       *testing.T
	flavor  string
	owner   string
	repo    string
	gitRoot string // contains <owner>/<repo>.git
	server  *httptest.Server

	mu          sync.Mutex
	repoInfo    map[string]any
	issues      []*Issue
	comments    []*Comment
	pulls       map[int64]*PullRequest
	pageSize    int
	sendLink    bool
	rateLimited bool
	authHeaders []string
	sinceParams []string
	requests    []string
}

func newFakeUpstream(t *testing.T, flavor string) *fakeUpstream {
	f := &fakeUpstream{
		t:        t,
		flavor:   flavor,
		owner:    "owner",
		repo:     "repo",
		gitRoot:  t.TempDir(),
		pulls:    map[int64]*PullRequest{},
		pageSize: 2,
		sendLink: true,
	}
	gitBackend, err := exec.Command("git", "--exec-path").Output()
	require.NoError(t, err)
	git := &cgi.Handler{
		Path: filepath.Join(strings.TrimSpace(string(gitBackend)), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + f.gitRoot, "GIT_HTTP_EXPORT_ALL=1"},
	}

	apiPrefix := "/api/v3"
	if flavor == "forgejo" {
		apiPrefix = "/api/v1"
	}
	repoPrefix := fmt.Sprintf("%s/repos/%s/%s", apiPrefix, f.owner, f.repo)

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/"+f.owner+"/"+f.repo+".git/") {
			git.ServeHTTP(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.URL.Path)
		f.authHeaders = append(f.authHeaders, r.Header.Get("Authorization"))
		if f.rateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == repoPrefix:
			f.writeJSON(w, f.repoInfo)
		case r.URL.Path == repoPrefix+"/issues":
			f.sinceParams = append(f.sinceParams, r.URL.Query().Get("since"))
			since := parseSince(r)
			items := make([]any, 0, len(f.issues))
			for _, i := range f.issues {
				if !i.UpdatedAt.Before(since) {
					items = append(items, i)
				}
			}
			f.writePage(w, r, items)
		case r.URL.Path == repoPrefix+"/issues/comments":
			since := parseSince(r)
			items := make([]any, 0, len(f.comments))
			for _, c := range f.comments {
				if !c.UpdatedAt.Before(since) {
					items = append(items, c)
				}
			}
			f.writePage(w, r, items)
		case strings.HasPrefix(r.URL.Path, repoPrefix+"/pulls/"):
			n, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, repoPrefix+"/pulls/"), 10, 64)
			pr, ok := f.pulls[n]
			if !ok {
				http.NotFound(w, r)
				return
			}
			f.writeJSON(w, pr)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func parseSince(r *http.Request) time.Time {
	since, _ := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	return since
}

func (f *fakeUpstream) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	require.NoError(f.t, json.NewEncoder(w).Encode(v))
}

func (f *fakeUpstream) writePage(w http.ResponseWriter, r *http.Request, items []any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	start := min((page-1)*f.pageSize, len(items))
	end := min(start+f.pageSize, len(items))
	if f.sendLink {
		links := []string{}
		if end < len(items) {
			q := r.URL.Query()
			q.Set("page", strconv.Itoa(page+1))
			links = append(links, fmt.Sprintf(`<%s%s?%s>; rel="next"`, f.server.URL, r.URL.Path, q.Encode()))
		}
		links = append(links, fmt.Sprintf(`<%s%s>; rel="first"`, f.server.URL, r.URL.Path))
		w.Header().Set("Link", strings.Join(links, ", "))
	}
	f.writeJSON(w, items[start:end])
}

// webURL is the address of the upstream repository, as configured on the push mirror.
func (f *fakeUpstream) webURL() string {
	return fmt.Sprintf("%s/%s/%s.git", f.server.URL, f.owner, f.repo)
}

// barePath is the path of the git repository served by the upstream.
func (f *fakeUpstream) barePath() string {
	return filepath.Join(f.gitRoot, f.owner, f.repo+".git")
}

func (f *fakeUpstream) lock() func() {
	f.mu.Lock()
	return f.mu.Unlock
}
