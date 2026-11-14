// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package markup_test

import (
	"strings"
	"testing"

	"forgejo.org/modules/git"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fork: single-owner mode, see docs/fork/single-owner.md.
func TestRender_SingleOwnerLinks(t *testing.T) {
	defer test.MockVariableValue(&setting.AppURL, markup.TestAppURL)()
	defer singleowner.Reset()

	render := func(t *testing.T, input string) string {
		t.Helper()
		buffer, err := markup.RenderString(&markup.RenderContext{
			Ctx:          git.DefaultContext,
			RelativePath: "a.md",
			Links: markup.Links{
				AbsolutePrefix: true,
				Base:           setting.AppSubURL,
			},
			Metas: localMetas,
		}, input)
		require.NoError(t, err)
		return strings.TrimSpace(buffer)
	}
	sha := "65f1bf27bc3bf70f64657658635e66094edbcb4d"

	cases := []struct {
		input, disabled, enabled string
	}{
		{"#12", "gogits/gogs/issues/12", "gogs/issues/12"},
		{"gogits/gogs#12", "gogits/gogs/issues/12", "gogs/issues/12"},
		{"other/repo#12", "other/repo/issues/12", "other/repo/issues/12"},
		{"gogits/gogs@" + sha, "gogits/gogs/commit/" + sha, "gogs/commit/" + sha},
		{"other/repo@" + sha, "other/repo/commit/" + sha, "other/repo/commit/" + sha},
	}

	t.Run("Disabled", func(t *testing.T) {
		singleowner.Reset()
		for _, c := range cases {
			assert.Contains(t, render(t, c.input), `href="`+markup.TestAppURL+c.disabled+`"`, c.input)
		}
	})
	t.Run("Enabled", func(t *testing.T) {
		singleowner.SetOwner(1, "GoGits") // case-insensitive
		for _, c := range cases {
			assert.Contains(t, render(t, c.input), `href="`+markup.TestAppURL+c.enabled+`"`, c.input)
		}
	})
}
