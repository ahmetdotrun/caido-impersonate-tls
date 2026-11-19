package relay

import (
	"slices"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/httptrace"
	tls "github.com/bogdanfinn/utls"
)

type http2HeaderProfile struct {
	Order                 []string                   `json:"order,omitempty"`
	OrderOverrides        []http2HeaderOrderOverride `json:"orderOverrides,omitempty"`
	PriorityByDestination map[string]string          `json:"priorityByDestination,omitempty"`
	PriorityOverrides     []http2PriorityOverride    `json:"priorityOverrides,omitempty"`
}

type http2HeaderOrderOverride struct {
	Destinations []string `json:"destinations"`
	Order        []string `json:"order"`
}

type http2PriorityOverride struct {
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	Value       string `json:"value"`
}

func withProfileHTTP2Headers(request *fhttp.Request, profileName string) *fhttp.Request {
	metadata, found := profileMetadata(profileName)
	if !found {
		return request
	}
	profile := catalog.HTTP2HeaderProfiles[metadata.HTTP2Headers]
	if len(profile.Order)+len(profile.OrderOverrides)+len(profile.PriorityByDestination)+len(profile.PriorityOverrides) == 0 {
		return request
	}
	// Pinned fhttp calls GotConn synchronously before serializing each request,
	// including reused H2 streams. ALPN is known here; HTTPS alone does not imply H2.
	trace := &httptrace.ClientTrace{GotConn: func(connection httptrace.GotConnInfo) {
		tlsConnection, ok := connection.Conn.(interface{ ConnectionState() tls.ConnectionState })
		if ok && tlsConnection.ConnectionState().NegotiatedProtocol == "h2" {
			profile.apply(request.Header)
		}
	}}
	return request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
}

func (profile http2HeaderProfile) apply(headers fhttp.Header) {
	destination, validDestination := singleHeaderValue(headers, "sec-fetch-dest")
	prioritySupplied := false
	for name := range headers {
		if strings.EqualFold(name, "priority") {
			prioritySupplied = true
			break
		}
	}
	if !prioritySupplied && validDestination {
		priority := profile.PriorityByDestination[destination]
		mode, validMode := singleHeaderValue(headers, "sec-fetch-mode")
		for _, override := range profile.PriorityOverrides {
			if validMode && destination == override.Destination && mode == override.Mode {
				priority = override.Value
				break
			}
		}
		if priority != "" {
			headers["Priority"] = []string{priority}
		}
	}

	order := profile.Order
	for _, override := range profile.OrderOverrides {
		if slices.Contains(override.Destinations, destination) {
			order = override.Order
			break
		}
	}
	if len(order) == 0 {
		return
	}
	original := headers[fhttp.HeaderOrderKey]
	ordered := make([]string, 0, len(order)+len(original))
	seen := make(map[string]bool, cap(ordered))
	for _, names := range [][]string{order, original} {
		for _, name := range names {
			name = strings.ToLower(name)
			if !seen[name] {
				ordered = append(ordered, name)
				seen[name] = true
			}
		}
	}
	headers[fhttp.HeaderOrderKey] = ordered
}

// Ambiguous fetch metadata cannot safely determine a browser-added value.
func singleHeaderValue(headers fhttp.Header, name string) (string, bool) {
	var value string
	count := 0
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			count += len(values)
			if len(values) == 1 {
				value = values[0]
			}
		}
	}
	if count != 1 {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(value)), true
}
