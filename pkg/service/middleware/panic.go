package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

func Panic(log *logrus.Entry) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if e := recover(); e != nil {
					// https://github.com/golang/go/issues/23643
					// This panics on streaming so we catch and ignore :/
					if e == http.ErrAbortHandler {
						return // ignore
					}
					log.Errorf("panic: %#v\n%s\n", e, string(debug.Stack()))
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()

			h.ServeHTTP(w, r)
		})
	}
}
