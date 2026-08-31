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
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/faroshq/provider-app-studio/store"
)

func TestParseAttachmentDraftRejectsPermanentUpload(t *testing.T) {
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set("X-Faros-Attachment-Draft", "false")
	if _, err := parseAttachmentDraft(request); err == nil || !strings.Contains(err.Error(), "permanent attachment upload is not supported") {
		t.Fatalf("permanent upload error = %v", err)
	}
}

type projectAssistantAttachmentReaderTestDouble struct {
	contents map[string][]byte
}

func (r projectAssistantAttachmentReaderTestDouble) ReadAttachment(_ context.Context, _ store.Scope, receipt projectAssistantAttachmentReceipt, _ string, offset int64, limit int) (projectAssistantAttachmentRead, error) {
	content := r.contents[receipt.ID]
	if offset >= int64(len(content)) {
		return projectAssistantAttachmentRead{Complete: true}, nil
	}
	end := min(int64(len(content)), offset+int64(limit))
	return projectAssistantAttachmentRead{
		Content:  append([]byte(nil), content[offset:end]...),
		Complete: end == int64(len(content)),
	}, nil
}

func attachmentReceiptForTest(id, filename, contentType string, content []byte) projectAssistantAttachmentReceipt {
	sum := sha256.Sum256(content)
	return projectAssistantAttachmentReceipt{
		ID: id, Filename: filename, ContentType: contentType, SizeBytes: int64(len(content)),
		SHA256: hex.EncodeToString(sum[:]), CreatedAt: time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC),
	}
}

func TestProjectAssistantAttachmentReceiptCanonicalizesPortalShape(t *testing.T) {
	var part projectAssistantContentPart
	if err := json.Unmarshal([]byte(`{"type":"attachment","attachment":{"id":"upload-1","filename":"notes.md","contentType":"TEXT/MARKDOWN","sizeBytes":5,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","createdAt":"2026-08-31T00:00:00Z"}}`), &part); err != nil {
		t.Fatalf("decode attachment: %v", err)
	}
	parts, _, derived, err := normalizeProjectAssistantContentParts([]projectAssistantContentPart{part}, nil, nil)
	if err != nil {
		t.Fatalf("normalize attachment: %v", err)
	}
	if len(parts) != 1 || parts[0].Attachment == nil || parts[0].Attachment.ContentType != "text/markdown" || derived != "[@attachment:upload-1]" {
		t.Fatalf("canonical attachment = %#v, derived=%q", parts, derived)
	}
	encoded, err := json.Marshal(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "filename", "contentType", "sizeBytes", "sha256", "createdAt"} {
		if !strings.Contains(string(encoded), `"`+key+`"`) {
			t.Fatalf("canonical receipt omitted %q: %s", key, encoded)
		}
	}
	if strings.Contains(string(encoded), "decoded") || strings.Contains(string(encoded), "sizeSet") {
		t.Fatalf("private receipt state leaked: %s", encoded)
	}
	if err := json.Unmarshal([]byte(`{"type":"attachment","attachment":{"id":"upload-1","filename":"x","contentType":"text/plain","sizeBytes":1,"sha256":"a","createdAt":"now","unknown":true}}`), &part); err == nil {
		t.Fatal("unknown attachment receipt field was accepted")
	}
}

