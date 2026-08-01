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
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectEinoAssistantModelFirstResponseTimeout = 60 * time.Second
	projectEinoAssistantModelStreamIdleTimeout    = 60 * time.Second
)

type projectEinoAssistantModelTimeoutError struct {
	Code string
}

func (e *projectEinoAssistantModelTimeoutError) Error() string {
	if e != nil && e.Code == "model_stream_idle_timeout" {
		return "model_stream_idle_timeout: assistant model stream produced no new data for 60 seconds"
	}
	return "model_first_response_timeout: assistant model produced no first response for 60 seconds"
}

func (*projectEinoAssistantModelTimeoutError) Timeout() bool   { return true }
func (*projectEinoAssistantModelTimeoutError) Temporary() bool { return true }

type projectEinoAssistantBoundedModel struct {
	einomodel.BaseChatModel

	firstResponseTimeout time.Duration
	streamIdleTimeout    time.Duration
}

func projectEinoAssistantBoundModel(base einomodel.BaseChatModel) einomodel.BaseChatModel {
	if base == nil {
		return nil
	}
	return &projectEinoAssistantBoundedModel{
		BaseChatModel:        base,
		firstResponseTimeout: projectEinoAssistantModelFirstResponseTimeout,
		streamIdleTimeout:    projectEinoAssistantModelStreamIdleTimeout,
	}
}

func (m *projectEinoAssistantBoundedModel) Generate(
	ctx context.Context,
	input []*schema.Message,
	opts ...einomodel.Option,
) (*schema.Message, error) {
	type result struct {
		message *schema.Message
		err     error
	}
	modelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resultCh := make(chan result)
	go func() {
		message, err := m.BaseChatModel.Generate(modelCtx, input, opts...)
		select {
		case resultCh <- result{message: message, err: err}:
		case <-modelCtx.Done():
		}
	}()
	timer := time.NewTimer(m.firstResponseTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, &projectEinoAssistantModelTimeoutError{Code: "model_first_response_timeout"}
	case result := <-resultCh:
		return result.message, result.err
	}
}

func (m *projectEinoAssistantBoundedModel) Stream(
	ctx context.Context,
	input []*schema.Message,
	opts ...einomodel.Option,
) (*schema.StreamReader[*schema.Message], error) {
	type result struct {
		reader *schema.StreamReader[*schema.Message]
		err    error
	}
	started := time.Now()
	modelCtx, cancel := context.WithCancel(ctx)
	resultCh := make(chan result)
	go func() {
		reader, err := m.BaseChatModel.Stream(modelCtx, input, opts...)
		select {
		case resultCh <- result{reader: reader, err: err}:
		case <-modelCtx.Done():
			if reader != nil {
				reader.Close()
			}
		}
	}()
	timer := time.NewTimer(m.firstResponseTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	case <-timer.C:
		cancel()
		return nil, &projectEinoAssistantModelTimeoutError{Code: "model_first_response_timeout"}
	case result := <-resultCh:
		if result.err != nil {
			cancel()
			return nil, result.err
		}
		if result.reader == nil {
			cancel()
			return nil, errors.New("assistant model returned no response stream")
		}
		remaining := m.firstResponseTimeout - time.Since(started)
		return projectEinoAssistantBoundedStream(
			ctx,
			modelCtx,
			cancel,
			result.reader,
			remaining,
			m.streamIdleTimeout,
		), nil
	}
}

