package relay

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/bogdanfinn/fhttp/http2"
	tls "github.com/bogdanfinn/utls"
)

func TestChrome152ProfileIdentity(t *testing.T) {
	profile, found := customTransportProfiles["chrome_152"]
	if !found {
		t.Fatal("Chrome 152 profile is not registered")
	}
	if got := profile.GetClientHelloStr(); got != "Chrome-152" {
		t.Fatalf("ClientHello ID = %q, want Chrome-152", got)
	}

	wantSettings := map[http2.SettingID]uint32{
		http2.SettingHeaderTableSize:   65536,
		http2.SettingEnablePush:        0,
		http2.SettingInitialWindowSize: 6291456,
		http2.SettingMaxHeaderListSize: 262144,
	}
	if !reflect.DeepEqual(profile.GetSettings(), wantSettings) {
		t.Errorf("HTTP/2 settings = %#v, want %#v", profile.GetSettings(), wantSettings)
	}
	wantOrder := []http2.SettingID{
		http2.SettingHeaderTableSize,
		http2.SettingEnablePush,
		http2.SettingInitialWindowSize,
		http2.SettingMaxHeaderListSize,
	}
	if !reflect.DeepEqual(profile.GetSettingsOrder(), wantOrder) {
		t.Errorf("HTTP/2 settings order = %#v, want %#v", profile.GetSettingsOrder(), wantOrder)
	}
	wantPseudoHeaders := []string{":method", ":authority", ":scheme", ":path"}
	if !reflect.DeepEqual(profile.GetPseudoHeaderOrder(), wantPseudoHeaders) {
		t.Errorf("pseudo-header order = %#v, want %#v", profile.GetPseudoHeaderOrder(), wantPseudoHeaders)
	}
	if got := profile.GetConnectionFlow(); got != 15663105 {
		t.Errorf("connection flow = %d, want 15663105", got)
	}
}

func TestChrome152SignatureAlgorithmGREASE(t *testing.T) {
	profile := customTransportProfiles["chrome_152"]
	seen := make(map[tls.SignatureScheme]struct{})

	for attempt := 0; attempt < 2000; attempt++ {
		spec, err := profile.GetClientHelloSpec()
		if err != nil {
			t.Fatalf("build ClientHello: %v", err)
		}

		var schemes []tls.SignatureScheme
		for _, extension := range spec.Extensions {
			if signatureAlgorithms, ok := extension.(*tls.SignatureAlgorithmsExtension); ok {
				schemes = signatureAlgorithms.SupportedSignatureAlgorithms
				break
			}
		}
		if len(schemes) == 0 {
			t.Fatal("Chrome 152 has no signature_algorithms extension")
		}

		value := uint16(schemes[0])
		if value>>8 != value&0xff || value&0x0f != 0x0a {
			t.Fatalf("first signature algorithm = 0x%04x, want GREASE", value)
		}
		seen[schemes[0]] = struct{}{}
	}

	if len(seen) != 16 {
		t.Errorf("observed %d of 16 GREASE signature algorithms", len(seen))
	}
}

func TestChrome152TrustAnchors(t *testing.T) {
	profile := customTransportProfiles["chrome_152"]
	for attempt := 0; attempt < 2; attempt++ {
		spec, err := profile.GetClientHelloSpec()
		if err != nil {
			t.Fatalf("build ClientHello: %v", err)
		}

		var payload []byte
		for _, extension := range spec.Extensions {
			if generic, ok := extension.(*tls.GenericExtension); ok && generic.Id == 0xca34 {
				payload = generic.Data
				break
			}
		}
		if !bytes.Equal(payload, chrome152TrustAnchors) {
			t.Fatal("trust_anchors payload changed within the process")
		}
	}

	if len(chrome152TrustAnchors) != 186 {
		t.Fatalf("trust_anchors payload length = %d, want 186", len(chrome152TrustAnchors))
	}
	if got := int(chrome152TrustAnchors[0])<<8 | int(chrome152TrustAnchors[1]); got != 184 {
		t.Fatalf("trust_anchors list length = %d, want 184", got)
	}

	records := 0
	for offset := 2; offset < len(chrome152TrustAnchors); {
		offset += 1 + int(chrome152TrustAnchors[offset])
		if offset > len(chrome152TrustAnchors) {
			t.Fatal("trust anchor ID runs past the payload")
		}
		records++
	}
	if records != 28 {
		t.Errorf("trust anchor count = %d, want 28", records)
	}
}
