package relay

import (
	"bufio"
	"io"
	"net/http"
	"testing"
)

func TestRelayPreservesOptionsAsterisk(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.RequestURI)
	}, nil)
	response := readRelayTest(t, dialRelayTest(t, relay, target, "OPTIONS", "*", ""))
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "*" {
		t.Fatalf("origin target = %q, %v", body, err)
	}
}

func TestRelayForwardsContinueBeforeUpload(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		_, _ = w.Write(body)
	}, nil)
	connection := dialRelayTest(t, relay, target, "POST", "/upload", "Expect: 100-continue\r\nContent-Length: 4")
	reader := bufio.NewReader(connection)
	interim, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	_ = interim.Body.Close()
	if interim.StatusCode != 100 {
		t.Fatalf("interim status = %d", interim.StatusCode)
	}
	if _, err := io.WriteString(connection, "body"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: "POST"})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || string(body) != "body" {
		t.Fatalf("upload response = %d %q, %v", response.StatusCode, body, err)
	}
}

func TestRelayForwardsEarlyHintsWithoutClosingExchange(t *testing.T) {
	relay, target, _ := startRelayTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", "</style.css>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		_, _ = io.WriteString(w, "final")
	}, nil)
	connection := dialRelayTest(t, relay, target, "GET", "/", "")
	reader := bufio.NewReader(connection)
	interim, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = interim.Body.Close()
	if interim.StatusCode != 103 || interim.Header.Get("Link") == "" || interim.Close {
		t.Fatalf("incorrect informational response: %#v", interim)
	}
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "final" {
		t.Fatalf("final response = %q, %v", body, err)
	}
}
