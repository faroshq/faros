package httputil

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

var (
	DefaultClient = &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Dial: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).Dial,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 120 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}

	DefaultInsecureClient = &http.Client{
		Timeout: 120 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Dial: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).Dial,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 120 * time.Second,
			ExpectContinueTimeout: 120 * time.Second,
			ForceAttemptHTTP2:     true,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
)
