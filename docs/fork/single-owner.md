# Single-owner mode (fork feature)

On a personal instance the owner segment of a repository URL is redundant.
Single-owner mode serves all repositories of one configured **primary owner**
(a user or an organization) at the root of the instance:

| Upstream                                 | Single-owner mode                   |
|------------------------------------------|-------------------------------------|
| `https://git.example.com/alice/notes`    | `https://git.example.com/notes`     |
| `https://git.example.com/alice/notes.git`| `https://git.example.com/notes.git` |
| `git@git.example.com:alice/notes.git`    | `git@git.example.com:notes.git`     |
| `https://git.example.com/alice`          | `https://git.example.com/`          |

When the option is not set, Forgejo behaves exactly like upstream.

## Enabling it

Add to `app.ini` and restart:

```ini
[single_owner]
;; Name of the user or organization whose repositories are served at /<repo>.
;; Empty (the default) disables the mode.
PRIMARY_OWNER = alice
```

With the stock Docker image the same can be done through the environment:
`FORGEJO__single_owner__PRIMARY_OWNER=alice`.

At startup the log says either
`Single-owner mode enabled: repositories of "alice" are served at /<repo>` or,
if the owner does not exist,
`PRIMARY_OWNER "alice" is not an existing user or organization, single-owner mode is disabled`
— the instance starts normally in that case, with upstream behaviour.

Enabling or disabling the mode needs no migration and changes nothing in the
database; it can be turned off at any time.

## Behaviour

### Incoming URLs

* `/<repo>` and every sub-page (`/<repo>/issues/1`, `/<repo>/src/branch/main/…`,
  releases, wiki, settings, raw, media, archives, compare, feeds `/<repo>.rss`, …)
  open the primary owner's repository.
* `GET`/`HEAD` requests to `/<owner>/<repo>/…` are answered with
  `301 Moved Permanently` to `/<repo>/…`, keeping the rest of the path and the
  query string. `/<owner>` redirects to `/`.
* Served in place at the old URL, **without** a redirect, so existing clients
  keep working: non-GET requests (form posts), git smart/dumb HTTP
  (`info/refs`, `git-upload-pack`, `git-receive-pack`, `*.git/…`), LFS
  (`info/lfs/…`), `?go-get=1`, and owner-level pages (`/<owner>/-/projects`,
  `/<owner>/-/packages`, …).
* `/` shows the primary owner's profile (or organization home) to everyone.
  The personal dashboard of signed-in users moves to
  **`/-/single-owner/dashboard`**; the navbar logo links there for signed-in users.
* System routes (`/explore`, `/user/…`, `/admin`, `/api/…`, `/assets/…`, `/v2`, …)
  are unchanged, and so are the URLs of all other users and organizations
  (`/<user>`, `/<user>/<repo>`).
* API paths do not change: `/api/v1/repos/<owner>/<repo>/…`.
* A renamed repository redirects from its old short URL to the new one, as
  upstream does for long URLs.

### Git

* HTTPS: `git clone https://git.example.com/<repo>.git` (fetch, push, LFS,
  password or token authentication). `https://git.example.com/<owner>/<repo>.git`
  keeps working.
* SSH: `git@git.example.com:<repo>.git` and
  `ssh://git@git.example.com:<port>/<repo>.git`, plus the old form with the owner.
  This works with both the OpenSSH setup of the Docker image and the built-in
  SSH server, since both run `forgejo serv`.

### Generated links

Every link to a repository of the primary owner that the instance generates
uses the short form: web UI, clone buttons (HTTPS and SSH), emails, RSS/Atom
feeds, rendered markdown (issue/commit references, `owner/repo#1`,
`owner/repo@sha`), `go get` meta tags, and web-URL fields such as `html_url`,
`clone_url` and `ssh_url` in API responses and webhook payloads.

`go get` meta tags declare the import path that was requested:
`git.example.com/<repo>` for short requests, `git.example.com/<owner>/<repo>`
for long ones, so existing `go.mod` imports keep resolving.

### Permissions and other users

Short URLs are served by exactly the same handlers as the long URLs, so
collaborators, teams (if the primary owner is an organization), visibility and
branch protection work as upstream, over the web, HTTPS and SSH. Other users'
repositories and forks keep `/<user>/<repo>`; a fork of a primary-owner
repository links back to its parent with the short URL.

## Name conflicts

The primary owner's repositories share the top-level namespace with system
routes, users and organizations. While the mode is configured:

