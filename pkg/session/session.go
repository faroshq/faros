package session

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/recover"
)

// SessionManager is slim runnable manager to maintain session TTLs.
// It is responsible for deleting marking session as expired,
// and deleting them after 24 hours(if not configured otherwise) after.

type Interface interface {
	Run(context.Context) error
}

type sessionManager struct {
	log    *logrus.Entry
	config *config.ServerConfig
	store  store.Store
}

func New(log *logrus.Entry, config *config.ServerConfig, store store.Store) (*sessionManager, error) {
	return &sessionManager{
		log:    log.WithField("component", "session"),
		config: config,
		store:  store,
	}, nil
}

func (m *sessionManager) Run(ctx context.Context) error {
	defer recover.Panic(m.log)

	ticker := time.NewTicker(m.config.Controller.SessionExpireInterval)
	defer ticker.Stop()

	for {
		sessions, err := m.store.ListAllClusterAccessSessions(ctx)
		if err != nil {
			return err
		}

		for _, session := range sessions {
			// cancel go routines before next loop.
			// overall this is time-bomb from performance perspective. Fix this later.
			ctx, _ := context.WithDeadline(ctx, time.Now().Add(m.config.Controller.SessionExpireInterval))
			go func(ctx context.Context, session models.ClusterAccessSession) {
				err := m.sessionExpire(ctx, session)
				if err != nil {
					m.log.Errorf("error expiring session %s: %s", session.ID, err)
				}
				err = m.sessionPurge(ctx, session)
				if err != nil {
					m.log.Errorf("error purging session: %s", err)
				}
			}(ctx, session)
		}

		select {
		case <-ctx.Done():
			m.log.Info("stopped service")
			return nil
		case <-ticker.C:
		}
	}
}

func (m *sessionManager) sessionExpire(ctx context.Context, session models.ClusterAccessSession) error {
	if session.Expired {
		return nil
	}
	if session.CreatedAt.Add(session.TTL).Before(time.Now()) {
		m.log.WithFields(logrus.Fields{
			"session_id":           session.ID,
			"session_cluster_id":   session.ClusterID,
			"session_namespace_id": session.NamespaceID,
			"session_name":         session.Name,
		}).Debug("expiring session")
		session.Expired = true
		_, err := m.store.UpdateClusterAccessSession(ctx, session)
		return err
	}
	return nil
}

func (m *sessionManager) sessionPurge(ctx context.Context, session models.ClusterAccessSession) error {
	if session.CreatedAt.Add(session.TTL).Add(m.config.Controller.SessionPurgeTTL).Before(time.Now()) {
		m.log.WithFields(logrus.Fields{
			"session_id":           session.ID,
			"session_cluster_id":   session.ClusterID,
			"session_namespace_id": session.NamespaceID,
			"session_name":         session.Name,
		}).Debug("purging session")
		return m.store.DeleteClusterAccessSession(ctx, session)
	}
	return nil
}
