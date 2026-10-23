// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package accesslog

import (
	"strings"

	"wardlock.nz/server/internal/store"
)

// toolMarkers name scripts, tools, crawlers and other bots. They are
// checked first: many bots also claim to be a browser.
var toolMarkers = []string{
	"bot", "crawl", "spider", "slurp", "fetcher", "scraper", "headless", "lighthouse", "preview",
	"facebookexternalhit", "chatgpt-user", "claude-user", "perplexity", "anthropic-ai",
	"curl/", "wget/", "httpie/", "python-", "python/", "aiohttp/", "httpx/", "go-http-client",
	"okhttp/", "java/", "apache-httpclient", "libwww", "lwp::", "ruby", "php/", "guzzlehttp",
	"axios/", "node-fetch", "node/", "undici", "deno/", "bun/", "postmanruntime", "insomnia/",
	"powershell", "dart:io", "reqwest/", "feed",
}

// otherBrowsers are browsers that also send Chrome's or Safari's token but
// are none of the families counted by name.
var otherBrowsers = []string{"opr/", "opera", "samsungbrowser/", "yabrowser/", "ucbrowser/", "vivaldi/", "duckduckgo/"}

// Classify returns the browser family of a User-Agent, by substrings
// alone: no lookup table to update and no third-party library. An empty
// User-Agent is a script; one naming no known browser is "other".
func Classify(userAgent string) store.Browser {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.TrimSpace(ua) == "" || containsAny(ua, toolMarkers):
		return store.BrowserTool
	case containsAny(ua, []string{"edg/", "edga/", "edgios/", "edge/"}):
		return store.BrowserEdge
	case containsAny(ua, otherBrowsers):
		return store.BrowserOther
	case containsAny(ua, []string{"firefox/", "fxios/"}):
		return store.BrowserFirefox
	case containsAny(ua, []string{"chrome/", "crios/", "chromium/"}):
		return store.BrowserChrome
	case strings.Contains(ua, "safari/"):
		return store.BrowserSafari
	}
	return store.BrowserOther
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
