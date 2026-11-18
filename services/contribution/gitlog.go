// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package contribution

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strconv"
	"strings"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/modules/git"
	"forgejo.org/modules/timeutil"
)

const (
	fieldSeparator   = "\x00"
	recordSeparator  = '\x1e'
	trailerSeparator = "\x01"
)

// readCommitContributions lists the commits reachable from `revision` (which can also be a range "old..new") in
// the git repository at repoPath, and returns one row per (commit, author or co-author email).
func readCommitContributions(ctx context.Context, repoPath, revision string) ([]*activities_model.CommitContribution, error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	}()

	var rows []*activities_model.CommitContribution
	stderr := new(strings.Builder)
	err = git.NewCommand(ctx, "log", "--date=raw",
		"--format=%H%x00%ae%x00%ad%x00%(trailers:key=Co-authored-by,valueonly,separator=%x01)%x1e").
		AddDynamicArguments(revision).
		AddArguments("--").
		Run(&git.RunOpts{
			Dir:    repoPath,
			Stdout: stdoutWriter,
			Stderr: stderr,
			PipelineFunc: func(ctx context.Context, cancel context.CancelFunc) error {
				_ = stdoutWriter.Close()
				rows, err = parseCommitContributions(stdoutReader)
				if err != nil {
					cancel()
				}
				return err
			},
		})
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w, stderr: %s", revision, err, stderr.String())
	}
	return rows, nil
}

func scanRecords(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, recordSeparator); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func parseCommitContributions(r io.Reader) ([]*activities_model.CommitContribution, error) {
	rows := make([]*activities_model.CommitContribution, 0)
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	scanner.Split(scanRecords)
	for scanner.Scan() {
		record := strings.TrimSpace(scanner.Text())
		if record == "" {
			continue
		}
		parsed, err := parseCommitRecord(record)
		if err != nil {
			return nil, err
		}
		rows = append(rows, parsed...)
	}
	return rows, scanner.Err()
}

func parseCommitRecord(record string) ([]*activities_model.CommitContribution, error) {
	fields := strings.SplitN(record, fieldSeparator, 4)
	if len(fields) < 3 {
		return nil, fmt.Errorf("malformed git log record: %q", record)
	}
	sha := strings.TrimSpace(fields[0])
	unix, offset, err := parseRawDate(fields[2])
	if err != nil {
		return nil, fmt.Errorf("commit %s: %w", sha, err)
	}

	emails := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	addEmail := func(email string) {
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" || len(email) > 255 || seen[email] {
			return
		}
		seen[email] = true
		emails = append(emails, email)
	}
	addEmail(fields[1])
	if len(fields) == 4 {
		for _, coAuthor := range strings.Split(fields[3], trailerSeparator) {
			addEmail(parseTrailerEmail(coAuthor))
		}
	}

	rows := make([]*activities_model.CommitContribution, 0, len(emails))
	for _, email := range emails {
		rows = append(rows, &activities_model.CommitContribution{
			CommitSHA:      sha,
			Email:          email,
			AuthorUnix:     timeutil.TimeStamp(unix),
			AuthorTZOffset: offset,
		})
	}
	return rows, nil
}

// parseRawDate parses a git "raw" date like "1700000000 +0300" into unix seconds and an offset in seconds east of UTC.
func parseRawDate(raw string) (int64, int, error) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return 0, 0, fmt.Errorf("empty date")
	}
	unix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid date %q: %w", raw, err)
	}
	if len(parts) < 2 {
		return unix, 0, nil
	}
	tz := parts[1]
	if len(tz) != 5 || (tz[0] != '+' && tz[0] != '-') {
		return unix, 0, nil
	}
	hours, errH := strconv.Atoi(tz[1:3])
	minutes, errM := strconv.Atoi(tz[3:5])
	if errH != nil || errM != nil {
		return unix, 0, nil
	}
	offset := hours*3600 + minutes*60
	if tz[0] == '-' {
		offset = -offset
	}
	return unix, offset, nil
}

// parseTrailerEmail extracts the email out of a "Name <email>" trailer value.
func parseTrailerEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(value); err == nil {
		return addr.Address
	}
	if start, end := strings.LastIndexByte(value, '<'), strings.LastIndexByte(value, '>'); start >= 0 && end > start {
		return value[start+1 : end]
	}
	return ""
}
