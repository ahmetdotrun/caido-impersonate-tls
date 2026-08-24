package relay

import (
	"fmt"
	"strings"
	"sync"

	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type clientPool struct {
	mu      sync.Mutex
	clients map[string]tls_client.HttpClient
}

func newClientPool() *clientPool {
	return &clientPool{clients: make(map[string]tls_client.HttpClient)}
}

func (pool *clientPool) get(profileName string) (tls_client.HttpClient, error) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	if client, found := pool.clients[profileName]; found {
		return client, nil
	}

	profile, found := profiles.MappedTLSClients[profileName]
	if !found {
		return nil, fmt.Errorf("unknown transport profile %q", profileName)
	}

	options := []tls_client.HttpClientOption{
		tls_client.WithClientProfile(profile),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithTimeoutSeconds(60),
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

	client, err := tls_client.NewHttpClient(nil, options...)
	if err != nil {
		return nil, fmt.Errorf("create transport profile %q: %w", profileName, err)
	}

	pool.clients[profileName] = client
	return client, nil
}
