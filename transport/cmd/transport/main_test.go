package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadTokenFileAcceptsPrivateSecret(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "transport.token")
	token := strings.Repeat("a", 64)
	if err := os.WriteFile(filePath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	got, err := readTokenFile(filePath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if got != token {
		t.Fatalf("token = %q, want %q", got, token)
	}
}

func TestReadTokenFileRejectsShortSecret(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "transport.token")
	if err := os.WriteFile(filePath, []byte("short\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	if _, err := readTokenFile(filePath); err == nil {
		t.Fatal("expected an error for a short token")
	}
}

func TestValidateListenAddressRequiresLiteralLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		if err := validateListenAddress(address); err != nil {
			t.Fatalf("validateListenAddress(%q): %v", address, err)
		}
	}

	for _, address := range []string{"0.0.0.0:0", "localhost:0", "example.com:443"} {
		if err := validateListenAddress(address); err == nil {
			t.Fatalf("validateListenAddress(%q) unexpectedly succeeded", address)
		}
	}
}

func TestWatchOwnerCancelsAfterHeartbeatExpires(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "transport.owner")
	if err := os.WriteFile(filePath, []byte("owner\n"), 0o600); err != nil {
		t.Fatalf("write owner file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchOwner(ctx, cancel, filePath, time.Millisecond, 0)

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner watcher did not cancel an expired heartbeat")
	}
}
