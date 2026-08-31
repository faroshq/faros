// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func attachmentTestScope() Scope {
	return Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "app", ProjectUID: "uid"}
}

func attachmentTestBlob() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
}

func attachmentTestRecord(now time.Time, draft bool, expiresAt *time.Time) Attachment {
	data := attachmentTestBlob()
	digest := sha256.Sum256(data)
	return Attachment{
		ID: "att-1", ActorID: "alice", Filename: "screen.png", ContentType: "image/png",
		SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Draft: draft,
		CreatedAt: now, ExpiresAt: expiresAt, Data: data,
	}
}

func TestMemoryAttachmentLifecycleAndReceiptAdmission(t *testing.T) {
	ctx := context.Background()
	scope := attachmentTestScope()
	now := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	s := NewMemoryStore()
	want := attachmentTestRecord(now, false, nil)
	created, err := s.CreateAttachment(ctx, scope, want)
	if err != nil {
		t.Fatal(err)
	}
	created.Data[0] = 'x'
	got, err := s.GetAttachment(ctx, scope, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Data[0] != 0x89 || got.ProjectName != scope.ProjectName || got.ProjectUID != scope.ProjectUID {
		t.Fatalf("attachment = %#v", got)
	}
	verified, err := VerifyAttachmentReceipt(ctx, s, scope, AttachmentReceipt{
		ID: want.ID, Filename: want.Filename, ContentType: want.ContentType,
		SizeBytes: want.SizeBytes, SHA256: want.SHA256, CreatedAt: want.CreatedAt,
	}, "alice")
	if err != nil || verified.ID != want.ID {
		t.Fatalf("verify receipt = %#v, %v", verified, err)
	}
	if _, err := VerifyAttachmentReceipt(ctx, s, scope, AttachmentReceipt{
		ID: want.ID, Filename: "changed.png", ContentType: want.ContentType,
		SizeBytes: want.SizeBytes, SHA256: want.SHA256,
	}, "alice"); !errors.Is(err, ErrAttachmentReceiptMismatch) {
		t.Fatalf("mismatched receipt error = %v", err)
	}
	if _, err := VerifyAttachmentReceipt(ctx, s, Scope{OrgUUID: "other", WorkspaceUUID: "workspace", ProjectName: "app", ProjectUID: "uid"}, AttachmentReceipt{ID: want.ID}, "alice"); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("cross-scope receipt error = %v", err)
	}
	listed, err := s.ListAttachments(ctx, scope)
	if err != nil || len(listed) != 1 || listed[0].ID != want.ID {
		t.Fatalf("listed attachments = %#v, %v", listed, err)
	}
	if err := s.DeleteAttachment(ctx, scope, want.ID, "bob"); !errors.Is(err, ErrAttachmentForbidden) {
		t.Fatalf("wrong actor delete error = %v", err)
	}
	if err := s.DeleteAttachment(ctx, scope, want.ID, "alice"); !errors.Is(err, ErrAttachmentImmutable) {
		t.Fatalf("retained owner delete error = %v", err)
	}
	draftExpires := now.Add(time.Hour)
	draft := attachmentTestRecord(now, true, &draftExpires)
	draft.ID = "att-draft-delete"
	if _, err := s.CreateAttachment(ctx, scope, draft); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAttachment(ctx, scope, draft.ID, "alice"); err != nil {
		t.Fatalf("draft owner delete: %v", err)
	}
	if _, err := s.GetAttachment(ctx, scope, draft.ID); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("get after draft delete error = %v", err)
	}
}

