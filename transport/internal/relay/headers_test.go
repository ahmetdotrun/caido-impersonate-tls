package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Decode the actual HPACK field sequence, rather than an unordered Header map.
func echoHTTP2Headers(connection *tls.Conn) error {
	if err := connection.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(connection, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("invalid HTTP/2 preface")
	}
	framer := http2.NewFramer(connection, connection)
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	if err := framer.WriteSettings(); err != nil {
		return err
	}
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			return err
		}
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				if err := framer.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.PingFrame:
			if !frame.IsAck() {
				if err := framer.WritePing(true, frame.Data); err != nil {
					return err
				}
			}
		case *http2.MetaHeadersFrame:
			fields := make([][2]string, 0, len(frame.Fields))
			for _, field := range frame.Fields {
				fields = append(fields, [2]string{field.Name, field.Value})
			}
			body, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			var block bytes.Buffer
			encoder := hpack.NewEncoder(&block)
			for _, field := range []hpack.HeaderField{
				{Name: ":status", Value: "200", Sensitive: true},
				{Name: "content-length", Value: fmt.Sprint(len(body)), Sensitive: true},
			} {
				if err := encoder.WriteField(field); err != nil {
					return err
				}
			}
			if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: frame.StreamID, BlockFragment: block.Bytes(), EndHeaders: true}); err != nil {
				return err
			}
			if err := framer.WriteData(frame.StreamID, true, body); err != nil {
				return err
			}
		case *http2.GoAwayFrame:
			return nil
		}
	}
}

func headerTestOrigin(t *testing.T) (*clientPool, *url.URL, *atomic.Int32) {
	t.Helper()
	connections := &atomic.Int32{}
	var active sync.Map
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("header capture unexpectedly received HTTP/1.1")
	}))
	origin.EnableHTTP2 = true
	origin.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
		"h2": func(_ *http.Server, connection *tls.Conn, _ http.Handler) {
			connections.Add(1)
			active.Store(connection, struct{}{})
			defer active.Delete(connection)
			_ = echoHTTP2Headers(connection)
		},
	}
	origin.StartTLS()
	t.Cleanup(func() {
		// A custom TLSNextProto handler owns its connections; net/http cannot
		// classify them as idle during httptest cleanup.
		active.Range(func(key, _ any) bool { _ = key.(*tls.Conn).Close(); return true })
		origin.Close()
	})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	pool := newClientPool()
	pool.create = func(profile string, websocket bool, proxy string) (tls_client.HttpClient, error) {
		return newProfileClient(profile, websocket, roots, proxy)
	}
	t.Cleanup(pool.close)
	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	return pool, target, connections
}

