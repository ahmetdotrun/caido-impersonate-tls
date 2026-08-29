package relay

import (
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type fingerprintResponse struct {
	HTTPVersion string `json:"http_version"`
	TLS         struct {
		JA3     string `json:"ja3"`
		JA3Hash string `json:"ja3_hash"`
		JA4     string `json:"ja4"`
	} `json:"tls"`
	HTTP2 struct {
		AkamaiFingerprint     string `json:"akamai_fingerprint"`
		AkamaiFingerprintHash string `json:"akamai_fingerprint_hash"`
	} `json:"http2"`
}

type expectedFingerprint struct {
	profile       string
	ja3           string
	normalizedJA3 string
	ja4           string
	http2         string
}

var advertisedFingerprints = []expectedFingerprint{
	{
		profile:       "chrome_152",
		normalizedJA3: "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-5-10-11-13-16-18-23-27-35-43-45-51-17613-51764-65037-65281,4588-29-23-24,0",
		ja4:           "t13d1517h2_8daaf6152771_cb7bf5808d99",
		http2:         "52d84b11737d980aef856699f885ca86",
	},
	{
		profile:       "chrome_146",
		normalizedJA3: "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-5-10-11-13-16-18-23-27-35-43-45-51-17613-51764-65037-65281,4588-29-23-24,0",
		ja4:           "t13d1517h2_8daaf6152771_dcad5a053991",
		http2:         "52d84b11737d980aef856699f885ca86",
	},
	{
		profile:       "chrome_144",
		normalizedJA3: "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-5-10-11-13-16-18-23-27-35-43-45-51-17613-65037-65281,4588-29-23-24,0",
		ja4:           "t13d1516h2_8daaf6152771_d8a2da3f94cd",
		http2:         "52d84b11737d980aef856699f885ca86",
	},
	{
		profile: "firefox_148",
		ja3:     "992b82b242c18c86da84eff5bf3e3100",
		ja4:     "t13d1917h2_4d8ed5baf28e_3cbfd9057e0d",
		http2:   "6ea73faa8fc5aac76bded7bd238f6433",
	},
	{
		profile: "firefox_147",
		ja3:     "6f7889b9fb1a62a9577e685c1fcfa919",
		ja4:     "t13d1717h2_5b57614c22b0_68c5a8c2958d",
		http2:   "6ea73faa8fc5aac76bded7bd238f6433",
	},
	{
		profile: "safari_ios_18_5",
		ja3:     "773906b0efdefa24a7f2b8eb6985bf37",
		ja4:     "t13d2014h2_a09f3c656075_7f0f34a4126d",
		http2:   "c52879e43202aeb92740be6e8c86ea96",
	},
	{
		profile: "safari_ios_26_0",
		ja3:     "ecdf4f49dd59effc439639da29186671",
		ja4:     "t13d2013h2_a09f3c656075_7f0f34a4126d",
		http2:   "c52879e43202aeb92740be6e8c86ea96",
	},
	{
		profile: "okhttp4_android_13",
		ja3:     "f79b6bad2ad0641e1921aef10262856b",
		ja4:     "t13d1513h2_8daaf6152771_40271e0a5736",
		http2:   "605a1154008045d7e3cb3c6fb062c0ce",
	},
}

func TestLiveAdvertisedProfileFingerprints(t *testing.T) {
	if os.Getenv("CAIDO_LIVE_FINGERPRINT_TEST") != "1" {
		t.Skip("set CAIDO_LIVE_FINGERPRINT_TEST=1 to contact tls.peet.ws")
	}

	pool := newClientPool()
	for _, expected := range advertisedFingerprints {
		t.Run(expected.profile, func(t *testing.T) {
			fingerprint := fetchLiveFingerprint(t, pool, expected.profile)
			assertFingerprint(t, expected, fingerprint)

			t.Logf(
				"HTTP=%s JA3=%s JA3Hash=%s JA4=%s Akamai=%s AkamaiHash=%s",
				fingerprint.HTTPVersion,
				fingerprint.TLS.JA3,
				fingerprint.TLS.JA3Hash,
				fingerprint.TLS.JA4,
				fingerprint.HTTP2.AkamaiFingerprint,
				fingerprint.HTTP2.AkamaiFingerprintHash,
			)

			if expected.normalizedJA3 != "" {
				second := fetchLiveFingerprint(t, newClientPool(), expected.profile)
				assertFingerprint(t, expected, second)
				if second.TLS.JA3Hash == fingerprint.TLS.JA3Hash {
					t.Errorf("Chrome JA3 did not change across independent handshakes: %s", second.TLS.JA3Hash)
				}
			}
		})
	}
}

func fetchLiveFingerprint(t *testing.T, pool *clientPool, profile string) fingerprintResponse {
	t.Helper()
	request := &incomingRequest{
		Method:     "GET",
		RequestURI: "/api/all",
		Protocol:   "HTTP/1.1",
		Headers: []header{
			{Name: "Host", Value: "tls.peet.ws"},
			{Name: "User-Agent", Value: "caido-impersonate-tls-fingerprint-audit"},
		},
	}
	metadata := routeMetadata{
		Scheme:  "https",
		Host:    "tls.peet.ws",
		Port:    "443",
		Profile: profile,
	}

	response, err := forward(pool, request, metadata)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("status = %d, body = %.500s", response.StatusCode, body)
	}

	var fingerprint fingerprintResponse
	if err := json.Unmarshal(body, &fingerprint); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return fingerprint
}

func assertFingerprint(t *testing.T, expected expectedFingerprint, fingerprint fingerprintResponse) {
	t.Helper()
	if fingerprint.HTTPVersion != "h2" {
		t.Errorf("HTTP version = %q, want h2", fingerprint.HTTPVersion)
	}
	if expected.normalizedJA3 != "" {
		if normalized := normalizeJA3ExtensionOrder(t, fingerprint.TLS.JA3); normalized != expected.normalizedJA3 {
			t.Errorf("normalized JA3 = %q, want %q", normalized, expected.normalizedJA3)
		}
		if len(fingerprint.TLS.JA3Hash) != 32 {
			t.Errorf("JA3 hash = %q", fingerprint.TLS.JA3Hash)
		}
	} else if fingerprint.TLS.JA3Hash != expected.ja3 {
		t.Errorf("JA3 hash = %q, want %q", fingerprint.TLS.JA3Hash, expected.ja3)
	}
	if fingerprint.TLS.JA4 != expected.ja4 {
		t.Errorf("JA4 = %q, want %q", fingerprint.TLS.JA4, expected.ja4)
	}
	if fingerprint.HTTP2.AkamaiFingerprintHash != expected.http2 {
		t.Errorf("Akamai HTTP/2 hash = %q, want %q", fingerprint.HTTP2.AkamaiFingerprintHash, expected.http2)
	}
}

func normalizeJA3ExtensionOrder(t *testing.T, value string) string {
	t.Helper()
	parts := strings.Split(value, ",")
	if len(parts) != 5 {
		t.Fatalf("JA3 has %d components: %q", len(parts), value)
	}

	extensions := strings.Split(parts[2], "-")
	numericExtensions := make([]int, len(extensions))
	for index, extension := range extensions {
		parsed, err := strconv.Atoi(extension)
		if err != nil {
			t.Fatalf("parse JA3 extension %q: %v", extension, err)
		}
		numericExtensions[index] = parsed
	}
	sort.Ints(numericExtensions)
	for index, extension := range numericExtensions {
		extensions[index] = strconv.Itoa(extension)
	}
	parts[2] = strings.Join(extensions, "-")
	return strings.Join(parts, ",")
}
