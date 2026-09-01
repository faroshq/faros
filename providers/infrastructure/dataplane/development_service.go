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

package dataplane

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

const developmentServiceLogsMaxBytes = 512 << 10

// serveDevelopmentServiceLogs authorizes the caller against the
// DevelopmentService first, then resolves the sandbox's control Service and
// Secret from its observed status. No control token is returned to the caller.
func (h *Handler) serveDevelopmentServiceLogs(w http.ResponseWriter, r *http.Request, id identity, req request, service *unstructured.Unstructured) {
	if service == nil {
		http.Error(w, "development service not found", http.StatusNotFound)
		return
	}
	projectName, _, projectNameErr := unstructured.NestedString(service.Object, "spec", "projectRef", "name")
	projectUID, _, projectUIDErr := unstructured.NestedString(service.Object, "spec", "projectRef", "uid")
	sandboxName, _, sandboxNameErr := unstructured.NestedString(service.Object, "spec", "sandboxRef", "name")
	sandboxUID, _, sandboxUIDErr := unstructured.NestedString(service.Object, "spec", "sandboxRef", "uid")
	if projectNameErr != nil || projectUIDErr != nil || sandboxNameErr != nil || sandboxUIDErr != nil ||
		strings.TrimSpace(projectName) == "" || strings.TrimSpace(projectUID) == "" ||
		strings.TrimSpace(sandboxName) == "" || strings.TrimSpace(sandboxUID) == "" {
		http.Error(w, "development service references require project and sandbox names and UIDs", http.StatusConflict)
		return
	}
	sandbox, err := h.instances.Get(r.Context(), req.workspace, id.token, infrav1alpha1.InstancesResource, sandboxName)
	if err != nil {
		writeKubeError(w, err)
		return
	}
	if sandbox == nil {
		http.Error(w, "development service sandbox not found", http.StatusConflict)
		return
	}
	if string(sandbox.GetUID()) != sandboxUID {
		http.Error(w, "development service sandbox UID does not match", http.StatusConflict)
		return
	}
	runtimeNamespace, serviceName, serviceNamespace, secretName, secretNamespace, ok := sandboxControlCoordinates(sandbox)
	if !ok {
		http.Error(w, "development service sandbox control plane is not ready", http.StatusConflict)
		return
	}
	token, err := h.runtime.ControlToken(r.Context(), secretNamespace, secretName)
	if err != nil {
		http.Error(w, "sandbox control token unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	proxyDevelopmentServiceLogs(w, r, h.runtime, runtimeNamespace, serviceName, serviceNamespace, service.GetName(), token)
}

func sandboxControlCoordinates(sandbox *unstructured.Unstructured) (runtimeNamespace, serviceName, serviceNamespace, secretName, secretNamespace string, ok bool) {
	if sandbox == nil {
		return "", "", "", "", "", false
	}
	runtimeNamespace, _, _ = unstructured.NestedString(sandbox.Object, "status", "runtimeNamespace")
	serviceName, _, _ = unstructured.NestedString(sandbox.Object, "status", "components", "workspace", "controlServiceRef", "name")
	serviceNamespace, _, _ = unstructured.NestedString(sandbox.Object, "status", "components", "workspace", "controlServiceRef", "namespace")
	secretName, _, _ = unstructured.NestedString(sandbox.Object, "status", "controlSecretRef", "name")
	secretNamespace, _, _ = unstructured.NestedString(sandbox.Object, "status", "controlSecretRef", "namespace")
	if serviceNamespace == "" {
		serviceNamespace = runtimeNamespace
	}
	if secretNamespace == "" {
		secretNamespace = runtimeNamespace
	}
	return runtimeNamespace, serviceName, serviceNamespace, secretName, secretNamespace,
		runtimeNamespace != "" && serviceName != "" && serviceNamespace != "" && secretName != "" && secretNamespace != ""
}

func proxyDevelopmentServiceLogs(w http.ResponseWriter, r *http.Request, runtime Runtime, runtimeNamespace, serviceName, serviceNamespace, developmentServiceName, token string) {
	transport, err := runtime.Transport()
	if err != nil {
		http.Error(w, "runtime transport unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	base, err := url.Parse(runtime.Host())
	if err != nil || base.Scheme == "" || base.Host == "" {
		http.Error(w, "invalid runtime host", http.StatusBadGateway)
		return
	}
	if strings.TrimSpace(developmentServiceName) == "" {
		http.Error(w, "development service name is empty", http.StatusConflict)
		return
	}
	upstreamPath := fmt.Sprintf("/api/v1/namespaces/%s/services/%s:control/proxy/service/logs", url.PathEscape(serviceNamespace), url.PathEscape(serviceName))
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Director: func(req *http.Request) {
			req.URL.Scheme = base.Scheme
			req.URL.Host = base.Host
			req.URL.Path = upstreamPath
			req.URL.RawQuery = url.Values{"name": []string{developmentServiceName}}.Encode()
			req.Host = base.Host
			req.Header.Del("Authorization")
			req.Header.Set(controlTokenHeader, token)
		},
		ModifyResponse: func(response *http.Response) error {
			// The agent already keeps a bounded ring, but keep the provider
			// endpoint bounded independently of that implementation detail.
			body := response.Body
			response.Body = &boundedReadCloser{Reader: io.LimitReader(body, developmentServiceLogsMaxBytes), Closer: body}
			response.ContentLength = -1
			response.Header.Del("Content-Length")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "runtime service logs proxy error: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

type boundedReadCloser struct {
	io.Reader
	io.Closer
}
