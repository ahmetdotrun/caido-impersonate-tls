package relay

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

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
