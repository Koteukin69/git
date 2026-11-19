// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/modules/publicmirror"
	"forgejo.org/modules/timeutil"
)

// Providers that a push mirror can use as "public mirror". The public mirror is the public face of a repository:
// stars, forks, issues and pull requests live there, are synced back into Forgejo, and the matching actions in
// Forgejo redirect to it.
const (
	PublicMirrorProviderNone    = ""
	PublicMirrorProviderGitHub  = publicmirror.ProviderGitHub
	PublicMirrorProviderForgejo = publicmirror.ProviderForgejo // Forgejo, Gitea and Codeberg share the same API
)

// IsValidPublicMirrorProvider reports whether p is a known provider (or empty, which disables the feature).
func IsValidPublicMirrorProvider(p string) bool {
	switch p {
	case PublicMirrorProviderNone, PublicMirrorProviderGitHub, PublicMirrorProviderForgejo:
		return true
	}
	return false
}

// PublicUpstream parses the remote address of a public mirror, it returns nil when this push mirror is not a
// public mirror or its address cannot be used.
func (m *PushMirror) PublicUpstream() *publicmirror.Upstream {
	if !m.IsPublic() {
		return nil
	}
	u, err := publicmirror.Parse(m.PublicProvider, m.RemoteAddress)
	if err != nil {
		return nil
	}
	return u
}

// IsPublic reports whether this push mirror is the public mirror of its repository.
func (m *PushMirror) IsPublic() bool {
	return m.PublicProvider != PublicMirrorProviderNone
}

// PublicMirrorIssue maps an issue or pull request of the public mirror to the local issue it was synced into.
type PublicMirrorIssue struct {
	ID                int64              `xorm:"pk autoincr"`
	RepoID            int64              `xorm:"UNIQUE(s) NOT NULL"`
	Source            string             `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"` // host/owner/repo of the upstream
	RemoteNumber      int64              `xorm:"UNIQUE(s) NOT NULL"`
	IssueID           int64              `xorm:"INDEX NOT NULL"`
	IsPull            bool               `xorm:"NOT NULL DEFAULT false"`
	RemoteURL         string             `xorm:"VARCHAR(2048)"`
	RemoteHeadSHA     string             `xorm:"VARCHAR(64)"`
	RemoteUpdatedUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
}

// PublicMirrorComment maps a comment of the public mirror to the local comment it was synced into.
type PublicMirrorComment struct {
	ID                int64              `xorm:"pk autoincr"`
	RepoID            int64              `xorm:"UNIQUE(s) NOT NULL"`
	Source            string             `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"`
	RemoteID          int64              `xorm:"UNIQUE(s) NOT NULL"`
	IssueID           int64              `xorm:"NOT NULL DEFAULT 0"` // local issue the comment was synced into
	CommentID         int64              `xorm:"INDEX NOT NULL"`
	RemoteUpdatedUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
}

func init() {
	db.RegisterModel(new(PublicMirrorIssue))
	db.RegisterModel(new(PublicMirrorComment))
}

// GetPublicPushMirror returns the push mirror configured as public mirror for the repository, or nil.
func GetPublicPushMirror(ctx context.Context, repoID int64) (*PushMirror, error) {
	m := new(PushMirror)
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND public_provider <> ?", repoID, PublicMirrorProviderNone).
		OrderBy("id").Get(m)
	if err != nil || !has {
		return nil, err
	}
	return m, nil
}

// SetPushMirrorPublicProvider sets the public mirror provider of m. As a repository has a single public face,
// enabling it on one push mirror disables it on the other push mirrors of the same repository.
func SetPushMirrorPublicProvider(ctx context.Context, m *PushMirror, provider string) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if provider != PublicMirrorProviderNone {
			if _, err := db.GetEngine(ctx).Table("push_mirror").
				Where("repo_id = ? AND id <> ?", m.RepoID, m.ID).
				Update(map[string]any{"public_provider": PublicMirrorProviderNone}); err != nil {
				return err
			}
		}
		if m.PublicProvider != provider {
			// a different provider means a different API, start over with the incremental sync
			m.PublicSyncedUnix = 0
		}
		m.PublicProvider = provider
		if _, err := db.GetEngine(ctx).ID(m.ID).Cols("public_provider", "public_synced_unix").Update(m); err != nil {
			return err
		}
		return UpdateRepositoryPublicMirrorCounts(ctx, m.RepoID)
	})
}

// UpdatePushMirrorPublicStatus saves the public mirror sync status (counters, cursor and error).
func UpdatePushMirrorPublicStatus(ctx context.Context, m *PushMirror) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).ID(m.ID).Cols("public_stars", "public_forks", "public_watchers",
			"public_synced_unix", "public_last_sync_unix", "public_last_error").Update(m); err != nil {
			return err
		}
		return UpdateRepositoryPublicMirrorCounts(ctx, m.RepoID)
	})
}

