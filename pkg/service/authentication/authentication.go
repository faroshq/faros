package authentication

import (
	"net/http"
)

// Currently we have 2 authentication modes:
// User authentication - user is authenticated by providers they use to access API.
// Primarily used for authentication to API and is backed By UserAccessSession object
//
// Kubectl authentication - user is authenticated by kubectl. It works based on
// ClusterAccessSession object initiated/owned by user.
//
// Depending on authentication mode, corresponding UserAccessSession or ClusterAccessSession
// object will be stored in request header and passed down the chain. Its handlers
// responsible for checking if user is authenticated or not based on session they handling.
// Cluster specific handlers should decline any requests with user authentication session
// and other way around.
//
// Authentications are implemented as middleware, and can be chained together.

type Authentication interface {
	// Authenticate checks if user is authenticated. If not, it returns error.
	// If user is authenticated, it returns nil.
	Authenticate() func(http.Handler) http.Handler
	// Enabled returns true if authenticator is enabled.
	Enabled() bool
}

// BasicAuth is a basic authentication implementation.
// When enabled, htpasswd file is used to authenticate users. Users are synced to database
// on login, and stored
// Example generating htpasswd file. Please note -B as we support ONLY Bcrypt algorithm for now
// $ htpasswd -B -b -c $(pwd)/secrets/htpasswd admin@faros.sh farosfaros
// $ htpasswd -B -b -c $(pwd)/secrets/htpasswd <username_as_email> <password>
