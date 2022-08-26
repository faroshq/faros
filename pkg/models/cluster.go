package models

import "time"

type Cluster struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index,constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	Name   string        `json:"name" yaml:"name"`
	Config ClusterConfig `json:"config" yaml:"config"  gorm:"json"`
}

type ClusterConfig struct {
	APIServerURL  string `json:"apiServerUrl" yaml:"apiServerUrl"`
	AuthProxyURL  string `json:"authProxyUrl" yaml:"authProxyUrl"`
	AuthProxy     bool   `json:"authProxy" yaml:"authProxy"`
	RawKubeConfig string `json:"rawKubeConfig" yaml:"rawKubeConfig"`
}
