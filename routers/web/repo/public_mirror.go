// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"net/http"
	"net/url"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/publicmirror"
	"forgejo.org/services/context"
)

// RedirectToPublicMirror is a middleware redirecting to the public mirror of the repository, if it has one. Starring,
// forking and opening issues or pull requests happen there.
func RedirectToPublicMirror(target func(*publicmirror.Upstream) string) func(ctx *context.Context) {
	return func(ctx *context.Context) {
		if ctx.Repo.PublicUpstream == nil {
			return
		}
		location := target(ctx.Repo.PublicUpstream)
		// keep the prefilled title/body of "reference in new issue", GitHub and Forgejo/Gitea both support them
		q := url.Values{}
		for _, key := range []string{"title", "body"} {
			if v := ctx.Req.URL.Query().Get(key); v != "" {
				q.Set(key, v)
			}
		}
		if len(q) > 0 && ctx.Req.Method == http.MethodGet {
			location += "?" + q.Encode()
		}
		ctx.Redirect(location)
	}
}

// publicMirrorItem returns the mapping of an issue or pull request synced from the current public mirror, or nil.
func publicMirrorItem(ctx *context.Context, issue *issues_model.Issue) *repo_model.PublicMirrorIssue {
	if ctx.Repo.PublicUpstream == nil || issue == nil {
		return nil
	}
	item, err := repo_model.GetPublicMirrorIssueByIssueID(ctx, issue.ID)
	if err != nil {
		log.Error("GetPublicMirrorIssueByIssueID(%d): %v", issue.ID, err)
		return nil
	}
	if item == nil || item.Source != ctx.Repo.PublicUpstream.Source() {
		return nil
	}
	// never trust the stored URL, it comes from the upstream API and is rendered as a link
	item.RemoteURL = ctx.Repo.PublicUpstream.ItemURL(item.RemoteURL, item.RemoteNumber, item.IsPull)
	return item
}
