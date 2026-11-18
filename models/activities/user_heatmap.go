// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package activities

import (
	"context"
	"sort"
	"strings"
	"time"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/container"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"

	"xorm.io/builder"
)

const (
	// contributionsMaxAgeSeconds How old data to retrieve for the heatmap.
	// 371 days to cover the entire heatmap (53 *full* weeks)
	contributionsMaxAgeSeconds = 32054400

	heatmapDateLayout = "2006-01-02"
	secondsPerDay     = 86400
)

// UserHeatmapData represents the data needed to create a heatmap.
//
// Every entry is one calendar day. Like on GitHub, commits are counted on the day of their author date in the time
// zone recorded in the commit, other contributions on their day in the server's UI time zone.
type UserHeatmapData struct {
	// Timestamp is 12:00 UTC of Date, so clients converting it to a local time zone still land on the same day
	Timestamp timeutil.TimeStamp `json:"timestamp"`
	// Date is the calendar day of the contributions, formatted as YYYY-MM-DD
	Date          string `json:"date"`
	Contributions int64  `json:"contributions"`
}

// GetUserHeatmapDataByUser returns an array of UserHeatmapData
func GetUserHeatmapDataByUser(ctx context.Context, user, doer *user_model.User) ([]*UserHeatmapData, error) {
	return getUserHeatmapData(ctx, user, nil, doer)
}

// GetUserHeatmapDataByUserTeam returns an array of UserHeatmapData
func GetUserHeatmapDataByUserTeam(ctx context.Context, user *user_model.User, team *organization.Team, doer *user_model.User) ([]*UserHeatmapData, error) {
	return getUserHeatmapData(ctx, user, team, doer)
}

func getUserHeatmapData(ctx context.Context, user *user_model.User, team *organization.Team, doer *user_model.User) ([]*UserHeatmapData, error) {
	if !ActivityReadable(user, doer) {
		return make([]*UserHeatmapData, 0), nil
	}

	since := timeutil.TimeStampNow() - contributionsMaxAgeSeconds
	perDay := make(map[string]int64)

	if err := countHeatmapActions(ctx, user, team, doer, since, perDay); err != nil {
		return nil, err
	}
	if err := countHeatmapCommits(ctx, user, team, doer, since, perDay); err != nil {
		return nil, err
	}

	hdata := make([]*UserHeatmapData, 0, len(perDay))
	for date, contributions := range perDay {
		day, err := time.Parse(heatmapDateLayout, date)
		if err != nil {
			return nil, err
		}
		hdata = append(hdata, &UserHeatmapData{
			Timestamp:     timeutil.TimeStamp(day.Add(12 * time.Hour).Unix()),
			Date:          date,
			Contributions: contributions,
		})
	}
	sort.Slice(hdata, func(i, j int) bool { return hdata[i].Date < hdata[j].Date })
	return hdata, nil
}

// heatmapActionCond selects the actions which GitHub counts as contributions besides commits: creating or forking a
// repository, opening an issue or a pull request, and submitting a pull request review. Except for repository
// creation, they only count in repositories which are not forks.
func heatmapActionCond() builder.Cond {
	review := builder.In("op_type", ActionApprovePullRequest, ActionRejectPullRequest).
		Or(builder.Eq{"op_type": ActionCommentPull}.And(builder.In("comment_id",
			builder.Select("id").From("comment").Where(builder.Eq{"type": issues_model.CommentTypeReview}))))

	notInFork := builder.In("op_type", ActionCreateIssue, ActionCreatePullRequest).Or(review).
		And(builder.NotIn("repo_id", builder.Select("id").From("repository").Where(builder.Eq{"is_fork": true})))

	return builder.Eq{"op_type": ActionCreateRepo}.Or(notInFork)
}

