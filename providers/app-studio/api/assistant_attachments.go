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

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
)

// Multipart adds room for MIME headers and form boundaries while the stored
// bytes remain bounded by AttachmentMaxBytes. The MaxBytesReader is still
// required because a multipart parser's memory limit is not a body limit.
const attachmentMultipartOverhead = 256 << 10

type attachmentReceiptResponse struct {
	ID          string     `json:"id"`
	Filename    string     `json:"filename"`
	ContentType string     `json:"contentType"`
	SizeBytes   int64      `json:"sizeBytes"`
	SHA256      string     `json:"sha256"`
	CreatedAt   time.Time  `json:"createdAt"`
	Draft       bool       `json:"draft,omitempty"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

func attachmentReceipt(attachment store.Attachment) attachmentReceiptResponse {
	return attachmentReceiptResponse{
		ID:          attachment.ID,
		Filename:    attachment.Filename,
		ContentType: attachment.ContentType,
		SizeBytes:   attachment.SizeBytes,
		SHA256:      attachment.SHA256,
		CreatedAt:   attachment.CreatedAt.UTC(),
		Draft:       attachment.Draft,
		ExpiresAt:   attachment.ExpiresAt,
	}
}

func (s *Server) projectAttachmentStore(w http.ResponseWriter) (store.AttachmentStore, bool) {
	if s != nil && s.attachments != nil {
		return s.attachments, true
	}
	if s != nil && s.store != nil {
		if attachmentStore, ok := s.store.(store.AttachmentStore); ok {
			return attachmentStore, true
		}
	}
	writeStatus(w, http.StatusNotImplemented, "NotImplemented", "project attachment store is not configured on this provider")
	return nil, false
}

// bindProjectAssistantAttachment is the turn-admission adapter. The caller
// must convert the HTTP receipt into store.AttachmentReceipt, including its
// CreatedAt value, before invoking this method. Binding verifies every
// immutable field under the authenticated actor and atomically promotes a
// draft to retained storage; repeated identical binds are safe.
func (s *Server) bindProjectAssistantAttachment(ctx context.Context, id identity, project *aiv1alpha1.Project, receipt store.AttachmentReceipt) (store.Attachment, error) {
	if s == nil || project == nil {
		return store.Attachment{}, store.ErrAttachmentNotFound
	}
	attachmentStore := s.attachments
	if attachmentStore == nil && s.store != nil {
		attachmentStore, _ = s.store.(store.AttachmentStore)
	}
	if attachmentStore == nil {
		return store.Attachment{}, fmt.Errorf("project attachment store is not configured")
	}
	return attachmentStore.BindAttachment(ctx, projectMessageScope(id.orgUUID, id.workspaceUUID, project), receipt, id.user)
}

// bindProjectAssistantContentPartAttachments is the durable admission boundary
// for attachment-bearing turns. It verifies the complete set before promoting
// any draft, so a stale or forged receipt cannot leave a partially-bound turn.
func (s *Server) bindProjectAssistantContentPartAttachments(ctx context.Context, id identity, project *aiv1alpha1.Project, parts []projectAssistantContentPart) error {
	if s == nil || project == nil {
		return newValidationError("project attachments are unavailable")
	}
	attachmentStore := s.attachments
	if attachmentStore == nil && s.store != nil {
		attachmentStore, _ = s.store.(store.AttachmentStore)
	}
	receipts := make([]store.AttachmentReceipt, 0)
	for _, part := range parts {
		if part.Type != projectAssistantContentPartAttachmentType || part.Attachment == nil {
			continue
		}
		receipts = append(receipts, store.AttachmentReceipt{
			ID:          part.Attachment.ID,
			Filename:    part.Attachment.Filename,
			ContentType: part.Attachment.ContentType,
			SizeBytes:   part.Attachment.SizeBytes,
			SHA256:      part.Attachment.SHA256,
			CreatedAt:   part.Attachment.CreatedAt,
		})
	}
	if len(receipts) == 0 {
		return nil
	}
	if attachmentStore == nil {
		return fmt.Errorf("project attachment store is not configured")
	}
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	for _, receipt := range receipts {
		if _, err := store.VerifyAttachmentReceipt(ctx, attachmentStore, scope, receipt, id.user); err != nil {
			return newValidationError("an attachment is unavailable or does not match its upload receipt")
		}
	}
	for _, receipt := range receipts {
		if _, err := attachmentStore.BindAttachment(ctx, scope, receipt, id.user); err != nil {
			return fmt.Errorf("bind project attachment: %w", err)
		}
	}
	return nil
}

// projectAssistantAttachmentReader returns the bounded model adapter for the
// same scoped store used by upload and turn admission. Keeping construction
// here prevents a future worker from accidentally using an unscoped backend.
func (s *Server) projectAssistantAttachmentReader() projectAssistantAttachmentReader {
	if s == nil {
		return nil
	}
	attachmentStore := s.attachments
	if attachmentStore == nil && s.store != nil {
		attachmentStore, _ = s.store.(store.AttachmentStore)
	}
	return newProjectAssistantStoreAttachmentReader(attachmentStore)
}

func (s *Server) listProjectAssistantAttachments(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	attachmentStore, ok := s.projectAttachmentStore(w)
	if !ok {
		return
	}
	attachments, err := attachmentStore.ListAttachments(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project))
	if err != nil {
		writeStatus(w, http.StatusInternalServerError, "InternalError", "list project attachments: "+err.Error())
		return
	}
	items := make([]attachmentReceiptResponse, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment.ActorID != id.user {
			continue
		}
		items = append(items, attachmentReceipt(attachment))
	}
	writeJSON(w, http.StatusOK, ListResponse[attachmentReceiptResponse]{Items: items})
}

func (s *Server) createProjectAssistantAttachment(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	if id.user == "" {
		writeStatus(w, http.StatusUnauthorized, "Unauthorized", "caller identity missing — the hub did not provide X-Faros-User")
		return
	}
	attachmentStore, ok := s.projectAttachmentStore(w)
	if !ok {
		return
	}

	maxRequestBytes := int64(store.AttachmentMaxBytes + attachmentMultipartOverhead)
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(maxRequestBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeStatus(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLarge", "attachment request exceeds the upload limit")
		} else {
			writeStatus(w, http.StatusBadRequest, "BadRequest", "invalid multipart attachment request: "+err.Error())
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", "multipart field file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, store.AttachmentMaxBytes+1))
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", "read attachment: "+err.Error())
		return
	}
	if len(data) > store.AttachmentMaxBytes {
		writeStatus(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLarge", fmt.Sprintf("attachment exceeds the %d-byte limit", store.AttachmentMaxBytes))
		return
	}
	contentType := detectProjectAttachmentContentType(header.Filename, header.Header.Get("Content-Type"), data)
	contentType, err = store.NormalizeAttachmentContentType(contentType)
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	if err := store.ValidateAttachmentContent(header.Filename, contentType, data); err != nil {
		if strings.Contains(err.Error(), "maximum") {
			writeStatus(w, http.StatusRequestEntityTooLarge, "RequestEntityTooLarge", err.Error())
		} else {
			writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		}
		return
	}
	digest := sha256.Sum256(data)
	now := time.Now().UTC()
	draft, err := parseAttachmentDraft(r)
	if err != nil {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	var expiresAt *time.Time
	if draft {
		expires := now.Add(s.attachmentRetention())
		expiresAt = &expires
	}
	created, err := attachmentStore.CreateAttachment(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), store.Attachment{
		ID:          "att-" + uuid.NewString(),
		ActorID:     id.user,
		Filename:    header.Filename,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		SHA256:      hex.EncodeToString(digest[:]),
		Draft:       draft,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		Data:        data,
	})
	if err != nil {
		if errors.Is(err, store.ErrAttachmentConflict) {
			writeStatus(w, http.StatusConflict, "Conflict", err.Error())
		} else {
			writeStatus(w, http.StatusInternalServerError, "InternalError", "create project attachment: "+err.Error())
		}
		return
	}
	w.Header().Set("Location", "/api/projects/"+mux.Vars(r)["project"]+"/assistant/attachments/"+created.ID)
	writeJSON(w, http.StatusCreated, attachmentReceipt(created))
}

func detectProjectAttachmentContentType(filename, declared string, data []byte) string {
	declared = strings.TrimSpace(declared)
	if declared != "" {
		if parsed, _, err := mime.ParseMediaType(declared); err == nil && parsed != "application/octet-stream" && parsed != "binary/octet-stream" {
			return declared
		}
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if detected := http.DetectContentType(data); detected != "application/octet-stream" {
		return detected
	}
	switch strings.ToLower(path.Ext(strings.TrimSpace(filename))) {
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	default:
		return declared
	}
}

func parseAttachmentDraft(r *http.Request) (bool, error) {
	raw := strings.TrimSpace(r.FormValue("draft"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("X-Faros-Attachment-Draft"))
	}
	if raw == "" {
		// Composer uploads are provisional until the turn admission path binds
		// the complete receipt. This keeps abandoned uploads bounded while
		// preserving a deliberate false value for non-composer callers.
		return true, nil
	}
	draft, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("draft must be a boolean")
	}
	return draft, nil
}

func (s *Server) getProjectAssistantAttachment(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	attachmentStore, ok := s.projectAttachmentStore(w)
	if !ok {
		return
	}
	attachment, err := attachmentStore.GetAttachment(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), mux.Vars(r)["attachment"])
	if err != nil {
		if errors.Is(err, store.ErrAttachmentNotFound) {
			writeStatus(w, http.StatusNotFound, "NotFound", "attachment not found")
		} else {
			writeStatus(w, http.StatusInternalServerError, "InternalError", "get project attachment: "+err.Error())
		}
		return
	}
	if attachment.ActorID != id.user {
		writeStatus(w, http.StatusNotFound, "NotFound", "attachment not found")
		return
	}
	w.Header().Set("Content-Type", attachment.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(attachment.SizeBytes, 10))
	w.Header().Set("ETag", `"`+attachment.SHA256+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(attachment.Data)
}

func (s *Server) deleteProjectAssistantAttachment(w http.ResponseWriter, r *http.Request) {
	_, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	if id.user == "" {
		writeStatus(w, http.StatusUnauthorized, "Unauthorized", "caller identity missing — the hub did not provide X-Faros-User")
		return
	}
	attachmentStore, ok := s.projectAttachmentStore(w)
	if !ok {
		return
	}
	err := attachmentStore.DeleteAttachment(r.Context(), projectMessageScope(id.orgUUID, id.workspaceUUID, project), mux.Vars(r)["attachment"], id.user)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrAttachmentNotFound):
		writeStatus(w, http.StatusNotFound, "NotFound", "attachment not found")
	case errors.Is(err, store.ErrAttachmentForbidden):
		writeStatus(w, http.StatusForbidden, "Forbidden", "attachment belongs to another caller")
	case errors.Is(err, store.ErrAttachmentImmutable):
		writeStatus(w, http.StatusConflict, "Conflict", "attachment cannot be deleted")
	default:
		writeStatus(w, http.StatusInternalServerError, "InternalError", "delete project attachment: "+err.Error())
	}
}
