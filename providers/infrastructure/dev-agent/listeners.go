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

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// listenerRuntimeOperations is additive to runtimeOperations. The public
// coordinator can expose observations whether it owns the supervisor in the
// same process or reaches the loopback runtime sidecar over HTTP.
type listenerRuntimeOperations interface {
	Listeners(context.Context) ([]listenerObservation, error)
}

func (r *localRuntime) Listeners(context.Context) ([]listenerObservation, error) {
	return discoverTCPListeners()
}

func (c *httpRuntimeClient) Listeners(ctx context.Context) ([]listenerObservation, error) {
	var response listenersResponse
	if err := c.call(ctx, http.MethodGet, "listeners", nil, &response); err != nil {
		return nil, err
	}
	return response.Listeners, nil
}

func (s *agentServer) handleListeners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeControl(w, r) {
		return
	}
	runtime, ok := s.runtime.(listenerRuntimeOperations)
	if !ok {
		http.Error(w, "listener observations are unavailable", http.StatusServiceUnavailable)
		return
	}
	listeners, err := runtime.Listeners(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, listenersResponse{Listeners: listeners})
}

// registerListenerRuntimeEndpoint wires the loopback-only endpoint used by a
// coordinator when the process supervisor runs in its own sidecar.
func registerListenerRuntimeEndpoint(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/internal/listeners", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		listeners, err := discoverTCPListeners()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, listenersResponse{Listeners: listeners})
	})
}

const (
	maxDiscoveredListeners = 64
	maxProcEntries         = 2048
	maxFDEntries           = 512
)

// listenerObservation is intentionally a small, non-authoritative snapshot.
// It is suggestions-only data for App Studio: it never creates a service or
// grants exposure to the observed port.
type listenerObservation struct {
	Port     int64  `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	Address  string `json:"address,omitempty"`
	Process  string `json:"process,omitempty"`
}

type listenersResponse struct {
	Listeners []listenerObservation `json:"listeners"`
}

// discoverTCPListeners inspects the pod's kernel socket table and maps socket
// inodes back to process names. It is deliberately read-only and bounded. The
// sandbox control-plane ports are excluded so callers cannot mistake the
// authenticated agent itself for an app listener.
func discoverTCPListeners() ([]listenerObservation, error) {
	entries := make([]procListener, 0, maxDiscoveredListeners)
	inodes := make(map[string]struct{})
	for _, file := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			entry, ok := parseProcTCPLine(line)
			if !ok || isSandboxControlPort(entry.port) {
				continue
			}
			entries = append(entries, entry)
			if entry.inode != "" {
				inodes[entry.inode] = struct{}{}
			}
		}
	}
	if len(entries) == 0 {
		return []listenerObservation{}, nil
	}
	processNames := processNamesBySocket(inodes)
	seen := make(map[string]struct{}, len(entries))
	observations := make([]listenerObservation, 0, minInt(len(entries), maxDiscoveredListeners))
	for _, entry := range entries {
		observation := listenerObservation{
			Port:     entry.port,
			Protocol: "TCP",
			Address:  entry.address,
			Process:  processNames[entry.inode],
		}
		key := fmt.Sprintf("%d|%s|%s|%s", observation.Port, observation.Protocol, observation.Address, observation.Process)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		observations = append(observations, observation)
		if len(observations) >= maxDiscoveredListeners {
			break
		}
	}
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].Port != observations[j].Port {
			return observations[i].Port < observations[j].Port
		}
		if observations[i].Address != observations[j].Address {
			return observations[i].Address < observations[j].Address
		}
		return observations[i].Process < observations[j].Process
	})
	return observations, nil
}

type procListener struct {
	port    int64
	address string
	inode   string
}

func parseProcTCPLine(line string) (procListener, bool) {
	fields := strings.Fields(line)
	// sl local_address rem_address st tx_queue rx_queue tr tm->when
	// retrnsmt uid timeout inode. The kernel currently emits tx/rx as one
	// field, so inode is field 9 after strings.Fields (field 0 is sl).
	if len(fields) < 10 || fields[3] != "0A" {
		return procListener{}, false
	}
	address, port, ok := parseProcEndpoint(fields[1])
	if !ok {
		return procListener{}, false
	}
	return procListener{port: port, address: address, inode: fields[9]}, true
}

func parseProcEndpoint(raw string) (string, int64, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return "", 0, false
	}
	port, err := strconv.ParseInt(parts[1], 16, 32)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	addressBytes, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", 0, false
	}
	switch len(addressBytes) {
	case net.IPv4len:
		reverseBytes(addressBytes)
	case net.IPv6len:
		// /proc/net/tcp6 stores each 32-bit word little-endian.
		for offset := 0; offset < len(addressBytes); offset += 4 {
			reverseBytes(addressBytes[offset : offset+4])
		}
	default:
		return "", 0, false
	}
	return net.IP(addressBytes).String(), port, true
}

func reverseBytes(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}

func processNamesBySocket(wanted map[string]struct{}) map[string]string {
	result := make(map[string]string, len(wanted))
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}
	seenProcesses := 0
	for _, entry := range entries {
		if seenProcesses >= maxProcEntries || len(result) == len(wanted) {
			break
		}
		if !isDecimal(entry.Name()) {
			continue
		}
		seenProcesses++
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for index, fd := range fds {
			if index >= maxFDEntries {
				break
			}
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if _, wanted := wanted[inode]; !wanted {
				continue
			}
			if _, exists := result[inode]; exists {
				continue
			}
			result[inode] = processName(entry.Name())
		}
	}
	return result
}

func processName(pid string) string {
	if raw, err := os.ReadFile(filepath.Join("/proc", pid, "comm")); err == nil {
		if name := strings.TrimSpace(string(raw)); name != "" {
			return name
		}
	}
	if raw, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil {
		if command := strings.SplitN(string(raw), "\x00", 2)[0]; command != "" {
			return filepath.Base(command)
		}
	}
	return ""
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isSandboxControlPort(port int64) bool {
	return port >= 7070 && port <= 7073
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