func TestMemoryAttachmentDraftExpiry(t *testing.T) {
	ctx := context.Background()
	scope := attachmentTestScope()
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	s := NewMemoryStore()
	if _, err := s.CreateAttachment(ctx, scope, attachmentTestRecord(now.Add(-time.Hour), true, &expired)); err != nil {
		t.Fatal(err)
	}
	if listed, err := s.ListAttachments(ctx, scope); err != nil || len(listed) != 0 {
		t.Fatalf("expired draft list = %#v, %v", listed, err)
	}
	if deleted, err := s.DeleteExpiredAttachments(ctx, now); err != nil || deleted != 0 {
		t.Fatalf("expired draft cleanup after lazy delete = %d, %v", deleted, err)
	}
	data := attachmentTestBlob()
	digest := sha256.Sum256(data)
	future := now.Add(time.Hour)
	if _, err := s.CreateAttachment(ctx, scope, Attachment{ID: "att-2", ActorID: "alice", Filename: "future.png", ContentType: "image/png", SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Draft: true, CreatedAt: now, ExpiresAt: &future, Data: data}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := s.DeleteExpiredAttachments(ctx, future); err != nil || deleted != 1 {
		t.Fatalf("expired draft cleanup = %d, %v", deleted, err)
	}
	bindExpiry := now.Add(time.Hour)
	draft := attachmentTestRecord(now, true, &bindExpiry)
	draft.ID = "att-bind"
	created, err := s.CreateAttachment(ctx, scope, draft)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("created timestamp was not canonicalized to PostgreSQL precision: %s", created.CreatedAt)
	}
	receipt := AttachmentReceipt{ID: created.ID, Filename: created.Filename, ContentType: created.ContentType, SizeBytes: created.SizeBytes, SHA256: created.SHA256, CreatedAt: created.CreatedAt}
	bound, err := s.BindAttachment(ctx, scope, receipt, "alice")
	if err != nil || bound.Draft || bound.ExpiresAt != nil {
		t.Fatalf("bound attachment = %#v, %v", bound, err)
	}
	again, err := s.BindAttachment(ctx, scope, receipt, "alice")
	if err != nil || again.Draft {
		t.Fatalf("idempotent bind = %#v, %v", again, err)
	}
	if err := s.DeleteProjectMessages(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if listed, err := s.ListAttachments(ctx, scope); err != nil || len(listed) != 0 {
		t.Fatalf("attachments after project deletion = %#v, %v", listed, err)
	}
}

func TestEncryptedStoreEncryptsAttachmentBytesAndVerifiesReceipt(t *testing.T) {
	ctx := context.Background()
	base := NewMemoryStore()
	wrapped, err := NewEncryptedStore(base, []EncryptionKey{{ID: "primary", Value: []byte("0123456789abcdef0123456789abcdef")}})
	if err != nil {
		t.Fatal(err)
	}
	attachments, ok := wrapped.(AttachmentStore)
	if !ok {
		t.Fatal("encrypted store does not preserve attachment capability")
	}
	scope := attachmentTestScope()
	expiresAt := time.Now().UTC().Add(time.Hour)
	want := attachmentTestRecord(time.Now().UTC(), true, &expiresAt)
	created, err := attachments.CreateAttachment(ctx, scope, want)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base.GetAttachment(ctx, scope, want.ID)
	if err != nil || !raw.DataEncrypted || raw.DataKeyID != "primary" || string(raw.Data) == string(want.Data) {
		t.Fatalf("raw attachment was not encrypted: %#v, %v", raw, err)
	}
	got, err := attachments.GetAttachment(ctx, scope, want.ID)
	if err != nil || string(got.Data) != string(want.Data) || got.DataEncrypted {
		t.Fatalf("decrypted attachment = %#v, %v", got, err)
	}
	receipt := AttachmentReceipt{ID: created.ID, Filename: created.Filename, ContentType: created.ContentType, SizeBytes: created.SizeBytes, SHA256: created.SHA256, CreatedAt: created.CreatedAt}
	if _, err := VerifyAttachmentReceipt(ctx, attachments, scope, receipt, "alice"); err != nil {
		t.Fatalf("verify encrypted receipt: %v", err)
	}
	bound, err := attachments.BindAttachment(ctx, scope, receipt, "alice")
	if err != nil || bound.Draft || bound.ExpiresAt != nil {
		t.Fatalf("bind encrypted receipt = %#v, %v", bound, err)
	}
}

func TestValidateAttachmentContentAllowlist(t *testing.T) {
	data := attachmentTestBlob()
	if err := ValidateAttachmentContent("screen.png", "image/png", data); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAttachmentContent("notes.md", "text/markdown", []byte("# hello\n")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, filename, contentType string
		data                        []byte
	}{
		{"wrong magic", "screen.png", "image/png", []byte("not png")},
		{"wrong extension", "notes.json", "text/plain", []byte("hello")},
		{"invalid utf8", "notes.txt", "text/plain", []byte{0xff}},
		{"path traversal", "../notes.txt", "text/plain", []byte("hello")},
		{"unsupported type", "movie.mp4", "video/mp4", []byte("video")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateAttachmentContent(test.filename, test.contentType, test.data); err == nil {
				t.Fatal("validation unexpectedly succeeded")
			}
		})
	}
}
