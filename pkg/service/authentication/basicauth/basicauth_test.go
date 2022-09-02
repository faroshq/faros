package basicauth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/util/htpasswd"
	servicetest "github.com/faroshq/faros/test/util/service"
	"github.com/stretchr/testify/assert"
)

func TestBasicAuth(t *testing.T) {
	ctx := context.Background()

	// init shared server for test as we might be clashing ports.
	// We could fix this by using freeport and bind to random ports :)
	// But who has time for this :)
	config := &config.Config{}
	config.API.AuthenticationProviders = []string{"basicauth"}

	// create test htpasswd file in temp store
	dir, err := os.MkdirTemp("/tmp", "basicauth-test-faros")
	assert.NoError(t, err)
	htpasswdFile := filepath.Join(dir, "htpasswd")

	err = htpasswd.SetPassword(htpasswdFile, "test@faros.sh", "farosfaros", htpasswd.HashBCrypt)
	assert.NoError(t, err)
	config.API.BasicAuthAuthenticationProviderFile = htpasswdFile

	server := servicetest.GetTestServer(ctx, config, t)

	tests := []struct {
		name       string
		request    func() *http.Request
		expectBody string
		expectCode int
	}{
		{
			name: "404",
			request: func() *http.Request {
				bodyReader := strings.NewReader("{}")
				httpRequest, _ := http.NewRequest("GET", "/foo/bar", bodyReader)
				httpRequest.Header.Set("Content-Type", "application/json")
				httpRequest.Header.Set("Accept", "*/*")
				return httpRequest
			},
			expectBody: "",
			expectCode: http.StatusNotFound,
		},
		{
			name: "403 - Forbidden",
			request: func() *http.Request {
				bodyReader := strings.NewReader("{}")
				httpRequest, _ := http.NewRequest("GET", "/api/v1/namespaces", bodyReader)
				httpRequest.Header.Set("Content-Type", "application/json")
				httpRequest.Header.Set("Accept", "*/*")
				return httpRequest
			},
			expectBody: "",
			expectCode: http.StatusForbidden,
		},
		{
			name: "200 - authenticated",
			request: func() *http.Request {
				bodyReader := strings.NewReader("{}")
				httpRequest, _ := http.NewRequest("GET", "/api/v1/namespaces", bodyReader)
				httpRequest.Header.Set("Content-Type", "application/json")
				httpRequest.Header.Set("Accept", "*/*")
				httpRequest.SetBasicAuth("test@faros.sh", "farosfaros")
				return httpRequest
			},
			expectBody: "",
			expectCode: http.StatusForbidden,
		},
		{
			name: "403 - Forbidden - bad password",
			request: func() *http.Request {
				bodyReader := strings.NewReader("{}")
				httpRequest, _ := http.NewRequest("GET", "/api/v1/namespaces", bodyReader)
				httpRequest.Header.Set("Content-Type", "application/json")
				httpRequest.Header.Set("Accept", "*/*")
				httpRequest.SetBasicAuth("test@faros.sh", "bob")
				return httpRequest
			},
			expectBody: "",
			expectCode: http.StatusForbidden,
		},
		{
			name: "400 - Forbidden - not an email",
			request: func() *http.Request {
				bodyReader := strings.NewReader("{}")
				httpRequest, _ := http.NewRequest("GET", "/api/v1/namespaces", bodyReader)
				httpRequest.Header.Set("Content-Type", "application/json")
				httpRequest.Header.Set("Accept", "*/*")
				httpRequest.SetBasicAuth("test", "farosfaros")
				return httpRequest
			},
			expectBody: "",
			expectCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := tt.request()

			server.ServeHTTP(w, r)

			// test checkers
			// TODO: move to cmd github.com/google/go-cmp/cmp
			if !reflect.DeepEqual(tt.expectBody, w.Body.String()) {
				t.Errorf("%s != %s", tt.expectBody, w.Body.String())
			}
			if tt.expectCode != w.Result().StatusCode {
				t.Errorf("expected code: %d, got: %d", tt.expectCode, w.Code)
			}
		})
	}
}
