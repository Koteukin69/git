// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package singleowner holds the runtime state and the pure (database free)
// helpers of the fork-specific single-owner mode, in which the repositories of
// one configured owner are served and linked at /<repo> instead of
// /<owner>/<repo>. See docs/fork/single-owner.md.
//
// The mode is only active once the configured owner has been resolved in the
// database (see services/singleowner.Init). While it is inactive every helper
// returns exactly what upstream code would produce.
package singleowner

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"forgejo.org/modules/setting"
)

// RoutePrefix is the fork-specific namespace for the few extra routes the
// mode needs. It lives below "/-/", which is reserved for user and repository
// names upstream, so it can never collide with a user, an organization or a
// repository.
const RoutePrefix = "/-/single-owner"

type owner struct {
	id   int64
	name string
}

var current atomic.Pointer[owner]

// SetOwner activates the mode for the given owner.
func SetOwner(id int64, name string) {
	current.Store(&owner{id: id, name: name})
}

// Reset deactivates the mode.
func Reset() {
	current.Store(nil)
}

// OwnerRenamed updates the cached owner name after the owner has been renamed.
func OwnerRenamed(id int64, newName string) {
	if o := current.Load(); o != nil && o.id == id {
		SetOwner(id, newName)
	}
}

// Active reports whether the mode is enabled and the owner is resolved.
func Active() bool {
	return current.Load() != nil
}

// OwnerID returns the primary owner's ID, or 0 when the mode is inactive.
func OwnerID() int64 {
	if o := current.Load(); o != nil {
		return o.id
	}
	return 0
}

// OwnerName returns the primary owner's current name, or "" when inactive.
func OwnerName() string {
	if o := current.Load(); o != nil {
		return o.name
	}
	return ""
}

// IsOwner reports whether name is the primary owner (case-insensitive).
func IsOwner(name string) bool {
	o := current.Load()
	return o != nil && name != "" && strings.EqualFold(o.name, name)
}

// IsConfiguredOwner reports whether name equals the owner configured in
// app.ini. Unlike IsOwner it does not need the database and also works in
// processes that never resolved the owner, like `forgejo serv`.
func IsConfiguredOwner(name string) bool {
	return setting.SingleOwner.PrimaryOwner != "" && strings.EqualFold(setting.SingleOwner.PrimaryOwner, name)
}

// RepoPath returns the escaped path of a repository relative to the instance
// root, without leading slash: "repo" for the primary owner in single-owner
// mode, "owner/repo" otherwise.
func RepoPath(ownerName, repoName string) string {
	if IsOwner(ownerName) {
		return url.PathEscape(repoName)
	}
	return url.PathEscape(ownerName) + "/" + url.PathEscape(repoName)
}

// RepoRelPath is the unescaped variant of RepoPath, for callers that escape
// the result themselves (e.g. util.URLJoin).
func RepoRelPath(ownerName, repoName string) string {
	if IsOwner(ownerName) {
		return repoName
	}
	return ownerName + "/" + repoName
}

// DashboardLink returns the link of the personal dashboard.
func DashboardLink() string {
	if Active() {
		return setting.AppSubURL + RoutePrefix + "/dashboard"
	}
	return setting.AppSubURL + "/"
}

// ShortenPath turns an escaped "/owner/repo/rest" path (without AppSubURL)
// into "/repo/rest" if owner is the primary owner. Other paths are returned
// unchanged.
func ShortenPath(p string) string {
	if !Active() {
		return p
	}
	trimmed := strings.TrimPrefix(p, "/")
	first, rest, ok := strings.Cut(trimmed, "/")
	if !ok || rest == "" || strings.HasPrefix(rest, "-/") || rest == "-" {
		return p
	}
	if unescaped, err := url.PathUnescape(first); err != nil || !IsOwner(unescaped) {
		return p
	}
	return "/" + rest
}

type routedKey struct{}

type routed struct {
	escaped string // escaped long path the request was routed as
	short   bool   // the request came in via a short URL
}

// WithRoutedPath records the path the request is routed as. It is called by
// the single-owner middleware when it rewrites the route of a request.
func WithRoutedPath(req *http.Request, escapedPath string, short bool) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), routedKey{}, &routed{escaped: escapedPath, short: short}))
}

func getRouted(req *http.Request) *routed {
	if req == nil {
		return nil
	}
	r, _ := req.Context().Value(routedKey{}).(*routed)
	return r
}

// RoutedPath returns the escaped path (without AppSubURL) the request was
// routed as: "/owner/repo/..." for a request that came in as "/repo/...", and
// req.URL.EscapedPath() for every other request.
func RoutedPath(req *http.Request) string {
	if r := getRouted(req); r != nil {
		return r.escaped
	}
	return req.URL.EscapedPath()
}

// RoutedURLPath is the unescaped variant of RoutedPath (compare req.URL.Path).
func RoutedURLPath(req *http.Request) string {
	if r := getRouted(req); r != nil {
		if p, err := url.PathUnescape(r.escaped); err == nil {
			return p
		}
	}
	return req.URL.Path
}

// IsShortRequest reports whether the request came in via a short /<repo> URL.
func IsShortRequest(req *http.Request) bool {
	r := getRouted(req)
	return r != nil && r.short
}
