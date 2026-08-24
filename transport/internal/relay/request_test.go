package relay

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadIncomingRequestPreservesHeaderOrder(t *testing.T) {
	raw := "POST /submit?q=1 HTTP/1.1\r\n" +
		"Host: example.test\r\n" +
		"X-First: one\r\n" +
		"x-second: two\r\n" +
		"Content-Length: 4\r\n\r\nbody"

	request, err := readIncomingRequest(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readIncomingRequest() error = %v", err)
	}
	if request.Method != "POST" || request.RequestURI != "/submit?q=1" {
		t.Fatalf("unexpected request line: %#v", request)
	}
	if string(request.Body) != "body" {
		t.Fatalf("body = %q", request.Body)
	}
	if len(request.Headers) != 4 {
		t.Fatalf("headers = %#v", request.Headers)
	}
	if request.Headers[1].Name != "X-First" || request.Headers[2].Name != "x-second" {
		t.Fatalf("header order was not preserved: %#v", request.Headers)
	}
}

func TestReadIncomingRequestRejectsConnect(t *testing.T) {
	raw := "CONNECT example.test:443 HTTP/1.1\r\nHost: example.test\r\n\r\n"
	_, err := readIncomingRequest(bufio.NewReader(strings.NewReader(raw)))
	if err == nil || !strings.Contains(err.Error(), "CONNECT") {
		t.Fatalf("expected CONNECT rejection, got %v", err)
	}
}

func TestReadIncomingRequestRejectsDuplicateContentLength(t *testing.T) {
	raw := "POST / HTTP/1.1\r\n" +
		"Host: example.test\r\n" +
		"Content-Length: 4\r\n" +
		"Content-Length: 4\r\n\r\nbody"
	_, err := readIncomingRequest(bufio.NewReader(strings.NewReader(raw)))
	if err == nil || !strings.Contains(err.Error(), "multiple Content-Length") {
		t.Fatalf("expected duplicate Content-Length rejection, got %v", err)
	}
}

func TestReadIncomingRequestRejectsUnsupportedTransferEncoding(t *testing.T) {
	raw := "POST / HTTP/1.1\r\n" +
		"Host: example.test\r\n" +
		"Transfer-Encoding: gzip, chunked\r\n\r\n"
	_, err := readIncomingRequest(bufio.NewReader(strings.NewReader(raw)))
	if err == nil || !strings.Contains(err.Error(), "unsupported Transfer-Encoding") {
		t.Fatalf("expected Transfer-Encoding rejection, got %v", err)
	}
}

func TestReadIncomingRequestRejectsMalformedChunkedEncoding(t *testing.T) {
	raw := "POST / HTTP/1.1\r\n" +
		"Host: example.test\r\n" +
		"Transfer-Encoding: chunked,\r\n\r\n"
	_, err := readIncomingRequest(bufio.NewReader(strings.NewReader(raw)))
	if err == nil || !strings.Contains(err.Error(), "unsupported Transfer-Encoding") {
		t.Fatalf("expected malformed Transfer-Encoding rejection, got %v", err)
	}
}

func TestMetadataRejectsDuplicatePrivateHeader(t *testing.T) {
	request := &incomingRequest{Headers: []header{
		{Name: headerToken, Value: "first"},
		{Name: headerToken, Value: "second"},
	}}
	_, err := request.metadata()
	if err == nil || !strings.Contains(err.Error(), "multiple "+headerToken) {
		t.Fatalf("expected duplicate private-header rejection, got %v", err)
	}
}

func TestMetadataRejectsURLControlCharactersInTargetHost(t *testing.T) {
	for _, host := range []string{
		"user@example.test",
		"example.test/path",
		"example.test?query",
		"example.test#fragment",
		"example.test\\path",
		"example test",
	} {
		request := &incomingRequest{Headers: []header{
			{Name: headerToken, Value: "correct-token-with-at-least-32-bytes"},
			{Name: headerScheme, Value: "https"},
			{Name: headerHost, Value: host},
			{Name: headerPort, Value: "443"},
			{Name: headerProfile, Value: "chrome_146"},
			{Name: headerTrace, Value: "test-1"},
		}}
		if _, err := request.metadata(); err == nil {
			t.Errorf("metadata accepted target host %q", host)
		}
	}
}
