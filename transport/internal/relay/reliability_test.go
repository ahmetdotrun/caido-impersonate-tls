package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "reliability-test-token-with-at-least-32-bytes"

func startRelayTest(t *testing.T, handler http.HandlerFunc, configure func(*Server)) (string, string, context.CancelFunc) {
	t.Helper()
	target := httptest.NewServer(handler)
	t.Cleanup(target.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := NewServer(testToken, log.New(io.Discard, "", 0))
	if configure != nil {
		configure(server)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("relay did not shut down")
		}
	})
	return listener.Addr().String(), target.URL, cancel
}

func dialRelayTest(t *testing.T, relayAddress, targetURL, method, path, extraHeaders string) net.Conn {
	t.Helper()
	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(target.Host)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", relayAddress, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	request := privateRequestForScheme(testToken, target.Scheme, host, port, path, "")
	request = strings.Replace(request, "GET ", method+" ", 1)
	if extraHeaders != "" {
		request = strings.Replace(request, "\r\n\r\n", "\r\n"+extraHeaders+"\r\n\r\n", 1)
	}
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	return connection
}

func readRelayTest(t *testing.T, connection net.Conn) *http.Response {
	t.Helper()
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestRelayStreamsBeyondHeaderDeadline(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 10; i++ {
			if _, err := fmt.Fprintf(w, "data: %d\n\n", i); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-time.After(30 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	}, func(server *Server) {
		server.limits.headerTimeout = 150 * time.Millisecond
		server.limits.idleTimeout = 150 * time.Millisecond
	})
	response := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/events", ""))
	reader := bufio.NewReader(response.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: 0\n" {
		t.Fatalf("first event = %q, %v", first, err)
	}
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "data: 9") {
		t.Fatalf("stream truncated: %q, %v", rest, err)
	}
}

func TestRelayCancelsIdleResponse(t *testing.T) {
	cancelled := make(chan struct{})
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}, func(server *Server) { server.limits.idleTimeout = 100 * time.Millisecond })
	response := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/idle", ""))
	body, err := io.ReadAll(response.Body)
	if err == nil || string(body) != "first\n" {
		t.Fatalf("idle body = %q, error = %v", body, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("idle upstream was not cancelled")
	}
}

func TestRelayCancelsWhenBrowserDisconnects(t *testing.T) {
	for _, sendHeaders := range []bool{false, true} {
		t.Run(fmt.Sprintf("headers=%v", sendHeaders), func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
				if sendHeaders {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(cancelled)
			}, nil)
			connection := dialRelayTest(t, relay, target, "GET", "/cancel", "")
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("target not reached")
			}
			_ = connection.Close()
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("disconnected request stayed upstream")
			}
		})
	}
}

func TestRelayReportsHeaderTimeout(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
		func(server *Server) { server.limits.headerTimeout = 100 * time.Millisecond })
	response := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/wait", ""))
	if response.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestRelayStreamsUploadLargerThan64MiB(t *testing.T) {
	const size = int64(70 * 1024 * 1024)
	firstReceived := make(chan struct{})
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.CopyN(io.Discard, r.Body, 32*1024); err != nil {
			t.Error(err)
			return
		}
		close(firstReceived)
		rest, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = fmt.Fprintf(w, "%d", rest+32*1024)
	}, nil)
	connection := dialRelayTest(t, relay, target, "POST", "/upload", fmt.Sprintf("Content-Length: %d", size))
	if _, err := connection.Write(make([]byte, 32*1024)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstReceived:
	case <-time.After(time.Second):
		t.Fatal("upload was buffered instead of reaching upstream incrementally")
	}
	if _, err := io.CopyN(connection, zeroReader{}, size-32*1024); err != nil {
		t.Fatal(err)
	}
	response := readRelayTest(t, connection)
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != fmt.Sprint(size) {
		t.Fatalf("upload result = %q, %v", body, err)
	}
}

func TestRelayEnforcesOptionalUploadLimit(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
			}, nil)
			headers := headerMaxBody + ": 4\r\nContent-Length: 5"
			if chunked {
				headers = headerMaxBody + ": 4\r\nTransfer-Encoding: chunked"
			}
			connection := dialRelayTest(t, relay, target, "POST", "/upload", headers)
			if chunked {
				_, _ = io.WriteString(connection, "5\r\nabcde\r\n0\r\n\r\n")
			}
			response := readRelayTest(t, connection)
			if response.StatusCode != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d", response.StatusCode)
			}
		})
	}
}

func TestStreamingBodyRejectsTruncation(t *testing.T) {
	request, err := readIncomingRequest(bufio.NewReader(strings.NewReader("POST / HTTP/1.1\r\nContent-Length: 5\r\n\r\nabc")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(request.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated upload error = %v", err)
	}
}

func TestRelayQueuesBusyRequestsAndReportsOverload(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(fmt.Sprintf("release=%v", release), func(t *testing.T) {
			started, unblock := make(chan struct{}), make(chan struct{})
			var testedServer *Server
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(unblock) }) })
			relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hold" {
					close(started)
					select {
					case <-unblock:
					case <-r.Context().Done():
						return
					}
				}
				_, _ = io.WriteString(w, "ok")
			}, func(server *Server) {
				testedServer = server
				server.slots = make(chan struct{}, 1)
				server.queued = make(chan struct{}, 1)
				server.limits.queueTimeout = 150 * time.Millisecond
			})
			first := dialRelayTest(t, relay, target, "GET", "/hold", "")
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("first request did not start")
			}
			second := dialRelayTest(t, relay, target, "GET", "/next", "")
			queueDeadline := time.Now().Add(time.Second)
			for len(testedServer.queued) == 0 && time.Now().Before(queueDeadline) {
				time.Sleep(time.Millisecond)
			}
			if len(testedServer.queued) != 1 {
				t.Fatal("second request did not enter the bounded queue")
			}
			third := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/overflow", ""))
			if third.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("full queue response = %d", third.StatusCode)
			}
			if release {
				once.Do(func() { close(unblock) })
			}
			response := readRelayTest(t, second)
			want := http.StatusServiceUnavailable
			if release {
				want = http.StatusOK
			}
			if response.StatusCode != want {
				t.Fatalf("queued response = %d, want %d", response.StatusCode, want)
			}
			_ = first.Close()
		})
	}
}

func TestWebSocketCapacityDoesNotBlockPages(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/socket" {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			_, _ = io.WriteString(connection, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
			_, _ = io.Copy(io.Discard, connection)
			return
		}
		_, _ = io.WriteString(w, "page")
	}, func(server *Server) {
		server.slots = make(chan struct{}, 1)
		server.websocketSlots = make(chan struct{}, 1)
	})
	socket := dialRelayTest(t, relay, target, "GET", "/socket", "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==")
	upgrade := readRelayTest(t, socket)
	if upgrade.StatusCode != 101 {
		t.Fatalf("upgrade = %d", upgrade.StatusCode)
	}
	page := readRelayTest(t, dialRelayTest(t, relay, target, "GET", "/page", ""))
	if page.StatusCode != 200 {
		t.Fatalf("page = %d", page.StatusCode)
	}
}

func TestShutdownCancelsActiveRequest(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	relay, target, shutdown := startRelayTest(t, func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}, nil)
	connection := dialRelayTest(t, relay, target, "GET", "/wait", "")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	shutdown()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel upstream")
	}
	var buffer [1]byte
	if _, err := connection.Read(buffer[:]); err == nil {
		t.Fatal("downstream connection survived shutdown")
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) { clear(buffer); return len(buffer), nil }
