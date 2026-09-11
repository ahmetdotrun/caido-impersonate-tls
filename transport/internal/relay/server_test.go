package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServerRejectsInvalidToken(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	server := NewServer("correct-token-with-at-least-32-bytes", log.New(io.Discard, "", 0))
	go server.handle(serverSide)

	request := privateRequest("wrong-token-with-at-least-32-bytes", "example.test", "443", "/")
	if _, err := io.WriteString(clientSide, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(clientSide), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestServerAuthenticatesBeforeReadingBody(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	server := NewServer("correct-token-with-at-least-32-bytes", log.New(io.Discard, "", 0))
	go server.handle(serverSide)
	defer clientSide.Close()
	_ = clientSide.SetDeadline(time.Now().Add(time.Second))

	request := strings.Join([]string{
		"POST / HTTP/1.1",
		"Host: example.test",
		fmt.Sprintf("%s: wrong-token-with-at-least-32-bytes", headerToken),
		fmt.Sprintf("%s: http", headerScheme),
		fmt.Sprintf("%s: example.test", headerHost),
		fmt.Sprintf("%s: 80", headerPort),
		fmt.Sprintf("%s: chrome_146", headerProfile),
		fmt.Sprintf("%s: test-1", headerTrace),
		fmt.Sprintf("Content-Length: %d", maxBodyBytes),
		"",
		"",
	}, "\r\n")
	if _, err := io.WriteString(clientSide, request); err != nil {
		t.Fatalf("write request headers: %v", err)
	}

	response, err := http.ReadResponse(bufio.NewReader(clientSide), nil)
	if err != nil {
		t.Fatalf("read response without sending body: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestServerRelaysAuthenticatedHTTP(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for _, name := range []string{
			headerToken,
			headerScheme,
			headerHost,
			headerPort,
			headerProfile,
			headerTrace,
		} {
			if value := request.Header.Get(name); value != "" {
				t.Errorf("private header %s reached target: %q", name, value)
			}
		}
		writer.Header().Set("X-Target", "reached")
		_, _ = io.WriteString(writer, "transport-ok")
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}
	host, port, err := net.SplitHostPort(targetURL.Host)
	if err != nil {
		t.Fatalf("split target: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token := "correct-token-with-at-least-32-bytes"
	events := make(chan []byte, 1)
	server := NewServer(
		token,
		log.New(io.Discard, "", 0),
		log.New(channelWriter(events), "", 0),
	)
	go func() {
		_ = server.Serve(ctx, listener)
	}()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial transport: %v", err)
	}
	defer connection.Close()

	request := privateRequestForScheme(
		token,
		"http",
		host,
		port,
		"/probe",
		"Mozilla/5.0 Chrome/146.0.0.0 Safari/537.36",
	)
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "transport-ok" {
		t.Fatalf("status/body = %d/%q", response.StatusCode, body)
	}
	if response.Header.Get("X-Target") != "reached" {
		t.Fatalf("target response header was not preserved")
	}

	select {
	case line := <-events:
		var event requestEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode activity event: %v", err)
		}
		if event.ID != "test-1" || event.Outcome != "succeeded" {
			t.Fatalf("unexpected activity event: %#v", event)
		}
		if event.StatusCode != http.StatusOK || event.Protocol != "HTTP/1.1" {
			t.Fatalf("missing response evidence: %#v", event)
		}
		wantWarning := "User-Agent reports Chrome 146 but the selected transport profile is Chrome 152"
		if event.Warning != wantWarning {
			t.Fatalf("warning = %q, want %q", event.Warning, wantWarning)
		}
	case <-time.After(time.Second):
		t.Fatal("activity event was not emitted")
	}
}

func TestServerKeepsCertificateVerificationEnabled(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}
	host, port, err := net.SplitHostPort(targetURL.Host)
	if err != nil {
		t.Fatalf("split target: %v", err)
	}

	serverSide, clientSide := net.Pipe()
	token := "correct-token-with-at-least-32-bytes"
	server := NewServer(token, log.New(io.Discard, "", 0))
	go server.handle(serverSide)

	request := privateRequestForScheme(token, "https", host, port, "/", "")
	if _, err := io.WriteString(clientSide, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(clientSide), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadGateway)
	}
}

