package relay

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

func TestBundledProfilesAreAvailable(t *testing.T) {
	pool := newClientPool()
	defer pool.close()
	for _, profile := range []string{
		"chrome_152",
		"chrome_152_cft",
		"chrome_146",
		"chrome_144",
		"firefox_148",
		"firefox_147",
		"safari_ios_18_5",
		"safari_ios_26_0",
		"okhttp4_android_13",
	} {
		_, release, err := pool.acquire(profile, "http://fixture.test:80", false)
		if err != nil {
			t.Fatalf("profile %q is unavailable: %v", profile, err)
		}
		release()
	}
}

func TestVerifiedHTTPSWithHTTP1AndHTTP2Origins(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "HTTP/1.1"
		if h2 {
			name = "HTTP/2.0"
		}
		t.Run(name, func(t *testing.T) {
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Proto != name {
					t.Errorf("origin protocol = %s, want %s", r.Proto, name)
				}
				w.Header().Set("Location", "/login")
				w.Header().Add("Set-Cookie", "one=1; Path=/")
				w.Header().Add("Set-Cookie", "two=2; HttpOnly")
				w.WriteHeader(302)
				_, _ = io.WriteString(w, "redirect")
			}))
			target.EnableHTTP2 = h2
			target.StartTLS()
			defer target.Close()
			roots := x509.NewCertPool()
			roots.AddCert(target.Certificate())
			pool := newClientPool()
			defer pool.close()
			pool.create = func(profile string, websocket bool) (tls_client.HttpClient, error) {
				return newProfileClient(profile, websocket, roots)
			}
			targetURL, _ := url.Parse(target.URL)
			host, port, _ := net.SplitHostPort(targetURL.Host)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := forwardContext(ctx, pool, &incomingRequest{Method: "GET", RequestURI: "/", Headers: []header{{Name: "Host", Value: targetURL.Host}}},
				routeMetadata{Scheme: "https", Host: host, Port: port, Profile: "chrome_152_cft"})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.Proto != name || response.StatusCode != 302 || len(response.Header.Values("Set-Cookie")) != 2 {
				t.Fatalf("lost origin behavior: %s %d %#v", response.Proto, response.StatusCode, response.Header)
			}
		})
	}
}

func TestClientPoolPartitionsOriginsAndProtocols(t *testing.T) {
	pool := newClientPool()
	defer pool.close()
	first, release, err := pool.acquire("chrome_152_cft", "https://one.test:443", false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, test := range []struct {
		profile, origin string
		websocket, same bool
	}{
		{"chrome_152_cft", "https://ONE.test:443", false, true},
		{"chrome_152_cft", "https://two.test:443", false, false},
		{"chrome_152_cft", "https://one.test:443", true, false},
		{"chrome_152", "https://one.test:443", false, false},
	} {
		client, release, err := pool.acquire(test.profile, test.origin, test.websocket)
		if err != nil {
			t.Fatal(err)
		}
		release()
		if (client == first) != test.same {
			t.Fatalf("incorrect client reuse for %+v", test)
		}
	}
}

func TestClientPoolEvictsOnlyIdleClients(t *testing.T) {
	pool := newClientPool()
	defer pool.close()
	pool.capacity = 2
	first, releaseFirst, err := pool.acquire("chrome_152_cft", "https://one.test:443", false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseFirst()
	_, releaseSecond, err := pool.acquire("chrome_152_cft", "https://two.test:443", false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if _, _, err := pool.acquire("chrome_152_cft", "https://three.test:443", false); !errors.Is(err, errClientCapacity) {
		t.Fatalf("active clients must not be evicted: %v", err)
	}
	releaseSecond()
	releaseSecond() // Closing a body twice must not make its lease negative.
	_, releaseThird, err := pool.acquire("chrome_152_cft", "https://three.test:443", false)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseThird()
	stillFirst, releaseAgain, err := pool.acquire("chrome_152_cft", "https://one.test:443", false)
	if err != nil {
		t.Fatal(err)
	}
	releaseAgain()
	if stillFirst != first || len(pool.clients) != 2 {
		t.Fatal("eviction replaced an active client or exceeded the bound")
	}
	pool.close()
	if _, _, err := pool.acquire("chrome_152_cft", "https://one.test:443", false); err == nil {
		t.Fatal("closed pool accepted a new lease")
	}
}

func TestForwardHoldsClientLeaseUntilBodyCloses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "body")
	}))
	defer target.Close()
	pool := newClientPool()
	defer pool.close()
	pool.capacity = 1
	response, err := forwardURL(t, context.Background(), pool, target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, _, err := pool.acquire("chrome_152_cft", "https://other.test:443", false); !errors.Is(err, errClientCapacity) {
		t.Fatalf("open body lost its lease: %v", err)
	}
	_ = response.Body.Close()
	_, release, err := pool.acquire("chrome_152_cft", "https://other.test:443", false)
	if err != nil {
		t.Fatalf("closed body retained its lease: %v", err)
	}
	release()
}

func TestProductionClientRejectsUntrustedTLSAndReleasesLease(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("untrusted TLS origin received an HTTP request")
	}))
	defer target.Close()
	pool := newClientPool()
	defer pool.close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := forwardURL(t, ctx, pool, target.URL)
	if response != nil {
		_ = response.Body.Close()
	}
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("wanted certificate trust failure, got %v", err)
	}
	for _, entry := range pool.clients {
		if entry.users != 0 {
			t.Fatal("failed request retained its client lease")
		}
	}
}

func TestSlowTLSOriginDoesNotBlockUnrelatedOrigin(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(fmt.Sprintf("warm=%v", warm), func(t *testing.T) {
			pool := newClientPool()
			defer pool.close()
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "healthy")
			}))
			defer healthy.Close()
			if warm {
				response, err := forwardURL(t, context.Background(), pool, healthy.URL)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			stalled, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer stalled.Close()
			handshakeStarted := make(chan struct{})
			go func() {
				conn, err := stalled.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.ReadFull(conn, make([]byte, 1)); err == nil {
					close(handshakeStarted)
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				response, err := forwardURL(t, ctx, pool, "https://"+stalled.Addr().String())
				if response != nil {
					_ = response.Body.Close()
				}
				finished <- err
			}()
			defer func() {
				cancel()
				select {
				case err := <-finished:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("stalled TLS did not honor cancellation: %v", err)
					}
				case <-time.After(time.Second):
					t.Error("stalled TLS did not stop")
				}
			}()
			select {
			case <-handshakeStarted:
			case <-time.After(time.Second):
				t.Fatal("stalled TLS handshake did not start")
			}
			healthyContext, cancelHealthy := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancelHealthy()
			response, err := forwardURL(t, healthyContext, pool, healthy.URL)
			if err != nil {
				t.Fatalf("unrelated origin blocked behind TLS handshake: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "healthy" {
				t.Fatalf("healthy response = %q, %v", body, err)
			}
		})
	}
}

func forwardURL(t *testing.T, ctx context.Context, pool *clientPool, target string) (*fhttp.Response, error) {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	return forwardContext(ctx, pool,
		&incomingRequest{Method: "GET", RequestURI: "/", Headers: []header{{Name: "Host", Value: parsed.Host}}},
		routeMetadata{Scheme: parsed.Scheme, Host: host, Port: port, Profile: "chrome_152_cft"})
}
