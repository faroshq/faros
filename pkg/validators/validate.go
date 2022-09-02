package validators

import (
	"errors"
	"strings"
)

var errInvalidEmail = errors.New("invalid email")

// validateEmail checks we actually have been given an email address,
// TODO: Make this better
func ValidateEmail(email string) error {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return errInvalidEmail
	}
	return nil
}
