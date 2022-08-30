package models

import "time"

type Namespace struct {
	ID        string    `json:"id" yaml:"id" gorm:"primaryKey;uniqueIndex"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt time.Time `json:"updatedAt" yaml:"updatedAt"`

	Name        string `json:"name" yaml:"name" gorm:"uniqueIndex"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}
