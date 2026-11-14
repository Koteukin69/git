// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package private

import "forgejo.org/modules/singleowner"

// singleOwnerServOwner maps the owner name configured for single-owner mode to
// the primary owner's current name. `forgejo serv` has no database access and
// prefixes short SSH paths with the name from app.ini, which is stale after
// the owner has been renamed. See docs/fork/single-owner.md.
func singleOwnerServOwner(ownerName string) string {
	if singleowner.Active() && singleowner.IsConfiguredOwner(ownerName) && !singleowner.IsOwner(ownerName) {
		return singleowner.OwnerName()
	}
	return ownerName
}
