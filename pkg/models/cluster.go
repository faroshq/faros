package models

import "time"

type Cluster struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index"`

	Name   string        `json:"name,omitempty" yaml:"name,omitempty"`
	Config ClusterConfig `json:"config,omitempty" yaml:"config,omitempty" gorm:"json"`
}

type ClusterConfig struct {
	RawKubeConfig string `json:"rawKubeConfig,omitempty" yaml:"rawKubeConfig,omitempty"`
}
