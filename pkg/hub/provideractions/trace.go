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

package provideractions

import "strings"

// forwardedTraceHeaders returns only a syntactically valid W3C trace context.
// Invalid or oversized values are deliberately dropped: tracing must never
// make an otherwise authorized provider action fail, and raw inbound headers
// must not be copied to a provider.
func forwardedTraceHeaders(rawTraceparent, rawTracestate string) (traceparent, tracestate string) {
	traceparent = strings.TrimSpace(rawTraceparent)
	if !validTraceparent(traceparent) {
		return "", ""
	}

	tracestate = strings.TrimSpace(rawTracestate)
	if tracestate != "" && !validTracestate(tracestate) {
		tracestate = ""
	}
	return traceparent, tracestate
}

// validTraceparent accepts the fixed fields required by the W3C trace-context
// recommendation and permits a version-specific suffix for future versions.
// Version 00 must not carry a suffix. Trace and parent IDs may not be all zero.
func validTraceparent(value string) bool {
	if len(value) < 55 || len(value) > maxHeaderValueLength {
		return false
	}
	if value[2] != '-' || value[35] != '-' || value[52] != '-' {
		return false
	}
	if !validLowerHex(value[:2]) || value[:2] == "ff" ||
		!validLowerHex(value[3:35]) || allZero(value[3:35]) ||
		!validLowerHex(value[36:52]) || allZero(value[36:52]) ||
		!validLowerHex(value[53:55]) {
		return false
	}
	if value[:2] == "00" {
		return len(value) == 55
	}
	if len(value) <= 56 || value[55] != '-' {
		return false
	}
	for _, r := range value[56:] {
		if r < '!' || r > '~' {
			return false
		}
	}
	return true
}

// validTracestate validates the W3C list-member shape while leaving the value
// contents opaque to the hub. OWS around commas is accepted, but controls,
// delimiters, and malformed key/value members are rejected.
func validTracestate(value string) bool {
	if len(value) == 0 || len(value) > maxHeaderValueLength {
		return false
	}
	for _, rawMember := range strings.Split(value, ",") {
		member := strings.TrimSpace(rawMember)
		separator := strings.IndexByte(member, '=')
		if separator <= 0 || separator == len(member)-1 || strings.IndexByte(member[separator+1:], '=') >= 0 || !validTracestateKey(member[:separator]) {
			return false
		}
		for _, r := range member[separator+1:] {
			if !validTracestateValueChar(r) {
				return false
			}
		}
	}
	return true
}

func validTracestateKey(value string) bool {
	if len(value) == 0 || len(value) > 256 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '*' && r != '/' {
			return false
		}
	}
	return true
}

func validTracestateValueChar(r rune) bool {
	return (r >= '!' && r <= '+') || (r >= '-' && r <= '<') || (r >= '>' && r <= '~')
}

func validLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func allZero(value string) bool {
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}
