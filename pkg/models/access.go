package models

import "time"

type ClusterAccessSession struct {
	ID          string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
	NamespaceID string    `json:"namespaceId" yaml:"namespaceId" gorm:"index,constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`
	ClusterID   string    `json:"clusterId" yaml:"clusterId" gorm:"index,constraint:OnUpdate:CASCADE,OnDelete:CASCADE;"`

	Name string        `json:"name" yaml:"name"`
	TTL  time.Duration `json:"ttl" yaml:"ttl"`
}