func projectEinoAssistantBoundedStream(
	ctx context.Context,
	modelCtx context.Context,
	cancel context.CancelFunc,
	source *schema.StreamReader[*schema.Message],
	firstResponseTimeout time.Duration,
	idleTimeout time.Duration,
) *schema.StreamReader[*schema.Message] {
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer cancel()
		defer source.Close()
		defer writer.Close()
		wait := firstResponseTimeout
		first := true
		for {
			if wait <= 0 {
				writer.Send(nil, &projectEinoAssistantModelTimeoutError{Code: "model_first_response_timeout"})
				return
			}
			type receiveResult struct {
				message *schema.Message
				err     error
			}
			received := make(chan receiveResult)
			go func() {
				message, err := source.Recv()
				select {
				case received <- receiveResult{message: message, err: err}:
				case <-modelCtx.Done():
				}
			}()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				writer.Send(nil, ctx.Err())
				return
			case <-modelCtx.Done():
				timer.Stop()
				writer.Send(nil, modelCtx.Err())
				return
			case <-timer.C:
				code := "model_stream_idle_timeout"
				if first {
					code = "model_first_response_timeout"
				}
				writer.Send(nil, &projectEinoAssistantModelTimeoutError{Code: code})
				return
			case result := <-received:
				timer.Stop()
				if errors.Is(result.err, io.EOF) {
					return
				}
				if result.err != nil {
					writer.Send(nil, result.err)
					return
				}
				if writer.Send(result.message, nil) {
					return
				}
				first = false
				wait = idleTimeout
			}
		}
	}()
	return reader
}

var projectEinoAssistantSerializedCookiePattern = regexp.MustCompile(
	`(?i)\\?["']\b(?:set-cookie|cookie)\b\\?["'][ \t]*:[ \t]*\\?["']`,
)

var projectEinoAssistantSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{
		pattern: regexp.MustCompile(
			`(?i)(\\?["']?\bauthorization\b\\?["']?[ \t]*:[ \t]*\\?["']?(?:bearer|basic)[ \t]+)[^ \t\r\n,;\\"'}]+`,
		),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(\bbearer[ \t]+)[^ \t\r\n,;]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(\b(?:set-cookie|cookie)\b[ \t]*:[ \t]*)[^\r\n]+`),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern: regexp.MustCompile(
			`(?i)(\\?["']?\b(?:api[_-]?key|access[_-]?token|token|secret|password)\b\\?["']?[ \t]*[:=][ \t]*)(?:\\"(?:[^"\\\r\n]|\\.)*\\"|\\'(?:[^'\\\r\n]|\\.)*\\'|"[^"\r\n]*"|'[^'\r\n]*'|[^ \t\r\n&,;]+)`,
		),
		replacement: `${1}[REDACTED]`,
	},
	{
		pattern:     regexp.MustCompile(`(?i)(\b[a-z][a-z0-9+.-]*://[^:/@\s]+:)[^@/\s]+(@)`),
		replacement: `${1}[REDACTED]${2}`,
	},
	{
		pattern:     regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]*\b`),
		replacement: `[REDACTED]`,
	},
}

func projectEinoAssistantShouldRetryModelError(err error) bool {
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var openAIError *openaimodel.APIError
	if errors.As(err, &openAIError) {
		return projectEinoAssistantRetryableHTTPStatus(openAIError.HTTPStatusCode)
	}
	var geminiError genai.APIError
	if errors.As(err, &geminiError) {
		return projectEinoAssistantRetryableHTTPStatus(geminiError.Code)
	}
	var geminiErrorPointer *genai.APIError
	if errors.As(err, &geminiErrorPointer) {
		return projectEinoAssistantRetryableHTTPStatus(geminiErrorPointer.Code)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) &&
		(networkError.Timeout() || networkError.Temporary())
}

func projectEinoAssistantRetryableHTTPStatus(status int) bool {
	return status == 408 ||
		status == 409 ||
		status == 425 ||
		status == 429 ||
		(status >= 500 && status <= 599)
}

func projectEinoAssistantModelRetryConfig(
	_ projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: 2,
		ShouldRetry: func(
			ctx context.Context,
			retryCtx *adk.RetryContext,
		) *adk.RetryDecision {
			if retryCtx == nil || ctx.Err() != nil {
				return &adk.RetryDecision{}
			}
			if retryCtx.Err != nil {
				// Once any model output exists, do not replay the request: a
				// partial stream may already contain a user-visible response or a
				// tool call observed by the callback/audit boundary. Exactly-once
				// replay is only safe before the first response item.
				if retryCtx.OutputMessage != nil {
					return &adk.RetryDecision{}
				}
				var timeoutErr *projectEinoAssistantModelTimeoutError
				if errors.As(retryCtx.Err, &timeoutErr) {
					if retryCtx.RetryAttempt == 1 {
						return &adk.RetryDecision{
							Retry:        true,
							RejectReason: "model timeout",
						}
					}
					// A model timeout receives exactly one transport retry. The
					// typed timeout remains the terminal diagnostic afterward.
					return &adk.RetryDecision{}
				}
				if !projectEinoAssistantShouldRetryModelError(retryCtx.Err) {
					return &adk.RetryDecision{}
				}
				return &adk.RetryDecision{
					Retry:        true,
					RejectReason: "transient model provider failure",
				}
			}
			if retryCtx.OutputMessage == nil {
				return &adk.RetryDecision{
					Retry:        true,
					RejectReason: "empty assistant response",
				}
			}
			if len(retryCtx.OutputMessage.ToolCalls) > 0 {
				if _, err := projectEinoAssistantAnalyzeToolBatch(retryCtx.OutputMessage.ToolCalls); err != nil {
					runState.discardLatestModelToolBatch(retryCtx.OutputMessage.ToolCalls)
					if projectEinoAssistantHasToolBatchCorrection(retryCtx.InputMessages) {
						return &adk.RetryDecision{RewriteError: err}
					}
					return &adk.RetryDecision{
						Retry:                 true,
						ModifiedInputMessages: projectEinoAssistantToolBatchCorrection(retryCtx.InputMessages, err),
						RejectReason:          "invalid tool batch",
					}
				}
			}
			if retryCtx.Err == nil &&
				len(retryCtx.OutputMessage.ToolCalls) == 0 &&
				strings.TrimSpace(projectEinoAssistantSummaryText(retryCtx.OutputMessage)) == "" {
				return &adk.RetryDecision{
					Retry:        true,
					RejectReason: "empty assistant response",
				}
			}
			return &adk.RetryDecision{}
		},
	}
}

func projectEinoAssistantWillRetry(err error) bool {
	var retryError *adk.WillRetryError
	return errors.As(err, &retryError)
}

func projectEinoAssistantPublishRetryStatus(
	err error,
	callbacks projectAssistantStreamCallbacks,
) {
	if callbacks.OnStatus == nil {
		return
	}
	var retryError *adk.WillRetryError
	if !errors.As(err, &retryError) {
		return
	}
	reason, _ := retryError.RejectReason().(string)
	switch reason {
	case "model timeout":
		callbacks.OnStatus("Model timed out; retrying 1/1")
	case "invalid tool batch":
		callbacks.OnStatus("Model returned an invalid action batch; retrying 1/1")
	case "empty assistant response":
		callbacks.OnStatus("Model returned no action; retrying")
	case "transient model provider failure":
		callbacks.OnStatus("Model request failed transiently; retrying")
	}
}

type projectEinoAssistantSafeToolErrorMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
}

var errProjectAssistantPlanRetirement = errors.New("assistant plan retirement failed")
var errProjectAssistantPlanGrantPersistence = errors.New("assistant plan grant persistence failed")

func (m *projectEinoAssistantSafeToolErrorMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	toolName := ""
	if toolCtx != nil {
		toolName = projectToolBaseName(toolCtx.Name)
	}
	return func(
		ctx context.Context,
		argumentsInJSON string,
		opts ...einotool.Option,
	) (string, error) {
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err == nil || projectEinoAssistantPropagateToolError(err) {
			return result, err
		}
		return projectEinoAssistantSafeToolFailureResult(toolName, err), nil
	}, nil
}

func (m *projectEinoAssistantSafeToolErrorMiddleware) WrapEnhancedInvokableToolCall(
	_ context.Context,
	endpoint adk.EnhancedInvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.EnhancedInvokableToolCallEndpoint, error) {
	toolName := ""
	if toolCtx != nil {
		toolName = projectToolBaseName(toolCtx.Name)
	}
	return func(
		ctx context.Context,
		toolArgument *schema.ToolArgument,
		opts ...einotool.Option,
	) (*schema.ToolResult, error) {
		result, err := endpoint(ctx, toolArgument, opts...)
		if err == nil || projectEinoAssistantPropagateToolError(err) {
			return result, err
		}
		return &schema.ToolResult{
			Parts: []schema.ToolOutputPart{{
				Type: schema.ToolPartTypeText,
				Text: projectEinoAssistantSafeToolFailureResult(toolName, err),
			}},
		}, nil
	}, nil
}

func projectEinoAssistantSafeToolFailureResult(toolName string, err error) string {
	safeReason := projectEinoAssistantSafeErrorText(err)
	recovery := ""
	lowerReason := strings.ToLower(safeReason)
	switch {
	case toolName == projectToolWriteFile && strings.Contains(lowerReason, "create-only"):
		recovery = " Recovery: do not retry write_file for an existing path. Use apply_patch with a small exact replacement after reading the current file."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, "oldtext was not found"):
		recovery = " Recovery: reread the relevant target section, copy a small unique exact oldText anchor, and retry. Do not fall back to write_file."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, "matched") && strings.Contains(lowerReason, "times"):
		recovery = " Recovery: add surrounding context so oldText is unique. Use replaceAll only when every match should change; do not fall back to write_file."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, "made no changes"):
		recovery = " Recovery: provide newText that implements the requested behavior and differs from oldText. Do not verify an unchanged workspace."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, string(workspace.PatchErrorContextNotFound)):
		recovery = " Recovery: reread the named file around the failed hunk, then retry one contextual patch with current unchanged lines and an @@ anchor when helpful."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, string(workspace.PatchErrorContextAmbiguous)):
		recovery = " Recovery: add more stable unchanged lines or an @@ class/function anchor so the failed hunk matches exactly one location."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, string(workspace.PatchErrorNoChanges)):
		recovery = " Recovery: revise the contextual patch so it makes the requested change; do not verify an unchanged workspace."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, string(workspace.PatchErrorWorkspaceConflict)):
		recovery = " Recovery: reread every affected existing file because workspace contents changed during the edit, then build a new contextual patch from current evidence."
	case toolName == projectToolApplyPatch && strings.Contains(lowerReason, string(workspace.PatchErrorInvalidPatch)):
		recovery = " Recovery: return one valid *** Begin Patch / *** End Patch envelope using Add File, Update File, Delete File, and optional Move to sections."
	}
	return truncateProjectToolInfo("Tool call failed: " + safeReason + recovery)
}

func projectEinoAssistantPropagateToolError(err error) bool {
	if _, ok := compose.IsInterruptRerunError(err); ok {
		return true
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, adk.ErrStreamCanceled) ||
		errors.Is(err, errProjectAssistantPlanRetirement) ||
		errors.Is(err, errProjectAssistantPlanGrantPersistence) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err)
}

func projectEinoAssistantSafeErrorText(err error) string {
	if err == nil {
		return ""
	}
	return projectEinoAssistantSafeText(err.Error())
}

func projectEinoAssistantSafeText(value string) string {
	value = projectEinoAssistantRedactSerializedCookieValues(value)
	for _, pattern := range projectEinoAssistantSecretPatterns {
		value = pattern.pattern.ReplaceAllString(value, pattern.replacement)
	}
	return truncateProjectToolInfo(value)
}

func projectEinoAssistantRedactSerializedCookieValues(value string) string {
	var out strings.Builder
	lastWrite := 0
	searchStart := 0
	for searchStart < len(value) {
		match := projectEinoAssistantSerializedCookiePattern.FindStringIndex(value[searchStart:])
		if match == nil {
			break
		}
		contentStart := searchStart + match[1]
		openingQuote := contentStart - 1
		openingEscapeCount := projectEinoAssistantBackslashCountBefore(value, openingQuote)

		closingQuote := -1
		closingEscapeStart := -1
		for i := contentStart; i < len(value); i++ {
			if value[i] != value[openingQuote] {
				continue
			}
			escapeCount := projectEinoAssistantBackslashCountBefore(value, i)
			if !projectEinoAssistantSerializedQuoteCloses(openingEscapeCount, escapeCount) {
				continue
			}
			if !projectEinoAssistantSerializedValueHasSafeSuffix(value, i, openingEscapeCount) {
				continue
			}
			closingQuote = i
			closingEscapeStart = i - escapeCount
			break
		}

		out.WriteString(value[lastWrite:contentStart])
		out.WriteString("[REDACTED]")
		if closingQuote >= 0 {
			lastWrite = closingEscapeStart
			searchStart = closingQuote + 1
			continue
		}

		lineEnd := len(value)
		if relativeEnd := strings.IndexAny(value[contentStart:], "\r\n"); relativeEnd >= 0 {
			lineEnd = contentStart + relativeEnd
		}
		lastWrite = lineEnd
		searchStart = lineEnd
	}
	if lastWrite == 0 {
		return value
	}
	out.WriteString(value[lastWrite:])
	return out.String()
}

func projectEinoAssistantBackslashCountBefore(value string, index int) int {
	count := 0
	for i := index - 1; i >= 0 && value[i] == '\\'; i-- {
		count++
	}
	return count
}

func projectEinoAssistantSerializedQuoteCloses(openingEscapeCount, candidateEscapeCount int) bool {
	modulus := 2 * (openingEscapeCount + 1)
	return candidateEscapeCount%modulus == openingEscapeCount
}

func projectEinoAssistantSerializedValueHasSafeSuffix(value string, closingQuote, escapeDepth int) bool {
	index := projectEinoAssistantSkipHorizontalSpace(value, closingQuote+1)
	if index >= len(value) {
		return false
	}
	if value[index] == '}' {
		return true
	}
	if value[index] != ',' {
		return false
	}

	index = projectEinoAssistantSkipHorizontalSpace(value, index+1)
	keyQuote := index + escapeDepth
	if keyQuote >= len(value) {
		return false
	}
	for i := index; i < keyQuote; i++ {
		if value[i] != '\\' {
			return false
		}
	}
	quote := value[keyQuote]
	if quote != '"' && quote != '\'' {
		return false
	}
	for i := keyQuote + 1; i < len(value); i++ {
		if value[i] != quote {
			continue
		}
		escapeCount := projectEinoAssistantBackslashCountBefore(value, i)
		if !projectEinoAssistantSerializedQuoteCloses(escapeDepth, escapeCount) {
			continue
		}
		index = projectEinoAssistantSkipHorizontalSpace(value, i+1)
		return index < len(value) && value[index] == ':'
	}
	return false
}

func projectEinoAssistantSkipHorizontalSpace(value string, index int) int {
	for index < len(value) && (value[index] == ' ' || value[index] == '\t') {
		index++
	}
	return index
}

func projectEinoAssistantPatchToolCallsMiddleware(
	ctx context.Context,
) (adk.ChatModelAgentMiddleware, error) {
	return patchtoolcalls.New(ctx, &patchtoolcalls.Config{
		PatchedContentGenerator: func(
			_ context.Context,
			toolName string,
			_ string,
		) (string, error) {
			return "The result for " + toolName +
					" was not recorded. Its completion is unknown; inspect current project or runtime state before retrying.",
				nil
		},
	})
}
