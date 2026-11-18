// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package contribution

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestParseRawDate(t *testing.T) {
	for raw, expected := range map[string][2]int64{
		"1700000000 +0300": {1700000000, 3 * 3600},
		"1700000000 -0500": {1700000000, -5 * 3600},
		"1700000000 +0545": {1700000000, 5*3600 + 45*60},
		"1700000000 -0930": {1700000000, -(9*3600 + 30*60)},
		"1700000000 +0000": {1700000000, 0},
		"1700000000":       {1700000000, 0},
	} {
		unix, offset, err := parseRawDate(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, expected[0], unix, raw)
		assert.EqualValues(t, expected[1], offset, raw)
	}

	_, _, err := parseRawDate("")
	require.Error(t, err)
	_, _, err = parseRawDate("abc +0100")
	require.Error(t, err)
}

func TestParseTrailerEmail(t *testing.T) {
	assert.Equal(t, "jane@example.com", parseTrailerEmail("Jane Doe <jane@example.com>"))
	assert.Equal(t, "jane@example.com", parseTrailerEmail("  Jane <jane@example.com>  "))
	assert.Equal(t, "x@y", parseTrailerEmail("weird name (with) [chars] <x@y>"))
	assert.Empty(t, parseTrailerEmail("no email here"))
	assert.Empty(t, parseTrailerEmail(""))
}

func TestParseCommitContributions(t *testing.T) {
	input := "aaa\x00Author@Example.com\x001700000000 +0300\x00Co <co@example.com>\x01Author <author@example.com>\x1e\n" +
		"bbb\x00b@example.com\x001700086400 -0500\x00\x1e\n"
	rows, err := parseCommitContributions(strings.NewReader(input))
	require.NoError(t, err)
	assert.Equal(t, []*activities_model.CommitContribution{
		{CommitSHA: "aaa", Email: "author@example.com", AuthorUnix: 1700000000, AuthorTZOffset: 3 * 3600},
		{CommitSHA: "aaa", Email: "co@example.com", AuthorUnix: 1700000000, AuthorTZOffset: 3 * 3600},
		{CommitSHA: "bbb", Email: "b@example.com", AuthorUnix: 1700086400, AuthorTZOffset: -5 * 3600},
	}, rows)
}

func runGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func commitWithDate(t *testing.T, dir, email, date, message string) string {
	t.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=Someone", "GIT_AUTHOR_EMAIL=" + email, "GIT_AUTHOR_DATE=" + date,
		"GIT_COMMITTER_NAME=Pusher", "GIT_COMMITTER_EMAIL=pusher@example.com", "GIT_COMMITTER_DATE=2024-01-01T00:00:00+00:00",
	}
	runGit(t, dir, env, "commit", "--allow-empty", "-m", message)
	return runGit(t, dir, nil, "rev-parse", "HEAD")
}

func TestReadCommitContributions(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, nil, "init", "-b", "main")

	first := commitWithDate(t, dir, "One@Example.com", "2023-03-01T23:30:00+03:00", "first")
	second := commitWithDate(t, dir, "two@example.com", "2023-03-05T10:00:00-05:00",
		"second\n\nCo-authored-by: One <one@example.com>\nCo-authored-by: Three <three@example.com>")

	rows, err := readCommitContributions(t.Context(), dir, "refs/heads/main")
	require.NoError(t, err)
	assert.ElementsMatch(t, []*activities_model.CommitContribution{
		{CommitSHA: first, Email: "one@example.com", AuthorUnix: 1677702600, AuthorTZOffset: 3 * 3600},
		{CommitSHA: second, Email: "two@example.com", AuthorUnix: 1678028400, AuthorTZOffset: -5 * 3600},
		{CommitSHA: second, Email: "one@example.com", AuthorUnix: 1678028400, AuthorTZOffset: -5 * 3600},
		{CommitSHA: second, Email: "three@example.com", AuthorUnix: 1678028400, AuthorTZOffset: -5 * 3600},
	}, rows)

	// range used by incremental syncs
	rows, err = readCommitContributions(t.Context(), dir, first+".."+second)
	require.NoError(t, err)
	assert.Len(t, rows, 3)

	isAnc, err := isAncestor(t.Context(), dir, first, second)
	require.NoError(t, err)
	assert.True(t, isAnc)
	isAnc, err = isAncestor(t.Context(), dir, second, first)
	require.NoError(t, err)
	assert.False(t, isAnc)
}
