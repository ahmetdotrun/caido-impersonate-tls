package relay

import (
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	tls_client "github.com/bogdanfinn/tls-client"
)

func TestBundledProfilesAreAvailable(t *testing.T) {
	pool := newClientPool()
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
		if _, err := pool.get(profile); err != nil {
			t.Fatalf("profile %q is unavailable: %v", profile, err)
		}
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
			client, err := tls_client.NewHttpClient(nil,
				tls_client.WithClientProfile(customTransportProfiles["chrome_152_cft"]),
				tls_client.WithNotFollowRedirects(),
				tls_client.WithTimeoutSeconds(5),
				tls_client.WithTransportOptions(&tls_client.TransportOptions{RootCAs: roots, DisableCompression: true}),
			)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			pool := newClientPool()
			pool.clients["chrome_152_cft"] = client
			targetURL, _ := url.Parse(target.URL)
			host, port, _ := net.SplitHostPort(targetURL.Host)
			response, err := forward(pool, &incomingRequest{Method: "GET", RequestURI: "/", Headers: []header{{Name: "Host", Value: targetURL.Host}}},
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
