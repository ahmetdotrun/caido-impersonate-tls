package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const headerEgress = "X-Morpheus-Egress"
const maxEgressFileBytes = 2 * 1024 * 1024

var (
	errUnknownEgress = errors.New("unknown egress profile; direct fallback is disabled")
	errInvalidEgress = errors.New("exactly one non-empty X-Morpheus-Egress value is required")
	errEgressConfig  = errors.New("egress configuration is unavailable or invalid; direct fallback is disabled")
	errEgressRequest = errors.New("egress proxy connection or upstream request failed; direct fallback is disabled")
)

type egressProfile struct {
	Proxy string `json:"proxy"`
	// A future TLS profile override belongs here, independently of the route name.
}

type egressRoute struct {
	name  string
	proxy string
}

type egressStore struct {
	file     string
	mu       sync.Mutex
	digest   [32]byte
	profiles map[string]egressProfile
}

func (store *egressStore) resolve(values []string) (egressRoute, error) {
	if len(values) == 0 {
		return egressRoute{}, nil
	}
	if len(values) != 1 || values[0] == "" {
		return egressRoute{}, errInvalidEgress
	}
	if store.file == "" {
		return egressRoute{}, errUnknownEgress
	}
	file, err := os.Open(store.file)
	if err != nil {
		return egressRoute{}, errEgressConfig
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return egressRoute{}, errEgressConfig
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxEgressFileBytes+1))
	if err != nil || len(contents) > maxEgressFileBytes || !utf8.Valid(contents) {
		return egressRoute{}, errEgressConfig
	}
	digest := sha256.Sum256(contents)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.profiles == nil || digest != store.digest {
		var profiles map[string]egressProfile
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&profiles) != nil || profiles == nil || len(profiles) > 256 {
			return egressRoute{}, errEgressConfig
		}
		if decoder.Decode(new(any)) != io.EOF {
			return egressRoute{}, errEgressConfig
		}
		for name, profile := range profiles {
			if name == "" || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) > 128 ||
				strings.ContainsFunc(name, func(r rune) bool { return r < 32 || r == 127 }) {
				return egressRoute{}, errEgressConfig
			}
			if _, err := parseEgressProxy(profile.Proxy); err != nil {
				return egressRoute{}, errEgressConfig
			}
		}
		store.digest, store.profiles = digest, profiles
	}
	profile, found := store.profiles[values[0]]
	if !found {
		return egressRoute{}, errUnknownEgress
	}
	return egressRoute{name: values[0], proxy: profile.Proxy}, nil
}

func parseEgressProxy(raw string) (*url.URL, error) {
	if raw == "" || len(raw) > 16384 || strings.ContainsFunc(raw, unicode.IsSpace) {
		return nil, errEgressConfig
	}
	proxy, err := url.Parse(raw)
	if err != nil || (proxy.Scheme != "http" && proxy.Scheme != "socks5h") ||
		proxy.Opaque != "" || proxy.Hostname() == "" || strings.ContainsAny(proxy.Hostname(), "%\\") ||
		(proxy.Path != "" && proxy.Path != "/") || proxy.RawQuery != "" || proxy.ForceQuery || proxy.Fragment != "" {
		return nil, errEgressConfig
	}
	if strings.Contains(proxy.Hostname(), ":") && net.ParseIP(proxy.Hostname()) == nil {
		return nil, errEgressConfig
	}
	if proxy.Port() != "" {
		port, err := strconv.ParseUint(proxy.Port(), 10, 16)
		if err != nil || port == 0 {
			return nil, errEgressConfig
		}
	}
	return proxy, nil
}
