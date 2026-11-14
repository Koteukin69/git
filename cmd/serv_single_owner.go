// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import "forgejo.org/modules/setting"

// singleOwnerServPath maps the short SSH path "<repo>.git" of single-owner mode
// to the primary owner's repository. rr is the repository path split at the
// first slash. Paths with an owner are returned unchanged.
// See docs/fork/single-owner.md.
func singleOwnerServPath(rr []string, repoPath string) ([]string, string, bool) {
	if len(rr) != 1 || setting.SingleOwner.PrimaryOwner == "" {
		return rr, repoPath, false
	}
	rr = []string{setting.SingleOwner.PrimaryOwner, rr[0]}
	return rr, rr[0] + "/" + rr[1], true
}
