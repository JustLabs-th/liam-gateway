package integration

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"llm-gateway/internal/config"
	"llm-gateway/internal/identity"
	httptransport "llm-gateway/internal/transport/http"
)

// Explicit opt-in fixture for the browser test. No external providers or credits.
func TestBrowserFixture(t *testing.T) {
	stop := os.Getenv("BROWSER_FIXTURE_STOP")
	if stop == "" {
		t.Skip("browser fixture is opt-in")
	}
	f := newFixture(t, func(c *config.Config) { c.PublicURL = "http://127.0.0.1:18080"; c.WebURL = "http://127.0.0.1:13000" })
	f.server.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:18080")
	if e != nil {
		t.Fatal(e)
	}
	auth, e := identity.New(t.Context(), f.c, f.rt.Store)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(httptransport.Router(f.c, f.rt.Store, f.rt.Quota, auth, f.rt.Archive))
	s.Listener = listener
	s.Start()
	defer s.Close()
	fmt.Println("BROWSER_FIXTURE_READY")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	for {
		if _, e := os.Stat(stop); e == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}
