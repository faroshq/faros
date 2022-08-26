package kubeconfig

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/appvia/cluster-registry-operator/pkg/service/middleware"
	kubernetesutil "github.com/appvia/cluster-registry-operator/pkg/util/kubernetes"
	utilpem "github.com/appvia/cluster-registry-operator/pkg/util/pem"
	"github.com/appvia/cluster-registry-operator/pkg/util/responsewriter"
	"github.com/appvia/cluster-registry-operator/pkg/util/restconfig"
	"github.com/ghodss/yaml"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clientcmdv1 "k8s.io/client-go/tools/clientcmd/api/v1"
)

const (
	kubeconfigTimeout = time.Hour * 24
)

type contextKey int

const (
	contextKeyClient contextKey = iota
	contextKeyResponse
)

// directorDirectAccess is called by the ReverseProxy. It converts an incoming request into
// the one that'll go out to the API server. It also resolves an HTTP client
// that will be able to make the ongoing request.
//
// Unfortunately the signature of httputil.ReverseProxy.Director does not allow
// us to return values.  We get around this limitation slightly naughtily by
// storing return information in the request context.
func (k *kubeconfig) directorDirectAccess(r *http.Request) {
	ctx := r.Context()

	kubeConfigSecret, _ := ctx.Value(middleware.ContextKeyAccessRequestSecret).(*corev1.Secret)
	if kubeConfigSecret == nil {
		k.error(r, http.StatusForbidden, nil)
		return
	}

	key := struct {
		namespace string
		name      string
		mode      string
	}{
		name:      kubeConfigSecret.Name,
		namespace: kubeConfigSecret.Namespace,
		mode:      "d",
	}

	rootSecretReference := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      string(kubeConfigSecret.Data["cluster-secret-name"]),
			Namespace: string(kubeConfigSecret.Data["cluster-namespace"]),
		},
	}

	kubeconfig, err := k.getKubeconfigFromSecret(ctx, rootSecretReference)
	if err != nil {
		k.error(r, http.StatusInternalServerError, err)
		return
	}

	restConfig, err := kubernetesutil.RestConfigFromV1Config(kubeconfig)
	if err != nil {
		k.error(r, http.StatusInternalServerError, err)
		return
	}

	cli := k.clientCache.Get(key)
	if cli == nil {
		var err error
		cli, err = k.cli(ctx, kubeconfig, restConfig)
		if err != nil {
			k.error(r, http.StatusInternalServerError, err)
			return
		}

		k.clientCache.Put(key, cli)
	}

	r.RequestURI = ""
	r.URL.Scheme = "https"
	r.URL.Host = strings.ReplaceAll(restConfig.Host, "https://", "")
	// /namespaces/{namespace}/clusters/{cluster}/access/{access}/direct/magic -> /magic
	// https://go.dev/play/p/u3-N1gmKyAA
	r.URL.Path = "/" + strings.Join(strings.Split(r.URL.Path, "/")[8:], "/")
	r.Header.Del("Authorization")
	r.Host = r.URL.Host

	// http.Request.WithContext returns a copy of the original Request with the
	// new context, but we have no way to return it, so we overwrite our
	// existing request.
	*r = *r.WithContext(context.WithValue(ctx, contextKeyClient, cli))

}

// cli returns an appropriately configured HTTP client for forwarding the
// incoming request to a cluster
func (k *kubeconfig) cli(ctx context.Context, kubeconfig *clientcmdv1.Config, restConfig *rest.Config) (*http.Client, error) {

	var b []byte
	b = append(b, kubeconfig.AuthInfos[0].AuthInfo.ClientKeyData...)
	b = append(b, kubeconfig.AuthInfos[0].AuthInfo.ClientCertificateData...)

	clientKey, clientCerts, err := utilpem.Parse(b)
	if err != nil {
		return nil, err
	}

	_, caCerts, err := utilpem.Parse(kubeconfig.Clusters[0].Cluster.CertificateAuthorityData)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	for _, caCert := range caCerts {
		pool.AddCert(caCert)
	}

	return &http.Client{
		Transport: &http.Transport{
			DialContext: restconfig.DialContext(restConfig),
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{
					{
						Certificate: [][]byte{
							clientCerts[0].Raw,
						},
						PrivateKey: clientKey,
					},
				},
				RootCAs: pool,
			},
		},
	}, nil
}

func (k *kubeconfig) getKubeconfigFromSecret(ctx context.Context, secretRef corev1.Secret) (*clientcmdv1.Config, error) {
	secret, err := k.store.GetSecret(ctx, secretRef)
	if err != nil {
		return nil, err
	}

	kubeconfigData := secret.Data["kubeconfig"]
	if len(kubeconfigData) == 0 {
		return nil, fmt.Errorf("kubeconfig is nil")
	}

	var kubeconfig *clientcmdv1.Config
	err = yaml.Unmarshal(kubeconfigData, &kubeconfig)
	if err != nil {
		return nil, err
	}

	return kubeconfig, nil
}

// roundTripper is called by ReverseProxy to make the onward request happen.  We
// check if we had an error earlier and return that if we did. Otherwise we dig
// out the client and call it.
func (k *kubeconfig) roundTripper(r *http.Request) (*http.Response, error) {
	if resp, ok := r.Context().Value(contextKeyResponse).(*http.Response); ok {
		return resp, nil
	}

	cli := r.Context().Value(contextKeyClient).(*http.Client)
	resp, err := cli.Do(r)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusSwitchingProtocols {
		resp.Body = newCancelBody(resp.Body.(io.ReadWriteCloser), kubeconfigTimeout)
	}

	return resp, err
}

func (k *kubeconfig) error(r *http.Request, statusCode int, err error) {
	if err != nil {
		k.log.Warn(err)
	}

	w := responsewriter.New(r)
	http.Error(w, http.StatusText(statusCode), statusCode)

	*r = *r.WithContext(context.WithValue(r.Context(), contextKeyResponse, w.Response()))
}

// cancelBody is a workaround for the fact that http timeouts are incompatible
// with hijacked connections (https://github.com/golang/go/issues/31391):
// net/http.cancelTimerBody does not implement Writer.
type cancelBody struct {
	io.ReadWriteCloser
	t *time.Timer
	c chan struct{}
}

func (b *cancelBody) wait() {
	select {
	case <-b.t.C:
		b.ReadWriteCloser.Close()
	case <-b.c:
		b.t.Stop()
	}
}

func (b *cancelBody) Close() error {
	select {
	case b.c <- struct{}{}:
	default:
	}

	return b.ReadWriteCloser.Close()
}

func newCancelBody(rwc io.ReadWriteCloser, d time.Duration) io.ReadWriteCloser {
	b := &cancelBody{
		ReadWriteCloser: rwc,
		t:               time.NewTimer(d),
		c:               make(chan struct{}),
	}

	go b.wait()

	return b
}
