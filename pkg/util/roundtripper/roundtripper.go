package roundtripper

import (
	"net/http"
)

type RoundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTripNoop is noop roundtripper to be used for test or as a placeholder for
// other integrations
func (f RoundTripperFunc) RoundTripNoop(req *http.Request) (*http.Response, error) {
	return f(req)
}
