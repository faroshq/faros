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
	"syscall"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
		pattern: regexp.MustCompile(
			`(?i)(\\"(?:set-cookie|cookie)\\"[ \t]*:[ \t]*)\\"[^\r\n]*?\\"([ \t]*[,}])`,
		),
		replacement: `${1}\"[REDACTED]\"${2}`,
	},
	{
		pattern: regexp.MustCompile(
			`(?i)(["']\b(?:set-cookie|cookie)\b["'][ \t]*:[ \t]*)(?:"(?:[^"\\\r\n]|\\.)*"|'(?:[^'\\\r\n]|\\.)*')`,
		),
		replacement: `${1}"[REDACTED]"`,
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

func projectEinoAssistantModelRetryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: 2,
		ShouldRetry: func(
			ctx context.Context,
			retryCtx *adk.RetryContext,
		) *adk.RetryDecision {
			if retryCtx == nil || ctx.Err() != nil ||
				retryCtx.OutputMessage != nil ||
				!projectEinoAssistantShouldRetryModelError(retryCtx.Err) {
				return &adk.RetryDecision{}
			}
			return &adk.RetryDecision{
				Retry:        true,
				RejectReason: "transient model provider failure",
			}
		},
	}
}

func projectEinoAssistantWillRetry(err error) bool {
	var retryError *adk.WillRetryError
	return errors.As(err, &retryError)
}

type projectEinoAssistantSafeToolErrorMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
}

func (m *projectEinoAssistantSafeToolErrorMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return func(
		ctx context.Context,
		argumentsInJSON string,
		opts ...einotool.Option,
	) (string, error) {
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if err == nil || projectEinoAssistantPropagateToolError(err) {
			return result, err
		}
		return truncateProjectToolInfo(
			"Tool call failed: " + projectEinoAssistantSafeErrorText(err),
		), nil
	}, nil
}

func (m *projectEinoAssistantSafeToolErrorMiddleware) WrapEnhancedInvokableToolCall(
	_ context.Context,
	endpoint adk.EnhancedInvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.EnhancedInvokableToolCallEndpoint, error) {
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
				Text: truncateProjectToolInfo(
					"Tool call failed: " + projectEinoAssistantSafeErrorText(err),
				),
			}},
		}, nil
	}, nil
}

func projectEinoAssistantPropagateToolError(err error) bool {
	if _, ok := compose.IsInterruptRerunError(err); ok {
		return true
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, adk.ErrStreamCanceled) ||
		apierrors.IsForbidden(err) ||
		apierrors.IsUnauthorized(err)
}

func projectEinoAssistantSafeErrorText(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	for _, pattern := range projectEinoAssistantSecretPatterns {
		value = pattern.pattern.ReplaceAllString(value, pattern.replacement)
	}
	return truncateProjectToolInfo(value)
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
