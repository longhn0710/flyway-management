package identityprovider

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOIDCDiscoveryCancellationDoesNotWaitForUnresponsiveProvider(t *testing.T) {
	entered, stop := make(chan struct{}), make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer server.Close()
	defer close(stop)
	caFile := t.TempDir() + "/ca.pem"
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := NewOIDC(ctx, OIDCConfig{Issuer: server.URL, ClientID: "client", ClientSecret: "synthetic-secret", RedirectURL: "https://manager.example/callback", CAFile: caFile})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("discovery did not reach the trusted fixture")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled discovery did not preserve its cancellation cause")
		}
		if strings.Contains(err.Error(), "synthetic-secret") {
			t.Fatal("cancelled discovery exposed the client secret")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled discovery kept waiting for its provider")
	}
}
