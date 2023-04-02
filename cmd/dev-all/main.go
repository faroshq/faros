package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/bootstrap"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controllers"
	devproxyclient "github.com/faroshq/faros/pkg/dev/client"
	"github.com/faroshq/faros/pkg/service"
)

func main() {

	klog.InitFlags(flag.CommandLine)

	flag.Parse()
	flag.Lookup("v").Value.Set("6")

	ctx := klog.NewContext(context.Background(), klog.NewKlogr())

	err := run(ctx)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {

	devVars := []string{
		"FAROS_OIDC_CLIENT_SECRET=ZXhhbXBsZS1hcHAtc2VjcmV0", //dev value hardcoded
		"FAROS_TLS_KEY_FILE=dev/server.pem",                 // go run ./hack/genkey -client localhost && 	mv localhost.* dev
		"FAROS_TLS_CERT_FILE=dev/server.pem",
		"FAROS_OIDC_ISSUER_URL=https://dex.dev.faros.sh",
		"FAROS_API_EXTERNAL_URL=https://faros.dev.faros.sh",
		"FAROS_CONTROLLERS_KUBECONFIG=dev/controller-kubeconfig",
		"FAROS_SKIP_TLS_VERIFY=true",
	}

	for _, v := range devVars {
		parts := strings.Split(v, "=")
		err := os.Setenv(parts[0], parts[1])
		if err != nil {
			return err
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// go run ./hack/genkey -client proxy-client && mv proxy-client.* dev
	// go run ./hack/genkey proxy && mv proxy.* dev
	clientAPI, err := devproxyclient.New("https://localhost:30443", "https://localhost:8443", "dev/proxy-client.crt", "dev/proxy-client.key", "faros-dev")
	if err != nil {
		return err
	}

	bootstrap, err := bootstrap.New(&cfg.FarosKCPConfig)
	if err != nil {
		return err
	}

	ctxT, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	klog.Info("Starting bootstrapper")
	err = bootstrap.Bootstrap(ctxT)
	if err != nil {
		return err
	}

	// Once bootstrap is done, we can extract the kubeconfig from the secret
	// for local development
	err = configureControllerConfig(ctx, cfg)
	if err != nil {
		return err
	}

	// reload config
	cfg, err = config.Load()
	if err != nil {
		return err
	}

	server, err := service.New(ctx, cfg)
	if err != nil {
		return err
	}

	controller, err := controllers.New(cfg)
	if err != nil {
		return err
	}

	go server.Run(ctx)
	go controller.Run(ctx)
	go clientAPI.Run(ctx)

	<-ctx.Done()
	return nil
}

func configureControllerConfig(ctx context.Context, cfg *config.Config) error {
	coreClientSet, err := kubernetes.NewForConfig(cfg.FarosKCPConfig.HostingClusterRestConfig)
	if err != nil {
		return err
	}

	secret, err := coreClientSet.CoreV1().Secrets(cfg.FarosKCPConfig.HostingClusterNamespace).Get(ctx, cfg.FarosKCPConfig.ControllerFarosConfigSecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	cluster, ok := secret.Data[cfg.FarosKCPConfig.ControllerClusterNameSecretKey]
	if !ok {
		return fmt.Errorf("cluster name not found in secret %s", cfg.FarosKCPConfig.ControllerFarosConfigSecretName)
	}
	os.Setenv("FAROS_CONTROLLERS_CLUSTER_NAME", string(cluster))

	kubeconfig := secret.Data[cfg.FarosKCPConfig.ControllerKubeConfigSecretKey]
	return os.WriteFile("dev/controller-kubeconfig", kubeconfig, 0644)
}
