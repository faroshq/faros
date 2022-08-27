package models

import "time"

var ClientClientRequestID = "faros-client-request-id"
var ClientRequestID = "faros-request-id"

// CorrelationData represents any data, used for metrics or tracing.
type CorrelationData struct {

	// ClientRequestID contains value of client-request-id
	ClientRequestID string `json:"clientRequestId,omitempty"`

	// RequestID contains value of request-id
	RequestID string `json:"requestID,omitempty"`

	// RequestTime is the time that the request was received
	RequestTime time.Time `json:"requestTime,omitempty"`
}
