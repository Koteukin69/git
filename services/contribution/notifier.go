// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package contribution

import (
	"context"

	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/repository"
	notify_service "forgejo.org/services/notify"
)

type contributionNotifier struct {
	notify_service.NullNotifier
}

var _ notify_service.Notifier = &contributionNotifier{}

func (n *contributionNotifier) pushed(ctx context.Context, repo *repo_model.Repository, opts *repository.PushUpdateOptions) {
	if !opts.RefFullName.IsBranch() || opts.RefFullName.BranchName() != repo.DefaultBranch {
		return
	}
	if opts.IsDelRef() {
		enqueue(ctx, &syncRequest{RepoID: repo.ID, Full: true})
		return
	}
	enqueue(ctx, &syncRequest{RepoID: repo.ID, OldCommitID: opts.OldCommitID, NewCommitID: opts.NewCommitID})
}

func (n *contributionNotifier) PushCommits(ctx context.Context, _ *user_model.User, repo *repo_model.Repository, opts *repository.PushUpdateOptions, _ *repository.PushCommits) {
	n.pushed(ctx, repo, opts)
}

func (n *contributionNotifier) SyncPushCommits(ctx context.Context, _ *user_model.User, repo *repo_model.Repository, opts *repository.PushUpdateOptions, _ *repository.PushCommits) {
	n.pushed(ctx, repo, opts)
}

func (n *contributionNotifier) MigrateRepository(ctx context.Context, _, _ *user_model.User, repo *repo_model.Repository) {
	enqueue(ctx, &syncRequest{RepoID: repo.ID, Full: true})
}

func (n *contributionNotifier) AdoptRepository(ctx context.Context, _, _ *user_model.User, repo *repo_model.Repository) {
	enqueue(ctx, &syncRequest{RepoID: repo.ID, Full: true})
}

func (n *contributionNotifier) ChangeDefaultBranch(ctx context.Context, repo *repo_model.Repository) {
	enqueue(ctx, &syncRequest{RepoID: repo.ID, Full: true})
}
