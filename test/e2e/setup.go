package e2e

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"testing"
	"time"

	"github.com/InVisionApp/go-health/v2"
	"github.com/faroshq/faros/pkg/client"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/supervisor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/faroshq/faros/pkg/util/htpasswd"
	logutil "github.com/faroshq/faros/pkg/util/log"
	"github.com/faroshq/faros/test/util/database"
	serviceutil "github.com/faroshq/faros/test/util/service"
)

var (
	log           *logrus.Entry
	contextCancel context.CancelFunc
	testingT      *testing.T

	Username = "test@faros.sh"
	Password = "farosfaros"

	APIClient *client.Client
)

var _ = BeforeSuite(func() {
	log = logutil.GetLogger()
	log.Info("BeforeSuite")
	t := testingT

	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(10 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	contextCancel = cancel

	config, err := config.Load(false)
	require.NoError(t, err)

	// configure certs
	key, cert, err := serviceutil.GetTestCertificates()
	require.NoError(t, err)
	config.API.TLSKey = key
	config.API.TLSCert = cert

	// configure htpasswd
	config.API.AuthenticationProviders = []string{"basicauth"}

	// create test htpasswd file in temp store
	dir, err := os.MkdirTemp("/tmp", "basicauth-test-faros")
	assert.NoError(t, err)
	htpasswdFile := filepath.Join(dir, "htpasswd")
	err = htpasswd.SetPassword(htpasswdFile, Username, Password, htpasswd.HashBCrypt)
	assert.NoError(t, err)
	config.API.BasicAuthAuthenticationProviderFile = htpasswdFile

	store, err := database.NewSQLLiteTestingStore(t)
	require.NoError(t, err)

	err = setupStack(ctx, log, config, store)
	require.NoError(t, err)
})

func setupStack(ctx context.Context, log *logrus.Entry, config *config.Config, store store.Store) error {
	h := health.New()
	h.AddCheck(&health.Config{
		Name:     "database",
		Interval: time.Second * 5,
		Checker:  store,
		Fatal:    true,
	})
	go h.Start()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	stop := make(chan struct{})
	done := make(chan struct{})

	sp, err := supervisor.New(ctx, log, config, store, h)
	if err != nil {
		return err
	}

	go sp.Run(ctx, stop, done)

	return nil
}

var _ = AfterSuite(func() {
	log.Info("AfterSuite")

	contextCancel()
	time.Sleep(time.Second * 5) // Hack to let controller stop fully
})
