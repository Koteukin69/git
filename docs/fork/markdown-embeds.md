# Markdown embeds (fork feature)

Markdown documents can embed external pages, such as videos or web games, as
sandboxed iframes:

```markdown
$[Intro video](https://www.youtube.com/embed/dQw4w9WgXcQ)
$[My game](https://html-classic.itch.zone/html/12345/index.html)
```

On other forges, and anywhere the feature does not apply, this renders as a
literal `$` followed by an ordinary link, so READMEs stay readable everywhere.

## Where it works

Only in markdown *documents*: repository READMEs, `.md` files, wiki pages,
user/organization profile READMEs and the "preview" of those files in the
web editor. In issues, pull requests, comments and releases the syntax renders
as `$` + link. The marker class cannot be forged with raw HTML in comments,
because only the document sanitizer policy allows it.

A link is embedded only when it is an absolute `https://` URL (also `http://`
when the forge itself is served over plain http) whose host is not the forge
itself and has no `user:password@`. Other links stay ordinary links.

Use the *embed* URL of a service, e.g. `https://www.youtube.com/embed/<id>`
rather than `https://www.youtube.com/watch?v=<id>`. Sites that forbid framing
(`X-Frame-Options`, `frame-ancestors`) show the browser's "refused to
connect" page.

## Security

The server never emits `<iframe>`; it renders
`<a class="markup-embed" href="…">name</a>` and
`web_src/js/markup/embed.js` replaces it by:

```html
<iframe sandbox="allow-scripts allow-same-origin allow-forms allow-popups
                 allow-popups-to-escape-sandbox allow-pointer-lock allow-presentation"
        allow="autoplay; encrypted-media; fullscreen; picture-in-picture; gamepad"
        allowfullscreen credentialless referrerpolicy="strict-origin" loading="lazy"
        src="…" title="name"></iframe>
```

- The embedded page runs in its own origin. It cannot read the forge page,
  its cookies or its CSRF token, and the forge's cookies are never sent to it.
- No `allow-top-navigation`: it cannot navigate the forge page away. No
  `allow-modals`: no `alert()` spam. Pop-ups (e.g. "watch on YouTube") open
  in a new, unsandboxed tab.
- Same-origin URLs are refused on the server and again in the browser. Combining
  `allow-scripts` with `allow-same-origin` would let a forge-hosted page
  remove its own sandbox.
- `credentialless` (Chromium) loads the embed without the visitor's existing
  cookies for that site and with throwaway storage. Firefox and Safari ignore it.
- `referrerpolicy="strict-origin"` sends only the forge origin, not the page
  path. YouTube refuses to play without a referrer.
- The visitor's browser contacts the embedded site when the iframe scrolls
  into view (`loading="lazy"`), so that site sees the visitor's IP address.

## Files touched (for rebases)

- `modules/markup/markdown/embed.go` (new): `$` marker parser and renderer, transformer
- `modules/markup/markdown/markdown.go`: registers the parser and renderer
- `modules/markup/markdown/goldmark.go`: calls `transformEmbed` for links
- `modules/markup/sanitizer.go`, `modules/markup/renderer.go`: document sanitizer policy
- `web_src/js/markup/embed.js` (new), `web_src/js/markup/content.js`
- `web_src/css/markup/content.css`
- tests: `modules/markup/markdown/embed_test.go`, `modules/markup/sanitizer_embed_test.go`,
  `web_src/js/markup/embed.test.js`
