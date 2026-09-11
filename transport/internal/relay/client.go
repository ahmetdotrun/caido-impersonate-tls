package relay

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type clientPool struct {
	mu               sync.Mutex
	clients          map[string]tls_client.HttpClient
	websocketClients map[string]tls_client.HttpClient
}

func newClientPool() *clientPool {
	return &clientPool{
		clients:          make(map[string]tls_client.HttpClient),
		websocketClients: make(map[string]tls_client.HttpClient),
	}
}

func (pool *clientPool) get(profileName string) (tls_client.HttpClient, error) {
	return pool.getForProtocol(profileName, false)
}

func (pool *clientPool) getWebSocket(profileName string) (tls_client.HttpClient, error) {
	return pool.getForProtocol(profileName, true)
}

func (pool *clientPool) getForProtocol(profileName string, forceHTTP1 bool) (tls_client.HttpClient, error) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	clients := pool.clients
	if forceHTTP1 {
		clients = pool.websocketClients
	}
	if client, found := clients[profileName]; found {
		return client, nil
	}

	profile, found := customTransportProfiles[profileName]
	if !found {
		profile, found = profiles.MappedTLSClients[profileName]
		if !found {
			return nil, fmt.Errorf("unknown transport profile %q", profileName)
		}
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profile),
		tls_client.WithNotFollowRedirects(),
		// The relay owns header and inactivity deadlines. A whole-response
		// timeout would truncate healthy streams and slow downloads.
		tls_client.WithTimeoutSeconds(0),
		tls_client.WithDialer(net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}),
		tls_client.WithTransportOptions(&tls_client.TransportOptions{
			DisableCompression:     true,
			MaxIdleConns:           128,
			MaxIdleConnsPerHost:    16,
			MaxResponseHeaderBytes: maxHeaderBytes,
		}),
	}
	if strings.HasPrefix(profileName, "chrome_") {
		options = append(options, tls_client.WithRandomTLSExtensionOrder())
	}
	if forceHTTP1 {
		// RFC 6455 uses an HTTP/1.1 Upgrade handshake. tls-client requires a
		// dedicated HTTP/1.1 transport so it does not negotiate h2 and reject
		// the Upgrade header before the request reaches the target.
		options = append(options, tls_client.WithForceHttp1())
	}

	client, err := tls_client.NewHttpClient(nil, options...)
	if err != nil {
		return nil, fmt.Errorf("create transport profile %q: %w", profileName, err)
	}

	clients[profileName] = client
	return client, nil
}