func TestProjectAssistantAttachmentMessagesKeepImagesMultimodalAndTextBounded(t *testing.T) {
	image := []byte("PNG bytes")
	text := []byte("small text")
	large := []byte(strings.Repeat("x", projectAssistantAttachmentInlineTextMaxBytes+1))
	parts := []projectAssistantContentPart{
		projectAssistantContentPartAttachment(attachmentReceiptForTest("image-1", "screen.png", "image/png", image)),
		projectAssistantContentPartAttachment(attachmentReceiptForTest("text-1", "notes.txt", "text/plain", text)),
		projectAssistantContentPartAttachment(attachmentReceiptForTest("large-1", "large.txt", "text/plain", large)),
	}
	state := newProjectEinoAssistantRunState()
	state.SetContentParts(parts)
	req := projectAssistantRunRequest{
		AttachmentReader: projectAssistantAttachmentReaderTestDouble{contents: map[string][]byte{
			"image-1": image, "text-1": text, "large-1": large,
		}},
	}
	messages, err := projectAssistantAttachmentMessages(context.Background(), req, state)
	if err != nil {
		t.Fatalf("build attachment messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("attachment message count = %d, want image + inline text", len(messages))
	}
	if messages[0].Role != schema.User || len(messages[0].UserInputMultiContent) != 2 || messages[0].UserInputMultiContent[1].Image == nil {
		t.Fatalf("image message = %#v", messages[0])
	}
	if got := messages[0].UserInputMultiContent[1].Image.Base64Data; got == nil || *got != base64.StdEncoding.EncodeToString(image) {
		t.Fatalf("image bytes = %#v", got)
	}
	if !strings.Contains(messages[1].Content, string(text)) || strings.Contains(messages[1].Content, string(large)) {
		t.Fatalf("inline text projection = %q", messages[1].Content)
	}
	if projectEinoAssistantAttachmentMessage(messages[0]) == false || projectEinoAssistantAttachmentMessage(messages[1]) == false {
		t.Fatal("synthetic attachment messages were not marked")
	}
	if projected := projectEinoMessagesToChat(messages); len(projected) != 0 {
		t.Fatalf("synthetic attachment messages entered durable projection: %#v", projected)
	}
	// A second sample (the same shape used after a checkpoint/resume) gets a
	// fresh image message, while the first synthetic message is not retained in
	// the durable model history.
	second, err := projectAssistantAttachmentMessages(context.Background(), req, state)
	if err != nil || len(second) != 2 || len(second[0].UserInputMultiContent) != 2 {
		t.Fatalf("resume attachment messages = %#v, err=%v", second, err)
	}
}

func TestProjectAssistantReadAttachmentToolReadsOnlySelectedBoundedText(t *testing.T) {
	// This is intentionally larger than the inline threshold: the model must
	// use read_attachment for large text, while the tool still returns only the
	// requested bounded range.
	content := []byte(strings.Repeat("0123456789", 4096))
	receipt := attachmentReceiptForTest("text-1", "notes.txt", "text/plain", content)
	state := newProjectEinoAssistantRunState()
	state.SetContentParts([]projectAssistantContentPart{projectAssistantContentPartAttachment(receipt)})
	result, err := projectAssistantReadAttachmentTool(context.Background(), projectAssistantToolCallRequest{
		AttachmentReader: projectAssistantAttachmentReaderTestDouble{contents: map[string][]byte{"text-1": content}},
		AttachmentScope:  store.Scope{OrgUUID: "org", WorkspaceUUID: "workspace"},
		RunState:         state,
		Arguments:        map[string]any{"attachmentID": "text-1", "offset": float64(2), "limit": float64(4)},
	})
	if err != nil {
		t.Fatalf("read attachment: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["content"] != "2345" || envelope["offset"] != float64(2) || envelope["complete"] != false {
		t.Fatalf("attachment read envelope = %#v", envelope)
	}
	_, err = projectAssistantReadAttachmentTool(context.Background(), projectAssistantToolCallRequest{
		AttachmentReader: projectAssistantAttachmentReaderTestDouble{contents: map[string][]byte{"text-1": content}},
		RunState:         newProjectEinoAssistantRunState(),
		Arguments:        map[string]any{"attachmentID": "text-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("unselected attachment error = %v", err)
	}
}

func TestProjectAssistantReadAttachmentToolAlignsMultibyteRange(t *testing.T) {
	content := []byte("a€b")
	receipt := attachmentReceiptForTest("text-utf8", "notes.txt", "text/plain", content)
	state := newProjectEinoAssistantRunState()
	state.SetContentParts([]projectAssistantContentPart{projectAssistantContentPartAttachment(receipt)})
	result, err := projectAssistantReadAttachmentTool(context.Background(), projectAssistantToolCallRequest{
		AttachmentReader: projectAssistantAttachmentReaderTestDouble{contents: map[string][]byte{"text-utf8": content}},
		RunState:         state,
		Arguments:        map[string]any{"attachmentID": "text-utf8", "offset": float64(1), "limit": float64(1)},
	})
	if err != nil {
		t.Fatalf("read multibyte attachment range: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(result), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["content"] != "€" || envelope["offset"] != float64(1) || envelope["nextOffset"] != float64(4) || envelope["complete"] != false {
		t.Fatalf("multibyte attachment range = %#v", envelope)
	}
}

func TestProjectAssistantReadAttachmentContentIsTransient(t *testing.T) {
	raw := `{"attachmentID":"text-1","filename":"notes.txt","contentType":"text/plain","sizeBytes":18,"offset":0,"nextOffset":18,"content":"private plan text","complete":true}`
	state := newProjectEinoAssistantRunState()
	placeholder := state.RegisterTransientToolResult(projectToolReadAttachment, raw)
	if strings.Contains(placeholder, "private plan text") || !strings.Contains(placeholder, "contentSHA256") {
		t.Fatalf("persistent placeholder leaked content: %s", placeholder)
	}
	expanded := state.ExpandTransientToolMessages([]*schema.Message{{
		Role: schema.Tool, ToolName: projectToolReadAttachment, ToolCallID: "call-1", Content: placeholder,
	}})
	if len(expanded) != 1 || expanded[0].Content != raw {
		t.Fatalf("immediate model input did not recover transient content: %#v", expanded)
	}
	state.RecordModelInput([]chatMessage{{Role: "tool", Name: projectToolReadAttachment, ToolCallID: "call-1", Content: raw}})
	checkpoint := state.CheckpointState()
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private plan text") || !strings.Contains(string(encoded), "contentSHA256") {
		t.Fatalf("checkpoint persisted raw attachment content: %s", encoded)
	}
}

func TestProjectAssistantAttachmentContentPartsEnforceTurnBounds(t *testing.T) {
	now := time.Now().UTC()
	digest := strings.Repeat("a", sha256.Size*2)
	parts := make([]projectAssistantContentPart, 0, projectAssistantMaxAttachmentsPerTurn+1)
	for index := 0; index <= projectAssistantMaxAttachmentsPerTurn; index++ {
		parts = append(parts, projectAssistantContentPartAttachment(projectAssistantAttachmentReceipt{
			ID: "att-" + string(rune('a'+index)), Filename: "screen.png", ContentType: "image/png",
			SizeBytes: 1, SHA256: digest, CreatedAt: now,
		}))
	}
	if _, _, _, err := normalizeProjectAssistantContentParts(parts, nil, nil); err == nil || !strings.Contains(err.Error(), "at most 8 attachments") {
		t.Fatalf("attachment count validation error = %v", err)
	}
	parts = parts[:3]
	for index := range parts {
		parts[index].Attachment.SizeBytes = 7 << 20
	}
	if _, _, _, err := normalizeProjectAssistantContentParts(parts, nil, nil); err == nil || !strings.Contains(err.Error(), "total at most") {
		t.Fatalf("attachment aggregate validation error = %v", err)
	}
}
