// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"

	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
)

// Fork: single-owner mode, see docs/fork/single-owner.md.

// CheckSingleOwnerRepoName returns db.ErrNameReserved if a repository named
// name, owned by ownerID, would not be reachable at /<name> in single-owner
// mode, because a system route or an existing user or organization already
// uses that top-level name. It is a no-op for every other owner and when the
// mode is not configured.
func CheckSingleOwnerRepoName(ctx context.Context, ownerID int64, name string) error {
	primaryID, err := user_model.SingleOwnerPrimaryOwnerID(ctx)
	if err != nil || primaryID == 0 || primaryID != ownerID {
		return err
	}
	if user_model.IsReservedTopLevelName(name) {
		return db.ErrNameReserved{Name: name}
	}
	exist, err := user_model.IsUserExist(ctx, 0, name)
	if err != nil {
		return err
	}
	if exist {
		return db.ErrNameReserved{Name: name}
	}
	return nil
}
