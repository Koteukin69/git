// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package user

import (
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/singleowner"
)

// singleOwnerRenamed keeps the cached name of the single-owner mode's primary
// owner up to date after a successful rename. See docs/fork/single-owner.md.
func singleOwnerRenamed(u *user_model.User, err error) error {
	if err == nil {
		singleowner.OwnerRenamed(u.ID, u.Name)
	}
	return err
}
