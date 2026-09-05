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

package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	edgeapi "github.com/faroshq/provider-edges/internal/edgeapi"
)

// newTestHostKey generates an sshd host key and returns its signer and its
// authorized_keys form.
func newTestHostKey(t *testing.T) (gossh.Signer, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}
	return signer, strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey())))
}

// startTestSSHServer runs a minimal SSH server (no client auth) on a loopback
// listener and returns a connection to it. A real socket rather than net.Pipe:
// the SSH version exchange writes before it reads on both sides, which
// deadlocks on an unbuffered pipe.
func startTestSSHServer(t *testing.T, signer gossh.Signer) net.Conn {
	t.Helper()
	cfg := &gossh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		sc, chans, reqs, err := gossh.NewServerConn(conn, cfg)
		if err != nil {
			conn.Close() //nolint:errcheck
			return
		}
		defer sc.Close() //nolint:errcheck
		go gossh.DiscardRequests(reqs)
		for ch := range chans {
			ch.Reject(gossh.Prohibited, "test server") //nolint:errcheck
		}
	}()

	clientConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialling test SSH server: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() }) //nolint:errcheck
	return clientConn
}

func TestNewSSHClientHostKeyVerification(t *testing.T) {
	serverSigner, serverKey := newTestHostKey(t)
	_, otherKey := newTestHostKey(t)

	cases := []struct {
		name        string
		hk          sshHostKeyVerification
		wantErr     string
		wantLearned string
	}{
		{
			name:    "garbage key errors before dialing",
			hk:      sshHostKeyVerification{Key: "not a key", Policy: edgeapi.SSHHostKeyPolicyTOFU, AllowUnverified: true},
			wantErr: "parsing SSH host key",
		},
		{
			name:    "empty key with strict policy errors",
			hk:      sshHostKeyVerification{Policy: edgeapi.SSHHostKeyPolicyStrict},
			wantErr: "no SSH host key known",
		},
		{
			name:    "empty key with no policy defaults to strict",
			hk:      sshHostKeyVerification{},
			wantErr: "no SSH host key known",
		},
		{
			name:        "empty key with tofu succeeds and learns the presented key",
			hk:          sshHostKeyVerification{Policy: edgeapi.SSHHostKeyPolicyTOFU},
			wantLearned: serverKey,
		},
		{
			name: "matching key succeeds and learns nothing",
			hk:   sshHostKeyVerification{Key: serverKey, Policy: edgeapi.SSHHostKeyPolicyStrict},
		},
		{
			name:    "mismatching key fails the handshake even under tofu and the escape hatch",
			hk:      sshHostKeyVerification{Key: otherKey, Policy: edgeapi.SSHHostKeyPolicyTOFU, AllowUnverified: true},
			wantErr: "failed to create SSH client connection",
		},
		{
			name: "empty key with the escape hatch succeeds without learning",
			hk:   sshHostKeyVerification{Policy: edgeapi.SSHHostKeyPolicyStrict, AllowUnverified: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := startTestSSHServer(t, serverSigner)
			client, learned, err := newSSHClient(context.Background(), conn, nil, tc.hk, klog.Background())
			if tc.wantErr != "" {
				if err == nil {
					client.Close() //nolint:errcheck
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newSSHClient: %v", err)
			}
			defer client.Close() //nolint:errcheck
			if learned != tc.wantLearned {
				t.Fatalf("learned key = %q, want %q", learned, tc.wantLearned)
			}
		})
	}
}

func TestApplyReportedSSHHostKeyIsWriteOnce(t *testing.T) {
	_, first := newTestHostKey(t)
	_, second := newTestHostKey(t)
	now := metav1.Now()

	status := map[string]interface{}{}
	applyReportedSSHHostKey(status, first, now)
	if status["sshHostKey"] != first {
		t.Fatalf("first report not recorded: %v", status["sshHostKey"])
	}
	if c := findStatusCondition(status, edgeapi.ConnectionConditionSSHHostKeyChanged); c != nil {
		t.Fatalf("unexpected condition after first report: %v", c)
	}

	// A different key is NOT recorded; the mismatch is surfaced as a condition
	// that names both fingerprints.
	applyReportedSSHHostKey(status, second, now)
	if status["sshHostKey"] != first {
		t.Fatalf("recorded key was replaced by a later report: %v", status["sshHostKey"])
	}
	c := findStatusCondition(status, edgeapi.ConnectionConditionSSHHostKeyChanged)
	if c == nil {
		t.Fatal("SSHHostKeyChanged condition not set on mismatch")
	}
	if c["status"] != string(metav1.ConditionTrue) || c["reason"] != "FingerprintMismatch" {
		t.Fatalf("condition = %v, want True/FingerprintMismatch", c)
	}
	msg, _ := c["message"].(string)
	for _, fp := range []string{sshHostKeyFingerprint(first), sshHostKeyFingerprint(second)} {
		if !strings.Contains(msg, fp) {
			t.Fatalf("condition message %q lacks fingerprint %s", msg, fp)
		}
	}

	// The same key again (with a comment appended, as sshd tooling emits)
	// keeps the record and clears the condition.
	applyReportedSSHHostKey(status, first+" host@example", now)
	if status["sshHostKey"] != first {
		t.Fatalf("recorded key changed on a matching report: %v", status["sshHostKey"])
	}
	c = findStatusCondition(status, edgeapi.ConnectionConditionSSHHostKeyChanged)
	if c == nil || c["status"] != string(metav1.ConditionFalse) {
		t.Fatalf("condition after matching report = %v, want False", c)
	}
}

func TestSSHHostKeyForPrefersSpecPin(t *testing.T) {
	edge := &sshEdgeView{}
	edge.Status.SSHHostKey = "ssh-ed25519 AAAAstatus"

	key, policy := sshHostKeyFor(edge)
	if key != "ssh-ed25519 AAAAstatus" || policy != edgeapi.SSHHostKeyPolicyStrict {
		t.Fatalf("status key / default policy = %q / %q", key, policy)
	}

	edge.Spec.SSHHostKey = " ssh-ed25519 AAAApinned \n"
	edge.Spec.SSHHostKeyPolicy = edgeapi.SSHHostKeyPolicyTOFU
	key, policy = sshHostKeyFor(edge)
	if key != "ssh-ed25519 AAAApinned" {
		t.Fatalf("spec pin did not win over status: %q", key)
	}
	if policy != edgeapi.SSHHostKeyPolicyTOFU {
		t.Fatalf("policy = %q, want tofu", policy)
	}
}
