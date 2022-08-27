package middleware

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/models"
	logutil "github.com/faroshq/faros/pkg/util/log"
)

type logResponseWriter struct {
	http.ResponseWriter

	statusCode int
	path       string
	bytes      int
}

func (w *logResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker := w.ResponseWriter.(http.Hijacker)
	return hijacker.Hijack()
}

func (w *logResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *logResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
	w.statusCode = statusCode
}

func (w *logResponseWriter) Flush() {
	flucher := w.ResponseWriter.(http.Flusher)
	flucher.Flush()
}

type logReadCloser struct {
	io.ReadCloser

	bytes int
}

func (rc *logReadCloser) Read(b []byte) (int, error) {
	n, err := rc.ReadCloser.Read(b)
	rc.bytes += n
	return n, err
}

func Log(log *logrus.Entry) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := time.Now()

			//route := mux.CurrentRoute(r)
			//path, _ := route.GetPathTemplate()

			r.Body = &logReadCloser{ReadCloser: r.Body}
			w = &logResponseWriter{ResponseWriter: w, statusCode: http.StatusOK, path: r.URL.Path}

			correlationData := &models.CorrelationData{
				ClientRequestID: r.Header.Get(models.ClientClientRequestID),
				RequestID:       uuid.New().String(),
				RequestTime:     t,
			}

			w.Header().Set(models.ClientRequestID, correlationData.RequestID)

			rlog := log
			rlog = logutil.EnrichWithCorrelationData(rlog, correlationData)

			ctx := r.Context()
			ctx = context.WithValue(ctx, ContextKeyLog, rlog)
			ctx = context.WithValue(ctx, ContextKeyCorrelationData, correlationData)

			r = r.WithContext(ctx)

			rlog = rlog.WithFields(
				logrus.Fields{
					"request_method":      r.Method,
					"request_path":        r.URL.Path,
					"request_proto":       r.Proto,
					"request_remote_addr": r.RemoteAddr,
					"request_user_agent":  r.UserAgent(),
				},
			)

			defer func() {

				rlog.WithFields(
					logrus.Fields{
						"body_read_bytes":      r.Body.(*logReadCloser).bytes,
						"body_written_bytes":   w.(*logResponseWriter).bytes,
						"duration":             time.Since(t).Seconds(),
						"response_status_code": w.(*logResponseWriter).statusCode,
					},
				).Warn("sent response")

			}()
			h.ServeHTTP(w, r)
		})
	}
}
