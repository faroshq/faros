package error

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// CloudError represents a cloud error.
type CloudError struct {
	// The status code.
	StatusCode int `json:"-"`

	// An error response from the service.
	*CloudErrorBody `json:"error,omitempty"`
}

func (err *CloudError) Error() string {
	var body string

	if err.CloudErrorBody != nil {
		body = ": " + err.CloudErrorBody.String()
	}

	return fmt.Sprintf("%d%s", err.StatusCode, body)
}

// CloudErrorBody represents the body of a cloud error.
type CloudErrorBody struct {
	// An identifier for the error. Codes are invariant and are intended to be consumed programmatically.
	Code string `json:"code"`

	// A message describing the error, intended to be suitable for display in a user interface.
	Message string `json:"message"`

	//A list of additional details about the error.
	Details []CloudErrorBody `json:"details,omitempty"`
}

func (b *CloudErrorBody) String() string {
	var details string

	if len(b.Details) > 0 {
		details = " Details: "
		for i, innerErr := range b.Details {
			details += innerErr.String()
			if i < len(b.Details)-1 {
				details += ", "
			}
		}
	}

	return fmt.Sprintf("%s: %s%s", b.Code, b.Message, details)
}

// CloudErrorCodes
var (
	CloudErrorCodeBadRequest            = "BadRequest"
	CloudErrorCodeInternalServerError   = "InternalServerError"
	CloudErrorCodeInvalidParameter      = "InvalidParameter"
	CloudErrorCodeInvalidRequestContent = "InvalidRequestContent"
	CloudErrorCodeInvalidResource       = "InvalidResource"
	CloudErrorCodeNotFound              = "NotFound"
	CloudErrorCodeForbidden             = "Forbidden"
	CloudErrorCodeConflict              = "Conflict"
	CloudErrorCodeUnauthorized          = "Unauthorized"
)

// NewCloudError returns a new CloudError
func NewCloudError(statusCode int, code, message string, a ...interface{}) *CloudError {
	if message == "" {
		// Fallback
		message = code
	}
	return &CloudError{
		StatusCode: statusCode,
		CloudErrorBody: &CloudErrorBody{
			Code:    code,
			Message: fmt.Sprintf(message, a...),
		},
	}
}

// WriteError constructs and writes a CloudError to the given ResponseWriter
func WriteError(w http.ResponseWriter, statusCode int, code, target, message string, a ...interface{}) {
	WriteCloudError(w, NewCloudError(statusCode, code, message, a...))
}

// WriteCloudError writes a CloudError to the given ResponseWriter
func WriteCloudError(w http.ResponseWriter, err *CloudError) {
	w.WriteHeader(err.StatusCode)
	e := json.NewEncoder(w)
	e.SetIndent("", "    ")
	_ = e.Encode(err)
}

// IsCloudError unmarshals errors and check content. It should not be used in
// server side. Only client side like CLI.
func IsCloudError(err error) (bool, *CloudError) {
	return isCloudError(err)
}

// IsSpecificCloudError unmarshals errors and check content. It should not be used in
// server side. Only client side like CLI.
func IsSpecificCloudError(err error, code string) bool {
	isError, cErr := isCloudError(err)
	if isError {
		return isSpecificCloudError(cErr, code)
	}
	return isError
}

func isCloudError(err error) (bool, *CloudError) {
	var cErr *CloudError
	uErr := json.Unmarshal([]byte(err.Error()), &cErr)
	if uErr != nil {
		return false, nil
	}
	return true, cErr
}

func isSpecificCloudError(err *CloudError, code string) bool {
	return err.Code == code
}
