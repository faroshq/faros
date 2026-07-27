/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workspace

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	einofs "github.com/cloudwego/eino/adk/filesystem"
)

const (
	maxEinoBackendAggregateBytes = 16 << 20
	maxEinoBackendMatches        = 1000
	einoBackendCandidateMarker   = "__app_studio_eino_candidate__"
)

var errEinoReadOnlyWorkspace = errors.New("App Studio project filesystem backend is read-only")

// EinoReadOnlyBackend exposes one App Studio project through Eino's filesystem
// interface without granting filesystem mutation capabilities.
type EinoReadOnlyBackend struct {
	store *FileStore
	scope Scope
}

// NewEinoReadOnlyBackend returns an Eino filesystem backend scoped to exactly
// one configured App Studio project.
func NewEinoReadOnlyBackend(store *FileStore, scope Scope) (*EinoReadOnlyBackend, error) {
	if store == nil {
		return nil, errors.New("project workspace store is not configured")
	}
	if _, err := store.scopeDir(scope); err != nil {
		return nil, err
	}
	return &EinoReadOnlyBackend{store: store, scope: scope}, nil
}

func (b *EinoReadOnlyBackend) projectFiles(ctx context.Context) ([]FileInfo, error) {
	list, err := b.store.ListFiles(ctx, b.scope, ListOptions{Limit: MaxListLimit})
	if err != nil {
		return nil, err
	}
	if list.Truncated {
		return nil, fmt.Errorf("project has more than %d files; narrow path or glob", MaxListLimit)
	}
	return list.Files, nil
}

func cleanEinoDirectoryPath(raw string) (string, error) {
	if raw == "" || raw == "." {
		return "/", nil
	}
	clean, err := cleanProjectPath(raw)
	if err != nil {
		return "", err
	}
	return "/" + clean, nil
}

