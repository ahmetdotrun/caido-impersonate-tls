package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/caido-impersonate-tls/transport/internal/relay"
)

const version = "0.2.0"
const minimumTokenLength = 32
const ownerPollInterval = 5 * time.Second
const ownerTimeout = 20 * time.Second

type readyEvent struct {
	Event   string `json:"event"`
	Port    int    `json:"port"`
	Version string `json:"version"`
}

func main() {
	listenAddress := flag.String("listen", "127.0.0.1:0", "private listen address")
	tokenFile := flag.String("token-file", "", "path to the one-time transport token")
	ownerFile := flag.String("owner-file", "", "path to the plugin ownership heartbeat")
	flag.Parse()

	if *tokenFile == "" || *ownerFile == "" {
		fmt.Fprintln(os.Stderr, "token-file and owner-file are required")
		os.Exit(2)
	}
	if err := validateListenAddress(*listenAddress); err != nil {
		fmt.Fprintf(os.Stderr, "listen address: %v\n", err)
		os.Exit(2)
	}

	token, err := readTokenFile(*tokenFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read token: %v\n", err)
		os.Exit(2)
	}
	if err := os.Remove(*tokenFile); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "remove token file: %v\n", err)
		os.Exit(2)
	}

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}

	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		fmt.Fprintln(os.Stderr, "listener did not return a TCP address")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer cancel()
	defer os.Remove(*ownerFile)

	go watchOwner(ctx, cancel, *ownerFile, ownerPollInterval, ownerTimeout)

	if err := json.NewEncoder(os.Stdout).Encode(readyEvent{
		Event:   "ready",
		Port:    tcpAddress.Port,
		Version: version,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "write ready event: %v\n", err)
		os.Exit(1)
	}

	server := relay.NewServer(
		token,
		log.New(os.Stderr, "transport: ", 0),
		log.New(os.Stdout, "", 0),
	)
	if err := server.Serve(ctx, listener); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

func validateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("must use a literal loopback IP address")
	}

	return nil
}

func readTokenFile(filePath string) (string, error) {
	contents, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(contents))
	if len(token) < minimumTokenLength {
		return "", fmt.Errorf("token must contain at least %d characters", minimumTokenLength)
	}

	return token, nil
}

func watchOwner(
	ctx context.Context,
	cancel context.CancelFunc,
	filePath string,
	pollInterval time.Duration,
	timeout time.Duration,
) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			info, err := os.Stat(filePath)
			if err != nil || now.Sub(info.ModTime()) > timeout {
				cancel()
				return
			}
		}
	}
}
