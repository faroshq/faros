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

		d := &d{}
		return d.DialContext(ctx, network, address)
	}
}

type d struct{}

func (d *d) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{
		Timeout:   time.Minute,
		KeepAlive: time.Minute,
	}).DialContext(ctx, network, address)
}
