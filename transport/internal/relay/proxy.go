package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

const proxyConnectTimeout = 30 * time.Second

// Only the proxy endpoint is dialed locally. The destination hostname is sent
// unchanged in CONNECT or SOCKS5, including for plain HTTP and WebSocket.
func newEgressDialer(raw string) (proxy.ContextDialer, error) {
	endpoint, err := parseEgressProxy(raw)
	if err != nil {
		return nil, errEgressConfig
	}
	port := endpoint.Port()
	if port == "" {
		port = "80"
		if endpoint.Scheme == "socks5h" {
			port = "1080"
		}
	}
	address := net.JoinHostPort(endpoint.Hostname(), port)
	dialer := &net.Dialer{Timeout: proxyConnectTimeout, KeepAlive: 30 * time.Second}
	if endpoint.Scheme == "socks5h" {
		var auth *proxy.Auth
		if endpoint.User != nil {
			password, _ := endpoint.User.Password()
			auth = &proxy.Auth{User: endpoint.User.Username(), Password: password}
		}
		socks, err := proxy.SOCKS5("tcp", address, auth, dialer)
		if err != nil {
			return nil, errEgressConfig
		}
		contextDialer, ok := socks.(proxy.ContextDialer)
		if !ok {
			return nil, errEgressConfig
		}
		return timedProxyDialer{dial: contextDialer.DialContext}, nil
	}
	return timedProxyDialer{dial: func(ctx context.Context, network, target string) (net.Conn, error) {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, err
		}
		deadline, _ := ctx.Deadline()
		_ = connection.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
		request := &http.Request{Method: "CONNECT", URL: &url.URL{Host: target}, Host: target, Header: make(http.Header)}
		if endpoint.User != nil {
			password, _ := endpoint.User.Password()
			request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(endpoint.User.Username()+":"+password)))
		}
		err = request.Write(connection)
		var buffered []byte
		if err == nil {
			limit := &io.LimitedReader{R: connection, N: maxHeaderBytes + 1}
			reader := bufio.NewReader(limit)
			response, readErr := http.ReadResponse(reader, request)
			err = readErr
			if err == nil && (response.StatusCode != http.StatusOK || limit.N == 0) {
				err = errEgressRequest
			}
			if err == nil && reader.Buffered() > 0 {
				buffered, err = io.ReadAll(io.LimitReader(reader, int64(reader.Buffered())))
			}
		}
		if !stop() && err == nil {
			err = ctx.Err()
		}
		if err != nil {
			_ = connection.Close()
			return nil, err
		}
		_ = connection.SetDeadline(time.Time{})
		return &proxyConn{Conn: connection, reader: io.MultiReader(bytes.NewReader(buffered), connection)}, nil
	}}, nil
}

type timedProxyDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (dialer timedProxyDialer) Dial(network, address string) (net.Conn, error) {
	return dialer.DialContext(context.Background(), network, address)
}

func (dialer timedProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errEgressRequest
	}
	bounded, cancel := context.WithTimeout(ctx, proxyConnectTimeout)
	defer cancel()
	connection, err := dialer.dial(bounded, network, address)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// Library and peer errors can contain proxy URLs or reflected credentials.
		return nil, errEgressRequest
	}
	return connection, nil
}

type proxyConn struct {
	net.Conn
	reader io.Reader
}

func (connection *proxyConn) Read(bytes []byte) (int, error) { return connection.reader.Read(bytes) }
