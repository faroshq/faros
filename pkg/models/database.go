package models

import (
	"time"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

// User is a model for the User database model storing the user information.
// It extends the User CRD with additional fields for database storage.
type User struct {
	ID        string    `json:"id" yaml:"id" gorm:"primaryKey"`
	CreatedAt time.Time `json:"createdAt" yaml:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt" yaml:"updatedAt"`
	// Email is the email of the user. Must be unique.
	Email string `json:"email" yaml:"email" gorm:"uniqueIndex"`

	User tenancyv1alpha1.User `json:"user" yaml:"user" gorm:"json"`
}
