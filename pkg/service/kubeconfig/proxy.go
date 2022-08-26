package kubeconfig

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/appvia/cluster-registry-operator/pkg/apis/registry.appvia.io/v1alpha1"
	"github.com/appvia/cluster-registry-operator/pkg/service/middleware"
)

// directorProxyAccess is called by the ReverseProxy. It converts an incoming request into
// the one that'll go out to the API server. It also resolves an HTTP client
// that will be able to make the ongoing request.
//
// Unfortunately the signature of httputil.ReverseProxy.Director does not allow
// us to return values.  We get around this limitation slightly naughtily by
// storing return information in the request context.
func (k *kubeconfig) directorProxyAccess(r *http.Request) {
	ctx := r.Context()

	accessRequest, _ := ctx.Value(middleware.ContextKeyClusterAccessRequest).(*v1alpha1.ClusterAccessRequest)
	if accessRequest == nil {
		k.error(r, http.StatusForbidden, nil)
		return
	}

	key := struct {
		namespace string
		name      string
		mode      string
	}{
		name:      accessRequest.Name,
		namespace: accessRequest.Namespace,
		mode:      "p",
	}

	cli := k.clientCache.Get(key)
	if cli == nil {
		cli := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		}

		k.clientCache.Put(key, cli)
	}

	r.RequestURI = ""
	r.URL.Scheme = "https"
	r.URL.Host = strings.ReplaceAll(accessRequest.Spec.ProxyURL, "https://", "")
	// /namespaces/{namespace}/clusters/{cluster}/access/{access}/direct/magic -> /magic
	// https://go.dev/play/p/u3-N1gmKyAA
	r.URL.Path = "/" + strings.Join(strings.Split(r.URL.Path, "/")[8:], "/")
	r.Host = r.URL.Host

	// http.Request.WithContext returns a copy of the original Request with the
	// new context, but we have no way to return it, so we overwrite our
	// existing request.
	*r = *r.WithContext(context.WithValue(ctx, contextKeyClient, cli))

}
