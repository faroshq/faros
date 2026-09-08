/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/oauth2"

	"github.com/faroshq/provider-code/backend"
)

type pollingRoundTripper func(*http.Request) (*http.Response, error)

func (f pollingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type brokenResponseBody struct{}

func (brokenResponseBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (brokenResponseBody) Close() error             { return nil }

func TestThrottleHeadersSurviveBodyFailure(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			b, clock := pollingBackend()
			calls := 0
			headers := http.Header{}
			if status == http.StatusTooManyRequests {
				headers.Set("Retry-After", "300")
			} else {
				headers.Set("X-RateLimit-Remaining", "0")
				headers.Set("X-RateLimit-Reset", fmt.Sprint(clock.now().Add(5*time.Minute).Unix()))
			}
			transport := &sharedTransport{cache: b.requestCache(), base: pollingRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: headers, Body: brokenResponseBody{}, Request: r}, nil
			})}
			ctx := packageCacheContext(context.Background(), pollingConnection("https://example.test"))
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.test/packages", nil)
			for i := 0; i < 2; i++ {
				if _, err := transport.RoundTrip(req); err == nil {
					t.Fatal("expected error")
				}
			}
			if calls != 1 {
				t.Fatalf("sent %d requests despite throttle headers; want 1", calls)
			}
			clock.advance(5*time.Minute + time.Second)
			if _, err := transport.RoundTrip(req); err == nil || calls != 2 {
				t.Fatalf("deadline recovery: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestCachedPagesDoNotDropExistingPackages(t *testing.T) {
	b, clock := pollingBackend()
	phase := 0
	firstPages := 0
	base := pollingRoundTripper(func(r *http.Request) (*http.Response, error) {
		status := 200
		body := `[]`
		headers := http.Header{}
		if r.URL.Path == "/api/v3/users/alice" {
			body = `{"type":"User"}`
		} else if r.URL.Query().Get("package_type") == "npm" {
			page := r.URL.Query().Get("page")
			switch page {
			case "":
				firstPages++
				name := "A"
				if phase == 2 {
					name = "X"
				}
				body = fmt.Sprintf(`[{"name":%q,"package_type":"npm","repository":{"name":"demo"}}]`, name)
				headers.Set("Link", `<https://example.test/api/v3/users/alice/packages?package_type=npm&page=2&per_page=100>; rel="next"`)
			case "2":
				switch phase {
				case 0:
					status = 503
					body = `{"message":"temporary"}`
				case 1:
					body = `[{"name":"B","package_type":"npm","repository":{"name":"demo"}}]`
				default:
					body = `[{"name":"A","package_type":"npm","repository":{"name":"demo"}}]`
					headers.Set("Link", `<https://example.test/api/v3/users/alice/packages?package_type=npm&page=3&per_page=100>; rel="next"`)
				}
			default:
				body = `[{"name":"B","package_type":"npm","repository":{"name":"demo"}}]`
			}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: base})
	conn := pollingConnection("https://example.test")
	cred := backend.Credential{Token: "mock"}
	_, err := b.ListPackages(ctx, conn, cred, pollingRepo("demo"))
	if err == nil {
		t.Fatal("expected first-page retry scenario")
	}
	phase = 1
	clock.advance(time.Minute)
	got, err := b.ListPackages(ctx, conn, cred, pollingRepo("demo"))
	if err != nil || len(got) != 2 {
		t.Fatalf("initial successful crawl=%v err=%v", got, err)
	}
	if firstPages != 2 {
		t.Fatalf("retry reused a page from failed refresh: first pages=%d", firstPages)
	}
	phase = 2
	clock.advance(61 * time.Second)
	got, err = b.ListPackages(ctx, conn, cred, pollingRepo("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "A" || got[1].Name != "B" {
		t.Fatalf("mixed snapshot before expiry: %+v", got)
	}
	clock.advance(time.Minute)
	got, err = b.ListPackages(ctx, conn, cred, pollingRepo("demo"))
	if err != nil || len(got) != 3 || got[0].Name != "X" || got[1].Name != "A" || got[2].Name != "B" {
		t.Fatalf("complete refreshed listing=%+v err=%v", got, err)
	}
}

type stalledResponseBody struct{ ctx context.Context }

func (b stalledResponseBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b stalledResponseBody) Close() error             { return nil }

func TestStalledResponseTimesOutAndReleasesSharedGate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := make(chan struct{})
		base := pollingRoundTripper(func(r *http.Request) (*http.Response, error) {
			body := io.NopCloser(strings.NewReader(`[]`))
			if r.URL.Path == "/api/v3/users/alice" {
				body = io.NopCloser(strings.NewReader(`{"type":"User"}`))
			}
			if strings.HasSuffix(r.URL.Path, "/packages") {
				body = stalledResponseBody{r.Context()}
				close(started)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body, Request: r}, nil
		})
		b := New()
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: base})
		cred := backend.Credential{Token: "mock"}
		slow, err := b.client(ctx, cred, "")
		if err != nil {
			t.Fatal(err)
		}
		if slow.Client().Timeout != githubRequestTimeout {
			t.Fatalf("timeout=%s", slow.Client().Timeout)
		}
		// Stall inside a paginated refresh while it holds the credential lease.
		done := make(chan error, 1)
		go func() {
			_, err := b.ListPackages(ctx, pollingConnection("https://example.test"), cred, pollingRepo("demo"))
			done <- err
		}()
		<-started
		time.Sleep(time.Second) // virtual time; gives the waiting request a later deadline
		fast, err := b.client(ctx, cred, "")
		if err != nil {
			t.Fatal(err)
		}
		recovered := make(chan error, 1)
		go func() {
			resp, err := fast.Client().Get("https://example.test/fast")
			if resp != nil {
				_ = resp.Body.Close()
			}
			recovered <- err
		}()
		if err := <-done; err == nil {
			t.Fatal("stalled response did not time out")
		}
		if err := <-recovered; err != nil {
			t.Fatalf("shared gate did not recover: %v", err)
		}
	})
}
