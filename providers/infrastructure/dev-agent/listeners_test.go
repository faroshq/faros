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

package main

import "testing"

func TestParseProcTCPLineUsesSocketInodeAndFiltersNonListeners(t *testing.T) {
	line := "0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 100 0 12345 1"
	got, ok := parseProcTCPLine(line)
	if !ok {
		t.Fatal("parseProcTCPLine() rejected a valid LISTEN row")
	}
	if got.port != 8080 || got.address != "127.0.0.1" || got.inode != "12345" {
		t.Fatalf("parsed listener = %+v, want port=8080 address=127.0.0.1 inode=12345", got)
	}
	for _, row := range []string{
		"1: 0100007F:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000 100 0 12345 1",
		"1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000 100 0",
	} {
		if _, ok := parseProcTCPLine(row); ok {
			t.Fatalf("parseProcTCPLine() accepted invalid row %q", row)
		}
	}
}

func TestParseProcEndpointIPv4AndIPv6AndControlPort(t *testing.T) {
	if got, port, ok := parseProcEndpoint("0100007F:1F90"); !ok || got != "127.0.0.1" || port != 8080 {
		t.Fatalf("IPv4 endpoint = %q/%d/%v", got, port, ok)
	}
	if got, port, ok := parseProcEndpoint("00000000000000000000000001000000:1F90"); !ok || got != "::1" || port != 8080 {
		t.Fatalf("IPv6 endpoint = %q/%d/%v", got, port, ok)
	}
	for _, port := range []int64{7070, 7071, 7072, 7073} {
		if !isSandboxControlPort(port) {
			t.Fatalf("control port %d was not filtered", port)
		}
	}
	if isSandboxControlPort(8080) {
		t.Fatal("application port 8080 was filtered as a control port")
	}
}
