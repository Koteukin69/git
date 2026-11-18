// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package publicmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"forgejo.org/modules/json"
	"forgejo.org/modules/publicmirror"
	migrations_allowlist "forgejo.org/services/migrations/allowlist"
)

// ErrRateLimited is returned when the upstream API refuses more requests for now.
var ErrRateLimited = errors.New("the API rate limit of the public mirror is exceeded")

const (
	maxPages        = 1000
	maxResponseSize = 32 << 20
	requestTimeout  = 60 * time.Second
)

// User is an account on the upstream.
type User struct {
	ID      int64  `json:"id"`
	Login   string `json:"login"`
	HTMLURL string `json:"html_url"`
}

// RepoInfo holds the public counters of the upstream repository.
type RepoInfo struct {
	HTMLURL  string
	Stars    int
	Forks    int
	Watchers int
}

// PullRequestRef is set on issues of the issue list that are pull requests.
type PullRequestRef struct {
	Merged   bool       `json:"merged"`
	MergedAt *time.Time `json:"merged_at"`
	Draft    bool       `json:"draft"`
}

// Issue is an issue or a pull request of the upstream, as returned by the issue list.
type Issue struct {
	Number      int64           `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	State       string          `json:"state"`
	User        *User           `json:"user"`
	HTMLURL     string          `json:"html_url"`
	Locked      bool            `json:"locked"`    // GitHub
	IsLocked    bool            `json:"is_locked"` // Forgejo/Gitea
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ClosedAt    *time.Time      `json:"closed_at"`
	Comments    int             `json:"comments"`
	PullRequest *PullRequestRef `json:"pull_request"`
}

// IsPull reports whether the issue is a pull request.
func (i *Issue) IsPull() bool { return i.PullRequest != nil }

// IsClosed reports whether the issue is closed (or merged).
func (i *Issue) IsClosed() bool { return strings.EqualFold(i.State, "closed") }

// IsLockedIssue reports whether the conversation is locked.
func (i *Issue) IsLockedIssue() bool { return i.Locked || i.IsLocked }

// PullBranch is the head or base of a pull request.
type PullBranch struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
}

// PullRequest holds the details of a pull request that the issue list does not have.
type PullRequest struct {
	Number         int64      `json:"number"`
	Draft          bool       `json:"draft"`
	Merged         bool       `json:"merged"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA *string    `json:"merge_commit_sha"`
	Head           PullBranch `json:"head"`
	Base           PullBranch `json:"base"`
}