* A repository of the primary owner cannot be created, forked, generated,
  migrated, adopted, pushed-to-create, renamed or transferred into the
  primary owner under a name that is a reserved system name (`explore`, `api`,
  `admin`, `user`, `assets`, …, plus `*.keys`, `*.gpg`, `*.png`) or the name of
  an existing user or organization. The user gets the standard
  "name is reserved" error.
* A user or organization cannot be created (web sign-up, admin UI, API, CLI,
  OAuth2/LDAP/SMTP/PAM/reverse-proxy auto-registration) or renamed to the name
  of an existing repository of the primary owner. Same "name is reserved" error.

These checks read `PRIMARY_OWNER` from `app.ini`, so they also apply to CLI
commands such as `forgejo admin user create`.

Conflicts that already exist when the mode is enabled do not prevent startup.
Each one is logged as a warning, and:

* **Repository vs. system route** — the system route wins. The repository stays
  reachable at its long URL `/<owner>/<repo>` (no redirect to the short form).
* **Repository vs. user or organization** — the repository wins at `/<name>`.
  The user's or organization's pages stay reachable through the fallback
  **`/-/single-owner/profile/<name>[/…]`**, e.g.
  `/-/single-owner/profile/bob` and `/-/single-owner/profile/bob/dotfiles`.
  The fallback only serves existing users and organizations; any other name,
  including reserved ones such as `api` or `admin`, returns 404.

## Renaming the primary owner

Short URLs do not contain the owner, so they keep working. Long URLs with the
old owner name are redirected by upstream's user-redirect mechanism. The
instance resolves the configured name through the user-redirect table, so
short SSH URLs also keep working with the old name in `app.ini`, but a warning
is logged at startup until `PRIMARY_OWNER` is updated — please update it. If
another account later takes over the old name, `PRIMARY_OWNER` must be updated.

## Limitations

* **Federation (ActivityPub):** actor IDs and URLs are unchanged.
* **Mentions** of a user whose name equals a repository of the primary owner
  (a pre-existing conflict) lead to the repository. Links generated inside
  the fallback profile `/-/single-owner/profile/<name>` still point to `/<name>/…`.
* **Pasted absolute short URLs** in markdown
  (`https://git.example.com/notes/issues/1`) are not turned into issue reference
  widgets; they render as plain links. References such as `alice/notes#1` and
  `#1` are rendered with short links.
* **Stored data** generated before the mode was enabled (compare URLs in old
  activity entries, links in sent emails or delivered webhooks) keeps the long
  form and is served through the 301 redirect.
* **Machine-facing links left in long form**, which work in place without a
  redirect: LFS batch `href`s, the `git-lfs-authenticate` SSH response, the
  Actions context (`github.server_url`/`github.repository`, so
  `actions/checkout` clones the long URL), the stopwatch link built from the API,
  abuse-report content links, and `html_url` of the owner itself (`/<owner>`,
  which redirects to `/`).
* **Private or limited owner:** `/` follows upstream visibility rules for the
  owner's profile, so anonymous visitors may get a 404 or a login prompt.
* **Cost:** a request whose first path segment is not a system route costs one
  or two extra indexed database queries.
* Only one primary owner; no per-user domains.

## Rebase cheat sheet

New files (no conflicts expected on rebase):

| File | Purpose |
|------|---------|
| `modules/setting/single_owner.go` | `[single_owner] PRIMARY_OWNER` setting |
| `modules/singleowner/singleowner.go` | runtime state, pure path helpers, routed-path request context |
| `services/singleowner/init.go` | resolve the owner at startup, warn about conflicts |
| `services/singleowner/middleware.go` | routing of short URLs, 301 redirects, fork routes |
| `models/repo/single_owner.go` | repository name conflict check |
| `models/user/single_owner.go` | user/org name conflict check, owner resolution |
| `services/user/single_owner.go` | keep cached owner name current on rename |
| `routers/private/single_owner.go` | map stale configured owner name for SSH |
| `cmd/serv_single_owner.go` | parse one-segment SSH paths |
| `web_src/js/features/single-owner.js` | issue popup info URL from link |
| `docs/fork/single-owner.md` | this document |
| `*_test.go`, `tests/integration/single_owner_test.go` | tests |

Upstream files touched (every hunk is marked `// fork: single-owner mode`
where the language allows comments):

