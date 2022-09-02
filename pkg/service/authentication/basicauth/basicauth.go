package basicauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/authentication"
	"github.com/faroshq/faros/pkg/service/middleware"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/file"
	"github.com/faroshq/faros/pkg/util/htpasswd"
	"github.com/faroshq/faros/pkg/validators"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

var _ authentication.Authentication = &BasicAuth{}

type BasicAuth struct {
	log    *logrus.Entry
	config *config.Config
	store  store.Store

	htpasswd htpasswd.HashedPasswords
}

func New(log *logrus.Entry, config *config.Config, store store.Store) (*BasicAuth, error) {
	b := &BasicAuth{
		log:    log,
		config: config,
		store:  store,
	}
	// if not enabled just return shallow provider. Authenticate method
	// will just proxy further in the chain
	if !b.Enabled() {
		return b, nil
	}

	exists, err := file.Exist(config.API.BasicAuthAuthenticationProviderFile)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("basic auth authentication provider file %s does not exist", config.API.BasicAuthAuthenticationProviderFile)
	}

	b.htpasswd, err = htpasswd.ParseHtpasswdFile(config.API.BasicAuthAuthenticationProviderFile)
	if err != nil {
		return nil, err
	}

	err = b.syncDatabase()
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (b *BasicAuth) Authenticate() func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log := middleware.GetLoggerFromRequest(r)
			ctx := r.Context()

			if !b.Enabled() {
				log.Warn("basic auth disabled")
				h.ServeHTTP(w, r)
				return
			}

			// Get the username and password from the request.
			username, password, ok := r.BasicAuth()
			if !ok {
				log.Error("basic auth header is not set")
			}
			if username == "" || password == "" {
				log.Error("username or password is empty")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// validate email
			// TODO: this is weak validator!
			err := validators.ValidateEmail(username)
			if err != nil {
				log.WithFields(logrus.Fields{"username": username}).Error("username is not email format")
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if hash, ok := b.htpasswd[username]; ok {
				if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
					log.Error("password not match")
					w.WriteHeader(http.StatusForbidden)
					return
				}
			}

			// if we here, we been authenticated with basic auth
			var user *models.User
			user, err = b.store.GetUser(ctx, models.User{
				Email:        username,
				ProviderName: models.AuthenticationProviderBasicAuth,
			})
			if err != nil && err == store.ErrRecordNotFound {
				user, err = b.store.CreateUser(ctx, models.User{
					Email:        username,
					ProviderName: models.AuthenticationProviderBasicAuth,
					PasswordHash: password,
				})
				if err != nil {
					log.WithError(err).Error("failed to create user")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				log.Infof("created user %s", user.Email)
			} else if err != nil {
				log.WithError(err).Error("failed to get user")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// at this point user object is set/created and authenticated
			// we still check just to be sure
			if user == nil {
				log.Error("user is nil. This should never happen!")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			session := &models.UserAccessSession{
				UserID: user.ID,
			}

			ctx = context.WithValue(ctx, middleware.ContextKeyUserAccessSession, session)
			r = r.WithContext(ctx)

			h.ServeHTTP(w, r)
		})
	}
}

func (b *BasicAuth) Enabled() bool {
	for _, provider := range b.config.API.AuthenticationProviders {
		if strings.EqualFold(provider, string(models.AuthenticationProviderBasicAuth)) {
			return true
		}
	}
	return false
}

// syncDatabase will sync users from file, and remove ones deleted.
func (b *BasicAuth) syncDatabase() error {
	ctx, _ := context.WithDeadline(context.Background(), time.Now().Add(time.Second*5))
	users, err := b.store.ListUsers(ctx, models.User{
		ProviderName: models.AuthenticationProviderBasicAuth,
	})
	if err != nil {
		return err
	}

	for _, user := range users {
		if _, ok := b.htpasswd[user.Email]; !ok {
			b.log.Infof("removing user %s", user.Email)
			if err := b.store.DeleteUser(ctx, user); err != nil {
				return err
			}
		}
	}

	return nil
}
