// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	giturl "forgejo.org/modules/git/url"
	issue_indexer "forgejo.org/modules/indexer/issues"
	"forgejo.org/modules/log"
	"forgejo.org/modules/process"
	"forgejo.org/modules/publicmirror"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/sync"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
	mirror_service "forgejo.org/services/mirror"
	pull_service "forgejo.org/services/pull"
)

// syncOverlap is subtracted from the cursor to tolerate clock skew and items updated while a sync was running.
const syncOverlap = 2 * time.Minute

var (
	repoLocks = sync.NewExclusivePool()
	// addToTaskQueue checks the mergeability of a pull request, replaced in tests
	addToTaskQueue = pull_service.AddToTaskQueue
)

func init() {
	mirror_service.PublicMirrorSyncer = Sync
}

// SyncAll syncs every public mirror. It is run by the cron task.
func SyncAll(ctx context.Context) error {
	mirrors, err := repo_model.GetPublicPushMirrorsToSync(ctx)
	if err != nil {
		return err
	}
	for _, m := range mirrors {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := Sync(ctx, m.ID); err != nil {
			log.Warn("public mirror sync of push mirror %d failed: %v", m.ID, err)
		}
	}
	return nil
}

// Sync syncs the counters, issues, pull requests and comments of the public mirror m back into the repository.
// It does nothing when m is not (or no longer) a public mirror. The error is also saved on the push mirror.
func Sync(ctx context.Context, pushMirrorID int64) error {
	m, exist, err := db.GetByID[repo_model.PushMirror](ctx, pushMirrorID)
	if err != nil || !exist || !m.IsPublic() {
		return err
	}
	repoLocks.CheckIn(fmt.Sprint(m.RepoID))
	defer repoLocks.CheckOut(fmt.Sprint(m.RepoID))

	// reload under the lock, another sync may just have finished
	m, exist, err = db.GetByID[repo_model.PushMirror](ctx, pushMirrorID)
	if err != nil || !exist || !m.IsPublic() {
		return err
	}
	if m.GetRepository(ctx) == nil {
		return fmt.Errorf("repository %d of push mirror %d not found", m.RepoID, m.ID)
	}

	ctx, _, finished := process.GetManager().AddContext(ctx, fmt.Sprintf("Syncing public mirror %s of %s", m.RemoteAddress, m.Repo.FullName()))
	defer finished()

	syncErr := runSync(ctx, m)
	m.PublicLastSyncUnix = timeutil.TimeStampNow()
	m.PublicLastError = ""
	if syncErr != nil {
		m.PublicLastError = util.SanitizeCredentialURLs(syncErr.Error())
	}
	if err := repo_model.UpdatePushMirrorPublicStatus(ctx, m); err != nil {
		return err
	}
	return syncErr
}

type syncer struct {
	m        *repo_model.PushMirror
	repo     *repo_model.Repository
	upstream *publicmirror.Upstream
	client   *Client
	source   string

	remoteURL      *giturl.GitURL
	privateKeyPath string

	syncIssues bool
	syncPulls  bool
	users      map[int64]int64
	touched    map[int64]bool
}

