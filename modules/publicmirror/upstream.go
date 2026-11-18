// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package publicmirror describes the upstream of a push mirror used as "public mirror": the public face of a
// repository on GitHub (or a Forgejo/Gitea instance) where stars, forks, issues and pull requests live.
package publicmirror

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	giturl "forgejo.org/modules/git/url"
)

const (
	ProviderGitHub  = "github"
	ProviderForgejo = "forgejo"
)

// ErrUnsupportedRemote is returned when the remote address cannot be used as public mirror.
var ErrUnsupportedRemote = errors.New("the remote address does not point to a repository of a supported public mirror provider")

// Upstream is the repository on the public mirror.
type Upstream struct {
	Provider string
	Scheme   string // scheme of the web UI and API: https unless the remote explicitly uses http
	Host     string // host[:port] of the web UI
	BasePath string // sub path of the instance, without trailing slash (Forgejo/Gitea only)
	Owner    string
	Repo     string
}

// Parse parses a push mirror remote address (https, ssh or scp-like) into an Upstream for the given provider.
func Parse(provider, remoteAddress string) (*Upstream, error) {
	if provider != ProviderGitHub && provider != ProviderForgejo {
		return nil, fmt.Errorf("unknown public mirror provider %q", provider)
	}
	u, err := giturl.Parse(strings.TrimSpace(remoteAddress))
	if err != nil {
		return nil, ErrUnsupportedRemote
	}

	up := &Upstream{Provider: provider, Scheme: "https"}
	switch u.Scheme {
	case "http", "https":
		up.Scheme = u.Scheme
		up.Host = u.Host
	case "ssh", "git", "git+ssh", "ssh+git":
		// the port of an ssh remote is the ssh port, not the one of the web UI
		up.Host = u.Hostname()
	default:
		return nil, ErrUnsupportedRemote
	}
	if up.Host == "" {
		return nil, ErrUnsupportedRemote
	}
	up.Host = strings.ToLower(up.Host)

	segments := make([]string, 0, 4)
	for s := range strings.SplitSeq(u.Path, "/") {
		if s != "" {
			segments = append(segments, s)
		}
	}
	if len(segments) < 2 {
		return nil, ErrUnsupportedRemote
	}
	up.Owner = segments[len(segments)-2]
	up.Repo = strings.TrimSuffix(segments[len(segments)-1], ".git")
	if up.Owner == "" || up.Repo == "" {
		return nil, ErrUnsupportedRemote
	}
	if prefix := segments[:len(segments)-2]; len(prefix) > 0 {
		if provider == ProviderGitHub {
			// GitHub (and GitHub Enterprise) repositories are always at /owner/repo
			return nil, ErrUnsupportedRemote
		}
		if u.Scheme == "http" || u.Scheme == "https" {
			up.BasePath = "/" + strings.Join(prefix, "/")
		}
	}
	return up, nil
}

// IsGitHubDotCom reports whether the upstream is hosted on github.com (as opposed to GitHub Enterprise).
func (u *Upstream) IsGitHubDotCom() bool {
	return u.Provider == ProviderGitHub && (u.Host == "github.com" || u.Host == "www.github.com")
}

// Source identifies the upstream repository, it is used to key synced data.
func (u *Upstream) Source() string {
	return strings.ToLower(u.Host + u.BasePath + "/" + u.Owner + "/" + u.Repo)
}

// DisplayName is the human readable name of the upstream service.
func (u *Upstream) DisplayName() string {
	if u.IsGitHubDotCom() {
		return "GitHub"
	}
	return u.Host
}

// FullName is owner/repo on the upstream.
func (u *Upstream) FullName() string {
	return u.Owner + "/" + u.Repo
}

func (u *Upstream) instanceURL() string {
	host := u.Host
	if u.IsGitHubDotCom() {
		host = "github.com"
	}
	return u.Scheme + "://" + host + u.BasePath
}

// WebURL is the URL of the repository page on the upstream.
func (u *Upstream) WebURL() string {
	return u.instanceURL() + "/" + url.PathEscape(u.Owner) + "/" + url.PathEscape(u.Repo)
}

// APIURL is the base URL of the REST API of the upstream instance, without trailing slash.
func (u *Upstream) APIURL() string {
	if u.Provider == ProviderGitHub {
		if u.IsGitHubDotCom() {
			return "https://api.github.com"
		}
		return u.instanceURL() + "/api/v3"
	}
	return u.instanceURL() + "/api/v1"
}

// APIRepoURL is the API URL of the repository.
func (u *Upstream) APIRepoURL() string {
	return u.APIURL() + "/repos/" + url.PathEscape(u.Owner) + "/" + url.PathEscape(u.Repo)
}

// StarURL is where a user can star the repository (the repository page, the star button is there).
func (u *Upstream) StarURL() string { return u.WebURL() }

// StargazersURL lists the users who starred the repository.
func (u *Upstream) StargazersURL() string {
	if u.Provider == ProviderGitHub {
		return u.WebURL() + "/stargazers"
	}
	return u.WebURL() + "/stars"
}

// ForkURL is where a user can fork the repository.
func (u *Upstream) ForkURL() string { return u.WebURL() + "/fork" }

// ForksURL lists the forks of the repository.
func (u *Upstream) ForksURL() string { return u.WebURL() + "/forks" }

// WatchersURL lists the users who watch the repository.
func (u *Upstream) WatchersURL() string { return u.WebURL() + "/watchers" }

// IssuesURL lists the issues of the repository.
func (u *Upstream) IssuesURL() string { return u.WebURL() + "/issues" }

// PullsURL lists the pull requests of the repository.
func (u *Upstream) PullsURL() string { return u.WebURL() + "/pulls" }

// NewIssueURL is where a user can open a new issue.
func (u *Upstream) NewIssueURL() string { return u.WebURL() + "/issues/new" }

// NewPullURL is where a user can open a new pull request.
func (u *Upstream) NewPullURL() string { return u.WebURL() + "/compare" }

// IssueURL is the URL of an issue (or pull request) on the upstream.
func (u *Upstream) IssueURL(number int64, isPull bool) string {
	if isPull {
		if u.Provider == ProviderGitHub {
			return fmt.Sprintf("%s/pull/%d", u.WebURL(), number)
		}
		return fmt.Sprintf("%s/pulls/%d", u.WebURL(), number)
	}
	return fmt.Sprintf("%s/issues/%d", u.WebURL(), number)
}
