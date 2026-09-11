package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

var internalHeaderNames = map[string]struct{}{
	strings.ToLower(headerToken):   {},
	strings.ToLower(headerScheme):  {},
	strings.ToLower(headerHost):    {},
	strings.ToLower(headerPort):    {},
	strings.ToLower(headerProfile): {},
	strings.ToLower(headerTrace):   {},
	strings.ToLower(headerMaxBody): {},
}

var fixedHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func forward(pool *clientPool, request *incomingRequest, metadata routeMetadata) (*fhttp.Response, error) {
	return forwardContext(context.Background(), pool, request, metadata)
}

func forwardContext(ctx context.Context, pool *clientPool, request *incomingRequest, metadata routeMetadata) (*fhttp.Response, error) {
	path, err := request.targetPath()
	if err != nil {
		return nil, errors.New("invalid request target")
	}

	authority := net.JoinHostPort(metadata.Host, metadata.Port)
	targetURL := fmt.Sprintf("%s://%s%s", metadata.Scheme, authority, path)
	upstream, err := fhttp.NewRequestWithContext(
		ctx,
		request.Method,
		targetURL,
		request.Body,
	)
	if err != nil {
		return nil, errors.New("create upstream request failed")
	}

	websocketUpgrade := request.isWebSocketUpgrade()
	connectionHeaders := request.connectionHeaderNames()
	headerOrder := make([]string, 0, len(request.Headers))
	orderedHeaders := make(map[string]struct{}, len(request.Headers))
	appendHeaderOrder := func(name string) {
		if _, found := orderedHeaders[name]; found {
			return
		}
		headerOrder = append(headerOrder, name)
		orderedHeaders[name] = struct{}{}
	}
	for _, item := range request.Headers {
		lowerName := strings.ToLower(item.Name)
		if _, internal := internalHeaderNames[lowerName]; internal {
			continue
		}
		websocketHeader := lowerName == "connection" || lowerName == "upgrade"
		if _, hop := fixedHopHeaders[lowerName]; hop && !(websocketUpgrade && websocketHeader) {
			continue
		}
		if _, hop := connectionHeaders[lowerName]; hop && !(websocketUpgrade && websocketHeader) {
			continue
		}

		appendHeaderOrder(lowerName)
		if lowerName == "host" || lowerName == "content-length" {
			continue
		}
		upstream.Header.Add(item.Name, item.Value)
	}
	if len(request.headerValues("User-Agent")) == 0 {
		upstream.Header.Set("User-Agent", "")
	}

	upstream.Host = request.firstHeader("Host")
	if upstream.Host == "" {
		upstream.Host = authority
	}
	upstream.ContentLength = request.BodyLength
	upstream.Close = false
	if len(headerOrder) > 0 {
		upstream.Header[fhttp.HeaderOrderKey] = headerOrder
	}

	client, err := pool.get(metadata.Profile)
	if websocketUpgrade {
		client, err = pool.getWebSocket(metadata.Profile)
	}
	if err != nil {
		return nil, err
	}

	response, err := client.Do(upstream)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return nil, fmt.Errorf("upstream request: %w", urlError.Err)
		}
		return nil, errors.New("upstream request failed")
	}
	return response, nil
}

func (request *incomingRequest) connectionHeaderNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, item := range request.Headers {
		if !strings.EqualFold(item.Name, "Connection") {
			continue
		}
		for _, name := range strings.Split(item.Value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" {
				names[name] = struct{}{}
			}
		}
	}
	return names
}

func (request *incomingRequest) isWebSocketUpgrade() bool {
	if !strings.EqualFold(request.firstHeader("Upgrade"), "websocket") {
		return false
	}
	_, found := request.connectionHeaderNames()["upgrade"]
	return found
}
