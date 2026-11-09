package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
)

type failingEgressStream struct{ err error }

func (stream failingEgressStream) Read([]byte) (int, error)  { return 0, stream.err }
func (stream failingEgressStream) Write([]byte) (int, error) { return 0, stream.err }
func (stream failingEgressStream) Close() error              { return stream.err }

func TestEgressRedactsLateHTTPAndWebSocketErrors(t *testing.T) {
	reflected := errors.New("peer reflected http://private-user:private-password@proxy.invalid")
	for _, proxied := range []bool{false, true} {
		for _, upstreamError := range []error{reflected, io.EOF, nil} {
			stream := failingEgressStream{err: upstreamError}
			releases := 0
			body := &leasedDuplexBody{leasedBody: &leasedBody{
				ReadCloser: stream, redactErrors: proxied, release: func() { releases++ },
			}, writer: stream}
			_, readError := body.Read(make([]byte, 1))
			_, writeError := body.Write([]byte("payload"))
			closeError := body.Close()
			want := upstreamError
			if proxied && upstreamError == reflected {
				want = errEgressRequest
			}
			for _, err := range []error{readError, writeError, closeError} {
				if err != want {
					t.Fatalf("proxied=%v: stream error was not handled safely", proxied)
				}
			}
			if releases != 1 {
				t.Fatal("error redaction lost the client lease release")
			}
		}
	}
}

func saveEgressTest(t *testing.T, file string, profiles map[string]egressProfile) {
	t.Helper()
	contents, err := json.Marshal(profiles)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file+".tmp", contents, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file+".tmp", file); err != nil {
		t.Fatal(err)
	}
}

// Each loopback proxy opens origin connections from a distinct source address.
// The .invalid destination exists only in the proxy's resolver, never locally.
func loopbackEgressProxy(t *testing.T, scheme, source string) string {
	t.Helper()
	connect := func(target string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			return nil, err
		}
		if host == "only.proxy.invalid" {
			host = "127.0.0.1"
		}
		dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
		return dialer.Dial("tcp", net.JoinHostPort(host, port))
	}
	bridge := func(down net.Conn, reader io.Reader, up net.Conn) {
		defer down.Close()
		defer up.Close()
		_ = down.SetDeadline(time.Now().Add(10 * time.Second))
		_ = up.SetDeadline(time.Now().Add(10 * time.Second))
		done := make(chan struct{})
		go func() { _, _ = io.Copy(up, reader); _ = up.Close(); close(done) }()
		_, _ = io.Copy(down, up)
		_ = down.Close()
		<-done
	}
	if scheme == "http" {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "CONNECT" || r.Header.Get(headerEgress) != "" {
				t.Error("unexpected proxy request")
				w.WriteHeader(400)
				return
			}
			// BasicAuth reads Authorization; CONNECT uses Proxy-Authorization.
			r.Header.Set("Authorization", r.Header.Get("Proxy-Authorization"))
			user, pass, ok := r.BasicAuth()
			if !ok || user != "user@name" || pass != "secret:password" {
				w.WriteHeader(407)
				return
			}
			up, err := connect(r.Host)
			if err != nil {
				w.WriteHeader(502)
				return
			}
			down, buffered, err := w.(http.Hijacker).Hijack()
			if err != nil {
				_ = up.Close()
				return
			}
			_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = buffered.Flush()
			bridge(down, buffered.Reader, up)
		}))
		t.Cleanup(server.Close)
		endpoint, _ := url.Parse(server.URL)
		endpoint.User = url.UserPassword("user@name", "secret:password")
		return endpoint.String()
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	t.Cleanup(func() { _ = listener.Close(); workers.Wait() })
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			down, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer down.Close()
				_ = down.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(down)
				greeting := make([]byte, 2)
				if _, err := io.ReadFull(reader, greeting); err != nil || greeting[0] != 5 {
					return
				}
				if _, err := io.CopyN(io.Discard, reader, int64(greeting[1])); err != nil {
					return
				}
				_, _ = down.Write([]byte{5, 2})
				readString := func() string {
					n, _ := reader.ReadByte()
					value := make([]byte, n)
					_, _ = io.ReadFull(reader, value)
					return string(value)
				}
				version, _ := reader.ReadByte()
				user, password := readString(), readString()
				if version != 1 || user != "user@name" || password != "secret:password" {
					_, _ = down.Write([]byte{1, 1})
					return
				}
				_, _ = down.Write([]byte{1, 0})
				head := make([]byte, 4)
				if _, err := io.ReadFull(reader, head); err != nil || head[0] != 5 || head[1] != 1 {
					return
				}
				var host string
				switch head[3] {
				case 3:
					host = readString()
				case 1, 4:
					size := 4
					if head[3] == 4 {
						size = 16
					}
					ip := make([]byte, size)
					if _, err := io.ReadFull(reader, ip); err != nil {
						return
					}
					host = net.IP(ip).String()
				default:
					return
				}
				var port uint16
				if binary.Read(reader, binary.BigEndian, &port) != nil {
					return
				}
				up, err := connect(net.JoinHostPort(host, fmt.Sprint(port)))
				if err != nil {
					_, _ = down.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				_, _ = down.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				bridge(down, reader, up)
			}()
		}
	}()
	return "socks5h://" + url.UserPassword("user@name", "secret:password").String() + "@" + listener.Addr().String()
}

