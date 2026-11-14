// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package user

import (
	"context"
	"strings"

	"forgejo.org/models/db"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
)

// Fork: single-owner mode, see docs/fork/single-owner.md.

// IsReservedTopLevelName reports whether /<name> is owned by a system route or
// a user-specific path pattern (keys, avatar, ...), so that it can't be used
// for a repository of the primary owner.
func IsReservedTopLevelName(name string) bool {
	return db.IsUsableName(reservedUsernames, reservedUserPatterns, name) != nil
}

// SingleOwnerPrimaryOwnerID returns the ID of the configured primary owner, or
// 0 if single-owner mode is not configured or the owner does not exist. It
// works without the runtime state of the web process (e.g. in CLI commands)
// and follows user redirects if the owner was renamed.
func SingleOwnerPrimaryOwnerID(ctx context.Context) (int64, error) {
	if singleowner.Active() {
		return singleowner.OwnerID(), nil
	}
	name := setting.SingleOwner.PrimaryOwner
	if name == "" {
		return 0, nil
	}
	u, err := GetUserByName(ctx, name)
	if err == nil {
		return u.ID, nil
	}
	if !IsErrUserNotExist(err) {
		return 0, err
	}
	redirect, err := GetUserRedirect(ctx, name)
	if err != nil {
		if IsErrUserRedirectNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return redirect.RedirectUserID, nil
}

// CheckSingleOwnerNameConflict returns db.ErrNameReserved if a user or an
// organization named name would collide with a repository of the primary
// owner, which is served at /<name> in single-owner mode.
func CheckSingleOwnerNameConflict(ctx context.Context, name string) error {
	ownerID, err := SingleOwnerPrimaryOwnerID(ctx)
	if err != nil || ownerID == 0 {
		return err
	}
	exist, err := db.GetEngine(ctx).Table("repository").
		Where("owner_id = ? AND lower_name = ?", ownerID, strings.ToLower(name)).Exist()
	if err != nil {
		return err
	}
	if exist {
		return db.ErrNameReserved{Name: name}
	}
	return nil
}
