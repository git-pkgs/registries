//go:build !tinygo

package fetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rs/dnscache"
)

const (
	dnsRefreshInterval    = 5 * time.Minute
	dialTimeout           = 30 * time.Second
	dialKeepAlive         = 30 * time.Second
	responseHeaderTimeout = 60 * time.Second
	maxIdleConns          = 100
	maxIdleConnsPerHost   = 10
	idleConnTimeout       = 90 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
)

func (f *Fetcher) initHTTPClient() {
	resolver := &dnscache.Resolver{}
	stop := make(chan struct{})
	f.stop = stop
	go func() {
		ticker := time.NewTicker(dnsRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				resolver.Refresh(true)
			case <-stop:
				return
			}
		}
	}()

	dialer := &net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: dialKeepAlive,
	}
	f.client = &http.Client{
		Timeout: httpClientTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := resolver.LookupHost(ctx, host)
				if err != nil {
					return nil, err
				}
				// Dial the checked IP directly to prevent DNS rebinding.
				var lastErr error
				for _, ip := range ips {
					if parsed := net.ParseIP(ip); parsed != nil {
						if err := f.ipChecker.Check(host, parsed); err != nil {
							lastErr = err
							continue
						}
					}
					conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
					if derr == nil {
						return conn, nil
					}
					lastErr = derr
				}
				if lastErr == nil {
					return nil, fmt.Errorf("no IPs resolved for %s", host)
				}
				return nil, fmt.Errorf("dialing %s: %w", host, lastErr)
			},
			MaxIdleConns:          maxIdleConns,
			MaxIdleConnsPerHost:   maxIdleConnsPerHost,
			IdleConnTimeout:       idleConnTimeout,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}
