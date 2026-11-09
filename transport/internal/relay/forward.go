package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	strings.ToLower(headerEgress):  {},
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
	egress, err := pool.egress.resolve(request.headerValues(headerEgress))
	if err != nil {
		return nil, err
	}
	path, err := request.targetPath()
	if err != nil {
		return nil, errors.New("invalid request target")
	}

	authority := net.JoinHostPort(metadata.Host, metadata.Port)
	targetURL := fmt.Sprintf("%s://%s/", metadata.Scheme, authority)
	upstream, err := fhttp.NewRequestWithContext(
		ctx,
		request.Method,
		targetURL,
		request.Body,
	)
	if err != nil {
		return nil, errors.New("create upstream request failed")
	}
	if path == "*" {
		upstream.URL.Path = "*"
	} else {
		parsed, err := url.ParseRequestURI(path)
		if err != nil {
			return nil, errors.New("invalid request target")
		}
		upstream.URL.Path = parsed.Path
		upstream.URL.RawPath = parsed.RawPath
		upstream.URL.RawQuery = parsed.RawQuery
		upstream.URL.ForceQuery = parsed.ForceQuery
	}

	websocketUpgrade := request.isWebSocketUpgrade()
	connectionHeaders := request.connectionHeaderNames()
	headerOrder := make([]string, 0, len(request.Headers))
	orderedHeaders := make(map[string]struct{}, len(request.Headers))
	headerNames := make(map[string]string, len(request.Headers))
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
		name, seen := headerNames[lowerName]
		if !seen {
			name = item.Name
			// fhttp handles these fields during serialization using canonical keys.
			switch lowerName {
			case "user-agent":
				name = "User-Agent"
			case "connection":
				name = "Connection"
			case "upgrade":
				name = "Upgrade"
			case "expect":
				name = "Expect"
			}
			headerNames[lowerName] = name
		}
		upstream.Header[name] = append(upstream.Header[name], item.Value)
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

	client, release, err := pool.acquire(metadata.Profile, metadata.Scheme+"://"+authority, websocketUpgrade, egress)
	if err != nil {
		if egress.name != "" && !errors.Is(err, errClientCapacity) {
			return nil, errEgressRequest
		}
		return nil, err
	}

	response, err := client.Do(upstream)
	if err != nil {
		release()
		if errors.Is(err, errBodyTooLarge) {
			return nil, errBodyTooLarge
		}
		if egress.name != "" {
			return nil, errEgressRequest
		}
		var urlError *url.Error
		if errors.As(err, &urlError) {
			return nil, fmt.Errorf("upstream request: %w", urlError.Err)
		}
		return nil, errors.New("upstream request failed")
	}
	body := &leasedBody{ReadCloser: response.Body, release: release}
	if duplex, ok := response.Body.(io.ReadWriteCloser); ok {
		response.Body = &leasedDuplexBody{leasedBody: body, writer: duplex}
	} else {
		response.Body = body
	}
	return response, nil
}

type leasedBody struct {
	io.ReadCloser
	release func()
}

func (body *leasedBody) Close() error {
	defer body.release()
	return body.ReadCloser.Close()
}

type leasedDuplexBody struct {
	*leasedBody
	writer io.Writer
}

func (body *leasedDuplexBody) Write(buffer []byte) (int, error) {
	return body.writer.Write(buffer)
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
