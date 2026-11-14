// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import "strings"

// SingleOwner holds the configuration of the fork-specific single-owner mode.
// See docs/fork/single-owner.md.
var SingleOwner = struct {
	// PrimaryOwner is the name of the user or organization whose repositories
	// are served at /<repo>. Empty disables the mode.
	PrimaryOwner string
}{}

func loadSingleOwnerFrom(rootCfg ConfigProvider) {
	sec := rootCfg.Section("single_owner")
	SingleOwner.PrimaryOwner = strings.TrimSpace(sec.Key("PRIMARY_OWNER").MustString(""))
}
