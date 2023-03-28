package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/service"
	utilhttp "github.com/faroshq/faros/pkg/util/http"
	"github.com/faroshq/faros/pkg/util/version"
)

func main() {

	klog.InitFlags(flag.CommandLine)
	flag.Parse()
	ctx := klog.NewContext(context.Background(), klog.NewKlogr())

	err := run(ctx)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	klog.Info("Version: ", version.GetVersion().Version)

	devVars := []string{
		"FAROS_TLS_KEY_FILE=dev/server.pem", // go run ./hack/genkey -client localhost && 	mv localhost.* dev
		"FAROS_TLS_CERT_FILE=dev/server.pem",
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
		return fmt.Errorf("failed to load config: %w", err)
	}

	server, err := service.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	go server.Run(ctx)
	time.Sleep(1 * time.Second)

	cli := utilhttp.GetInsecureClient()
	resp, err := cli.Get("https://localhost:8443/swagger.json")
	if err != nil {
		return err
	}
	b, err := io.ReadAll(resp.Body)
	// b, err := ioutil.ReadAll(resp.Body)  Go.1.15 and earlier
	if err != nil {
		log.Fatalln(err)
	}

	return os.WriteFile("swagger.json", b, 0644)
}
