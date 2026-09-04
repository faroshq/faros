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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// newAuthTestServer builds a Server with a kcp config present (so delegated
// authorization is active), an injected authorize func, and a static-token set
// populated the way a legacy FAROS_STATIC_TOKENS config would have — to prove
// that set no longer short-circuits authorization anywhere.
func newAuthTestServer(t *testing.T, authorizeErr error, calls *[]string) *Server {
	t.Helper()
	kcp := &rest.Config{Host: "https://kcp.invalid"}
	s := testServer("/services/providers/edges/edgeproxy")
	s.kcpConfig = kcp
	s.tenantConfig = func(context.Context, string) (*rest.Config, error) { return kcp, nil }
	s.edgeConnManager = NewConnManager()
	s.logger = klog.Background()
	s.staticTokens = map[string]struct{}{"static-secret": {}}
	s.authorizeFn = func(_ context.Context, _, _ *rest.Config, token, cluster, verb, group, resource, name string) error {
		*calls = append(*calls, token+" "+verb+" "+group+"/"+resource+"/"+name+"@"+cluster)
		return authorizeErr
	}
	return s
}

func TestEdgesProxyHandlerAuthorizesEveryToken(t *testing.T) {
	const edgePath = "/clusters/ws-1/apis/edges.faros.sh/v1alpha1/linuxservers/edge-1/ssh"
	const servicePath = "/clusters/ws-1/apis/edges.faros.sh/v1alpha1/services/svc-1/proxy/"

	cases := []struct {
		name         string
		token        string
		path         string
		authorizeErr error
		wantStatus   int
		wantCall     string
	}{
		{
			// Allowed by kcp: the handler proceeds to the tunnel lookup and, with
			// no agent connected, answers 502 — proving authorize ran and passed.
			name:       "static token is authorized like any other token",
			token:      "static-secret",
			path:       edgePath,
			wantStatus: http.StatusBadGateway,
			wantCall:   "static-secret proxy edges.faros.sh/linuxservers/edge-1@ws-1",
		},
		{
			name:         "static token denied by kcp is forbidden",
			token:        "static-secret",
			path:         edgePath,
			authorizeErr: errors.New("access denied"),
			wantStatus:   http.StatusForbidden,
			wantCall:     "static-secret proxy edges.faros.sh/linuxservers/edge-1@ws-1",
		},
		{
			name:       "user token is authorized",
			token:      "user-token",
			path:       edgePath,
			wantStatus: http.StatusBadGateway,
			wantCall:   "user-token proxy edges.faros.sh/linuxservers/edge-1@ws-1",
		},
		{
			name:         "static token on the service proxy is authorized too",
			token:        "static-secret",
			path:         servicePath,
			authorizeErr: errors.New("access denied"),
			wantStatus:   http.StatusForbidden,
			wantCall:     "static-secret proxy edges.faros.sh/services/svc-1@ws-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			s := newAuthTestServer(t, tc.authorizeErr, &calls)

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rr := httptest.NewRecorder()
			s.buildEdgesProxyHandler().ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if len(calls) != 1 {
				t.Fatalf("authorize called %d times, want exactly once: %v", len(calls), calls)
			}
			if calls[0] != tc.wantCall {
				t.Fatalf("authorize call = %q, want %q", calls[0], tc.wantCall)
			}
		})
	}
}

// New refuses the test-only static-token set unless explicitly opted in, and
// never alongside a kcp config.
func TestNewRejectsStaticTokensOutsideTestBypass(t *testing.T) {
	kind := KindConfig{
		GVR:  schema.GroupVersionResource{Group: "edges.faros.sh", Version: "v1alpha1", Resource: "linuxservers"},
		Kind: "LinuxServer",
	}
	base := Config{Kinds: []KindConfig{kind}, AgentPickupPath: "/agent/proxy", Logger: klog.Background()}

	cfg := base
	cfg.StaticTokens = []string{"static-secret"}
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted StaticTokens without AllowStaticTokenBypass")
	}

	cfg = base
	cfg.StaticTokens = []string{"static-secret"}
	cfg.AllowStaticTokenBypass = true
	cfg.KCPConfig = &rest.Config{Host: "https://kcp.invalid"}
	if _, err := New(cfg); err == nil {
		t.Fatal("New accepted AllowStaticTokenBypass alongside a KCPConfig")
	}

	cfg = base
	cfg.StaticTokens = []string{"static-secret"}
	cfg.AllowStaticTokenBypass = true
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New with test bypass and no kcp config: %v", err)
	}
	if _, ok := s.staticTokens["static-secret"]; !ok {
		t.Fatal("test bypass did not register the static token")
	}
}