func captureHeaderRequest(t *testing.T, pool *clientPool, target *url.URL, profile string, headers []header) [][2]string {
	t.Helper()
	host, port, err := net.SplitHostPort(target.Host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := forwardContext(ctx, pool, &incomingRequest{
		Method: "GET", RequestURI: "/headers", Protocol: "HTTP/1.1", Headers: headers,
	}, routeMetadata{Scheme: "https", Host: host, Port: port, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("origin received %s", response.Proto)
	}
	var fields [][2]string
	if err := json.NewDecoder(response.Body).Decode(&fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func wireHeaderValues(fields [][2]string, name string) []string {
	var values []string
	for _, field := range fields {
		if field[0] == name {
			values = append(values, field[1])
		}
	}
	return values
}

func TestChrome157HTTP2PriorityOnFreshAndReusedConnections(t *testing.T) {
	pool, target, connections := headerTestOrigin(t)
	tests := []struct {
		destination, mode, want string
	}{
		{"document", "navigate", "u=0, i"}, {"iframe", "navigate", "u=0, i"},
		{"frame", "navigate", "u=0, i"}, {"style", "no-cors", "u=0"}, {"font", "cors", "u=0"},
		{"script", "no-cors", "u=1"}, {"empty", "cors", "u=1, i"}, {"empty", "same-origin", "u=1, i"},
		{"empty", "no-cors", "u=4, i"}, {"image", "no-cors", "i"},
		{"audio", "no-cors", "i"}, {"video", "no-cors", "i"}, {"track", "cors", "i"},
		{"unknown", "cors", ""}, {"", "cors", ""},
	}
	for _, profile := range []string{"chrome_157_cft", "chrome_157_cft_PSK"} {
		for _, test := range tests {
			t.Run(profile+"/"+test.destination+"/"+test.mode, func(t *testing.T) {
				headers := []header{{"sEc-FeTcH-DeSt", test.destination}, {"Sec-Fetch-Mode", test.mode}}
				fields := captureHeaderRequest(t, pool, target, profile, headers)
				var want []string
				if test.want != "" {
					want = []string{test.want}
				}
				if got := wireHeaderValues(fields, "priority"); !reflect.DeepEqual(got, want) {
					t.Fatalf("Priority = %q, want %q", got, want)
				}
			})
		}
	}
	if connections.Load() != 2 {
		t.Fatalf("requests did not reuse their profile's connection: %d connections", connections.Load())
	}
}

func TestChrome157HTTP2PreservesSuppliedPriorityAndAmbiguousMetadata(t *testing.T) {
	pool, target, _ := headerTestOrigin(t)
	for _, test := range []struct {
		name  string
		extra []header
		want  []string
	}{
		{"supplied", []header{{"pRiOrItY", "u=7, i"}}, []string{"u=7, i"}},
		{"empty", []header{{"Priority", ""}}, []string{""}},
		{"multiple", []header{{"Priority", "u=2"}, {"PRIORITY", "i"}}, []string{"u=2", "i"}},
		{"duplicate destination", []header{{"SEC-FETCH-DEST", "image"}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			headers := append([]header{{"Sec-Fetch-Dest", "document"}}, test.extra...)
			fields := captureHeaderRequest(t, pool, target, "chrome_157_cft_PSK", headers)
			if got := wireHeaderValues(fields, "priority"); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Priority = %q, want %q", got, test.want)
			}
		})
	}
}

func TestChrome157HTTP2OrdersBrowserHeadersBeforeCustomHeaders(t *testing.T) {
	pool, target, _ := headerTestOrigin(t)
	for _, destination := range []string{"document", "empty"} {
		t.Run(destination, func(t *testing.T) {
			fields := captureHeaderRequest(t, pool, target, "chrome_157_cft_PSK", []header{
				{"X-First", "one"}, {"x-FiRsT", "two"}, {"Priority", "u=7"},
				{"Accept-Language", "en-US"}, {"Sec-Fetch-Dest", destination}, {"Accept", "*/*"},
				{"Sec-Ch-Ua-Mobile", "?0"}, {"X-Last", "last"}, {"User-Agent", "preserved-agent"},
				{"Sec-Ch-Ua-Platform", "Linux"}, {"Sec-Ch-Ua", "Chromium"},
			})
			var names []string
			for _, field := range fields {
				if !strings.HasPrefix(field[0], ":") {
					names = append(names, field[0])
				}
			}
			want := []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "user-agent"}
			if destination == "empty" {
				want = []string{"sec-ch-ua-platform", "user-agent", "sec-ch-ua", "sec-ch-ua-mobile"}
			}
			want = append(want, "accept", "sec-fetch-dest", "accept-language", "priority", "x-first", "x-first", "x-last")
			if !reflect.DeepEqual(names, want) || !reflect.DeepEqual(wireHeaderValues(fields, "x-first"), []string{"one", "two"}) ||
				!reflect.DeepEqual(wireHeaderValues(fields, "user-agent"), []string{"preserved-agent"}) {
				t.Fatalf("incorrect ordered headers: %#v", fields)
			}
		})
	}
}

func TestHTTP2HeaderPolicyAppliesThroughEgress(t *testing.T) {
	for _, scheme := range []string{"http", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			proxy := loopbackEgressProxy(t, scheme, "127.0.0.2")
			// Create the pool after its proxy so cleanup closes clients first.
			pool, target, _ := headerTestOrigin(t)
			pool.egress.file = filepath.Join(t.TempDir(), "egress.json")
			saveEgressTest(t, pool.egress.file, map[string]egressProfile{"route": {Proxy: proxy}})
			fields := captureHeaderRequest(t, pool, target, "chrome_157_cft_PSK", []header{{headerEgress, "route"}, {"Sec-Fetch-Dest", "script"}})
			if !reflect.DeepEqual(wireHeaderValues(fields, "priority"), []string{"u=1"}) || wireHeaderValues(fields, strings.ToLower(headerEgress)) != nil {
				t.Fatalf("egress lost the header profile or leaked its selector: %#v", fields)
			}
		})
	}
}