func runSync(ctx context.Context, m *repo_model.PushMirror) error {
	upstream := m.PublicUpstream()
	if upstream == nil {
		return fmt.Errorf("%w: %s", publicmirror.ErrUnsupportedRemote, m.RemoteAddress)
	}
	repo := m.Repo
	if err := repo.LoadOwner(ctx); err != nil {
		return err
	}

	remoteURL, err := git.GetRemoteURL(ctx, repo.RepoPath(), m.RemoteName)
	if err != nil {
		return fmt.Errorf("unable to read the address of remote %s: %w", m.RemoteName, err)
	}
	// Same checks as for pushing: the allow/block lists may have changed since the mirror was created.
	if err := migrations_allowlist.IsPushMirrorURLAllowed(remoteURL.String(), repo.Owner); err != nil {
		return util.SanitizeErrorCredentialURLs(err)
	}
	token := ""
	if remoteURL.User != nil {
		token, _ = remoteURL.User.Password()
	}

	s := &syncer{
		m:          m,
		repo:       repo,
		upstream:   upstream,
		client:     NewClient(upstream, token),
		source:     upstream.Source(),
		remoteURL:  remoteURL,
		syncIssues: repo.UnitEnabled(ctx, unit.TypeIssues),
		syncPulls:  repo.UnitEnabled(ctx, unit.TypePullRequests),
		users:      map[int64]int64{},
		touched:    map[int64]bool{},
	}

	info, err := s.client.GetRepo(ctx)
	if err != nil {
		return err
	}
	m.PublicStars, m.PublicForks, m.PublicWatchers = info.Stars, info.Forks, info.Watchers
	m.PublicHTMLURL = info.HTMLURL
	// save the counters now, they are useful even if the rest fails (e.g. rate limit)
	if err := repo_model.UpdatePushMirrorPublicStatus(ctx, m); err != nil {
		return err
	}

	if !s.syncIssues && !s.syncPulls {
		return nil
	}

	if len(m.PublicKey) != 0 {
		cleanup, err := s.writePrivateKey()
		if err != nil {
			return err
		}
		defer cleanup()
	}

	startedAt := time.Now()
	var since time.Time
	if m.PublicSyncedUnix > 0 {
		since = m.PublicSyncedUnix.AsTime().Add(-syncOverlap)
	}

	defer s.finish(ctx)

	if err := s.client.ListIssues(ctx, since, func(items []*Issue) error {
		return s.syncIssuePage(ctx, items)
	}); err != nil {
		return err
	}
	if err := s.client.ListComments(ctx, since, func(items []*Comment) error {
		for _, c := range items {
			if err := s.syncComment(ctx, c); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	m.PublicSyncedUnix = timeutil.TimeStamp(startedAt.Unix())
	return nil
}

// finish refreshes the derived data of what was changed: issue counters of the repository and the issue indexer.
func (s *syncer) finish(ctx context.Context) {
	if len(s.touched) == 0 {
		return
	}
	for _, isPull := range []bool{false, true} {
		for _, isClosed := range []bool{false, true} {
			if err := repo_model.UpdateRepoIssueNumbers(ctx, s.repo.ID, isPull, isClosed); err != nil {
				log.Error("UpdateRepoIssueNumbers: %v", err)
			}
		}
	}
	for issueID := range s.touched {
		issue_indexer.UpdateIssueIndexer(ctx, issueID)
	}
}

func (s *syncer) writePrivateKey() (func(), error) {
	privateKey, err := s.m.Privatekey()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(os.TempDir(), s.m.RemoteName)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		f.Close()
		if err := os.Remove(f.Name()); err != nil {
			log.Error("os.Remove: %v", err)
		}
	}
	if _, err := f.Write(privateKey); err != nil {
		cleanup()
		return nil, err
	}
	s.privateKeyPath = f.Name()
	return cleanup, nil
}

// posterID returns the local user linked to the upstream account (through an external login source named after the
// provider), or 0.
func (s *syncer) posterID(ctx context.Context, u *User) int64 {
	if u == nil || u.ID == 0 {
		return 0
	}
	if id, ok := s.users[u.ID]; ok {
		return id
	}
	id, err := user_model.GetUserIDByExternalUserID(ctx, s.upstream.Provider, fmt.Sprint(u.ID))
	if err != nil {
		log.Error("GetUserIDByExternalUserID: %v", err)
		id = 0
	}
	s.users[u.ID] = id
	return id
}

type remappable interface {
	RemapExternalUser(externalName string, externalID, userID int64) error
}

func (s *syncer) setPoster(ctx context.Context, u *User, target remappable) error {
	if id := s.posterID(ctx, u); id > 0 {
		return target.RemapExternalUser("", 0, id)
	}
	if u == nil {
		return target.RemapExternalUser("ghost", 0, user_model.GhostUserID)
	}
	return target.RemapExternalUser(u.Login, u.ID, user_model.GhostUserID)
}

func (s *syncer) syncIssuePage(ctx context.Context, items []*Issue) error {
	var newHeads []*pullHead
	for _, it := range items {
		if it.Number <= 0 || (it.IsPull() && !s.syncPulls) || (!it.IsPull() && !s.syncIssues) {
			continue
		}
		head, err := s.syncIssue(ctx, it)
		if err != nil {
			return fmt.Errorf("sync of %s #%d: %w", s.upstream.FullName(), it.Number, err)
		}
		if head != nil {
			newHeads = append(newHeads, head)
		}
	}
	if len(newHeads) > 0 {
		s.updatePullHeads(ctx, newHeads)
	}
	return nil
}

// pullHead is a synced pull request whose head commit must be fetched into refs/pull/<index>/head.
type pullHead struct {
	pr      *issues_model.PullRequest
	remote  *PullRequest
	mapping *repo_model.PublicMirrorIssue
	recheck bool // run the conflict/mergeability check
}

func (s *syncer) syncIssue(ctx context.Context, it *Issue) (*pullHead, error) {
	mapping, err := repo_model.GetPublicMirrorIssue(ctx, s.repo.ID, s.source, it.Number)
	if err != nil {
		return nil, err
	}
	var issue *issues_model.Issue
	if mapping != nil {
		issue, err = issues_model.GetIssueByID(ctx, mapping.IssueID)
		if issues_model.IsErrIssueNotExist(err) || (err == nil && (issue.RepoID != s.repo.ID || issue.IsPull != it.IsPull())) {
			// the local issue was deleted (or is unusable): sync it again from scratch
			if err := repo_model.DeletePublicMirrorIssueByID(ctx, mapping.ID); err != nil {
				return nil, err
			}
			mapping, issue = nil, nil
		} else if err != nil {
			return nil, err
		}
	}
	if mapping != nil && int64(mapping.RemoteUpdatedUnix) == it.UpdatedAt.Unix() {
		return nil, nil // unchanged
	}

	var remotePR *PullRequest
	if it.IsPull() {
		if remotePR, err = s.client.GetPullRequest(ctx, it.Number); err != nil {
			return nil, err
		}
	}

	if mapping == nil {
		return s.createIssue(ctx, it, remotePR)
	}
	return s.updateIssue(ctx, it, remotePR, mapping, issue)
}

func titleOf(it *Issue, remotePR *PullRequest) string {
	title := it.Title
	if remotePR != nil && remotePR.Draft && !issues_model.HasWorkInProgressPrefix(title) &&
		len(setting.Repository.PullRequest.WorkInProgressPrefixes) > 0 {
		title = setting.Repository.PullRequest.WorkInProgressPrefixes[0] + " " + title
	}
	return util.TruncateRunes(title, 255)
}

func headBranchOf(remotePR *PullRequest) string {
	head := remotePR.Head.Label
	if head == "" || !strings.Contains(head, ":") {
		head = remotePR.Head.Ref
	}
	if head == "" {
		head = fmt.Sprintf("pull-%d", remotePR.Number)
	}
	return util.TruncateRunes(head, 255)
}

func (s *syncer) createIssue(ctx context.Context, it *Issue, remotePR *PullRequest) (*pullHead, error) {
	issue := &issues_model.Issue{
		RepoID:      s.repo.ID,
		Repo:        s.repo,
		Title:       titleOf(it, remotePR),
		Content:     it.Body,
		IsPull:      it.IsPull(),
		IsClosed:    it.IsClosed(),
		IsLocked:    it.IsLockedIssue(),
		CreatedUnix: timeutil.TimeStamp(it.CreatedAt.Unix()),
		UpdatedUnix: timeutil.TimeStamp(it.UpdatedAt.Unix()),
	}
	if it.ClosedAt != nil && issue.IsClosed {
		issue.ClosedUnix = timeutil.TimeStamp(it.ClosedAt.Unix())
	}
	if err := s.setPoster(ctx, it.User, issue); err != nil {
		return nil, err
	}

	var pr *issues_model.PullRequest
	if remotePR != nil {
		pr = &issues_model.PullRequest{
			HeadRepoID: s.repo.ID,
			BaseRepoID: s.repo.ID,
			HeadBranch: headBranchOf(remotePR),
			BaseBranch: remotePR.Base.Ref,
			MergeBase:  remotePR.Base.SHA,
			HasMerged:  remotePR.Merged,
			Flow:       issues_model.PullRequestFlowAGit, // the head lives in refs/pull/<index>/head, not in a branch
			Status:     issues_model.PullRequestStatusMergeable,
			Issue:      issue,
		}
		if !issue.IsClosed {
			pr.Status = issues_model.PullRequestStatusChecking
		}
		setMerged(pr, remotePR)
	}

	mapping := &repo_model.PublicMirrorIssue{
		RepoID:            s.repo.ID,
		Source:            s.source,
		RemoteNumber:      it.Number,
		IsPull:            it.IsPull(),
		RemoteURL:         it.HTMLURL,
		RemoteUpdatedUnix: timeutil.TimeStamp(it.UpdatedAt.Unix()),
	}
	if mapping.RemoteURL == "" {
		mapping.RemoteURL = s.upstream.IssueURL(it.Number, it.IsPull())
	}

	err := db.WithTx(ctx, func(ctx context.Context) error {
		// keep the upstream number when it is free so that references like #12 keep working
		index := it.Number
		if _, err := issues_model.GetIssueByIndex(ctx, s.repo.ID, index); err == nil {
			if index, err = db.GetNextResourceIndex(ctx, "issue_index", s.repo.ID); err != nil {
				return err
			}
		} else if !issues_model.IsErrIssueNotExist(err) {
			return err
		}
		issue.Index = index
		if pr != nil {
			pr.Index = index
			if err := issues_model.InsertPullRequests(ctx, pr); err != nil {
				return err
			}
		} else if err := issues_model.InsertIssues(ctx, issue); err != nil {
			return err
		}
		// local issues must never get a number used by a synced one
		if err := db.SyncMaxResourceIndex(ctx, "issue_index", s.repo.ID, index); err != nil {
			return err
		}
		mapping.IssueID = issue.ID
		return repo_model.SavePublicMirrorIssue(ctx, mapping)
	})
	if err != nil {
		return nil, err
	}
	s.touched[issue.ID] = true

	if pr == nil {
		return nil, nil
	}
	return &pullHead{pr: pr, remote: remotePR, mapping: mapping, recheck: !issue.IsClosed}, nil
}

func setMerged(pr *issues_model.PullRequest, remotePR *PullRequest) {
	if !remotePR.Merged {
		return
	}
	pr.HasMerged = true
	pr.Status = issues_model.PullRequestStatusMergeable
	if remotePR.MergedAt != nil {
		pr.MergedUnix = timeutil.TimeStamp(remotePR.MergedAt.Unix())
	}
	if remotePR.MergeCommitSHA != nil {
		pr.MergedCommitID = *remotePR.MergeCommitSHA
	}
}

func (s *syncer) updateIssue(ctx context.Context, it *Issue, remotePR *PullRequest, mapping *repo_model.PublicMirrorIssue, issue *issues_model.Issue) (*pullHead, error) {
	var pr *issues_model.PullRequest
	if issue.IsPull {
		var err error
		if pr, err = issues_model.GetPullRequestByIssueIDWithNoAttributes(ctx, issue.ID); err != nil {
			return nil, err
		}
		pr.Issue = issue
	}

	issue.Title = titleOf(it, remotePR)
	issue.Content = it.Body
	issue.IsLocked = it.IsLockedIssue()
	issue.UpdatedUnix = timeutil.TimeStamp(it.UpdatedAt.Unix())
	// A pull request merged in Forgejo is pushed to the upstream, which may not notice that it was merged (e.g. with
	// a squash merge): never reopen it.
	if pr == nil || !pr.HasMerged {
		issue.IsClosed = it.IsClosed()
		issue.ClosedUnix = 0
		if issue.IsClosed {
			issue.ClosedUnix = issue.UpdatedUnix
			if it.ClosedAt != nil {
				issue.ClosedUnix = timeutil.TimeStamp(it.ClosedAt.Unix())
			}
		}
	}

	headChanged := false
	if pr != nil {
		headChanged = remotePR.Head.SHA != "" && remotePR.Head.SHA != mapping.RemoteHeadSHA
		pr.HeadBranch = headBranchOf(remotePR)
		if remotePR.Base.Ref != "" {
			pr.BaseBranch = remotePR.Base.Ref
		}
		if !pr.HasMerged {
			setMerged(pr, remotePR)
		}
	}

	mapping.RemoteUpdatedUnix = timeutil.TimeStamp(it.UpdatedAt.Unix())
	if it.HTMLURL != "" {
		mapping.RemoteURL = it.HTMLURL
	}

	err := db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).ID(issue.ID).NoAutoTime().
			Cols("name", "content", "is_closed", "closed_unix", "is_locked", "updated_unix").Update(issue); err != nil {
			return err
		}
		if pr != nil {
			if _, err := db.GetEngine(ctx).ID(pr.ID).NoAutoTime().
				Cols("head_branch", "base_branch", "has_merged", "merged_unix", "merged_commit_id", "status").Update(pr); err != nil {
				return err
			}
		}
		return repo_model.SavePublicMirrorIssue(ctx, mapping)
	})
	if err != nil {
		return nil, err
	}
	s.touched[issue.ID] = true

	if pr == nil {
		return nil, nil
	}
	isOpen := !issue.IsClosed && !pr.HasMerged
	if !headChanged && !isOpen {
		return nil, nil
	}
	return &pullHead{pr: pr, remote: remotePR, mapping: mapping, recheck: isOpen}, nil
}

