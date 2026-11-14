// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package singleowner implements the database dependent parts of the
// fork-specific single-owner mode: resolving the configured owner at startup
// and routing short /<repo> URLs. See docs/fork/single-owner.md.
package singleowner

import (
	"context"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/singleowner"
)

// Init resolves the configured primary owner and activates the mode. A
// missing owner never prevents the instance from starting: the mode simply
// stays disabled and a warning is logged.
func Init(ctx context.Context) error {
	singleowner.Reset()
	name := setting.SingleOwner.PrimaryOwner
	if name == "" {
		return nil
	}

	owner, err := user_model.GetUserByName(ctx, name)
	if err != nil {
		if !user_model.IsErrUserNotExist(err) {
			return err
		}
		owner = nil
		if redirect, err := user_model.GetUserRedirect(ctx, name); err == nil {
			if owner, err = user_model.GetUserByID(ctx, redirect.RedirectUserID); err == nil {
				log.Warn("[single_owner] PRIMARY_OWNER %q has been renamed to %q, please update app.ini", name, owner.Name)
			} else {
				owner = nil
			}
		}
	}
	if owner == nil || (!owner.IsIndividual() && !owner.IsOrganization()) {
		log.Warn("[single_owner] PRIMARY_OWNER %q is not an existing user or organization, single-owner mode is disabled", name)
		return nil
	}

	singleowner.SetOwner(owner.ID, owner.Name)
	log.Info("[single_owner] Single-owner mode enabled: repositories of %q are served at /<repo>", owner.Name)
	return warnAboutConflicts(ctx, owner)
}

// warnAboutConflicts logs every existing repository of the primary owner
// whose name collides with a system route or with a user or organization.
func warnAboutConflicts(ctx context.Context, owner *user_model.User) error {
	repos := make([]*repo_model.Repository, 0, 10)
	if err := db.GetEngine(ctx).Where("owner_id = ?", owner.ID).Cols("name").Find(&repos); err != nil {
		return err
	}
	for _, repo := range repos {
		if user_model.IsReservedTopLevelName(repo.Name) {
			log.Warn("[single_owner] Repository %q collides with a system route; it is only reachable at /%s/%s", repo.Name, owner.Name, repo.Name)
			continue
		}
		exist, err := user_model.IsUserExist(ctx, 0, repo.Name)
		if err != nil {
			return err
		}
		if exist {
			log.Warn("[single_owner] Repository %q has the same name as a user or organization; the repository wins at /%s, the profile is reachable at %s/profile/%s", repo.Name, repo.Name, singleowner.RoutePrefix, repo.Name)
		}
	}
	return nil
}
