package relay

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

func TestCatalogCoversEveryPinnedProfile(t *testing.T) {
	seen := make(map[string]bool)
	for _, item := range catalog.Profiles {
		if item.ID == "" || item.Label == "" || seen[item.ID] {
			t.Fatalf("invalid or duplicate profile: %+v", item)
		}
		seen[item.ID] = true
		switch item.Family {
		case "chromium", "firefox", "safari", "okhttp", "application":
		default:
			t.Fatalf("unknown profile family: %+v", item)
		}
		if _, local := customTransportProfiles[item.ID]; !local {
			if _, available := profiles.MappedTLSClients[item.ID]; !available {
				t.Fatalf("unavailable profile: %s", item.ID)
			}
		}
	}
	for name := range profiles.MappedTLSClients {
		if !seen[name] {
			t.Errorf("missing upstream profile: %s", name)
		}
	}
	for name := range customTransportProfiles {
		if !seen[name] {
			t.Errorf("missing local profile: %s", name)
		}
	}
}

func TestEveryAdvertisedProfileCanUseVerifiedTLS(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	pool := newClientPool()
	defer pool.close()
	pool.create = func(name string, websocket bool, proxy string) (tls_client.HttpClient, error) {
		return newProfileClient(name, websocket, roots, proxy)
	}
	target, _ := url.Parse(origin.URL)
	host, port, _ := net.SplitHostPort(target.Host)
	for _, item := range catalog.Profiles {
		for _, websocket := range []bool{false, true} {
			mode := "negotiated"
			if websocket {
				mode = "http1"
			}
			t.Run(item.ID+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				request := &incomingRequest{Method: "GET", RequestURI: "/"}
				if websocket {
					request.Headers = []header{{"Connection", "Upgrade"}, {"Upgrade", "websocket"}}
				}
				response, err := forwardContext(ctx, pool, request, routeMetadata{Scheme: "https", Host: host, Port: port, Profile: item.ID})
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if websocket && response.Proto != "HTTP/1.1" {
					t.Fatalf("Upgrade negotiated %s", response.Proto)
				}
				body, err := io.ReadAll(response.Body)
				if err != nil || response.StatusCode != 200 || string(body) != "ok" {
					t.Fatalf("response = %d %q, %v", response.StatusCode, body, err)
				}
			})
		}
	}
}