func TestHTTP2HeaderPolicyDoesNotLeakAcrossProfiles(t *testing.T) {
	pool, target, _ := headerTestOrigin(t)
	for _, profile := range []string{"chrome_152_cft", "chrome_146", "firefox_148", "safari_ios_26_0"} {
		t.Run(profile, func(t *testing.T) {
			fields := captureHeaderRequest(t, pool, target, profile, []header{{"Accept", "*/*"}, {"User-Agent", "original"}, {"Sec-Fetch-Dest", "document"}})
			if wireHeaderValues(fields, "priority") != nil {
				t.Fatal("Chrome 157's header policy leaked into another profile")
			}
		})
	}
}

func TestHTTP2HeaderPolicyDoesNotApplyToHTTP1OrWebSocket(t *testing.T) {
	for _, secure := range []bool{false, true} {
		for _, websocket := range []bool{false, true} {
			t.Run(fmt.Sprintf("TLS=%v/WS=%v", secure, websocket), func(t *testing.T) {
				origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.ProtoMajor != 1 || len(request.Header.Values("Priority")) != 0 {
						t.Errorf("HTTP/1.1 request was changed: %s %#v", request.Proto, request.Header)
					}
					w.WriteHeader(204)
				}))
				if secure {
					origin.StartTLS()
				} else {
					origin.Start()
				}
				defer origin.Close()
				roots := x509.NewCertPool()
				if secure {
					roots.AddCert(origin.Certificate())
				}
				pool := newClientPool()
				defer pool.close()
				pool.create = func(profile string, forceHTTP1 bool, proxy string) (tls_client.HttpClient, error) {
					return newProfileClient(profile, forceHTTP1, roots, proxy)
				}
				target, _ := url.Parse(origin.URL)
				host, port, _ := net.SplitHostPort(target.Host)
				headers := []header{{"Sec-Fetch-Dest", "document"}}
				if websocket {
					headers = append(headers, header{"Connection", "Upgrade"}, header{"Upgrade", "websocket"})
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				response, err := forwardContext(ctx, pool, &incomingRequest{Method: "GET", RequestURI: "/", Headers: headers},
					routeMetadata{Scheme: target.Scheme, Host: host, Port: port, Profile: "chrome_157_cft_PSK"})
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
			})
		}
	}
}

func TestHTTP2HeaderProfilesAreSafeForConcurrentStreams(t *testing.T) {
	pool, target, _ := headerTestOrigin(t)
	for i := range 32 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			destination, want := "document", "u=0, i"
			if i%2 != 0 {
				destination, want = "image", "i"
			}
			fields := captureHeaderRequest(t, pool, target, "chrome_157_cft_PSK", []header{{"Sec-Fetch-Dest", destination}})
			if !reflect.DeepEqual(wireHeaderValues(fields, "priority"), []string{want}) {
				t.Fatal("concurrent requests shared header policy state")
			}
		})
	}
}
