package models

import "time"

type Namespace struct {
	ID        string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt" grom:"index"`
	UpdatedAt time.Time `json:"updatedAt" yaml:"updatedAt"`

	Name        string `json:"name" yaml:"name" gorm:"primaryKey"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}
