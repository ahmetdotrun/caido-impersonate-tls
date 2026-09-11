package relay

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestForwardErrorDoesNotExposeRequestTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
		_ = listener.Close()
		close(accepted)
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	request := &incomingRequest{
		Method:     "GET",
		RequestURI: "/private/path?session=secret",
		Protocol:   "HTTP/1.1",
		Headers:    []header{{Name: "Host", Value: host}},
	}
	metadata := routeMetadata{
		Scheme:  "http",
		Host:    host,
		Port:    port,
		Profile: "chrome_146",
	}

	_, err = forward(newClientPool(), request, metadata)
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("transport did not reach the test listener")
	}
	if err == nil {
		t.Fatal("forward unexpectedly succeeded")
	}
	for _, sensitive := range []string{"private", "session", "secret"} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("error exposed request target: %q", err)
		}
	}
}

func TestForwardPreservesGeneratedHeaderPositions(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	type capture struct {
		lines []string
		err   error
	}
	captured := make(chan capture, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			captured <- capture{err: acceptErr}
			return
		}
		defer connection.Close()

		reader := bufio.NewReader(connection)
		lines := make([]string, 0, 8)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				captured <- capture{err: readErr}
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if line == "" {
				break
			}
			lines = append(lines, line)
		}
		if _, readErr := io.CopyN(io.Discard, reader, 4); readErr != nil {
			captured <- capture{err: readErr}
			return
		}
		_, writeErr := io.WriteString(
			connection,
			"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		)
		captured <- capture{lines: lines, err: writeErr}
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	request := &incomingRequest{
		Method:     http.MethodPost,
		RequestURI: "/submit",
		Protocol:   "HTTP/1.1",
		Headers: []header{
			{Name: "Host", Value: net.JoinHostPort(host, port)},
			{Name: "X-First", Value: "one"},
			{Name: "Content-Length", Value: "4"},
			{Name: "X-Second", Value: "two"},
		},
		Body: []byte("body"),
	}
	metadata := routeMetadata{
		Scheme:  "http",
		Host:    host,
		Port:    port,
		Profile: "chrome_146",
	}

	response, err := forward(newClientPool(), request, metadata)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	response.Body.Close()

	result := <-captured
	if result.err != nil {
		t.Fatalf("capture request: %v", result.err)
	}
	joined := strings.Join(result.lines, "\n")
	if strings.Contains(joined, "Go-http-client") {
		t.Fatalf("request exposed the Go default User-Agent:\n%s", joined)
	}

	previousIndex := -1
	for _, name := range []string{"host", "x-first", "content-length", "x-second"} {
		index := headerLineIndex(result.lines, name)
		if index <= previousIndex {
			t.Fatalf("header %q is out of order in:\n%s", name, joined)
		}
		previousIndex = index
	}
}

func TestForwardPreservesWebSocketUpgradeHeaders(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	captured := make(chan []string, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			captured <- nil
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		var lines []string
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				captured <- nil
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if line == "" {
				break
			}
			lines = append(lines, line)
		}
		captured <- lines
		_, _ = io.WriteString(connection, "HTTP/1.1 426 Upgrade Required\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	request := &incomingRequest{
		Method:     http.MethodGet,
		RequestURI: "/socket",
		Protocol:   "HTTP/1.1",
		Headers: []header{
			{Name: "Host", Value: net.JoinHostPort(host, port)},
			{Name: "Connection", Value: "Upgrade"},
			{Name: "Upgrade", Value: "websocket"},
			{Name: "Sec-WebSocket-Version", Value: "13"},
			{Name: "Sec-WebSocket-Key", Value: "dGhlIHNhbXBsZSBub25jZQ=="},
		},
	}
	metadata := routeMetadata{Scheme: "http", Host: host, Port: port, Profile: "chrome_152_cft"}

	response, err := forward(newClientPool(), request, metadata)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	response.Body.Close()

	lines := <-captured
	joined := strings.ToLower(strings.Join(lines, "\n"))
	for _, expected := range []string{"connection: upgrade", "upgrade: websocket", "sec-websocket-key:"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in forwarded handshake:\n%s", expected, joined)
		}
	}
}

func headerLineIndex(lines []string, name string) int {
	prefix := strings.ToLower(name) + ":"
	for index, line := range lines {
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return index
		}
	}
	return -1
}
