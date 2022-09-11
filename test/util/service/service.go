package service

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"testing"

	"github.com/InVisionApp/go-health/v2"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	"github.com/faroshq/faros/pkg/service"
	"github.com/faroshq/faros/pkg/util/log"
	"github.com/stretchr/testify/require"

	utiltls "github.com/faroshq/faros/pkg/util/tls"
	databasetest "github.com/faroshq/faros/test/util/database"
)

// GetTestServer will return test server. It will accept config as it drives server
// functionality required in tests
func GetTestServer(ctx context.Context, config *config.Config, t *testing.T) *service.Service {
	log := log.GetLogger()

	store, err := databasetest.NewSQLLiteTestingStore(t)
	require.NoError(t, err)

	h := health.New()
	defer h.Stop()

	key, cert, err := GetTestCertificates()
	require.NoError(t, err)
	config.API.TLSKey = key
	config.API.TLSCert = cert

	// service will require TLS certificates to be present.

	ctrl, err := controller.New(log, config, store)
	require.NoError(t, err)

	service, err := service.New(ctx, log, config, ctrl, h)
	require.NoError(t, err)

	go func() {
		err = service.Run(ctx)
		require.NoError(t, err)
	}()

	return service
}

func GetTestCertificates() (keyByte []byte, certByte []byte, err error) {
	var signingKey *rsa.PrivateKey
	var signingCert *x509.Certificate

	name := "localhost"
	key, cert, err := utiltls.GenerateKeyAndCertificate(name, signingKey, signingCert, false, false)
	if err != nil {
		return
	}

	// key and cert in PKCS#8 PEM format for Azure Key Vault.
	return x509.MarshalPKCS1PrivateKey(key), cert[0].Raw, nil
}
