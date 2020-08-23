package network

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"context"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"testing"

	"go.uber.org/zap"

	"github.com/faroshq/faros/pkg/util/logger"
)

type fakeClient struct {
	resp *http.Response
	err  error
}

func (c *fakeClient) Do(req *http.Request) (*http.Response, error) {
	return c.resp, c.err
}

func TestNetwork(t *testing.T) {
	urltocheck := "https://not-used-in-test.io"
	tests := []struct {
		name    string
		cli     *fakeClient
		wantErr bool
	}{
		{
			name: "200 ok",
			cli: &fakeClient{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Body:       ioutil.NopCloser(&bytes.Buffer{}),
				},
			},
		},
		{
			name: "400 bad request",
			cli: &fakeClient{
				resp: &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       ioutil.NopCloser(&bytes.Buffer{}),
				},
			},
		},
		{
			name: "unreachable error",
			cli: &fakeClient{
				err: &url.Error{
					URL: urltocheck,
					Err: &net.OpError{
						Err: os.NewSyscallError("socket", syscall.ENETUNREACH),
					},
				},
			},
			wantErr: true,
		},
		{
			name:    "timeout",
			cli:     &fakeClient{err: context.DeadlineExceeded},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := logger.GetZapLoggerInstance("", zap.InfoLevel)
			r := &Network{
				log: logger.NewLogRLogger(log),
			}

			if err := r.check(tt.cli, urltocheck); (err != nil) != tt.wantErr {
				t.Errorf("InternetChecker.check() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
