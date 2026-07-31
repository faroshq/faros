// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	agentsv1alpha1 "github.com/faroshq/provider-agents/apis/v1alpha1"
	"github.com/faroshq/provider-agents/engine"
)

const (
	webFetchMaxBody   = 200 * 1024
	webFetchMaxReturn = 12000
	braveSearchURL    = "https://api.search.brave.com/res/v1/web/search"

	// Search backends a websearch Connection can speak, selected by
	// spec.config["provider"].
	searchProviderBrave   = "brave"
	searchProviderSearXNG = "searxng"
)

// dialGuard rejects non-public dial targets. Checking the resolved socket
// address (not the pre-resolution hostname) is what defeats DNS rebinding.
// allowPrivate relaxes the loopback/private rules for a host the tenant
// explicitly allow-listed on a Connection — link-local stays blocked either
// way, because that is where cloud instance-metadata endpoints live and no
// tenant opt-in should expose those.
func dialGuard(allowPrivate bool) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("unparseable dial address %q", host)
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("refusing to connect to link-local address %s", ip)
		}
		if allowPrivate {
			return nil
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() {
			return fmt.Errorf("refusing to connect to non-public address %s", ip)
		}
		return nil
	}
}

func newHTTPClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: dialGuard(allowPrivate)}).DialContext,
		},
	}
}

// guardedHTTPClient is the default: public destinations only.
var guardedHTTPClient = newHTTPClient(false)

// relaxedHTTPClient serves allow-listed hosts (a self-hosted search backend on
// a cluster-internal or loopback address in local development).
var relaxedHTTPClient = newHTTPClient(true)

// hostAllowed reports whether host matches one of a Connection's allowedHosts
// entries. Entries match the host exactly or as a leading-dot suffix
// (".example.com" matches "searxng.example.com"). Port is ignored.
func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if a == host || (strings.HasPrefix(a, ".") && strings.HasSuffix(host, a)) {
			return true
		}
	}
	return false
}

// Web returns the web family: SSRF-guarded fetch and search. Search needs a
// websearch Connection, which speaks either a self-hosted SearXNG instance
// (no API key — see the searxng infrastructure Template) or the Brave API.
func Web(d Deps) []engine.Tool {
	return []engine.Tool{
		{
			Name: "web_fetch",
			Desc: "Fetch a public web page over HTTP(S) and return its readable text (truncated). Private/internal addresses are blocked.",
			Params: map[string]engine.Param{
				"url": {Type: "string", Desc: "absolute http(s) URL", Required: true},
			},
			Exec: func(ctx context.Context, argsJSON string) (string, error) {
				args, err := parseArgs(argsJSON)
				if err != nil {
					return "", err
				}
				return webFetch(ctx, argString(args, "url"))
			},
		},
		{
			Name: "web_search",
			Desc: "Search the web and return the top results (title, URL, snippet). Follow up with web_fetch to read a result in full. Requires a websearch connection in this workspace.",
			Params: map[string]engine.Param{
				"query": {Type: "string", Desc: "search query", Required: true},
			},
			Exec: func(ctx context.Context, argsJSON string) (string, error) {
				args, err := parseArgs(argsJSON)
				if err != nil {
					return "", err
				}
				return webSearch(ctx, d, argString(args, "query"))
			},
		},
	}
}

func webFetch(ctx context.Context, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("url must be an absolute http(s) URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "kedge-agents/0.1 (+https://github.com/faroshq/kedge)")
	resp, err := guardedHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBody))
	if err != nil {
		return "", err
	}
	text := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		text = htmlToText(text)
	}
	return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, u.String(), clip(text, webFetchMaxReturn)), nil
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlank  = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a crude readability pass: drop script/style, strip tags,
// collapse whitespace. Good enough for the model to read an article.
func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, "\n")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return reBlank.ReplaceAllString(strings.Join(out, "\n"), "\n\n")
}

