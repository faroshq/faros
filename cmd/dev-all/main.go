package main

import (
	"context"
	"flag"
	"os"
	"strings"

	"github.com/faroshq/faros/pkg/config"
	devproxyclient "github.com/faroshq/faros/pkg/dev/client"
	"github.com/faroshq/faros/pkg/server"
	"k8s.io/klog/v2"
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
	}

	for _, v := range devVars {
		parts := strings.Split(v, "=")
		err := os.Setenv(parts[0], parts[1])
		if err != nil {
			return err
		}
	}

	ca, err := config.LoadAPI()
	if err != nil {
		return err
	}

	// go run ./hack/genkey -client proxy-client && mv proxy-client.* dev
	// go run ./hack/genkey proxy && mv proxy.* dev
	clientAPI, err := devproxyclient.New("https://localhost:30443", "https://localhost:8443", "dev/proxy-client.crt", "dev/proxy-client.key", "faros-dev")
	if err != nil {
		return err
	}

	server, err := server.New(ctx, ca)
	if err != nil {
		return err
	}

	go server.Run(ctx)
	go clientAPI.Run(ctx)

	<-ctx.Done()
	return nil
}
