package restconfig

import (
	"context"
	"fmt"
	"net"
	"time"

	"k8s.io/client-go/rest"
)

func DialContext(restConfig *rest.Config) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" {
			return nil, fmt.Errorf("unimplemented network %q", network)
		}

		d := &dialer{}
		return d.DialContext(ctx, network, address)
	}
}

type dialer struct{}

func (d *dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext(ctx, network, address)
}
