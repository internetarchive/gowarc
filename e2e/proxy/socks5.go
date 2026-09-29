package proxy

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/things-go/go-socks5"
)

// socks5.RuleSet that permits all connections and increments a counter on every request.
type countingRuleSet struct {
	count atomic.Int64
}

func (r *countingRuleSet) Allow(ctx context.Context, req *socks5.Request) (context.Context, bool) {
	r.count.Add(1)
	return ctx, true
}

// NewSOCKS5Proxy starts a SOCKS5 proxy on a random port and returns its
// address, a connection counter, and a cleanup function that stops the server.
func NewSOCKS5Proxy(t *testing.T) (addr string, count *atomic.Int64) {
	t.Helper()

	rule := &countingRuleSet{}
	server := socks5.NewServer(socks5.WithRule(rule))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for proxy: %v", err)
	}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, net.ErrClosed) {
			panic(err)
		}
	}()

	t.Cleanup(func() { listener.Close() })

	return listener.Addr().String(), &rule.count
}
