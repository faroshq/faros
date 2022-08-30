package models

import (
	"time"
)

type ClusterAccessSession struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey,uniqueIndex"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index"`
	ClusterID   string    `json:"clusterId" yaml:"clusterId" gorm:"index"`

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
