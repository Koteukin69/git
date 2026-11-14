// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Fork: single-owner mode (docs/fork/single-owner.md). Derives the URL of an
// issue's "info" endpoint from the issue link itself, so it works for both
// /<owner>/<repo>/issues/<n> and the short /<repo>/issues/<n> links.
export function issueInfoUrl(href) {
  let url;
  try {
    url = new URL(href || '', window.location.origin);
  } catch {
    return null;
  }
  if (url.origin !== window.location.origin) return null;
  const match = /^(.*\/(?:issues|pulls)\/[0-9]+)\/?$/.exec(url.pathname);
  return match ? `${match[1]}/info` : null;
}
