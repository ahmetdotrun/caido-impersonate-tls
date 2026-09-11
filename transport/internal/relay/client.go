package relay

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const maxPooledClients = 256

var errClientCapacity = errors.New("transport origin-client capacity exhausted")

type clientKey struct {
	profile   string
	origin    string
	websocket bool
}

type pooledClient struct {
	client   tls_client.HttpClient
	users    int
	lastUsed uint64
}

type clientPool struct {
	mu       sync.Mutex
	clients  map[clientKey]*pooledClient
	capacity int
	clock    uint64
	closed   bool
	create   func(string, bool) (tls_client.HttpClient, error)
}

func newClientPool() *clientPool {
	return &clientPool{
		clients:  make(map[clientKey]*pooledClient),
		capacity: maxPooledClients,
		create: func(profile string, websocket bool) (tls_client.HttpClient, error) {
			return newProfileClient(profile, websocket, nil)
		},
	}
}

// tls-client serializes initial TLS negotiation inside a client. Partition by
// origin so a slow site cannot hold the transport lock for unrelated traffic.
// A lease lasts through response-body close (or the WebSocket lifetime).
func (pool *clientPool) acquire(profileName, origin string, websocket bool) (tls_client.HttpClient, func(), error) {
	pool.mu.Lock()
	var evicted tls_client.HttpClient
	defer func() {
		pool.mu.Unlock()
		if evicted != nil {
			evicted.CloseIdleConnections()
		}
	}()
	if pool.closed {
		return nil, nil, errors.New("transport client pool is closed")
	}
	key := clientKey{profileName, strings.ToLower(origin), websocket}
	entry := pool.clients[key]
	if entry == nil {
		if len(pool.clients) >= pool.capacity {
			var oldest *pooledClient
			var oldestKey clientKey
			for candidateKey, candidate := range pool.clients {
				if candidate.users == 0 && (oldest == nil || candidate.lastUsed < oldest.lastUsed) {
					oldest, oldestKey = candidate, candidateKey
				}
			}
			if oldest == nil {
				return nil, nil, errClientCapacity
			}
			delete(pool.clients, oldestKey)
			evicted = oldest.client
		}
		client, err := pool.create(profileName, websocket)
		if err != nil {
			return nil, nil, err
		}
		entry = &pooledClient{client: client}
		pool.clients[key] = entry
	}
	pool.clock++
	entry.lastUsed = pool.clock
	entry.users++
	release := sync.OnceFunc(func() {
		pool.mu.Lock()
		entry.users--
		pool.clock++
		entry.lastUsed = pool.clock
		closed := pool.closed
		pool.mu.Unlock()
		if closed {
			entry.client.CloseIdleConnections()
		}
	})
	return entry.client, release, nil
}

func (pool *clientPool) close() {
	pool.mu.Lock()
	pool.closed = true
	clients := pool.clients
	pool.clients = make(map[clientKey]*pooledClient)
	pool.mu.Unlock()
	for _, entry := range clients {
		entry.client.CloseIdleConnections()
	}
}

func newProfileClient(profileName string, forceHTTP1 bool, roots *x509.CertPool) (tls_client.HttpClient, error) {
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
			RootCAs:                roots,
			DisableCompression:     true,
			MaxIdleConns:           16,
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

	return client, nil
}