func countHeatmapActions(ctx context.Context, user *user_model.User, team *organization.Team, doer *user_model.User, since timeutil.TimeStamp, perDay map[string]int64) error {
	// Group by 15 minute intervals, which are then converted to days in the UI time zone.
	// The interval is based on the fact that there are timezones such as UTC +5:30 and UTC +12:45.
	groupBy := "created_unix / 900 * 900"
	if setting.Database.Type.IsMySQL() {
		groupBy = "created_unix DIV 900 * 900"
	}

	cond, err := activityQueryCondition(ctx, GetFeedsOptions{
		RequestedUser:  user,
		RequestedTeam:  team,
		Actor:          doer,
		IncludePrivate: true, // don't filter by private, as we already filter by repo access
		// * Heatmaps for individual users only include actions that the user themself did.
		// * For organizations actions by all users that were made in owned
		//   repositories are counted.
		OnlyPerformedBy: !user.IsOrganization(),
	})
	if err != nil {
		return err
	}

	type bucket struct {
		Timestamp     int64
		Contributions int64
	}
	buckets := make([]*bucket, 0)
	if err := db.GetEngine(ctx).
		Select(groupBy+" AS timestamp, count(user_id) as contributions").
		Table("action").
		Where(cond).
		And(heatmapActionCond()).
		And("created_unix >= ?", since).
		GroupBy("timestamp").
		Find(&buckets); err != nil {
		return err
	}

	for _, b := range buckets {
		perDay[time.Unix(b.Timestamp, 0).In(setting.DefaultUILocation).Format(heatmapDateLayout)] += b.Contributions
	}
	return nil
}

func countHeatmapCommits(ctx context.Context, user *user_model.User, team *organization.Team, doer *user_model.User, since timeutil.TimeStamp, perDay map[string]int64) error {
	cond := builder.NewCond()

	if user.IsOrganization() {
		// organizations count every commit made in the repositories they own
		cond = cond.And(builder.In("repo_id", builder.Select("id").From("repository").Where(builder.Eq{"owner_id": user.ID})))
	} else {
		// users get the commits authored or co-authored with one of their verified email addresses
		emailCond := builder.In("email", builder.Select("lower_email").From("email_address").
			Where(builder.Eq{"uid": user.ID, "is_activated": true}))
		if placeholder := user.GetPlaceholderEmail(); placeholder != "" {
			emailCond = emailCond.Or(builder.Eq{"email": strings.ToLower(placeholder)})
		}
		cond = cond.And(emailCond)
	}

	// commits in forks do not count
	cond = cond.And(builder.In("repo_id", builder.Select("id").From("repository").Where(builder.Eq{"is_fork": false})))

	if doer == nil || !doer.IsAdmin {
		cond = cond.And(builder.In("repo_id", repo_model.AccessibleRepoIDsQuery(doer)))
	}

	if team != nil {
		teamRepoIDs, err := accessibleTeamRepoIDs(ctx, user, team)
		if err != nil {
			return err
		}
		cond = cond.And(builder.In("repo_id", teamRepoIDs))
	}

	// keep one extra day, as the day of a commit depends on its time zone
	cond = cond.And(builder.Gte{"author_unix": since - secondsPerDay})

	// A commit is counted once, even if it was authored and co-authored with several emails of the same user.
	seen := make(container.Set[string])
	return db.GetEngine(ctx).Table("commit_contribution").Where(cond).
		Cols("repo_id", "commit_sha", "author_unix", "author_tz_offset").
		Iterate(new(CommitContribution), func(_ int, bean any) error {
			c := bean.(*CommitContribution)
			if !seen.Add(c.CommitSHA) {
				return nil
			}
			perDay[commitDate(c.AuthorUnix, c.AuthorTZOffset)]++
			return nil
		})
}

// commitDate returns the calendar day of a commit in the time zone recorded in the commit.
func commitDate(unix timeutil.TimeStamp, tzOffset int) string {
	return time.Unix(int64(unix), 0).In(time.FixedZone("", tzOffset)).Format(heatmapDateLayout)
}

// GetTotalContributionsInHeatmap returns the total number of contributions in a heatmap
func GetTotalContributionsInHeatmap(hdata []*UserHeatmapData) int64 {
	var total int64
	for _, v := range hdata {
		total += v.Contributions
	}
	return total
}
