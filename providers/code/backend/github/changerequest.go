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
	"strings"

	gogithub "github.com/google/go-github/v66/github"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
)

// EnsureChangeRequest creates or observes the unique pull request for the
// (repository, base, head) tuple. Reconciliation never creates duplicates.
func (b *Backend) EnsureChangeRequest(ctx context.Context, conn *codev1alpha1.Connection, cred backend.Credential, repo *codev1alpha1.Repository, input backend.ChangeRequestInput) (backend.ChangeRequestResult, error) {
	c, err := b.client(ctx, cred, conn.Spec.BaseURL)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	org := owner(conn, repo)
	head := strings.TrimSpace(input.HeadBranch)
	base := strings.TrimSpace(input.BaseBranch)
	if head == "" || base == "" {
		return backend.ChangeRequestResult{}, fmt.Errorf("github: base and head branches are required")
	}
	prs, resp, err := c.PullRequests.List(ctx, org, repo.Spec.Name, &gogithub.PullRequestListOptions{State: "all", Head: org + ":" + head, Base: base, ListOptions: gogithub.ListOptions{PerPage: 100}})
	if err != nil {
		return backend.ChangeRequestResult{}, classify(resp, err)
	}
	var pr *gogithub.PullRequest
	if len(prs) > 0 {
		pr = prs[0]
	} else {
		pr, resp, err = c.PullRequests.Create(ctx, org, repo.Spec.Name, &gogithub.NewPullRequest{Title: gogithub.String(input.Title), Head: gogithub.String(head), Base: gogithub.String(base), Body: gogithub.String(input.Body)})
		if err != nil {
			return backend.ChangeRequestResult{}, classify(resp, err)
		}
	}
	pr, resp, err = c.PullRequests.Get(ctx, org, repo.Spec.Name, pr.GetNumber())
	if err != nil {
		return backend.ChangeRequestResult{}, classify(resp, err)
	}
	return observeChangeRequest(ctx, c, org, repo.Spec.Name, pr)
}

// MergeChangeRequest asks the host to merge the reviewed request. GitHub
// branch protection remains authoritative; a rejected merge is returned as an
// error and the controller retries after its poll interval.
func (b *Backend) MergeChangeRequest(ctx context.Context, conn *codev1alpha1.Connection, cred backend.Credential, repo *codev1alpha1.Repository, number int64, expectedHeadSHA string) (backend.ChangeRequestResult, error) {
	c, err := b.client(ctx, cred, conn.Spec.BaseURL)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	org := owner(conn, repo)
	result, resp, err := c.PullRequests.Merge(ctx, org, repo.Spec.Name, int(number), "", &gogithub.PullRequestOptions{MergeMethod: "merge", SHA: expectedHeadSHA})
	if err != nil {
		return backend.ChangeRequestResult{}, classify(resp, err)
	}
	if !result.GetMerged() {
		return backend.ChangeRequestResult{}, fmt.Errorf("github: pull request was not merged: %s", result.GetMessage())
	}
	pr, resp, err := c.PullRequests.Get(ctx, org, repo.Spec.Name, int(number))
	if err != nil {
		return backend.ChangeRequestResult{}, classify(resp, err)
	}
	observed, err := observeChangeRequest(ctx, c, org, repo.Spec.Name, pr)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	if observed.MergeSHA == "" {
		observed.MergeSHA = result.GetSHA()
	}
	return observed, nil
}

func observeChangeRequest(ctx context.Context, c *gogithub.Client, org, repo string, pr *gogithub.PullRequest) (backend.ChangeRequestResult, error) {
	if pr == nil {
		return backend.ChangeRequestResult{}, fmt.Errorf("github: pull request is nil")
	}
	reviews, resp, err := c.PullRequests.ListReviews(ctx, org, repo, pr.GetNumber(), &gogithub.ListOptions{PerPage: 100})
	if err != nil {
		return backend.ChangeRequestResult{}, classify(resp, err)
	}
	states := map[int64]string{}
	for _, review := range reviews {
		if review == nil || review.User == nil {
			continue
		}
		states[review.User.GetID()] = strings.ToUpper(review.GetState())
	}
	var approvals int32
	for _, state := range states {
		if state == "APPROVED" {
			approvals++
		}
	}
	return backend.ChangeRequestResult{
		Number: int64(pr.GetNumber()), URL: pr.GetHTMLURL(), HeadSHA: pr.GetHead().GetSHA(),
		Approvals: approvals, Open: strings.EqualFold(pr.GetState(), "open"),
		Merged: pr.GetMerged(), MergeSHA: pr.GetMergeCommitSHA(),
	}, nil
}
