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
	"syscall"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"google.golang.org/genai"
)

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
