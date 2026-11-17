// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package markdown

import (
	"net/url"
	"regexp"
	"strings"

	"forgejo.org/modules/setting"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Embeds use the syntax `$[name](https://example.com/page)`.
//
// The parser only recognizes the leading `$` (as an embedMarker node); the
// `[name](link)` part is parsed by goldmark's regular link parser. The AST
// transformer then turns a marker directly followed by a valid link into
// `<a class="markup-embed" href="...">name</a>`, which the frontend replaces
// by a sandboxed iframe. In every other case the marker is rendered as a
// literal `$`, so the output is identical to what it was before.

// EmbedClass is the class put on links that should be rendered as embeds.
const EmbedClass = "markup-embed"

// KindEmbedMarker is the NodeKind for embedMarker.
var KindEmbedMarker = ast.NewNodeKind("EmbedMarker")

type embedMarker struct {
	ast.BaseInline
}

func (n *embedMarker) Kind() ast.NodeKind { return KindEmbedMarker }

func (n *embedMarker) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

var embedStartRE = regexp.MustCompile(`^\$\[[^\]\n]*\]\(`)

type embedMarkerParser struct{}

func (embedMarkerParser) Trigger() []byte { return []byte{'$'} }

func (embedMarkerParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	if !embedStartRE.Match(line) {
		return nil
	}
	prev := block.PrecendingCharacter()
	if prev == '\\' || prev == '$' || (prev < 256 && isAlphanumeric(byte(prev))) {
		return nil
	}
	block.Advance(1)
	return &embedMarker{}
}

func isAlphanumeric(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

type embedMarkerRenderer struct{}

func (embedMarkerRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindEmbedMarker, func(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			// An unconsumed marker is just the literal `$` it was parsed from.
			if err := w.WriteByte('$'); err != nil {
				return ast.WalkStop, err
			}
		}
		return ast.WalkContinue, nil
	})
}

// isEmbeddableURL reports whether link is an absolute http(s) URL that does
// not point to this instance. Same-origin pages must never be embedded: with
// scripts and same-origin allowed in the sandbox they could escape it.
func isEmbeddableURL(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	if appURL, err := url.Parse(setting.AppURL); err == nil && strings.EqualFold(u.Hostname(), appURL.Hostname()) {
		return false
	}
	return true
}

// transformEmbed converts `$[name](link)` into an embed link in documents.
func (g *ASTTransformer) transformEmbed(isDocument bool, v *ast.Link) {
	marker, ok := v.PreviousSibling().(*embedMarker)
	if !ok || !isDocument || !isEmbeddableURL(string(v.Destination)) {
		return
	}
	marker.Parent().RemoveChild(marker.Parent(), marker)
	v.SetAttributeString("class", []byte(EmbedClass))
}