func TestServerRelaysWebSocketUpgrade(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer target.Close()
	targetResult := make(chan error, 1)
	go func() {
		connection, acceptErr := target.Accept()
		if acceptErr != nil {
			targetResult <- acceptErr
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(connection)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				targetResult <- readErr
				return
			}
			if line == "\r\n" {
				break
			}
		}
		if _, writeErr := io.WriteString(connection,
			"HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: test\r\n\r\n"); writeErr != nil {
			targetResult <- writeErr
			return
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			targetResult <- readErr
			return
		}
		if line != "ping\n" {
			targetResult <- fmt.Errorf("upgraded payload = %q", line)
			return
		}
		_, writeErr := io.WriteString(connection, "pong\n")
		targetResult <- writeErr
	}()

	host, port, err := net.SplitHostPort(target.Addr().String())
	if err != nil {
		t.Fatalf("split target: %v", err)
	}
	serverSide, clientSide := net.Pipe()
	token := "correct-token-with-at-least-32-bytes"
	events := make(chan []byte, 1)
	server := NewServer(token, log.New(io.Discard, "", 0), log.New(channelWriter(events), "", 0))
	go server.handle(serverSide)
	defer clientSide.Close()
	_ = clientSide.SetDeadline(time.Now().Add(5 * time.Second))

	request := privateRequestForScheme(token, "http", host, port, "/socket", "")
	request = strings.Replace(request, "Connection: close", strings.Join([]string{
		"Connection: Upgrade",
		"Upgrade: websocket",
		"Sec-WebSocket-Version: 13",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
	}, "\r\n"), 1)
	if _, err := io.WriteString(clientSide, request); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	reader := bufio.NewReader(clientSide)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "101 Switching Protocols") {
		t.Fatalf("status = %q", status)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read response headers: %v", readErr)
		}
		if line == "\r\n" {
			break
		}
	}
	if _, err := io.WriteString(clientSide, "ping\n"); err != nil {
		t.Fatalf("write upgraded payload: %v", err)
	}
	payload, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read upgraded payload: %v", err)
	}
	if payload != "pong\n" {
		t.Fatalf("upgraded payload = %q", payload)
	}
	if err := <-targetResult; err != nil {
		t.Fatalf("target: %v", err)
	}

	select {
	case line := <-events:
		var event requestEvent
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("decode activity event: %v", err)
		}
		if event.Outcome != "succeeded" || event.StatusCode != http.StatusSwitchingProtocols {
			t.Fatalf("unexpected activity event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("activity event was not emitted")
	}
}

func privateRequest(token, host, port, path string) string {
	return privateRequestForScheme(token, "http", host, port, path, "")
}

func privateRequestForScheme(token, scheme, host, port, path, userAgent string) string {
	lines := []string{
		fmt.Sprintf("GET %s HTTP/1.1", path),
		fmt.Sprintf("Host: %s:%s", host, port),
		fmt.Sprintf("%s: %s", headerToken, token),
		fmt.Sprintf("%s: %s", headerScheme, scheme),
		fmt.Sprintf("%s: %s", headerHost, host),
		fmt.Sprintf("%s: %s", headerPort, port),
		fmt.Sprintf("%s: chrome_152", headerProfile),
		fmt.Sprintf("%s: test-1", headerTrace),
	}
	if userAgent != "" {
		lines = append(lines, fmt.Sprintf("User-Agent: %s", userAgent))
	}
	lines = append(lines,
		"Connection: close",
		"",
		"",
	)
	return strings.Join(lines, "\r\n")
}

type channelWriter chan []byte

func (writer channelWriter) Write(data []byte) (int, error) {
	copyOfData := append([]byte(nil), data...)
	writer <- copyOfData
	return len(data), nil
}
