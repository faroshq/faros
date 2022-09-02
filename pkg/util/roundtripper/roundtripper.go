package roundtripper

import (
	"net/http"
)

type RoundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip is noop roundtripper to be used for test or as a placeholder for
// other integrations
func (f RoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type BasicAuthRT struct {
	cli      *http.Client
	username string
	password string
}

func NewBasicAuthRT(username string, password string, cli *http.Client) (*BasicAuthRT, error) {
	return &BasicAuthRT{
		cli:      cli,
		username: username,
		password: password,
	}, nil
}

func (b *BasicAuthRT) RoundTripper(r *http.Request) (*http.Response, error) {
	r.SetBasicAuth(b.username, b.password)
	return b.cli.Do(r)
}
