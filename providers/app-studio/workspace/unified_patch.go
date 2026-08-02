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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PatchErrorCode is a stable, safe classification for a contextual patch
// failure. Callers may expose these codes to the model and audit log.
type PatchErrorCode string

const (
	PatchErrorInvalidPatch      PatchErrorCode = "invalid_patch"
	PatchErrorContextNotFound   PatchErrorCode = "context_not_found"
	PatchErrorContextAmbiguous  PatchErrorCode = "context_ambiguous"
	PatchErrorTargetExists      PatchErrorCode = "target_exists"
	PatchErrorTargetNotFound    PatchErrorCode = "target_not_found"
	PatchErrorWorkspaceConflict PatchErrorCode = "workspace_conflict"
	PatchErrorNoChanges         PatchErrorCode = "no_changes"
	PatchErrorApplyFailed       PatchErrorCode = "apply_failed"
	PatchErrorStrategyChange    PatchErrorCode = "strategy_change_required"
)

var standardUnifiedDiffHunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+[0-9]+(?:,[0-9]+)? @@(?: .*)?$`)

// PatchError is a typed, model-safe patch failure. ActualChanges is populated
// only if an I/O failure could not be completely rolled back.
type PatchError struct {
	Code                   PatchErrorCode   `json:"code"`
	Path                   string           `json:"path,omitempty"`
	Hunk                   int              `json:"hunk,omitempty"`
	Matches                int              `json:"matches,omitempty"`
	Message                string           `json:"message"`
	ExpectedContext        string           `json:"expectedContext,omitempty"`
	ActualContext          string           `json:"actualContext,omitempty"`
	SourceMutationRevision uint64           `json:"sourceMutationRevision"`
	ActualChanges          []MutationResult `json:"actualChanges,omitempty"`
}

func (e *PatchError) Error() string {
	if e == nil {
		return ""
	}
	detail := strings.TrimSpace(e.Message)
	if detail == "" {
		detail = string(e.Code)
	}
	parts := []string{string(e.Code)}
	if e.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%q", e.Path))
	}
	if e.Hunk > 0 {
		parts = append(parts, fmt.Sprintf("hunk=%d", e.Hunk))
	}
	if e.Matches > 0 {
		parts = append(parts, fmt.Sprintf("matches=%d", e.Matches))
	}
	return strings.Join(parts, " ") + ": " + detail
}

func newPatchError(code PatchErrorCode, filePath string, hunk, matches int, format string, args ...any) *PatchError {
	return &PatchError{
		Code:    code,
		Path:    filePath,
		Hunk:    hunk,
		Matches: matches,
		Message: fmt.Sprintf(format, args...),
	}
}

func withPatchErrorContext(err *PatchError, expected, actual string) *PatchError {
	if err == nil {
		return nil
	}
	err.ExpectedContext = boundedPatchContext(expected)
	err.ActualContext = boundedPatchContext(actual)
	return err
}

func boundedPatchContext(value string) string {
	const maxRunes = 2_000
	value = strings.Trim(value, "\r\n")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}

type patchOperationKind uint8

const (
	patchOperationAdd patchOperationKind = iota + 1
	patchOperationDelete
	patchOperationUpdate
)

type parsedPatch struct {
	operations []patchOperation
}

type patchOperation struct {
	kind     patchOperationKind
	path     string
	movePath string
	content  string
	chunks   []patchChunk
}

type patchChunk struct {
	anchor    string
	oldLines  []string
	newLines  []string
	endOfFile bool
}

// PatchPaths parses a patch without touching the workspace and returns every
// canonical source and destination path it can affect. Authorization layers
// must approve every returned path before invoking ApplyPatch.
func PatchPaths(patch string) ([]string, error) {
	parsed, err := parseUnifiedPatch(patch)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0, len(parsed.operations)*2)
	for _, operation := range parsed.operations {
		for _, candidate := range []string{operation.path, operation.movePath} {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			paths = append(paths, candidate)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// PatchReadPaths returns the canonical existing-file paths whose current
// contents a patch relies on. Add File targets are intentionally excluded.
func PatchReadPaths(patch string) ([]string, error) {
	parsed, err := parseUnifiedPatch(patch)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(parsed.operations))
	for _, operation := range parsed.operations {
		if operation.kind != patchOperationAdd {
			paths = append(paths, operation.path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// ValidateCommittablePatch verifies that an assistant patch is syntactically
// valid before it reaches the workspace mutation boundary. The repository
// bridge supports every parsed operation, including deletion and move.
func ValidateCommittablePatch(patch string) error {
	_, err := parseUnifiedPatch(patch)
	return err
}

func parseUnifiedPatch(raw string) (parsedPatch, error) {
	if len([]byte(raw)) > MaxUnifiedPatchBytes {
		return parsedPatch{}, newPatchError(
			PatchErrorInvalidPatch,
			"",
			0,
			0,
			"patch is too large: %d > %d bytes",
			len([]byte(raw)),
			MaxUnifiedPatchBytes,
		)
	}
	if !validTextContent(raw) {
		return parsedPatch{}, newPatchError(PatchErrorInvalidPatch, "", 0, 0, "patch must be UTF-8 text without NUL bytes")
	}
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.TrimSpace(normalized)
	lines := strings.Split(normalized, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return parsedPatch{}, newPatchError(PatchErrorInvalidPatch, "", 0, 0, "the first line must be '*** Begin Patch'")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return parsedPatch{}, newPatchError(PatchErrorInvalidPatch, "", 0, 0, "the last line must be '*** End Patch'")
	}

	parsed := parsedPatch{}
	for lineIndex := 1; lineIndex < len(lines)-1; {
		line := lines[lineIndex]
		if strings.TrimSpace(line) == "" {
			lineIndex++
			continue
		}
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			filePath, err := parsePatchMarkerPath(line, "*** Add File: ", lineIndex+1)
			if err != nil {
				return parsedPatch{}, err
			}
			lineIndex++
			added := []string{}
			for lineIndex < len(lines)-1 && !isPatchFileMarker(lines[lineIndex]) {
				// An Add File body is unambiguous: every line until the next
				// top-level marker is new content. Accept omitted '+' prefixes
				// while preserving the canonical prefixed form. This mirrors the
				// exact-context leniency used for Update File hunks and remains
				// safe because Add File preflight rejects an existing target.
				added = append(added, strings.TrimPrefix(lines[lineIndex], "+"))
				lineIndex++
			}
			if len(added) == 0 {
				return parsedPatch{}, invalidPatchLine(lineIndex+1, "Add File requires at least one content line")
			}
			parsed.operations = append(parsed.operations, patchOperation{
				kind:    patchOperationAdd,
				path:    filePath,
				content: strings.Join(added, "\n") + "\n",
			})

		case strings.HasPrefix(line, "*** Delete File: "):
			filePath, err := parsePatchMarkerPath(line, "*** Delete File: ", lineIndex+1)
			if err != nil {
				return parsedPatch{}, err
			}
			lineIndex++
			if lineIndex < len(lines)-1 && !isPatchFileMarker(lines[lineIndex]) {
				return parsedPatch{}, invalidPatchLine(lineIndex+1, "Delete File cannot contain patch lines")
			}
			parsed.operations = append(parsed.operations, patchOperation{kind: patchOperationDelete, path: filePath})

		case strings.HasPrefix(line, "*** Update File: "):
			operation, next, err := parseUpdateOperation(lines, lineIndex)
			if err != nil {
				return parsedPatch{}, err
			}
			parsed.operations = append(parsed.operations, operation)
			lineIndex = next

		default:
			return parsedPatch{}, invalidPatchLine(lineIndex+1, "expected Add File, Delete File, or Update File header")
		}
	}
	if len(parsed.operations) == 0 {
		return parsedPatch{}, newPatchError(PatchErrorInvalidPatch, "", 0, 0, "patch must contain at least one file operation")
	}
	if err := validatePatchOperationPaths(parsed.operations); err != nil {
		return parsedPatch{}, err
	}
	return parsed, nil
}

func parseUpdateOperation(lines []string, start int) (patchOperation, int, error) {
	filePath, err := parsePatchMarkerPath(lines[start], "*** Update File: ", start+1)
	if err != nil {
		return patchOperation{}, start, err
	}
	operation := patchOperation{kind: patchOperationUpdate, path: filePath}
	lineIndex := start + 1
	if lineIndex < len(lines)-1 && strings.HasPrefix(lines[lineIndex], "*** Move to: ") {
		movePath, err := parsePatchMarkerPath(lines[lineIndex], "*** Move to: ", lineIndex+1)
		if err != nil {
			return patchOperation{}, start, err
		}
		operation.movePath = movePath
		lineIndex++
	}

	var current *patchChunk
	flush := func() {
		if current != nil {
			operation.chunks = append(operation.chunks, *current)
			current = nil
		}
	}
	for lineIndex < len(lines)-1 && !isPatchFileMarker(lines[lineIndex]) {
		line := lines[lineIndex]
		switch {
		case standardUnifiedDiffHunkHeader.MatchString(line):
			return patchOperation{}, start, invalidPatchLine(lineIndex+1, "numeric unified-diff hunk headers are not supported; use exactly '@@' or '@@ <literal source line>'")
		case line == "@@" || strings.HasPrefix(line, "@@ "):
			flush()
			current = &patchChunk{anchor: strings.TrimSpace(strings.TrimPrefix(line, "@@"))}
		case line == "*** End of File":
			if current == nil {
				return patchOperation{}, start, invalidPatchLine(lineIndex+1, "End of File requires a preceding hunk")
			}
			current.endOfFile = true
			lineIndex++
			if lineIndex < len(lines)-1 && !isPatchFileMarker(lines[lineIndex]) {
				return patchOperation{}, start, invalidPatchLine(lineIndex+1, "End of File must finish the current file update")
			}
			continue
		case strings.HasPrefix(line, " "), strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			if current == nil {
				current = &patchChunk{}
			}
			text := line[1:]
			switch line[0] {
			case ' ':
				current.oldLines = append(current.oldLines, text)
				current.newLines = append(current.newLines, text)
			case '+':
				current.newLines = append(current.newLines, text)
			case '-':
				current.oldLines = append(current.oldLines, text)
			}
		default:
			// Be lenient with an omitted context marker. We still preflight
			// this exact line against the immutable source snapshot before
			// applying anything, so a model cannot turn invented text into
			// a mutation by leaving off the leading space.
			if current == nil {
				current = &patchChunk{}
			}
			current.oldLines = append(current.oldLines, line)
			current.newLines = append(current.newLines, line)
		}
		lineIndex++
	}
	flush()
	if len(operation.chunks) == 0 && operation.movePath == "" {
		return patchOperation{}, start, newPatchError(PatchErrorInvalidPatch, filePath, 0, 0, "Update File requires at least one hunk or Move to")
	}
	return operation, lineIndex, nil
}

func parsePatchMarkerPath(line, marker string, lineNumber int) (string, error) {
	rawPath := strings.TrimSpace(strings.TrimPrefix(line, marker))
	if rawPath == "" || rawPath == line {
		return "", invalidPatchLine(lineNumber, fmt.Sprintf("%s requires a relative path", strings.TrimSpace(marker)))
	}
	clean, err := cleanProjectPath(rawPath)
	if err != nil {
		return "", newPatchError(PatchErrorInvalidPatch, rawPath, 0, 0, "invalid path on line %d: %v", lineNumber, err)
	}
	return clean, nil
}

func invalidPatchLine(lineNumber int, message string) *PatchError {
	return newPatchError(PatchErrorInvalidPatch, "", 0, 0, "line %d: %s", lineNumber, message)
}

func isPatchFileMarker(line string) bool {
	return strings.HasPrefix(line, "*** Add File: ") ||
		strings.HasPrefix(line, "*** Delete File: ") ||
		strings.HasPrefix(line, "*** Update File: ")
}

func validatePatchOperationPaths(operations []patchOperation) error {
	touched := make(map[string]string, len(operations)*2)
	for _, operation := range operations {
		if previous, exists := touched[operation.path]; exists {
			return newPatchError(PatchErrorInvalidPatch, operation.path, 0, 0, "path is touched more than once (%s and another operation)", previous)
		}
		touched[operation.path] = "source"
		if operation.movePath == "" {
			continue
		}
		if operation.movePath == operation.path {
			return newPatchError(PatchErrorNoChanges, operation.path, 0, 0, "Move to path is the same as the source path")
		}
		if previous, exists := touched[operation.movePath]; exists {
			return newPatchError(PatchErrorInvalidPatch, operation.movePath, 0, 0, "move destination is also used as a %s path", previous)
		}
		touched[operation.movePath] = "destination"
	}
	return nil
}

type textLines struct {
	lines       []string
	lineEnding  string
	finalEnding bool
}

func splitPatchText(content string) textLines {
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	finalEnding := strings.HasSuffix(normalized, "\n")
	if finalEnding {
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	if normalized == "" {
		return textLines{lineEnding: lineEnding, finalEnding: finalEnding}
	}
	return textLines{lines: strings.Split(normalized, "\n"), lineEnding: lineEnding, finalEnding: finalEnding}
}

func (t textLines) string() string {
	content := strings.Join(t.lines, t.lineEnding)
	if t.finalEnding {
		content += t.lineEnding
	}
	return content
}

type patchMatchTier uint8

const (
	patchMatchExact patchMatchTier = iota
	patchMatchTrailingWhitespace
	patchMatchWhitespace
	patchMatchUnicode
)

func findUniquePatchSequence(lines, pattern []string, start int, endOfFile bool) (int, int) {
	if start < 0 {
		start = 0
	}
	if len(pattern) == 0 || len(pattern) > len(lines) || start > len(lines)-len(pattern) {
		return -1, 0
	}
	for tier := patchMatchExact; tier <= patchMatchUnicode; tier++ {
		matches := []int{}
		first := start
		last := len(lines) - len(pattern)
		if endOfFile {
			first = last
		}
		for index := first; index <= last; index++ {
			if patchSequenceMatches(lines[index:index+len(pattern)], pattern, tier) {
				matches = append(matches, index)
			}
		}
		if len(matches) > 0 {
			if len(matches) == 1 {
				return matches[0], 1
			}
			return -1, len(matches)
		}
	}
	return -1, 0
}

func patchSequenceMatches(actual, expected []string, tier patchMatchTier) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		left, right := actual[index], expected[index]
		switch tier {
		case patchMatchTrailingWhitespace:
			left, right = strings.TrimRight(left, " \t"), strings.TrimRight(right, " \t")
		case patchMatchWhitespace:
			left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		case patchMatchUnicode:
			left, right = normalizePatchPunctuation(left), normalizePatchPunctuation(right)
		}
		if left != right {
			return false
		}
	}
	return true
}

func normalizePatchPunctuation(value string) string {
	return strings.Map(func(char rune) rune {
		switch char {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u2018', '\u2019', '\u201a', '\u201b':
			return '\''
		case '\u201c', '\u201d', '\u201e', '\u201f':
			return '"'
		case '\u00a0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u202f', '\u205f', '\u3000':
			return ' '
		default:
			return char
		}
	}, strings.TrimSpace(value))
}

// applyPatchChunks accepts independently matchable hunks in any order. Models
// frequently group edits by concern instead of by source position; the patch
// language does not carry numeric coordinates, so requiring the caller to
// rediscover and reorder already-unique context only creates read/retry loops.
// Try the authored order first to preserve dependent-hunk semantics, then make
// one safe retry in source order when every hunk resolves uniquely against the
// unchanged file.
func applyPatchChunks(filePath, content string, chunks []patchChunk) (string, int, error) {
	next, changed, err := applyPatchChunksInOrder(filePath, content, chunks)
	if err == nil {
		return next, changed, err
	}
	next, changed, ok := applyIndependentPatchChunks(content, chunks)
	if !ok {
		return "", 0, err
	}
	return next, changed, nil
}

type locatedPatchChunk struct {
	chunk patchChunk
	start int
	end   int
	order int
}

func applyIndependentPatchChunks(content string, chunks []patchChunk) (string, int, bool) {
	text := splitPatchText(content)
	located := make([]locatedPatchChunk, 0, len(chunks))
	normalizedContext := false
	for index, chunk := range chunks {
		start := 0
		if chunk.anchor != "" {
			anchorIndex, matches := findUniquePatchSequence(text.lines, []string{chunk.anchor}, 0, false)
			if matches == 1 {
				start = anchorIndex + 1
			} else if matches == 0 || len(chunk.oldLines) == 0 {
				return "", 0, false
			}
			// A repeated literal anchor is only a navigation hint. Let the full
			// unchanged hunk body disambiguate it against the immutable source.
		}
		matchIndex := start
		switch {
		case len(chunk.oldLines) > 0:
			var matches int
			matchIndex, matches = findUniquePatchSequence(text.lines, chunk.oldLines, start, chunk.endOfFile)
			if matches == 0 && chunk.anchor != "" && stringSliceContains(chunk.oldLines, chunk.anchor) {
				// Some models repeat the literal anchor in the hunk body even
				// though the contract says not to. The body is authoritative
				// when it resolves uniquely against the original snapshot.
				matchIndex, matches = findUniquePatchSequence(text.lines, chunk.oldLines, 0, chunk.endOfFile)
				normalizedContext = matches == 1
			}
			if matches != 1 {
				return "", 0, false
			}
		case chunk.endOfFile:
			matchIndex = len(text.lines)
		case chunk.anchor == "":
			return "", 0, false
		}
		located = append(located, locatedPatchChunk{
			chunk: chunk,
			start: matchIndex,
			end:   matchIndex + len(chunk.oldLines),
			order: index,
		})
	}

	sort.SliceStable(located, func(left, right int) bool {
		return located[left].start < located[right].start
	})
	changedOrder := false
	for index := range located {
		if located[index].order != index {
			changedOrder = true
		}
		if index > 0 && located[index-1].end > located[index].start {
			return "", 0, false
		}
	}
	if !changedOrder && !normalizedContext {
		return "", 0, false
	}
	changedHunks := 0
	for index := len(located) - 1; index >= 0; index-- {
		item := located[index]
		next := make([]string, 0, len(text.lines)-len(item.chunk.oldLines)+len(item.chunk.newLines))
		next = append(next, text.lines[:item.start]...)
		next = append(next, item.chunk.newLines...)
		next = append(next, text.lines[item.end:]...)
		if !equalStringSlices(item.chunk.oldLines, item.chunk.newLines) {
			changedHunks++
		}
		text.lines = next
	}
	return text.string(), changedHunks, true
}

func stringSliceContains(values []string, candidate string) bool {
	for _, value := range values {
		if patchSequenceMatches([]string{value}, []string{candidate}, patchMatchUnicode) {
			return true
		}
	}
	return false
}

func applyPatchChunksInOrder(filePath, content string, chunks []patchChunk) (string, int, error) {
	text := splitPatchText(content)
	cursor := 0
	changedHunks := 0
	for chunkIndex, chunk := range chunks {
		hunkNumber := chunkIndex + 1
		if chunk.anchor != "" {
			anchorIndex, matches := findUniquePatchSequence(text.lines, []string{chunk.anchor}, cursor, false)
			switch {
			case matches == 0:
				return "", 0, withPatchErrorContext(
					newPatchError(PatchErrorContextNotFound, filePath, hunkNumber, 0, "hunk anchor %q was not found after line %d", chunk.anchor, cursor),
					chunk.anchor,
					patchActualLinesPreview(text.lines, cursor, 1),
				)
			case matches > 1 && len(chunk.oldLines) == 0:
				return "", 0, withPatchErrorContext(
					newPatchError(PatchErrorContextAmbiguous, filePath, hunkNumber, matches, "hunk anchor %q is not unique; include more context", chunk.anchor),
					chunk.anchor,
					patchActualLinesPreview(text.lines, cursor, 1),
				)
			case matches > 1:
				// Codex-style @@ anchors are navigation hints. When a literal
				// anchor repeats, the full unchanged body below remains the
				// authoritative, safely unique match.
				anchorIndex = -1
			}
			if anchorIndex >= 0 {
				cursor = anchorIndex + 1
			}
		}
		if len(chunk.oldLines) == 0 && len(chunk.newLines) == 0 {
			continue
		}
		matchIndex := -1
		matches := 0
		if len(chunk.oldLines) == 0 {
			switch {
			case chunk.endOfFile:
				matchIndex, matches = len(text.lines), 1
			case chunk.anchor != "":
				matchIndex, matches = cursor, 1
			default:
				return "", 0, newPatchError(PatchErrorInvalidPatch, filePath, hunkNumber, 0, "an insertion requires context, an @@ anchor, or End of File")
			}
		} else {
			matchIndex, matches = findUniquePatchSequence(text.lines, chunk.oldLines, cursor, chunk.endOfFile)
		}
		switch {
		case matches == 0:
			return "", 0, withPatchErrorContext(newPatchError(
				PatchErrorContextNotFound,
				filePath,
				hunkNumber,
				0,
				"failed to find the expected lines after line %d:\n%s",
				cursor,
				patchExpectedLinesPreview(chunk.oldLines),
			), patchExpectedLinesPreview(chunk.oldLines), patchActualLinesPreview(text.lines, cursor, len(chunk.oldLines)))
		case matches > 1:
			return "", 0, withPatchErrorContext(
				newPatchError(PatchErrorContextAmbiguous, filePath, hunkNumber, matches, "hunk context matched %d locations; include more surrounding context or an @@ anchor", matches),
				patchExpectedLinesPreview(chunk.oldLines),
				patchActualLinesPreview(text.lines, cursor, len(chunk.oldLines)),
			)
		}
		next := make([]string, 0, len(text.lines)-len(chunk.oldLines)+len(chunk.newLines))
		next = append(next, text.lines[:matchIndex]...)
		next = append(next, chunk.newLines...)
		next = append(next, text.lines[matchIndex+len(chunk.oldLines):]...)
		if !equalStringSlices(chunk.oldLines, chunk.newLines) {
			changedHunks++
		}
		text.lines = next
		cursor = matchIndex + len(chunk.newLines)
	}
	return text.string(), changedHunks, nil
}

func patchExpectedLinesPreview(lines []string) string {
	const (
		maxLines     = 12
		maxLineRunes = 240
	)
	count := len(lines)
	if count > maxLines {
		count = maxLines
	}
	preview := make([]string, 0, count+1)
	for _, line := range lines[:count] {
		runes := []rune(line)
		if len(runes) > maxLineRunes {
			line = string(runes[:maxLineRunes]) + "…"
		}
		preview = append(preview, line)
	}
	if len(lines) > count {
		preview = append(preview, fmt.Sprintf("… (%d more lines)", len(lines)-count))
	}
	return strings.Join(preview, "\n")
}

func patchActualLinesPreview(lines []string, cursor, expectedLines int) string {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(lines) {
		cursor = len(lines)
	}
	count := max(expectedLines, 3)
	if count > 12 {
		count = 12
	}
	end := min(cursor+count, len(lines))
	return patchExpectedLinesPreview(lines[cursor:end])
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type patchFileState struct {
	path          string
	before        []byte
	beforeExisted bool
	after         []byte
	afterExisted  bool
	beforeMode    fs.FileMode
	afterMode     fs.FileMode
}

type preparedPatchOperation struct {
	operation patchOperation
	states    []*patchFileState
	result    MutationResult
}

func (s *FileStore) applyUnifiedPatch(ctx context.Context, scope Scope, opts PatchOptions) (MutationResult, error) {
	if s == nil {
		return MutationResult{}, errors.New("project workspace store is not configured")
	}
	parsed, err := parseUnifiedPatch(opts.Patch)
	if err != nil {
		return MutationResult{}, err
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	prepared, states, err := s.preflightUnifiedPatch(ctx, scope, parsed)
	if err != nil {
		return MutationResult{}, err
	}
	if err := s.prepareUnifiedPatchSnapshots(ctx, scope, opts.SnapshotID, states); err != nil {
		return MutationResult{}, err
	}
	if err := s.verifyPatchBaselines(ctx, scope, states); err != nil {
		s.resetPatchSnapshotStates(scope, opts.SnapshotID, states)
		return MutationResult{}, err
	}

	applied := make([]*patchFileState, 0, len(states))
	for _, operation := range prepared {
		for _, state := range operation.states {
			if err := s.applyPatchFileState(ctx, scope, state); err != nil {
				result, patchErr := s.rollbackUnifiedPatch(ctx, scope, opts.SnapshotID, states, applied, state, err)
				return result, patchErr
			}
			applied = append(applied, state)
		}
	}
	return aggregatePatchMutationResults(prepared), nil
}

func (s *FileStore) preflightUnifiedPatch(ctx context.Context, scope Scope, parsed parsedPatch) ([]preparedPatchOperation, []*patchFileState, error) {
	prepared := make([]preparedPatchOperation, 0, len(parsed.operations))
	states := make([]*patchFileState, 0, len(parsed.operations)*2)
	for _, operation := range parsed.operations {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		before, existed, mode, err := s.readPatchTarget(ctx, scope, operation.path)
		if err != nil {
			return nil, nil, patchTargetError(ctx, operation.path, err)
		}
		switch operation.kind {
		case patchOperationAdd:
			if existed {
				return nil, nil, newPatchError(PatchErrorTargetExists, operation.path, 0, 0, "Add File target already exists")
			}
			if err := validateMutationContent(operation.path, operation.content); err != nil {
				return nil, nil, newPatchError(PatchErrorInvalidPatch, operation.path, 0, 0, "%v", err)
			}
			state := &patchFileState{
				path:         operation.path,
				after:        []byte(operation.content),
				afterExisted: true,
				afterMode:    0o644,
			}
			result := mutationResult("add_file", operation.path, nil, operation.content, 0)
			result.Patch = strings.Replace(result.Patch, "--- a/"+operation.path, "--- /dev/null", 1)
			prepared = append(prepared, preparedPatchOperation{operation: operation, states: []*patchFileState{state}, result: result})
			states = append(states, state)

		case patchOperationDelete:
			if !existed {
				return nil, nil, newPatchError(PatchErrorTargetNotFound, operation.path, 0, 0, "Delete File target does not exist")
			}
			if !validTextContent(string(before)) {
				return nil, nil, newPatchError(PatchErrorInvalidPatch, operation.path, 0, 0, "Delete File target must be UTF-8 text without NUL bytes")
			}
			state := &patchFileState{
				path:          operation.path,
				before:        before,
				beforeExisted: true,
				beforeMode:    mode,
			}
			result := mutationResult("delete_file", operation.path, before, "", 0)
			result.Patch = strings.Replace(result.Patch, "+++ b/"+operation.path, "+++ /dev/null", 1)
			prepared = append(prepared, preparedPatchOperation{operation: operation, states: []*patchFileState{state}, result: result})
			states = append(states, state)

		case patchOperationUpdate:
			if !existed {
				return nil, nil, newPatchError(PatchErrorTargetNotFound, operation.path, 0, 0, "Update File target does not exist")
			}
			if len(before) > MaxWriteBytes {
				return nil, nil, newPatchError(PatchErrorInvalidPatch, operation.path, 0, 0, "file is too large to patch: %d > %d bytes", len(before), MaxWriteBytes)
			}
			if !validTextContent(string(before)) {
				return nil, nil, newPatchError(PatchErrorInvalidPatch, operation.path, 0, 0, "Update File target must be UTF-8 text without NUL bytes")
			}
			next, changedHunks, err := applyPatchChunks(operation.path, string(before), operation.chunks)
			if err != nil {
				return nil, nil, err
			}
			resultPath := operation.path
			if operation.movePath != "" {
				resultPath = operation.movePath
			}
			if err := validateMutationContent(resultPath, next); err != nil {
				return nil, nil, newPatchError(PatchErrorInvalidPatch, resultPath, 0, 0, "%v", err)
			}
			if operation.movePath == "" && bytes.Equal(before, []byte(next)) {
				return nil, nil, newPatchError(PatchErrorNoChanges, operation.path, 0, 0, "Update File made no changes")
			}
			if operation.movePath == "" {
				state := &patchFileState{
					path:          operation.path,
					before:        before,
					beforeExisted: true,
					after:         []byte(next),
					afterExisted:  true,
					beforeMode:    mode,
					afterMode:     mode,
				}
				result := mutationResult("update_file", operation.path, before, next, changedHunks)
				prepared = append(prepared, preparedPatchOperation{operation: operation, states: []*patchFileState{state}, result: result})
				states = append(states, state)
				continue
			}
			moveBefore, moveExisted, _, err := s.readPatchTarget(ctx, scope, operation.movePath)
			if err != nil {
				return nil, nil, patchTargetError(ctx, operation.movePath, err)
			}
			if moveExisted {
				return nil, nil, newPatchError(PatchErrorTargetExists, operation.movePath, 0, 0, "Move to target already exists")
			}
			sourceState := &patchFileState{
				path:          operation.path,
				before:        before,
				beforeExisted: true,
				beforeMode:    mode,
			}
			destinationState := &patchFileState{
				path:          operation.movePath,
				before:        moveBefore,
				beforeExisted: false,
				after:         []byte(next),
				afterExisted:  true,
				afterMode:     mode,
			}
			result := mutationResult("move_file", operation.movePath, before, next, changedHunks)
			result.PreviousPath = operation.path
			result.Patch = strings.Replace(result.Patch, "--- a/"+operation.movePath, "--- a/"+operation.path, 1)
			// Materialize the destination before deleting the source. If source
			// removal fails, rollback removes the new destination.
			opStates := []*patchFileState{destinationState, sourceState}
			prepared = append(prepared, preparedPatchOperation{operation: operation, states: opStates, result: result})
			states = append(states, opStates...)
		}
	}
	return prepared, states, nil
}

func patchTargetError(ctx context.Context, filePath string, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	var patchErr *PatchError
	if errors.As(err, &patchErr) {
		return patchErr
	}
	return newPatchError(PatchErrorWorkspaceConflict, filePath, 0, 0, "workspace target is not safely accessible: %v", err)
}

func (s *FileStore) readPatchTarget(ctx context.Context, scope Scope, clean string) ([]byte, bool, fs.FileMode, error) {
	content, existed, err := s.readMutationTarget(ctx, scope, clean)
	if err != nil || !existed {
		return content, existed, 0, err
	}
	dir, err := s.scopeDir(scope)
	if err != nil {
		return nil, false, 0, err
	}
	info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(clean)))
	if err != nil {
		return nil, false, 0, fmt.Errorf("stat %q: %w", clean, err)
	}
	return content, true, info.Mode().Perm(), nil
}

func (s *FileStore) prepareUnifiedPatchSnapshots(ctx context.Context, scope Scope, snapshotID string, states []*patchFileState) error {
	prepared := make([]*patchFileState, 0, len(states))
	for _, state := range states {
		if err := s.prepareSnapshotFileWithModes(
			ctx,
			scope,
			snapshotID,
			state.path,
			state.before,
			state.beforeExisted,
			state.beforeMode,
			state.after,
			state.afterExisted,
			state.afterMode,
		); err != nil {
			s.resetPatchSnapshotStates(scope, snapshotID, prepared)
			return err
		}
		prepared = append(prepared, state)
	}
	return nil
}

func (s *FileStore) verifyPatchBaselines(ctx context.Context, scope Scope, states []*patchFileState) error {
	for _, state := range states {
		current, existed, err := s.readMutationTarget(ctx, scope, state.path)
		if err != nil {
			return patchTargetError(ctx, state.path, err)
		}
		if existed != state.beforeExisted || !bytes.Equal(current, state.before) {
			return withPatchErrorContext(
				newPatchError(PatchErrorWorkspaceConflict, state.path, 0, 0, "workspace changed after patch preflight; no patch operations were applied"),
				string(state.before),
				string(current),
			)
		}
	}
	return nil
}

func (s *FileStore) applyPatchFileState(ctx context.Context, scope Scope, state *patchFileState) error {
	if !state.afterExisted {
		return s.restoreFileState(ctx, scope, state.path, nil, false)
	}
	return s.writePatchFile(ctx, scope, state.path, state.after, state.afterMode, !state.beforeExisted)
}

func (s *FileStore) writePatchFile(ctx context.Context, scope Scope, clean string, content []byte, mode fs.FileMode, createOnly bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.scopeDir(scope)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(clean))
	if err := ensureWithin(dir, target); err != nil {
		return err
	}
	if err := mkdirAllForFile(dir, clean); err != nil {
		return fmt.Errorf("create parent directory for %q: %w", clean, err)
	}
	if err := rejectSymlink(target, clean); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	err = writeFileAtomically(filepath.Dir(target), target, content, mode, createOnly)
	if errors.Is(err, fs.ErrExist) {
		return newPatchError(PatchErrorWorkspaceConflict, clean, 0, 0, "target appeared after patch preflight")
	}
	if err != nil {
		return fmt.Errorf("write %q: %w", clean, err)
	}
	return nil
}

func (s *FileStore) rollbackUnifiedPatch(
	ctx context.Context,
	scope Scope,
	snapshotID string,
	allStates []*patchFileState,
	applied []*patchFileState,
	failed *patchFileState,
	applyErr error,
) (MutationResult, error) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	rollbackErrs := []error{}
	for index := len(applied) - 1; index >= 0; index-- {
		state := applied[index]
		if err := s.restorePatchFileState(rollbackCtx, scope, state); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("roll back %q: %w", state.path, err))
		}
	}
	actual := s.currentPatchDeltas(rollbackCtx, scope, allStates)
	s.resetPatchSnapshotStates(scope, snapshotID, allStates)
	patchErr := newPatchError(PatchErrorApplyFailed, failed.path, 0, 0, "patch application failed: %v", applyErr)
	patchErr.ActualChanges = append([]MutationResult(nil), actual...)
	if len(rollbackErrs) > 0 {
		patchErr.Message += "; rollback was incomplete: " + errors.Join(rollbackErrs...).Error()
	}
	return aggregateActualPatchChanges(actual), patchErr
}

func (s *FileStore) restorePatchFileState(ctx context.Context, scope Scope, state *patchFileState) error {
	if !state.beforeExisted {
		return s.restoreFileState(ctx, scope, state.path, nil, false)
	}
	return s.writePatchFile(ctx, scope, state.path, state.before, state.beforeMode, false)
}

func (s *FileStore) currentPatchDeltas(ctx context.Context, scope Scope, states []*patchFileState) []MutationResult {
	actual := []MutationResult{}
	for _, state := range states {
		current, existed, err := s.readMutationTarget(ctx, scope, state.path)
		if err != nil {
			continue
		}
		if existed == state.beforeExisted && bytes.Equal(current, state.before) {
			continue
		}
		result := mutationResult("actual_change", state.path, state.before, string(current), 0)
		if !existed {
			result.Size = 0
		}
		actual = append(actual, result)
	}
	return actual
}

func (s *FileStore) resetPatchSnapshotStates(scope Scope, snapshotID string, states []*patchFileState) {
	if strings.TrimSpace(snapshotID) == "" {
		return
	}
	resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, state := range states {
		current, existed, mode, err := s.readPatchTarget(resetCtx, scope, state.path)
		if err != nil {
			continue
		}
		_ = s.prepareSnapshotFileWithModes(
			resetCtx,
			scope,
			snapshotID,
			state.path,
			state.before,
			state.beforeExisted,
			state.beforeMode,
			current,
			existed,
			mode,
		)
	}
}

func aggregatePatchMutationResults(prepared []preparedPatchOperation) MutationResult {
	files := make([]MutationResult, 0, len(prepared))
	for _, operation := range prepared {
		files = append(files, operation.result)
	}
	return aggregateActualPatchChanges(files)
}

func aggregateActualPatchChanges(files []MutationResult) MutationResult {
	result := MutationResult{Operation: "apply_patch", Files: append([]MutationResult(nil), files...)}
	patches := make([]string, 0, len(files))
	seenPaths := make(map[string]struct{}, len(files)*2)
	for _, file := range files {
		for _, candidate := range []string{file.PreviousPath, file.Path} {
			if candidate == "" {
				continue
			}
			if _, ok := seenPaths[candidate]; ok {
				continue
			}
			seenPaths[candidate] = struct{}{}
			result.Paths = append(result.Paths, candidate)
		}
		result.Size += file.Size
		result.Replacements += file.Replacements
		result.Additions += file.Additions
		result.Deletions += file.Deletions
		if strings.TrimSpace(file.Patch) != "" {
			patches = append(patches, strings.TrimRight(file.Patch, "\n"))
		}
	}
	if len(files) == 1 {
		result.Path = files[0].Path
		result.PreviousPath = files[0].PreviousPath
	}
	result.Patch = strings.Join(patches, "\n")
	if result.Patch != "" {
		result.Patch += "\n"
	}
	return result
}