| File | Change | Why |
|------|--------|-----|
| `modules/setting/setting.go` | `loadSingleOwnerFrom(cfg)` in `loadCommonSettingsFrom` | load the setting in every process, including `forgejo serv` |
| `routers/init.go` | `mustInitCtx(ctx, singleowner_service.Init)` + import | resolve the owner after the DB is ready |
| `routers/common/middleware.go` | add `singleowner_service.Middleware` after `stripSlashesMiddleware` + import | route short URLs before chi matches a route |
| `models/repo/repo.go` | `HTMLURL`, `Link`, `ComposeCompareURL`, `ComposeHTTPSCloneURL`, `ComposeSSHCloneURL` use `singleowner.RepoPath` + import | central repository link builders (UI, API, webhooks, mail, feeds, clone URLs) |
| `models/activities/action.go` | `GetRepoLink`, `GetRepoAbsoluteLink` use `RepoPath`; `net/url` import dropped | dashboard activity feed and user RSS |
| `modules/markup/html.go` | 4 `util.URLJoin` calls use `singleowner.RepoRelPath` + import | issue/commit references in rendered markup |
| `modules/markup/renderer.go` | external-render iframe URL + import | iframe `src` for external renderers |
| `modules/markup/asciicast/asciicast.go` | raw URL + import | asciicast player source |
| `services/context/repo.go` | `RedirectToRepo` and `.wiki` redirect use `RoutedPath`/`ShortenPath`; `RepoRelPath` data + import | renamed-repo redirects from short URLs; JS issue links |
| `services/auth/method/auth.go` | 3 path matchers use `singleowner.RoutedURLPath` + import | allow basic/token auth for git, raw, LFS, archive on short URLs |
| `routers/web/goget.go` | parse `RoutedPath`; source prefix via `RepoPath`; short import path for short requests + import | `go get` on short import paths |
| `routers/web/repo/compare.go` | `SourceCommitURL`, `RawCommitURL` use `RepoPath` + import | links in compare/diff views |
| `routers/web/repo/pull.go` | 2 redirects after fork use `RepoPath`; `net/url` import dropped | land on the short URL after forking |
| `routers/web/repo/migrate.go` | redirect after migration uses `RepoPath`; `net/url` import dropped | land on the short URL after migration |
| `models/user/user.go` | `DashboardLink` returns `singleowner.DashboardLink()`; `createUser` calls `CheckSingleOwnerNameConflict` + import | dashboard moved to `/-/single-owner/dashboard`; block user names that equal owner repos (all sign-up paths) |
| `models/organization/org.go` | `CreateOrganization` calls `CheckSingleOwnerNameConflict` | block org names that equal owner repos |
| `services/user/user.go` | `renameUser` calls `CheckSingleOwnerNameConflict`; `RenameUser`/`AdminRenameUser` wrap with `singleOwnerRenamed` | block renames to owner repo names; keep the cached owner name current |
| `modules/repository/create.go` | `CreateRepositoryByExample` calls `CheckSingleOwnerRepoName` | block conflicting repo names on create/fork/generate/migrate/adopt/push-create |
| `models/repo/update.go` | `CheckCreateRepository` calls `CheckSingleOwnerRepoName` | migration pre-check |
| `services/repository/transfer.go` | `changeRepositoryName` and `transferOwnership` call `CheckSingleOwnerRepoName` | block conflicting renames and transfers into the owner |
| `cmd/serv.go` | `singleOwnerServPath` after splitting the path; recompute `repoPath` from the `ServCommand` result | SSH paths without owner, also after an owner rename |
| `routers/private/serv.go` | `ownerName = singleOwnerServOwner(ownerName)` | map the stale configured owner name after a rename |
| `templates/base/head_navbar.tmpl` | logo `href` uses `.SignedUser.DashboardLink` for signed-in users | dashboard moved off `/` |
| `templates/repo/issue/view_content/sidebar/due_deadline.tmpl` | form action uses `.Repository.Link` | was built from owner/name |
| `templates/repo/header_fork.tmpl` | fork link uses `.Link` | was built from `FullName` |
| `templates/org/team/repositories.tmpl` | repo link uses `.Link` | was built from org home link + name |
| `web_src/js/features/contextpopup.js` | compute `infoUrl` from the link, pass it in the event | issue hover popups on short links |
| `web_src/js/components/ContextPopup.vue` | fetch `data.infoUrl` if present | same |

Rebase tips:

* `grep -rn "fork: single-owner mode"` lists the Go/template hunks.
* If upstream adds a new place that builds `owner + "/" + repo` URLs, route it
  through `singleowner.RepoPath` / `RepoRelPath`; if it parses
  `req.URL.Path` for an owner/repo shape, use `singleowner.RoutedPath` /
  `RoutedURLPath`.
* If upstream adds a new top-level route, add its first segment to
  `reservedUsernames` (upstream does this already for user names); the mode
  relies on that list.
* Run `go test ./modules/singleowner/ ./models/repo/ ./models/user/ ./modules/markup/`
  and the integration tests `TestSingleOwner*`.