func cleanEinoGlobPattern(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("glob pattern cannot be empty")
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("glob pattern %q must be relative", raw)
	}
	if _, err := cleanProjectPath(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func einoMetadataSnapshot(ctx context.Context, files []FileInfo) (*einofs.InMemoryBackend, error) {
	snapshot := einofs.NewInMemoryBackend()
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := snapshot.Write(ctx, &einofs.WriteRequest{
			FilePath: "/" + file.Path,
			Content:  einoBackendCandidateMarker,
		}); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func (b *EinoReadOnlyBackend) GlobInfo(ctx context.Context, req *einofs.GlobInfoRequest) ([]einofs.FileInfo, error) {
	if req == nil {
		return nil, errors.New("glob request is required")
	}
	basePath, err := cleanEinoDirectoryPath(req.Path)
	if err != nil {
		return nil, err
	}
	pattern, err := cleanEinoGlobPattern(req.Pattern)
	if err != nil {
		return nil, err
	}
	files, err := b.projectFiles(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := einoMetadataSnapshot(ctx, files)
	if err != nil {
		return nil, err
	}
	infos, err := snapshot.GlobInfo(ctx, &einofs.GlobInfoRequest{Path: basePath, Pattern: pattern})
	if err != nil {
		return nil, err
	}
	sizes := make(map[string]int64, len(files))
	for _, file := range files {
		sizes[file.Path] = file.Size
	}
	for i := range infos {
		if basePath != "/" {
			infos[i].Path = strings.TrimPrefix(basePath, "/") + "/" + infos[i].Path
		}
		infos[i].Path = strings.TrimPrefix(infos[i].Path, "/")
		infos[i].Size = sizes[infos[i].Path]
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

func (b *EinoReadOnlyBackend) LsInfo(ctx context.Context, req *einofs.LsInfoRequest) ([]einofs.FileInfo, error) {
	if req == nil {
		return nil, errors.New("ls request is required")
	}
	basePath, err := cleanEinoDirectoryPath(req.Path)
	if err != nil {
		return nil, err
	}
	files, err := b.projectFiles(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := einoMetadataSnapshot(ctx, files)
	if err != nil {
		return nil, err
	}
	infos, err := snapshot.LsInfo(ctx, &einofs.LsInfoRequest{Path: basePath})
	if err != nil {
		return nil, err
	}
	sizes := make(map[string]int64, len(files))
	for _, file := range files {
		sizes[file.Path] = file.Size
	}
	for i := range infos {
		if !infos[i].IsDir {
			fullPath := infos[i].Path
			if basePath != "/" {
				fullPath = strings.TrimPrefix(basePath, "/") + "/" + fullPath
			}
			infos[i].Size = sizes[fullPath]
		}
		infos[i].ModifiedAt = ""
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	return infos, nil
}

func (b *EinoReadOnlyBackend) Read(ctx context.Context, req *einofs.ReadRequest) (*einofs.FileContent, error) {
	if req == nil {
		return nil, errors.New("read request is required")
	}
	clean, err := cleanProjectPath(req.FilePath)
	if err != nil {
		return nil, err
	}
	file, err := b.store.ReadFile(ctx, b.scope, ReadOptions{Path: clean, MaxBytes: MaxReadMaxBytes})
	if err != nil {
		return nil, err
	}
	if file.Binary {
		return nil, fmt.Errorf("file %q is binary", file.Path)
	}
	if file.Truncated {
		return nil, fmt.Errorf("file %q is too large to read", file.Path)
	}
	snapshot := einofs.NewInMemoryBackend()
	if err := snapshot.Write(ctx, &einofs.WriteRequest{FilePath: "/" + clean, Content: file.Content}); err != nil {
		return nil, err
	}
	return snapshot.Read(ctx, &einofs.ReadRequest{FilePath: "/" + clean, Offset: req.Offset, Limit: req.Limit})
}

func (b *EinoReadOnlyBackend) GrepRaw(ctx context.Context, req *einofs.GrepRequest) ([]einofs.GrepMatch, error) {
	if req == nil {
		return nil, errors.New("grep request is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	basePath, err := cleanEinoDirectoryPath(req.Path)
	if err != nil {
		return nil, err
	}
	files, err := b.projectFiles(ctx)
	if err != nil {
		return nil, err
	}
	metadata, err := einoMetadataSnapshot(ctx, files)
	if err != nil {
		return nil, err
	}
	candidateReq := *req
	candidateReq.Path = basePath
	candidateReq.Pattern = regexp.QuoteMeta(einoBackendCandidateMarker)
	candidateReq.CaseInsensitive = false
	candidateReq.EnableMultiline = false
	candidateReq.BeforeLines = 0
	candidateReq.AfterLines = 0
	candidates, err := metadata.GrepRaw(ctx, &candidateReq)
	if err != nil {
		return nil, err
	}
	candidatePaths := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidatePaths[strings.TrimPrefix(candidate.Path, "/")] = struct{}{}
	}
	paths := make([]string, 0, len(candidatePaths))
	for candidatePath := range candidatePaths {
		paths = append(paths, candidatePath)
	}
	sort.Strings(paths)

	content := einofs.NewInMemoryBackend()
	totalBytes := 0
	for _, candidatePath := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := b.store.ReadFile(ctx, b.scope, ReadOptions{Path: candidatePath, MaxBytes: MaxReadMaxBytes})
		if err != nil {
			return nil, err
		}
		if file.Binary {
			continue
		}
		if file.Truncated {
			return nil, fmt.Errorf("file %q is too large to search; narrow request", file.Path)
		}
		totalBytes += len([]byte(file.Content))
		if totalBytes > maxEinoBackendAggregateBytes {
			return nil, fmt.Errorf("search exceeds %d bytes; narrow request", maxEinoBackendAggregateBytes)
		}
		if err := content.Write(ctx, &einofs.WriteRequest{FilePath: "/" + file.Path, Content: file.Content}); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	searchReq := *req
	searchReq.Path = basePath
	matches, err := content.GrepRaw(ctx, &searchReq)
	if err != nil {
		return nil, err
	}
	for i := range matches {
		matches[i].Path = strings.TrimPrefix(matches[i].Path, "/")
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Content < matches[j].Content
	})
	if len(matches) > maxEinoBackendMatches {
		return nil, fmt.Errorf("search produced more than %d matches; narrow request", maxEinoBackendMatches)
	}
	return matches, nil
}

func (b *EinoReadOnlyBackend) Write(context.Context, *einofs.WriteRequest) error {
	return errEinoReadOnlyWorkspace
}

func (b *EinoReadOnlyBackend) Edit(context.Context, *einofs.EditRequest) error {
	return errEinoReadOnlyWorkspace
}
