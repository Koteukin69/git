// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package markdown_test

import (
	"strings"
	"testing"

	"forgejo.org/modules/git"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbed(t *testing.T) {
	setting.AppURL = AppURL

	render := func(mode, input string) string {
		var metas map[string]string
		if mode != "" {
			metas = map[string]string{"mode": mode}
		}
		buffer, err := markdown.RenderString(&markup.RenderContext{
			Ctx:   git.DefaultContext,
			Links: markup.Links{Base: FullURL},
			Metas: metas,
		}, input)
		require.NoError(t, err)
		return strings.TrimSpace(string(buffer))
	}

	t.Run("Document", func(t *testing.T) {
		assert.Equal(t,
			`<p><a class="markup-embed" href="https://www.youtube.com/embed/abc" rel="nofollow">Video</a></p>`,
			render("document", "$[Video](https://www.youtube.com/embed/abc)"))
		assert.Equal(t,
			`<p>Play: <a class="markup-embed" href="https://example.com/game/?a=1&amp;b=2" rel="nofollow">My <strong>game</strong></a>!</p>`,
			render("document", "Play: $[My **game**](https://example.com/game/?a=1&b=2)!"))
	})

	t.Run("CommentIsUnchanged", func(t *testing.T) {
		for _, mode := range []string{"", "comment"} {
			assert.Equal(t,
				`<p>$<a href="https://www.youtube.com/embed/abc" rel="nofollow">Video</a></p>`,
				render(mode, "$[Video](https://www.youtube.com/embed/abc)"))
		}
	})

	t.Run("RawHTMLInComment", func(t *testing.T) {
		assert.Equal(t,
			`<p><a href="https://example.com" rel="nofollow">a</a></p>`,
			render("comment", `<a class="markup-embed" href="https://example.com">a</a>`))
	})

	t.Run("NotEmbeddable", func(t *testing.T) {
		for input, expected := range map[string]string{
			"$[a](b.md)":                      `<p>$<a href="http://localhost:3000/gogits/gogs/b.md" rel="nofollow">a</a></p>`,
			"$[a](ftp://example.com/x)":       `<p>$<a href="ftp://example.com/x" rel="nofollow">a</a></p>`,
			"$[a](http://localhost:3000/u/r)": `<p>$<a href="http://localhost:3000/u/r" rel="nofollow">a</a></p>`,
			"$[a](https://LOCALHOST/u/r)":     `<p>$<a href="https://LOCALHOST/u/r" rel="nofollow">a</a></p>`,
			"$[a](https://u:p@example.com/)":  `<p>$<a href="https://u:p@example.com/" rel="nofollow">a</a></p>`,
			`\$[a](https://example.com)`:      `<p>$<a href="https://example.com" rel="nofollow">a</a></p>`,
			"x$[a](https://example.com)":      `<p>x$<a href="https://example.com" rel="nofollow">a</a></p>`,
			"$[a] text":                       `<p>$[a] text</p>`,
			"`$[a](https://example.com)`":     `<p><code>$[a](https://example.com)</code></p>`,
			"$ [a](https://example.com)":      `<p>$ <a href="https://example.com" rel="nofollow">a</a></p>`,
		} {
			assert.Equal(t, expected, render("document", input), "input: %s", input)
		}
	})

	t.Run("JavaScriptScheme", func(t *testing.T) {
		out := render("document", "$[a](javascript:alert(1))")
		assert.NotContains(t, out, "markup-embed")
		assert.NotContains(t, out, "javascript:")
	})

	t.Run("WithMath", func(t *testing.T) {
		out := render("document", "$[a](https://example.com) and $x$")
		assert.Contains(t, out, `<a class="markup-embed" href="https://example.com" rel="nofollow">a</a> and `)
		assert.Contains(t, out, `language-math`)
	})
}
