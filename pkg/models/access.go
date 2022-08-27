package models

import (
	"time"
)

type ClusterAccessSession struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index,constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ClusterID   string    `json:"clusterId" yaml:"clusterId" gorm:"index,constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	Name string        `json:"name" yaml:"name"`
	TTL  time.Duration `json:"ttl" yaml:"ttl"`

	// Token is access token from user. Not stored in database, used only for authentication
	Token string `json:"token" yaml:"token" gorm:"-"`
	// EncryptedToken is encrypted access token from user. Stored in database, never returned to user
	// Stored in base64 over encrypted with bcrypt
	EncryptedToken string `json:"-" yaml:"-"`
}

type KubeConfig struct {
	KubeConfig string `json:"kubeconfig" yaml:"kubeconfig"`
}