// updatePullHeads fetches the head commits of the pull requests from the upstream into refs/pull/<index>/head.
// Those refs are never pushed back: the push mirror only pushes branches and tags. Failures are logged, the pull
// request is then shown as broken until the next successful sync.
func (s *syncer) updatePullHeads(ctx context.Context, heads []*pullHead) {
	refspecs := make([]string, 0, len(heads))
	for _, h := range heads {
		if h.remote.Number > 0 && h.remote.Head.SHA != "" && h.remote.Head.SHA != h.mapping.RemoteHeadSHA {
			refspecs = append(refspecs, fmt.Sprintf("+refs/pull/%d/head:%s", h.remote.Number, h.pr.GetGitRefName()))
		}
	}
	if len(refspecs) > 0 {
		if err := s.fetch(ctx, refspecs...); err != nil {
			// one missing ref fails the whole fetch, retry one by one
			for _, refspec := range refspecs {
				if err := s.fetch(ctx, refspec); err != nil {
					log.Warn("public mirror %s: fetch %s failed: %v", s.upstream.Source(), refspec, err)
				}
			}
		}
	}

	repoPath := s.repo.RepoPath()
	for _, h := range heads {
		sha := h.remote.Head.SHA
		if sha == "" {
			continue
		}
		current, _, err := git.NewCommand(ctx, "rev-parse", "--verify", "--quiet").AddDynamicArguments(h.pr.GetGitRefName()).RunStdString(&git.RunOpts{Dir: repoPath})
		current = strings.TrimSpace(current)
		if err != nil || current != sha {
			// e.g. the upstream deleted the ref but the commit is known locally (merged pull requests)
			if _, _, err := git.NewCommand(ctx, "cat-file", "-e").AddDynamicArguments(sha + "^{commit}").RunStdString(&git.RunOpts{Dir: repoPath}); err != nil {
				if current == "" {
					log.Warn("public mirror %s: head %s of #%d is not available", s.upstream.Source(), sha, h.remote.Number)
				}
				continue
			}
			if _, _, err := git.NewCommand(ctx, "update-ref", "--no-deref").AddDynamicArguments(h.pr.GetGitRefName(), sha).RunStdString(&git.RunOpts{Dir: repoPath}); err != nil {
				log.Error("public mirror %s: update-ref %s: %v", s.upstream.Source(), h.pr.GetGitRefName(), err)
				continue
			}
		}

		if mergeBase, _, err := git.NewCommand(ctx, "merge-base").AddDashesAndList(git.BranchPrefix+h.pr.BaseBranch, sha).RunStdString(&git.RunOpts{Dir: repoPath}); err == nil {
			if mb := strings.TrimSpace(mergeBase); mb != "" && mb != h.pr.MergeBase {
				h.pr.MergeBase = mb
				if err := h.pr.UpdateCols(ctx, "merge_base"); err != nil {
					log.Error("UpdateCols(merge_base): %v", err)
				}
			}
		}

		h.mapping.RemoteHeadSHA = sha
		if err := repo_model.SavePublicMirrorIssue(ctx, h.mapping); err != nil {
			log.Error("SavePublicMirrorIssue: %v", err)
		}
		if h.recheck {
			addToTaskQueue(ctx, h.pr)
		}
	}
}

