// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package contribution

import (
	"context"
	"errors"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/graceful"
	"forgejo.org/modules/log"
	"forgejo.org/modules/queue"
	notify_service "forgejo.org/services/notify"
)

// syncRequest asks to update the commit contributions of a repository.
type syncRequest struct {
	RepoID      int64
	OldCommitID string
	NewCommitID string
	Full        bool
}

var syncQueue *queue.WorkerPoolQueue[*syncRequest]

func handler(items ...*syncRequest) []*syncRequest {
	ctx := graceful.GetManager().ShutdownContext()
	for _, item := range items {
		repo, err := repo_model.GetRepositoryByID(ctx, item.RepoID)
		if err != nil {
			if !repo_model.IsErrRepoNotExist(err) {
				log.Error("commit contributions: unable to load repository %d: %v", item.RepoID, err)
			}
			continue
		}
		if item.Full {
			err = FullSync(ctx, repo)
		} else {
			err = IncrementalSync(ctx, repo, item.OldCommitID, item.NewCommitID)
		}
		if err != nil {
			log.Error("commit contributions: unable to sync %-v: %v", repo, err)
		}
	}
	return nil
}

// Init starts the queue that keeps the commit contributions up to date and registers the notifier feeding it.
func Init() error {
	syncQueue = queue.CreateSimpleQueue(graceful.GetManager().ShutdownContext(), "commit_contribution", handler)
	if syncQueue == nil {
		return errors.New("unable to create commit_contribution queue")
	}
	go graceful.GetManager().RunWithCancel(syncQueue)

	notify_service.RegisterNotifier(&contributionNotifier{})
	return nil
}

func enqueue(_ context.Context, req *syncRequest) {
	if syncQueue == nil {
		return
	}
	if err := syncQueue.Push(req); err != nil {
		log.Error("commit contributions: unable to enqueue sync of repository %d: %v", req.RepoID, err)
	}
}

// FlushQueue waits until all pending syncs are done. It is meant for tests.
func FlushQueue(ctx context.Context) error {
	if syncQueue == nil {
		return nil
	}
	return syncQueue.FlushWithContext(ctx, 0)
}