// UpdateRepositoryPublicMirrorCounts copies the stars, forks and watchers counts of the public mirror of a
// repository into the repository, so that lists, sorting and the API can use them without loading the push mirrors.
// They are cleared when the repository has no (usable) public mirror anymore, the local counts are used again then.
// It must be called whenever a push mirror becomes, stops being or is deleted while being the public mirror, and
// when the counts are synced.
func UpdateRepositoryPublicMirrorCounts(ctx context.Context, repoID int64) error {
	cols := map[string]any{
		"has_public_mirror":  false,
		"public_num_stars":   0,
		"public_num_forks":   0,
		"public_num_watches": 0,
	}
	m, err := GetPublicPushMirror(ctx, repoID)
	if err != nil {
		return err
	}
	if m != nil && m.PublicUpstream() != nil {
		cols["has_public_mirror"] = true
		cols["public_num_stars"] = m.PublicStars
		cols["public_num_forks"] = m.PublicForks
		cols["public_num_watches"] = m.PublicWatchers
	}
	// NoAutoTime: syncing the counts is not an update of the repository
	_, err = db.GetEngine(ctx).Table("repository").Where("id = ?", repoID).NoAutoTime().Update(cols)
	return err
}

// DisplayNumStars returns the number of stars to show for the repository: the stars of the upstream while it has a
// public mirror (starring redirects there), the local stars otherwise.
func (repo *Repository) DisplayNumStars() int {
	if repo.HasPublicMirror {
		return repo.PublicNumStars
	}
	return repo.NumStars
}

// DisplayNumForks returns the number of forks to show for the repository, see DisplayNumStars.
func (repo *Repository) DisplayNumForks() int {
	if repo.HasPublicMirror {
		return repo.PublicNumForks
	}
	return repo.NumForks
}

// DisplayNumWatches returns the number of watchers to show for the repository, see DisplayNumStars.
func (repo *Repository) DisplayNumWatches() int {
	if repo.HasPublicMirror {
		return repo.PublicNumWatches
	}
	return repo.NumWatches
}

// GetPublicPushMirrorsToSync returns all public mirrors of repositories which are not archived.
func GetPublicPushMirrorsToSync(ctx context.Context) ([]*PushMirror, error) {
	mirrors := make([]*PushMirror, 0, 10)
	return mirrors, db.GetEngine(ctx).Table("push_mirror").
		Join("INNER", "`repository`", "`repository`.id = `push_mirror`.repo_id").
		Where("`push_mirror`.public_provider <> ?", PublicMirrorProviderNone).
		And("`repository`.is_archived = ?", false).
		OrderBy("`push_mirror`.public_last_sync_unix ASC").
		Find(&mirrors)
}

// GetPublicMirrorIssue returns the mapping for an upstream issue number, or nil.
func GetPublicMirrorIssue(ctx context.Context, repoID int64, source string, number int64) (*PublicMirrorIssue, error) {
	m := new(PublicMirrorIssue)
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND source = ? AND remote_number = ?", repoID, source, number).Get(m)
	if err != nil || !has {
		return nil, err
	}
	return m, nil
}

// GetPublicMirrorIssueByIssueID returns the mapping of a local issue, or nil if it was not synced from a public mirror.
func GetPublicMirrorIssueByIssueID(ctx context.Context, issueID int64) (*PublicMirrorIssue, error) {
	m := new(PublicMirrorIssue)
	has, err := db.GetEngine(ctx).Where("issue_id = ?", issueID).Get(m)
	if err != nil || !has {
		return nil, err
	}
	return m, nil
}

// SavePublicMirrorIssue inserts or updates a mapping.
func SavePublicMirrorIssue(ctx context.Context, m *PublicMirrorIssue) error {
	if m.ID == 0 {
		_, err := db.GetEngine(ctx).Insert(m)
		return err
	}
	_, err := db.GetEngine(ctx).ID(m.ID).AllCols().Update(m)
	return err
}

// GetPublicMirrorComment returns the mapping for an upstream comment, or nil.
func GetPublicMirrorComment(ctx context.Context, repoID int64, source string, remoteID int64) (*PublicMirrorComment, error) {
	m := new(PublicMirrorComment)
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND source = ? AND remote_id = ?", repoID, source, remoteID).Get(m)
	if err != nil || !has {
		return nil, err
	}
	return m, nil
}

// SavePublicMirrorComment inserts or updates a mapping.
func SavePublicMirrorComment(ctx context.Context, m *PublicMirrorComment) error {
	if m.ID == 0 {
		_, err := db.GetEngine(ctx).Insert(m)
		return err
	}
	_, err := db.GetEngine(ctx).ID(m.ID).AllCols().Update(m)
	return err
}

// DeletePublicMirrorIssueByID removes a stale mapping (e.g. when the local issue was deleted).
func DeletePublicMirrorIssueByID(ctx context.Context, id int64) error {
	_, err := db.GetEngine(ctx).ID(id).Delete(new(PublicMirrorIssue))
	return err
}

// DeletePublicMirrorCommentByID removes a stale mapping (e.g. when the local comment was deleted).
func DeletePublicMirrorCommentByID(ctx context.Context, id int64) error {
	_, err := db.GetEngine(ctx).ID(id).Delete(new(PublicMirrorComment))
	return err
}
