package relay

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestWriteResponseTerminatesChunkedBody(t *testing.T) {
	response := &fhttp.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Header:        make(fhttp.Header),
		Body:          io.NopCloser(strings.NewReader("chunked-body")),
		ContentLength: -1,
	}
	var wire bytes.Buffer
	if err := writeResponse(&wire, http.MethodGet, response); err != nil {
		t.Fatalf("write response: %v", err)
	}

	parsed, err := http.ReadResponse(bufio.NewReader(&wire), nil)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	defer parsed.Body.Close()
	body, err := io.ReadAll(parsed.Body)
	if err != nil {
		t.Fatalf("read chunked body: %v", err)
	}
	if string(body) != "chunked-body" {
		t.Fatalf("body = %q", body)
	}
}

func TestResponsePreservesCookiesRedirectAndCompression(t *testing.T) {
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	_, _ = io.WriteString(zipper, "compressed response")
	_ = zipper.Close()
	response := &fhttp.Response{
		StatusCode: 302,
		Header: fhttp.Header{
			"Location":         []string{"/next"},
			"Set-Cookie":       []string{"first=1; Path=/", "second=2; HttpOnly"},
			"Content-Encoding": []string{"gzip"},
			"Connection":       []string{"keep-alive, X-Local-Hop"},
			"Keep-Alive":       []string{"timeout=30"},
			"X-Local-Hop":      []string{"remove"},
		},
		ContentLength: int64(compressed.Len()),
		Body:          io.NopCloser(bytes.NewReader(compressed.Bytes())),
	}
	var wire bytes.Buffer
	if err := writeResponse(&wire, "GET", response); err != nil {
		t.Fatal(err)
	}
	parsed, err := http.ReadResponse(bufio.NewReader(&wire), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Body.Close()
	if parsed.StatusCode != 302 || parsed.Header.Get("Location") != "/next" || len(parsed.Header.Values("Set-Cookie")) != 2 {
		t.Fatalf("lost redirect or cookies: %#v", parsed)
	}
	if parsed.Header.Get("Keep-Alive") != "" || parsed.Header.Get("X-Local-Hop") != "" {
		t.Fatal("hop headers leaked")
	}
	zipReader, err := gzip.NewReader(parsed.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer zipReader.Close()
	body, err := io.ReadAll(zipReader)
	if err != nil || string(body) != "compressed response" {
		t.Fatalf("compression changed: %q, %v", body, err)
	}
}

func TestResponseReportsTruncatedDownload(t *testing.T) {
	response := &fhttp.Response{StatusCode: 200, Header: make(fhttp.Header), ContentLength: 10, Body: io.NopCloser(strings.NewReader("short"))}
	var wire bytes.Buffer
	if err := writeResponse(&wire, "GET", response); err == nil {
		t.Fatal("truncated download reported success")
	}
}

func TestResponseDoesNotTerminateFailedChunkedStream(t *testing.T) {
	response := &fhttp.Response{
		StatusCode: 200, Header: make(fhttp.Header), ContentLength: -1,
		Body: io.NopCloser(io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(io.ErrUnexpectedEOF))),
	}
	var wire bytes.Buffer
	if err := writeResponse(&wire, "GET", response); err == nil {
		t.Fatal("failed stream reported success")
	}
	if bytes.HasSuffix(wire.Bytes(), []byte("0\r\n")) {
		t.Fatal("failed stream emitted the successful final chunk")
	}
	parsed, err := http.ReadResponse(bufio.NewReader(&wire), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer parsed.Body.Close()
	if _, err := io.ReadAll(parsed.Body); err == nil {
		t.Fatal("downstream could not detect the truncated stream")
	}
}
