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

	if _, err := io.WriteString(connection, privateRequest(token, host, port, "/probe")); err != nil {
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
		if event.StatusCode != http.StatusOK || event.Protocol == "" {
			t.Fatalf("missing response evidence: %#v", event)
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

	request := privateRequestForScheme(token, "https", host, port, "/")
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

func privateRequest(token, host, port, path string) string {
	return privateRequestForScheme(token, "http", host, port, path)
}

func privateRequestForScheme(token, scheme, host, port, path string) string {
	lines := []string{
		fmt.Sprintf("GET %s HTTP/1.1", path),
		fmt.Sprintf("Host: %s:%s", host, port),
		fmt.Sprintf("%s: %s", headerToken, token),
		fmt.Sprintf("%s: %s", headerScheme, scheme),
		fmt.Sprintf("%s: %s", headerHost, host),
		fmt.Sprintf("%s: %s", headerPort, port),
		fmt.Sprintf("%s: chrome_146", headerProfile),
		fmt.Sprintf("%s: test-1", headerTrace),
		"Connection: close",
		"",
		"",
	}
	return strings.Join(lines, "\r\n")
}

type channelWriter chan []byte

func (writer channelWriter) Write(data []byte) (int, error) {
	copyOfData := append([]byte(nil), data...)
	writer <- copyOfData
	return len(data), nil
}