func TestEgressHTTPAndWebSocketRoutes(t *testing.T) {
	for _, scheme := range []string{"http", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "egress.json")
			saveEgressTest(t, file, map[string]egressProfile{
				"caller chooses this": {Proxy: loopbackEgressProxy(t, scheme, "127.0.0.2")},
				"second":              {Proxy: loopbackEgressProxy(t, scheme, "127.0.0.3")},
			})
			relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
				if _, found := r.Header[http.CanonicalHeaderKey(headerEgress)]; found {
					t.Error("egress selector reached origin")
				}
				if r.Header.Get("Proxy-Authorization") != "" {
					t.Error("proxy credentials reached origin")
				}
				host, _, _ := net.SplitHostPort(r.RemoteAddr)
				if r.URL.Path != "/ws" {
					_, _ = io.WriteString(w, host)
					return
				}
				connection, reader, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer connection.Close()
				_, _ = reader.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
				_ = reader.Flush()
				line, _ := reader.ReadString('\n')
				if line != "ping\n" {
					t.Error("upgraded payload lost")
					return
				}
				_, _ = io.WriteString(connection, host+"\n")
			}, func(server *Server) { server.SetEgressFile(file) })
			for _, name := range []string{"caller chooses this", "second", ""} {
				want := "127.0.0.1"
				if name == "caller chooses this" {
					want = "127.0.0.2"
				}
				if name == "second" {
					want = "127.0.0.3"
				}
				endpoint := target
				if name != "" {
					endpoint = strings.Replace(endpoint, "127.0.0.1", "only.proxy.invalid", 1)
				}
				for _, websocket := range []bool{false, true} {
					extra := ""
					if name != "" {
						extra = "x-morpheus-egress: " + name
					}
					path := "/"
					if websocket {
						path = "/ws"
						extra += "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13"
					}
					connection := dialRelayTest(t, relay, endpoint, "GET", path, strings.TrimPrefix(extra, "\r\n"))
					reader := bufio.NewReader(connection)
					response, err := http.ReadResponse(reader, nil)
					if err != nil {
						t.Fatal(err)
					}
					if websocket {
						if response.StatusCode != 101 {
							t.Fatalf("upgrade = %d", response.StatusCode)
						}
						_, _ = io.WriteString(connection, "ping\n")
						line, err := reader.ReadString('\n')
						if err != nil || line != want+"\n" {
							t.Fatalf("WebSocket exit = %q, %v", line, err)
						}
					} else {
						body, err := io.ReadAll(response.Body)
						if err != nil || response.StatusCode != 200 || string(body) != want {
							t.Fatalf("HTTP exit = %d %q, %v", response.StatusCode, body, err)
						}
					}
					_ = response.Body.Close()
					_ = connection.Close()
				}
			}
		})
	}
}

