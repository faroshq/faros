package http

import (
	"net/http"
)

// JSONContentTypeRoundTripper is simple RT to inject the JSON content type
type JSONContentTypeRoundTripper struct {
	delegate http.RoundTripper
}

// NewClusterRoundTripper creates a new cluster aware round tripper
func NewJSONContentTypeRoundTripper(delegate http.RoundTripper) *JSONContentTypeRoundTripper {
	return &JSONContentTypeRoundTripper{
		delegate: delegate,
	}
}

func (c *JSONContentTypeRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Content-Type", "application/json")
	return c.delegate.RoundTrip(req)
}
