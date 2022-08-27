package auth

import (
	"context"
	"encoding/base64"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

// Authenticator authenticator is used to authenticate internally stored authentication resources
type Authenticator interface {
	HashPassword(plainText string) ([]byte, error)
	AuthenticateClusterAccessSession(ctx context.Context, req *models.ClusterAccessSession, token string) (bool, error)
}

// Static check
var _ Authenticator = &AuthenticatorImpl{}

type AuthenticatorImpl struct {
	log    *logrus.Entry
	store  store.Store
	config *config.Config
}

func NewAuthenticator(log *logrus.Entry, cfg *config.Config, store store.Store) (*AuthenticatorImpl, error) {
	da := &AuthenticatorImpl{
		log:    log,
		config: cfg,
		store:  store,
	}

	return da, nil
}

func generatePasswordHash(password []byte) ([]byte, error) {
	hashedPassword, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return hashedPassword, nil
}

// Authenticate is used to authenticate cluster access sessions
func (a *AuthenticatorImpl) AuthenticateClusterAccessSession(ctx context.Context, req *models.ClusterAccessSession, token string) (bool, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(req.EncryptedToken)
	if err != nil {
		a.log.WithError(err).Error("failed to decode token")
		return false, ErrorAuthenticationFailed
	}

	if bcrypt.CompareHashAndPassword(decoded, []byte(token)); err == nil {
		return true, nil
	}

	return false, ErrorAuthenticationFailed
}

// HashPassword password, used when creating a new internal account or updating an existing one
func (a *AuthenticatorImpl) HashPassword(plainText string) ([]byte, error) {
	return generatePasswordHash([]byte(plainText))
}
