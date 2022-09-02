package bcrypt

import (
	"golang.org/x/crypto/bcrypt"
)

// HashPassword password, used when creating a new internal account or updating an existing one
func HashPassword(plainText string) ([]byte, error) {
	return generatePasswordHash([]byte(plainText))
}

func generatePasswordHash(password []byte) ([]byte, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return hashedPassword, nil
}