func (s *syncer) fetch(ctx context.Context, refspecs ...string) error {
	opts := &git.RunOpts{Dir: s.repo.RepoPath(), Timeout: time.Duration(setting.Git.Timeout.Mirror) * time.Second}
	if s.privateKeyPath != "" {
		opts.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -i "+s.privateKeyPath+
			" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+
			filepath.Join(setting.SSH.RootPath, "known_hosts"))
	}
	args := append([]string{s.m.RemoteName}, refspecs...)
	_, _, err := git.NewCommand(ctx, "fetch", "--no-tags", "--no-write-fetch-head").AddDashesAndList(args...).RunStdString(opts)
	if err != nil {
		return util.SanitizeErrorCredentialURLs(err)
	}
	return nil
}

func (s *syncer) syncComment(ctx context.Context, c *Comment) error {
	number := c.IssueNumber()
	if c.ID == 0 || number == 0 {
		return nil
	}
	issueMapping, err := repo_model.GetPublicMirrorIssue(ctx, s.repo.ID, s.source, number)
	if err != nil || issueMapping == nil {
		return err // not synced (e.g. its unit is disabled)
	}

	mapping, err := repo_model.GetPublicMirrorComment(ctx, s.repo.ID, s.source, c.ID)
	if err != nil {
		return err
	}
	if mapping != nil {
		if int64(mapping.RemoteUpdatedUnix) == c.UpdatedAt.Unix() {
			return nil
		}
		comment, err := issues_model.GetCommentByID(ctx, mapping.CommentID)
		if issues_model.IsErrCommentNotExist(err) {
			// deleted locally, keep it deleted
			mapping.RemoteUpdatedUnix = timeutil.TimeStamp(c.UpdatedAt.Unix())
			return repo_model.SavePublicMirrorComment(ctx, mapping)
		} else if err != nil {
			return err
		}
		comment.Content = c.Body
		comment.UpdatedUnix = timeutil.TimeStamp(c.UpdatedAt.Unix())
		mapping.RemoteUpdatedUnix = comment.UpdatedUnix
		return db.WithTx(ctx, func(ctx context.Context) error {
			if _, err := db.GetEngine(ctx).ID(comment.ID).NoAutoTime().Cols("content", "updated_unix").Update(comment); err != nil {
				return err
			}
			s.touched[comment.IssueID] = true
			return repo_model.SavePublicMirrorComment(ctx, mapping)
		})
	}

	comment := &issues_model.Comment{
		Type:        issues_model.CommentTypeComment,
		IssueID:     issueMapping.IssueID,
		Content:     c.Body,
		CreatedUnix: timeutil.TimeStamp(c.CreatedAt.Unix()),
		UpdatedUnix: timeutil.TimeStamp(c.UpdatedAt.Unix()),
	}
	if err := s.setPoster(ctx, c.User, comment); err != nil {
		return err
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := issues_model.InsertIssueComments(ctx, []*issues_model.Comment{comment}); err != nil {
			return err
		}
		s.touched[comment.IssueID] = true
		return repo_model.SavePublicMirrorComment(ctx, &repo_model.PublicMirrorComment{
			RepoID:            s.repo.ID,
			Source:            s.source,
			RemoteID:          c.ID,
			CommentID:         comment.ID,
			RemoteUpdatedUnix: comment.UpdatedUnix,
		})
	})
}
