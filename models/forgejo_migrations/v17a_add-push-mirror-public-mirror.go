// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package forgejo_migrations

import (
	"forgejo.org/modules/timeutil"

	"code.forgejo.org/xorm/xorm"
)

func init() {
	registerMigration(&Migration{
		Description: "add public mirror support to push mirrors",
		Upgrade:     addPushMirrorPublicMirror,
	})
}

func addPushMirrorPublicMirror(x *xorm.Engine) error {
	type PushMirror struct {
		PublicProvider     string `xorm:"VARCHAR(20) NOT NULL DEFAULT ''"`
		PublicStars        int    `xorm:"NOT NULL DEFAULT 0"`
		PublicForks        int    `xorm:"NOT NULL DEFAULT 0"`
		PublicWatchers     int    `xorm:"NOT NULL DEFAULT 0"`
		PublicSyncedUnix   timeutil.TimeStamp
		PublicLastSyncUnix timeutil.TimeStamp
		PublicLastError    string `xorm:"text"`
	}
	type PublicMirrorIssue struct {
		ID                int64              `xorm:"pk autoincr"`
		RepoID            int64              `xorm:"UNIQUE(s) NOT NULL"`
		Source            string             `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"`
		RemoteNumber      int64              `xorm:"UNIQUE(s) NOT NULL"`
		IssueID           int64              `xorm:"INDEX NOT NULL"`
		IsPull            bool               `xorm:"NOT NULL DEFAULT false"`
		RemoteURL         string             `xorm:"VARCHAR(2048)"`
		RemoteHeadSHA     string             `xorm:"VARCHAR(64)"`
		RemoteUpdatedUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	}
	type PublicMirrorComment struct {
		ID                int64              `xorm:"pk autoincr"`
		RepoID            int64              `xorm:"UNIQUE(s) NOT NULL"`
		Source            string             `xorm:"VARCHAR(255) UNIQUE(s) NOT NULL"`
		RemoteID          int64              `xorm:"UNIQUE(s) NOT NULL"`
		IssueID           int64              `xorm:"NOT NULL DEFAULT 0"`
		CommentID         int64              `xorm:"INDEX NOT NULL"`
		RemoteUpdatedUnix timeutil.TimeStamp `xorm:"NOT NULL DEFAULT 0"`
	}
	_, err := x.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true}, new(PushMirror), new(PublicMirrorIssue), new(PublicMirrorComment))
	return err
}