func TestEgressNeverFallsBackOrLeaksCredentials(t *testing.T) {
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := dead.Addr().String()
	_ = dead.Close()
	file := filepath.Join(t.TempDir(), "egress.json")
	profiles := map[string]egressProfile{"dead": {Proxy: "http://private-user:private-password@" + address}}
	saveEgressTest(t, file, profiles)
	var hits atomic.Int32
	var logs strings.Builder
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "direct") }, func(server *Server) {
		server.SetEgressFile(file)
		server.logger = log.New(&logs, "", 0)
		server.activityLogger = log.New(&logs, "", 0)
	})
	for _, test := range []struct {
		header string
		code   int
	}{
		{headerEgress + ": missing", 400},
		{headerEgress + ": dead", 502},
		{headerEgress + ":", 400},
		{headerEgress + ": dead\r\nx-morpheus-egress: missing", 400},
	} {
		response := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/", test.header))
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != test.code {
			t.Fatalf("egress status = %d, want %d: %s", response.StatusCode, test.code, body)
		}
		if strings.Contains(string(body), "private-") {
			t.Fatal("proxy credentials leaked in response")
		}
	}
	if hits.Load() != 0 {
		t.Fatal("failed egress went direct")
	}
	if strings.Contains(logs.String(), "private-") {
		t.Fatal("proxy credentials leaked in logs or Activity")
	}
	if err := os.WriteFile(file, []byte(`{"broken":`), 0600); err != nil {
		t.Fatal(err)
	}
	response := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/", headerEgress+": dead"))
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != 503 || hits.Load() != 0 {
		t.Fatal("corrupt configuration did not fail closed")
	}
	response = readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/", ""))
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != 200 || hits.Load() != 1 {
		t.Fatal("direct routing should not depend on egress configuration")
	}
}

func TestEgressIsolatesConnectionsAndTLSSessions(t *testing.T) {
	for _, closeConnections := range []bool{false, true} {
		t.Run(fmt.Sprintf("force-new-connection=%v", closeConnections), func(t *testing.T) {
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if closeConnections {
					w.Header().Set("Connection", "close")
				}
				_, _ = fmt.Fprintf(w, "%s %v", r.RemoteAddr, r.TLS.DidResume)
			}))
			origin.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
			origin.StartTLS()
			defer origin.Close()
			roots := x509.NewCertPool()
			roots.AddCert(origin.Certificate())
			proxyURL := loopbackEgressProxy(t, "http", "127.0.0.2")
			file := filepath.Join(t.TempDir(), "egress.json")
			saveEgressTest(t, file, map[string]egressProfile{"A": {Proxy: proxyURL}, "B": {Proxy: proxyURL}})
			pool := newClientPool()
			defer pool.close()
			pool.egress.file = file
			pool.create = func(profile string, websocket bool, proxy string) (tls_client.HttpClient, error) {
				return newProfileClient(profile, websocket, roots, proxy)
			}
			destination, _ := url.Parse(origin.URL)
			host, port, _ := net.SplitHostPort(destination.Host)
			fetch := func(name string) string {
				headers := []header{{Name: "Host", Value: destination.Host}}
				if name != "" {
					headers = append(headers, header{Name: headerEgress, Value: name})
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				response, err := forwardContext(ctx, pool, &incomingRequest{Method: "GET", RequestURI: "/", Headers: headers}, routeMetadata{Scheme: "https", Host: host, Port: port, Profile: "chrome_157_cft_PSK"})
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				return string(body)
			}
			first, second, other, again, direct := fetch("A"), fetch("A"), fetch("B"), fetch("A"), fetch("")
			if !strings.HasSuffix(first, "false") || !strings.HasSuffix(other, "false") || !strings.HasSuffix(direct, "false") {
				t.Fatalf("new route resumed another route's session: %q / %q / %q", first, other, direct)
			}
			if closeConnections {
				if !strings.HasSuffix(second, "true") || !strings.HasSuffix(again, "true") {
					t.Fatalf("test did not exercise TLS resumption within A: %q / %q", second, again)
				}
			} else if first != second || first != again || strings.Fields(first)[0] == strings.Fields(other)[0] {
				t.Fatalf("routes shared a connection or failed to reuse their own: %q / %q / %q / %q", first, second, other, again)
			}
			// Updating A's endpoint must replace its client/session cache immediately.
			saveEgressTest(t, file, map[string]egressProfile{"A": {Proxy: loopbackEgressProxy(t, "http", "127.0.0.3")}})
			changed := fetch("A")
			if !strings.HasPrefix(changed, "127.0.0.3:") || !strings.HasSuffix(changed, "false") {
				t.Fatalf("changed route reused its old connection/session: %q", changed)
			}
		})
	}
}
