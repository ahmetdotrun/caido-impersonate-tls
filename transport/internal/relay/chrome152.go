package relay

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math/big"

	"github.com/bogdanfinn/fhttp/http2"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
)

// Captured profile source: https://github.com/bogdanfinn/tls-client/pull/265
const chrome152TrustAnchorsCapture = "00b80582df13020108839a648c9b2d010c08839a648c9b2d010704d679090c08839a648c9b2d010a04d679090b08839a648c9b2d010d0582df13020e08839a648c9b2d010b04d67909050582df13020d0582df13021404d679090404d679090804d679090d04d679090a04d679090708839a648c9b2d011204d67909010582df13020608839a648c9b2d01080582df13021208839a648c9b2d011304d679090f0582df13021308839a648c9b2d01090582df13020f04d6790906"

var chrome152TrustAnchors = shuffledTrustAnchors(chrome152TrustAnchorsCapture)

var customTransportProfiles = map[string]profiles.ClientProfile{
	"chrome_152": newChrome152Profile(),
}

func newChrome152Profile() profiles.ClientProfile {
	clientHelloID := tls.ClientHelloID{
		Client:               "Chrome",
		RandomExtensionOrder: false,
		Version:              "152",
		Seed:                 nil,
		SpecFactory: func() (tls.ClientHelloSpec, error) {
			return tls.ClientHelloSpec{
				CipherSuites: []uint16{
					tls.GREASE_PLACEHOLDER,
					tls.TLS_AES_128_GCM_SHA256,
					tls.TLS_AES_256_GCM_SHA384,
					tls.TLS_CHACHA20_POLY1305_SHA256,
					tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
					tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
					tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
					tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
					tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
					tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
					tls.TLS_RSA_WITH_AES_128_CBC_SHA,
					tls.TLS_RSA_WITH_AES_256_CBC_SHA,
				},
				CompressionMethods: []byte{tls.CompressionNone},
				Extensions: []tls.TLSExtension{
					&tls.UtlsGREASEExtension{},
					&tls.KeyShareExtension{KeyShares: []tls.KeyShare{
						{Group: tls.CurveID(tls.GREASE_PLACEHOLDER), Data: []byte{0}},
						{Group: tls.X25519MLKEM768},
						{Group: tls.X25519},
					}},
					&tls.SNIExtension{},
					&tls.ApplicationSettingsExtensionNew{SupportedProtocols: []string{"h2"}},
					&tls.RenegotiationInfoExtension{Renegotiation: tls.RenegotiateOnceAsClient},
					&tls.SupportedCurvesExtension{Curves: []tls.CurveID{
						tls.GREASE_PLACEHOLDER,
						tls.X25519MLKEM768,
						tls.X25519,
						tls.CurveP256,
						tls.CurveP384,
					}},
					&tls.UtlsCompressCertExtension{Algorithms: []tls.CertCompressionAlgo{
						tls.CertCompressionBrotli,
					}},
					&tls.SessionTicketExtension{},
					&tls.StatusRequestExtension{},
					&tls.ExtendedMasterSecretExtension{},
					&tls.SupportedVersionsExtension{Versions: []uint16{
						tls.GREASE_PLACEHOLDER,
						tls.VersionTLS13,
						tls.VersionTLS12,
					}},
					&tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tls.SignatureScheme{
						chrome152GREASESignatureScheme(),
						tls.SignatureScheme(0x0904),
						tls.SignatureScheme(0x0905),
						tls.SignatureScheme(0x0906),
						tls.ECDSAWithP256AndSHA256,
						tls.PSSWithSHA256,
						tls.PKCS1WithSHA256,
						tls.ECDSAWithP384AndSHA384,
						tls.PSSWithSHA384,
						tls.PKCS1WithSHA384,
						tls.PSSWithSHA512,
						tls.PKCS1WithSHA512,
					}},
					&tls.SCTExtension{},
					&tls.SupportedPointsExtension{SupportedPoints: []byte{tls.PointFormatUncompressed}},
					tls.BoringGREASEECH(),
					&tls.ALPNExtension{AlpnProtocols: []string{"h2", "http/1.1"}},
					&tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}},
					&tls.GenericExtension{Id: 0xca34, Data: chrome152TrustAnchors},
					&tls.UtlsGREASEExtension{},
				},
			}, nil
		},
	}

	settings := map[http2.SettingID]uint32{
		http2.SettingHeaderTableSize:   65536,
		http2.SettingEnablePush:        0,
		http2.SettingInitialWindowSize: 6291456,
		http2.SettingMaxHeaderListSize: 262144,
	}
	settingsOrder := []http2.SettingID{
		http2.SettingHeaderTableSize,
		http2.SettingEnablePush,
		http2.SettingInitialWindowSize,
		http2.SettingMaxHeaderListSize,
	}
	pseudoHeaderOrder := []string{":method", ":authority", ":scheme", ":path"}

	return profiles.NewClientProfile(
		clientHelloID,
		settings,
		settingsOrder,
		pseudoHeaderOrder,
		15663105,
		nil,
		nil,
		0,
		false,
		nil,
		nil,
		0,
		nil,
		false,
	)
}

// Chrome 152 draws a GREASE signature algorithm for every ClientHello. The
// pinned uTLS fork does not yet replace a GREASE placeholder in this extension,
// so the profile chooses the wire value in its per-connection SpecFactory.
func chrome152GREASESignatureScheme() tls.SignatureScheme {
	var seed [2]byte
	if _, err := rand.Read(seed[:]); err != nil {
		panic(err)
	}

	value := binary.LittleEndian.Uint16(seed[:])
	value = (value & 0x00f0) | 0x000a
	value |= value << 8
	return tls.SignatureScheme(value)
}

// Chrome writes trust anchor IDs in process-stable hash-table order. Shuffle
// the captured Chrome Root Store 39 IDs once at process start to reproduce that
// behavior without hard-coding the captured machine's order.
func shuffledTrustAnchors(capture string) []byte {
	payload, err := hex.DecodeString(capture)
	if err != nil {
		panic(err)
	}
	if len(payload) < 2 {
		panic("Chrome 152 trust anchor payload is truncated")
	}

	list := payload[2:]
	if int(binary.BigEndian.Uint16(payload[:2])) != len(list) {
		panic("Chrome 152 trust anchor payload has an invalid length")
	}

	records := make([][]byte, 0, 28)
	for offset := 0; offset < len(list); {
		end := offset + 1 + int(list[offset])
		if end > len(list) {
			panic("Chrome 152 trust anchor ID is truncated")
		}
		records = append(records, list[offset:end])
		offset = end
	}

	for index := len(records) - 1; index > 0; index-- {
		selected, err := rand.Int(rand.Reader, big.NewInt(int64(index+1)))
		if err != nil {
			panic(err)
		}
		other := int(selected.Int64())
		records[index], records[other] = records[other], records[index]
	}

	result := make([]byte, 2, len(payload))
	for _, record := range records {
		result = append(result, record...)
	}
	binary.BigEndian.PutUint16(result[:2], uint16(len(result)-2))
	return result
}
