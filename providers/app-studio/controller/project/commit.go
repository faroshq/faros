/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/hubmcp"
	"github.com/faroshq/provider-app-studio/workspace"
)

// Keeping git in step with the workspace.
//
// Edits land in the on-disk workspace (and stream into the dev sandbox)
// immediately; git is the durable copy, and it converges on this reconcile
// loop: the workspace store tracks uncommitted paths, and when the project is
// idle the controller commits them. No change feed, no post-write hook — a
// burst of edits collapses into a single commit, and a commit that fails is
// simply retried next pass. Interactive commits (the assistant's commit tool)
// keep working; both paths share the workspace settlement ledger so neither
// double-commits what the other already settled.
//
// Commits wait for the project to be idle: committing mid-turn would capture
// a half-written app and produce a history nobody wants to read.

// Annotation keys bridging the CR keyspace to the workspace/store keyspace
// (the reconciler only knows the cluster; scopes are keyed by org/workspace
// UUIDs the hub derives from the tenant path). Stamped at project creation.
const (
	orgUUIDAnnotation       = "ai.kedge.faros.sh/org-uuid"
	workspaceUUIDAnnotation = "ai.kedge.faros.sh/workspace-uuid"
)

// scopeOf derives the workspace scope from the Project's identity
// annotations. ok is false for legacy Projects created before the
// annotations existed — commit convergence silently skips those.
func scopeOf(p *aiv1alpha1.Project) (workspace.Scope, bool) {
	org := strings.TrimSpace(p.Annotations[orgUUIDAnnotation])
	ws := strings.TrimSpace(p.Annotations[workspaceUUIDAnnotation])
	if org == "" || ws == "" {
		return workspace.Scope{}, false
	}
	return workspace.Scope{
		OrgUUID:       org,
		WorkspaceUUID: ws,
		ProjectName:   p.Name,
		ProjectUID:    string(p.UID),
	}, true
}

// commitWorkspace pushes dirty workspace files to git when the project is
// idle. Returns (dirty, err): dirty=true means uncommitted work remains (not
// committed this pass, or partially skipped) so the caller keeps polling.
func (r *Reconciler) commitWorkspace(ctx context.Context, c client.Client, p *aiv1alpha1.Project, repo *unstructured.Unstructured) (bool, error) {
	if r.Workspace == nil || r.HubBase == "" {
		return false, nil // commit convergence not wired (REST-only dev)
	}
	b := p.Spec.Repository
	if b == nil || strings.TrimSpace(b.RepositoryRef) == "" {
		return false, nil
	}
	scope, ok := scopeOf(p)
	if !ok {
		return false, nil // legacy project without identity annotations
	}

	// Heal a settlement another writer recorded but could not reconcile
	// (e.g. the assistant crashed between commit and ledger settle).
	if _, err := r.Workspace.ReconcileCommitSettlement(ctx, scope); err != nil {
		return true, fmt.Errorf("reconcile commit settlement: %w", err)
	}

	paths, err := r.Workspace.UncommittedPaths(ctx, scope)
	if err != nil {
		return true, fmt.Errorf("list uncommitted paths: %w", err)
	}
	if len(paths) == 0 {
		return false, nil
	}
	if !repositoryReady(repo) {
		return true, nil // repository still provisioning; poll
	}
	if r.Busy != nil && r.Busy(scope) {
		return true, nil // an assistant turn owns the workspace; poll
	}

	token, err := r.ensureIdentity(ctx, c, p)
	if err != nil {
		return true, err
	}
	if token == "" {
		return true, nil // token controller not done; poll
	}
	mcp := hubmcp.NewClient(r.HubBase, clusterOf(p), token, r.HubInsecure)
	if !mcp.Ready() {
		return true, nil
	}

	// Build the payload the same way the assistant's commit tool does:
	// missing files are deletions, binary/oversized files are skipped (they
	// stay dirty for an interactive commit to deal with).
	sort.Strings(paths)
	files := make([]map[string]string, 0, len(paths))
	deletePaths := make([]string, 0)
	committed := make([]string, 0, len(paths))
	skipped := 0
	for _, path := range paths {
		read, err := r.Workspace.ReadFile(ctx, scope, workspace.ReadOptions{Path: path, MaxBytes: workspace.MaxWriteBytes})
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				deletePaths = append(deletePaths, path)
				committed = append(committed, path)
				continue
			}
			return true, fmt.Errorf("read %s: %w", path, err)
		}
		if read.Binary || read.Truncated {
			skipped++
			continue
		}
		files = append(files, map[string]string{"path": read.Path, "content": read.Content})
		committed = append(committed, path)
	}
	if len(files) == 0 && len(deletePaths) == 0 {
		return skipped > 0, nil
	}

	commitArgs := map[string]any{
		"repositoryRef": b.RepositoryRef,
		"message":       commitMessage(len(files), len(deletePaths)),
		"files":         files,
	}
	if len(deletePaths) > 0 {
		commitArgs["deletePaths"] = deletePaths
	}
	result, err := mcp.CallCodeTool(ctx, "code__commit_files", commitArgs)
	if err != nil {
		return true, fmt.Errorf("commit workspace: %w", err)
	}

	digest, err := r.Workspace.WorkspaceDigest(ctx, scope, committed)
	if err != nil {
		return true, fmt.Errorf("workspace digest after commit: %w", err)
	}
	if err := r.Workspace.RecordCommitSettlement(ctx, scope, digest, committed); err != nil {
		return true, fmt.Errorf("record commit settlement: %w", err)
	}
	if _, err := r.Workspace.ReconcileCommitSettlement(ctx, scope); err != nil {
		return true, fmt.Errorf("settle committed paths: %w", err)
	}

	sha := ""
	var commit struct {
		CommitSHA string `json:"commitSHA"`
	}
	if json.Unmarshal(result, &commit) == nil && len(commit.CommitSHA) >= 7 {
		sha = " @ " + commit.CommitSHA[:7]
	}
	log.Printf("app-studio project %s: committed %d files (%d deletions)%s", p.Name, len(files), len(deletePaths), sha)
	return skipped > 0, nil
}

// commitMessage summarizes a background convergence commit.
func commitMessage(files, deletions int) string {
	msg := fmt.Sprintf("chore: sync workspace (%d files", files)
	if deletions > 0 {
		msg += fmt.Sprintf(", %d deletions", deletions)
	}
	return msg + ")"
}

// clusterOf returns the workspace's logical-cluster id, which kcp stamps as
// an annotation on every object the VW serves.
func clusterOf(p *aiv1alpha1.Project) string {
	return p.Annotations["kcp.io/cluster"]
}