func webSearch(ctx context.Context, d Deps, query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	conns, err := d.CR.ListConnections(ctx)
	if err != nil {
		return "", err
	}
	var search *agentsv1alpha1.Connection
	for i := range conns {
		if conns[i].Spec.Type == agentsv1alpha1.ConnectionTypeWebSearch {
			search = &conns[i]
			break
		}
	}
	if search == nil {
		return "", fmt.Errorf("no websearch connection in this workspace — add one on the Connections tab (a self-hosted SearXNG instance, or a Brave API key)")
	}
	token := d.connToken(ctx, search.Name)
	req, err := searchRequest(ctx, search, token, query)
	if err != nil {
		return "", err
	}
	client := guardedHTTPClient
	if hostAllowed(req.URL.Host, search.Spec.AllowedHosts) {
		client = relaxedHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("search API HTTP %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	results, err := parseSearchResults(searchProvider(search), raw)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "no results", nil
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, r.Title, r.URL, clip(htmlToText(r.Snippet), 300))
	}
	return b.String(), nil
}

// searchProvider resolves which backend a websearch Connection speaks.
// Brave is the default so a connection that only carries an API token keeps
// working without extra configuration.
func searchProvider(conn *agentsv1alpha1.Connection) string {
	if p := strings.ToLower(strings.TrimSpace(conn.Spec.Config["provider"])); p != "" {
		return p
	}
	return searchProviderBrave
}

// searchRequest builds the backend-specific query. Brave authenticates with
// its own header and is a fixed hosted endpoint; SearXNG is self-hosted, so it
// takes the Connection's baseURL and an optional bearer token (the kedge
// searxng Template fronts it with a token gate, but a bare instance needs no
// credential at all).
func searchRequest(ctx context.Context, conn *agentsv1alpha1.Connection, token, query string) (*http.Request, error) {
	base := strings.TrimSpace(conn.Spec.BaseURL)
	switch searchProvider(conn) {
	case searchProviderSearXNG:
		if base == "" {
			return nil, fmt.Errorf("websearch connection %q is searxng but has no baseURL — set it to your instance URL", conn.Name)
		}
		// Accept either the instance root or the /search endpoint: pointing a
		// connection at the URL the template reports is the obvious thing to do.
		endpoint := strings.TrimRight(base, "/")
		if !strings.HasSuffix(endpoint, "/search") {
			endpoint += "/search"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			endpoint+"?q="+url.QueryEscape(query)+"&format=json", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return req, nil

	case searchProviderBrave:
		if token == "" {
			return nil, fmt.Errorf("websearch connection %q has no API token", conn.Name)
		}
		if base == "" {
			base = braveSearchURL
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			base+"?q="+url.QueryEscape(query)+"&count=5", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Subscription-Token", token)
		return req, nil

	default:
		return nil, fmt.Errorf("websearch connection %q has unknown provider %q (want %q or %q)",
			conn.Name, searchProvider(conn), searchProviderBrave, searchProviderSearXNG)
	}
}

// searchResult is one hit, normalized across backends.
type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// searchResultLimit bounds how many hits reach the model — the agent follows up
// with web_fetch on whichever look relevant, so a long list only burns context.
const searchResultLimit = 5

func parseSearchResults(provider string, raw []byte) ([]searchResult, error) {
	switch provider {
	case searchProviderSearXNG:
		var parsed struct {
			Results []struct {
				Title   string `json:"title"`
				URL     string `json:"url"`
				Content string `json:"content"`
			} `json:"results"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("parsing searxng response: %w", err)
		}
		out := make([]searchResult, 0, min(len(parsed.Results), searchResultLimit))
		for _, r := range parsed.Results {
			if len(out) == searchResultLimit {
				break
			}
			out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
		}
		return out, nil

	default:
		var parsed struct {
			Web struct {
				Results []struct {
					Title       string `json:"title"`
					URL         string `json:"url"`
					Description string `json:"description"`
				} `json:"results"`
			} `json:"web"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil, fmt.Errorf("parsing search response: %w", err)
		}
		out := make([]searchResult, 0, len(parsed.Web.Results))
		for _, r := range parsed.Web.Results {
			out = append(out, searchResult{Title: r.Title, URL: r.URL, Snippet: r.Description})
		}
		return out, nil
	}
}
