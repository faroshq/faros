package roundtripper

import (
	"net/http"
)

type RoundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTripperFunc is noop roundtripper to be used for test or as a placeholder for
// other integrations
func (f RoundTripperFunc) RoundTripperFunc(req *http.Request) (*http.Response, error) {
	return f(req)
}
