package models

import (
	"time"
)

type AuthenticationProvider string

var AuthenticationProviderBasicAuth AuthenticationProvider = "basicauth"

// User provides information about a user. Stored in database.
// In basic auth mode, the username is the email address and records are reflected
// from configuration file to database on startup
type User struct {
	ID        string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" yaml:"updatedAt"`

	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	Email string `json:"email,omitempty" yaml:"email,omitempty" gorm:"uniqueIndex"`

	// external users attributes
	ProviderName AuthenticationProvider `json:"providerName,omitempty" yaml:"providerName,omitempty" gorm:"index"`

	// internal user attributes
	PasswordHash string `json:"-" yaml:"-"`
}

// UserAccessSession is object that represents authenticated user session. Stored
// in request context when user is authenticated. Used to store all session related data.
// In the future we can externalize to external session management system if we need to
type UserAccessSession struct {
	NamespaceID string `json:"namespaceId" yaml:"namespaceId" gorm:"index"`
	ClusterID   string `json:"clusterId" yaml:"clusterId" gorm:"index"`
	UserID      string `json:"userId" yaml:"userId" gorm:"index"`
}

// ClusterAccessSession is object that represents cluster access session. Stored in
// database once session is created
type ClusterAccessSession struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey,uniqueIndex"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index"`
	ClusterID   string    `json:"clusterId" yaml:"clusterId" gorm:"index"`
	UserID      string    `json:"userId" yaml:"userId" gorm:"index"`

	Name string        `json:"name,omitempty" yaml:"name,omitempty"`
	TTL  time.Duration `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	// Expired is true if the session has expired
	Expired bool `json:"expired,omitempty" yaml:"expired,omitempty"`

	// Token is access token from user. Not stored in database, used only for authentication
	Token string `json:"token,omitempty" yaml:"token,omitempty" gorm:"-"`
	// EncryptedToken is encrypted access token from user. Stored in database, never returned to user
	// Stored in base64 over encrypted with bcrypt
	EncryptedToken string `json:"-" yaml:"-"`
}

type KubeConfig struct {
	KubeConfig string `json:"kubeconfig,omitempty" yaml:"kubeconfig,omitempty"`
}

// ClusterRegistrationToken is object that represents cluster registration token. Stored in
// database once session is created
type ClusterRegistrationToken struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey,uniqueIndex"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index"`
	ClusterName string    `json:"clusterName,omitempty" yaml:"clusterName,omitempty"`

	// Token is access token from user. Not stored in database, used only for authentication
	Token string `json:"token,omitempty" yaml:"token,omitempty" gorm:"-"`
	// EncryptedToken is encrypted access token from user. Stored in database, never returned to user
	// Stored in base64 over encrypted with bcrypt
	EncryptedToken string `json:"-" yaml:"-"`
}
