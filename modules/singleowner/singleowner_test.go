// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package singleowner

import (
	"net/http/httptest"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
)

func TestDisabled(t *testing.T) {
	Reset()
	assert.False(t, Active())
	assert.False(t, IsOwner(""))
	assert.False(t, IsOwner("alice"))
	assert.Equal(t, "alice/my%20repo", RepoPath("alice", "my repo"))
	assert.Equal(t, "alice/my repo", RepoRelPath("alice", "my repo"))
	assert.Equal(t, setting.AppSubURL+"/", DashboardLink())
	assert.Equal(t, "/alice/repo/issues", ShortenPath("/alice/repo/issues"))
}

func TestEnabled(t *testing.T) {
	defer test.MockVariableValue(&setting.AppSubURL, "/sub")()
	SetOwner(7, "Alice")
	defer Reset()

	assert.True(t, Active())
	assert.Equal(t, int64(7), OwnerID())
	assert.True(t, IsOwner("alice"))
	assert.False(t, IsOwner("bob"))

	assert.Equal(t, "my%20repo", RepoPath("alice", "my repo"))
	assert.Equal(t, "my repo", RepoRelPath("ALICE", "my repo"))
	assert.Equal(t, "bob/repo", RepoPath("bob", "repo"))
	assert.Equal(t, "bob/repo", RepoRelPath("bob", "repo"))
	assert.Equal(t, "/sub/-/single-owner/dashboard", DashboardLink())

	assert.Equal(t, "/repo/issues/1", ShortenPath("/alice/repo/issues/1"))
	assert.Equal(t, "/repo", ShortenPath("/Alice/repo"))
	assert.Equal(t, "/alice/-/projects", ShortenPath("/alice/-/projects"))
	assert.Equal(t, "/alice", ShortenPath("/alice"))
	assert.Equal(t, "/bob/repo", ShortenPath("/bob/repo"))

	OwnerRenamed(8, "carol") // other user: ignored
	assert.True(t, IsOwner("alice"))
	OwnerRenamed(7, "carol")
	assert.True(t, IsOwner("carol"))
	assert.False(t, IsOwner("alice"))
}

func TestIsConfiguredOwner(t *testing.T) {
	defer test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, "")()
	assert.False(t, IsConfiguredOwner(""))
	setting.SingleOwner.PrimaryOwner = "Alice"
	assert.True(t, IsConfiguredOwner("alice"))
	assert.False(t, IsConfiguredOwner("bob"))
}

func TestRoutedPath(t *testing.T) {
	req := httptest.NewRequest("GET", "/repo/raw/branch/main/a%20b", nil)
	assert.Equal(t, "/repo/raw/branch/main/a%20b", RoutedPath(req))
	assert.Equal(t, "/repo/raw/branch/main/a b", RoutedURLPath(req))
	assert.False(t, IsShortRequest(req))

	req = WithRoutedPath(req, "/alice/repo/raw/branch/main/a%20b", true)
	assert.Equal(t, "/alice/repo/raw/branch/main/a%20b", RoutedPath(req))
	assert.Equal(t, "/alice/repo/raw/branch/main/a b", RoutedURLPath(req))
	assert.True(t, IsShortRequest(req))
}