// Comment is a comment on an issue or a pull request of the upstream.
type Comment struct {
	ID             int64     `json:"id"`
	Body           string    `json:"body"`
	User           *User     `json:"user"`
	HTMLURL        string    `json:"html_url"`
	IssueURL       string    `json:"issue_url"`
	PullRequestURL string    `json:"pull_request_url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// IssueNumber extracts the number of the issue or pull request the comment belongs to.
func (c *Comment) IssueNumber() int64 {
	for _, u := range []string{c.IssueURL, c.PullRequestURL, c.HTMLURL} {
		if u == "" {
			continue
		}
		p := u
		if i := strings.IndexAny(p, "#?"); i >= 0 {
			p = p[:i]
		}
		p = strings.TrimRight(p, "/")
		if n, err := strconv.ParseInt(p[strings.LastIndex(p, "/")+1:], 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// Client talks to the REST API of the upstream. GitHub and Forgejo/Gitea expose the same endpoints for everything
// that is used here, only a few field names differ.
type Client struct {
	upstream   *publicmirror.Upstream
	token      string
	httpClient *http.Client
}

// NewClient creates a client. The token is optional, public repositories can be read anonymously (with a lower
// rate limit). Requests are subject to the migration allow/block lists to prevent SSRF.
func NewClient(upstream *publicmirror.Upstream, token string) *Client {
	return &Client{
		upstream: upstream,
		token:    token,
		httpClient: &http.Client{
			Transport: migrations_allowlist.NewMigrationHTTPTransport(),
			Timeout:   requestTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
	}
}

func (c *Client) get(ctx context.Context, rawURL string, v any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.upstream.Provider == publicmirror.ProviderGitHub {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0") {
		return nil, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s: unexpected status %s: %s", sanitizeURL(rawURL), resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(v); err != nil {
		return nil, fmt.Errorf("GET %s: invalid response: %w", sanitizeURL(rawURL), err)
	}
	return resp.Header, nil
}

func sanitizeURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		u.User = nil
		u.RawQuery = ""
		return u.String()
	}
	return ""
}

// hasNextPage uses the Link header (GitHub and Forgejo/Gitea both send it); without it, keep going until a page is
// empty.
func hasNextPage(header http.Header, items int) bool {
	links := header.Values("Link")
	if len(links) == 0 {
		return items > 0
	}
	for _, l := range links {
		if strings.Contains(l, `rel="next"`) {
			return true
		}
	}
	return false
}

// GetRepo returns the counters of the upstream repository.
func (c *Client) GetRepo(ctx context.Context) (*RepoInfo, error) {
	var raw struct {
		HTMLURL          string `json:"html_url"`
		StargazersCount  *int   `json:"stargazers_count"`  // GitHub
		SubscribersCount *int   `json:"subscribers_count"` // GitHub
		StarsCount       *int   `json:"stars_count"`       // Forgejo/Gitea
		WatchersCount    *int   `json:"watchers_count"`    // Forgejo/Gitea (on GitHub it is a legacy alias of stars)
		ForksCount       int    `json:"forks_count"`
	}
	if _, err := c.get(ctx, c.upstream.APIRepoURL(), &raw); err != nil {
		return nil, err
	}
	info := &RepoInfo{HTMLURL: raw.HTMLURL, Forks: raw.ForksCount}
	deref := func(i *int) int {
		if i == nil {
			return 0
		}
		return *i
	}
	if c.upstream.Provider == publicmirror.ProviderGitHub {
		info.Stars = deref(raw.StargazersCount)
		info.Watchers = deref(raw.SubscribersCount)
	} else {
		info.Stars = deref(raw.StarsCount)
		info.Watchers = deref(raw.WatchersCount)
	}
	return info, nil
}

func (c *Client) listURL(path string, since time.Time, page int) string {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", "100") // GitHub
	q.Set("limit", "50")     // Forgejo/Gitea (default maximum)
	q.Set("sort", "updated")
	q.Set("direction", "asc")
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	return c.upstream.APIRepoURL() + path + "?" + q.Encode()
}

// ListIssues calls fn with every page of issues and pull requests updated since the given time (all of them when
// since is zero). On GitHub, pages are sorted by update time ascending.
func (c *Client) ListIssues(ctx context.Context, since time.Time, fn func([]*Issue) error) error {
	q := "&state=all"
	for page := 1; page <= maxPages; page++ {
		var items []*Issue
		header, err := c.get(ctx, c.listURL("/issues", since, page)+q, &items)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			if err := fn(items); err != nil {
				return err
			}
		}
		if !hasNextPage(header, len(items)) {
			return nil
		}
	}
	return nil
}

// ListComments calls fn with every page of issue and pull request comments updated since the given time.
func (c *Client) ListComments(ctx context.Context, since time.Time, fn func([]*Comment) error) error {
	for page := 1; page <= maxPages; page++ {
		var items []*Comment
		header, err := c.get(ctx, c.listURL("/issues/comments", since, page), &items)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			if err := fn(items); err != nil {
				return err
			}
		}
		if !hasNextPage(header, len(items)) {
			return nil
		}
	}
	return nil
}

// ListIssueComments calls fn with every page of comments of an issue or pull request.
func (c *Client) ListIssueComments(ctx context.Context, number int64, fn func([]*Comment) error) error {
	for page := 1; page <= maxPages; page++ {
		var items []*Comment
		header, err := c.get(ctx, c.listURL(fmt.Sprintf("/issues/%d/comments", number), time.Time{}, page), &items)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.IssueNumber() == 0 {
				// Forgejo/Gitea do not always fill issue_url for comments of pull requests
				item.IssueURL = fmt.Sprintf("%s/issues/%d", c.upstream.APIRepoURL(), number)
			}
		}
		if len(items) > 0 {
			if err := fn(items); err != nil {
				return err
			}
		}
		if !hasNextPage(header, len(items)) {
			return nil
		}
	}
	return nil
}

// GetPullRequest returns the details of a pull request.
func (c *Client) GetPullRequest(ctx context.Context, number int64) (*PullRequest, error) {
	pr := new(PullRequest)
	if _, err := c.get(ctx, fmt.Sprintf("%s/pulls/%d", c.upstream.APIRepoURL(), number), pr); err != nil {
		return nil, err
	}
	return pr, nil
}
