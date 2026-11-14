// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package user_test

import (
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fork: single-owner mode, see docs/fork/single-owner.md.
func TestCheckSingleOwnerNameConflict(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, "")()
	defer singleowner.Reset()
	ctx := db.DefaultContext

	require.NoError(t, user_model.CheckSingleOwnerNameConflict(ctx, "repo1"))

	setting.SingleOwner.PrimaryOwner = "user2"
	for _, name := range []string{"repo1", "REPO1", "lfs"} {
		err := user_model.CheckSingleOwnerNameConflict(ctx, name)
		assert.True(t, db.IsErrNameReserved(err), "%s: %v", name, err)
	}
	require.NoError(t, user_model.CheckSingleOwnerNameConflict(ctx, "repo3")) // org3's repo
	require.NoError(t, user_model.CheckSingleOwnerNameConflict(ctx, "brand-new"))

	// the owner is resolved through the user redirect table after a rename
	setting.SingleOwner.PrimaryOwner = "olduser1"
	id, err := user_model.SingleOwnerPrimaryOwnerID(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, id)

	setting.SingleOwner.PrimaryOwner = "nobody-here"
	id, err = user_model.SingleOwnerPrimaryOwnerID(ctx)
	require.NoError(t, err)
	assert.Zero(t, id)
}

func TestCreateUserSingleOwnerConflict(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SingleOwner.PrimaryOwner, "user2")()

	u := &user_model.User{Name: "repo1", Email: "repo1@example.com", Passwd: "password1!", MustChangePassword: false}
	err := user_model.CreateUser(db.DefaultContext, u)
	assert.True(t, db.IsErrNameReserved(err), "%v", err)
}

func TestIsReservedTopLevelName(t *testing.T) {
	for _, name := range []string{"api", "explore", "assets", "-", "x.keys", "x.png", "Admin"} {
		assert.True(t, user_model.IsReservedTopLevelName(name), name)
	}
	for _, name := range []string{"repo1", "my-project", "x.rss2"} {
		assert.False(t, user_model.IsReservedTopLevelName(name), name)
	}
}
