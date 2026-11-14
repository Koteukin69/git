// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package singleowner

import (
	"net/http"
	"net/url"
	"strings"

	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"

	chi "github.com/go-chi/chi/v5"
)

// Middleware routes the short URLs of single-owner mode. It has to run before
// chi matches a route (right after the slash-stripping middleware) and only
// rewrites chi's RoutePath: req.URL keeps the short form, so every link that
// is derived from the request URL (ctx.Link, pagination, forms) stays short.
// The long path the request is routed as is available via
// singleowner.RoutedPath.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
		if !singleowner.Active() {
			next.ServeHTTP(resp, req)
			return
		}
		rctx := chi.RouteContext(req.Context())
		if rctx == nil {
			next.ServeHTTP(resp, req)
			return
		}
		p := rctx.RoutePath
		if p == "" {
			p = req.URL.EscapedPath()
		}
		req, handled := route(resp, req, rctx, p)
		if handled {
			return
		}
		next.ServeHTTP(resp, req)
	})
}

func isReadRequest(req *http.Request) bool {
	return req.Method == http.MethodGet || req.Method == http.MethodHead
}

// trimRepoSuffixes strips the suffixes the repository routes accept after
// the repository name (git, wiki, feeds).
func trimRepoSuffixes(name string) string {
	name = strings.TrimSuffix(name, ".git")
	name = strings.TrimSuffix(name, ".wiki")
	name = strings.TrimSuffix(name, ".rss")
	name = strings.TrimSuffix(name, ".atom")
	return name
}

// isGitProtocolPath reports whether the part of the path after
// /<owner>/<repo> belongs to git smart/dumb HTTP or LFS.
func isGitProtocolPath(repoSegment, rest string) bool {
	if strings.HasSuffix(repoSegment, ".git") {
		return true
	}
	return rest == "HEAD" || rest == "git-upload-pack" || rest == "git-receive-pack" ||
		strings.HasPrefix(rest, "info/") || strings.HasPrefix(rest, "objects/")
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// route applies the single-owner routing rules. It returns handled=true if it
// already wrote a response (a redirect).
func route(resp http.ResponseWriter, req *http.Request, rctx *chi.Context, p string) (*http.Request, bool) {
	ctx := req.Context()
	ownerID, ownerName := singleowner.OwnerID(), singleowner.OwnerName()
	segs := strings.SplitN(strings.TrimPrefix(p, "/"), "/", 3)
	first := segs[0]

	// "/" shows the primary owner's profile.
	if first == "" {
		if req.Method == http.MethodGet {
			rctx.RoutePath = "/" + url.PathEscape(ownerName)
		}
		return req, false
	}

	// Fork-specific routes below /-/single-owner/.
	if first == "-" {
		if len(segs) < 3 || segs[1] != strings.TrimPrefix(singleowner.RoutePrefix, "/-/") {
			return req, false
		}
		sub := segs[2]
		if sub == "dashboard" {
			rctx.RoutePath = "/"
			return req, false
		}
		if escapedName, ok := strings.CutPrefix(sub, "profile/"); ok {
			name, tail, _ := strings.Cut(escapedName, "/")
			unescapedName := unescape(name)
			if unescapedName == "" || user_model.IsReservedTopLevelName(unescapedName) {
				return req, false // no such route: 404
			}
			exist, err := user_model.IsUserExist(ctx, 0, unescapedName)
			if err != nil {
				log.Error("single-owner: IsUserExist: %v", err)
			}
			if err != nil || !exist {
				return req, false // no such route: 404
			}
			rctx.RoutePath = "/" + name
			if tail != "" {
				rctx.RoutePath += "/" + tail
			}
		}
		return req, false
	}

	// Long URLs of the primary owner: permanently redirect pages to the short form.
	if singleowner.IsOwner(unescape(first)) {
		if !isReadRequest(req) {
			return req, false
		}
		if len(segs) == 1 {
			redirect(resp, req, setting.AppSubURL+"/")
			return req, true
		}
		repoSegment, rest := segs[1], ""
		if len(segs) == 3 {
			rest = segs[2]
		}
		if repoSegment == "-" || isGitProtocolPath(repoSegment, rest) || req.URL.Query().Get("go-get") == "1" {
			return req, false
		}
		repoName := trimRepoSuffixes(unescape(repoSegment))
		if repoName == "" || user_model.IsReservedTopLevelName(repoName) {
			return req, false
		}
		if _, err := repo_model.GetRepositoryByName(ctx, ownerID, repoName); err != nil {
			if _, err := repo_model.GetRedirect(ctx, ownerID, repoName); err != nil {
				return req, false // let upstream answer (404)
			}
		}
		target := setting.AppSubURL + "/" + repoSegment
		if rest != "" {
			target += "/" + rest
		}
		redirect(resp, req, target)
		return req, true
	}

	// Short URLs: /<repo>/... of the primary owner.
	repoName := trimRepoSuffixes(unescape(first))
	if repoName == "" || user_model.IsReservedTopLevelName(repoName) {
		return req, false
	}
	if _, err := repo_model.GetRepositoryByName(ctx, ownerID, repoName); err != nil {
		if !repo_model.IsErrRepoNotExist(err) {
			log.Error("single-owner: GetRepositoryByName: %v", err)
			return req, false
		}
		// Users and organizations keep their URLs, unless a repository of the
		// primary owner has that name (handled above: the repository wins).
		if exist, err := user_model.IsUserExist(ctx, 0, repoName); err != nil || exist {
			return req, false
		}
		// Old name of a renamed repository: upstream RepoAssignment redirects.
		if _, err := repo_model.GetRedirect(ctx, ownerID, repoName); err != nil {
			return req, false
		}
	}
	routed := "/" + url.PathEscape(ownerName) + p
	rctx.RoutePath = routed
	return singleowner.WithRoutedPath(req, routed, true), false
}

func redirect(resp http.ResponseWriter, req *http.Request, target string) {
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	http.Redirect(resp, req, target, http.StatusMovedPermanently)
}
