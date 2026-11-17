// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package markup

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeEmbed(t *testing.T) {
	sanitize := func(renderer string, isDocument bool, input string) string {
		var out strings.Builder
		require.NoError(t, sanitizeReader(strings.NewReader(input), renderer, isDocument, &out))
		return out.String()
	}

	link := `<a class="markup-embed" href="https://example.com">a</a>`

	// only markdown documents may carry the embed marker
	assert.Equal(t, `<a class="markup-embed" href="https://example.com" rel="nofollow">a</a>`, sanitize("markdown", true, link))
	assert.Equal(t, `<a href="https://example.com" rel="nofollow">a</a>`, sanitize("markdown", false, link))
	assert.Equal(t, `<a href="https://example.com" rel="nofollow">a</a>`, sanitize("orgmode", true, link))
	assert.Equal(t, `<a href="https://example.com" rel="nofollow">a</a>`, Sanitize(link))

	// other classes are still not allowed, and iframes are still removed
	assert.Equal(t, `<a href="https://example.com" rel="nofollow">a</a>`, sanitize("markdown", true, `<a class="markup-embed x" href="https://example.com">a</a>`))
	assert.Empty(t, sanitize("markdown", true, `<iframe src="https://example.com"></iframe>`))
}
