package kubeconfig

// Based on https://github.com/golang/build/tree/master/revdial/v2
// Based on https://github.com/kcp-dev/kcp/tree/main/pkg/tunneler

import (
	"io"
	"net/http"
	"time"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/middleware"
	"github.com/faroshq/faros/pkg/util/dialer"

	"github.com/aojea/rwconn"
)

func (k *kubeconfig) Tunnel(apiHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		cluster, _ := ctx.Value(middleware.ContextKeyCluster).(*models.Cluster)
		if cluster == nil {
			k.error(r, http.StatusForbidden, nil)
			return
		}

		key := struct {
			namespaceID string
			clusterID   string
		}{
			clusterID:   cluster.ID,
			namespaceID: cluster.NamespaceID,
		}

		// TODO: check authentication

		k.log.Debugf("tunnel request: %s/%s: %s", cluster.NamespaceID, cluster.ID, r.URL.Path)

		d := k.dialerCache.Get(key)
		// First flush response headers
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "flusher not implemented", http.StatusInternalServerError)
			return
		}

		fw := &flushWriter{w: w, f: flusher}
		doneCh := make(chan struct{})
		conn := rwconn.NewConn(r.Body, fw, rwconn.SetWriteDelay(500*time.Millisecond), rwconn.SetCloseHook(func() {
			// exit the handler
			close(doneCh)
		}))
		if d == nil || isClosedChan(d.Done()) {
			// start clean
			k.dialerCache.Delete(key)
			k.dialerCache.Put(key, dialer.New(conn))
			// start control loop
			select {
			case <-r.Context().Done():
				conn.Close()
			case <-doneCh:
			}
			k.log.Debugf("stopped tunnel %s/%s control connection ", cluster.NamespaceID, cluster.ID)
			return
		}
		// create a reverse connection
		k.log.Debugf("tunnel %s/%s started", cluster.NamespaceID, cluster.ID)
		select {
		case d.IncomingConn() <- conn:
		case <-d.Done():
			http.Error(w, "tunnels: tunnel closed", http.StatusInternalServerError)
			return
		}
		// keep the handler alive until the connection is closed
		select {
		case <-r.Context().Done():
			conn.Close()
		case <-doneCh:
		}
		k.log.Debugf("connection from %s done", r.RemoteAddr)

		apiHandler.ServeHTTP(w, r)
	}
}

func (k *kubeconfig) ProxyRev(apiHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiHandler.ServeHTTP(w, r)
	}
}

// flushWriter
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (w *flushWriter) Write(data []byte) (int, error) {
	n, err := w.w.Write(data)
	w.f.Flush()
	return n, err
}

func (w *flushWriter) Close() error {
	return nil
}

func isClosedChan(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}
