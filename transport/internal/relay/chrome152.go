package relay

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math/big"

	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
)

// Captured from Linux Chrome for Testing 152.0.7977.54 and .82.
const chrome152CfTTrustAnchorsCapture = "00cc04d679090804d67909020582df13020e04d679090e08839a648c9b2d011308839a648c9b2d010708839a648c9b2d010d04d67909090582df13020d04d679090608839a648c9b2d010c0582df1302130582df13020604d679090404d679090f0582df13020f04d679090a04d67909070582df13021204d67909050582df13020104d679090b0582df13021408839a648c9b2d010804d679090c08839a648c9b2d010a08839a648c9b2d010904d679090308839a648c9b2d010b04d679090104d679090d08839a648c9b2d0112"

var chrome152CfTTrustAnchors = shuffledTrustAnchors(chrome152CfTTrustAnchorsCapture)
var chrome152TrustAnchors = profileTrustAnchors(profiles.Chrome_152)

// Linux CfT 157.0.8103.38 emitted this order in independent browser processes.
const chrome157CfTTrustAnchorsCapture = "00b80582df1302010582df1302060582df13020d0582df13020e0582df13020f0582df1302120582df1302130582df13021408839a648c9b2d010708839a648c9b2d010808839a648c9b2d010908839a648c9b2d010a08839a648c9b2d010b08839a648c9b2d010c08839a648c9b2d010d08839a648c9b2d011208839a648c9b2d011304d679090104d679090404d679090504d679090604d679090704d679090804d679090a04d679090b04d679090c04d679090d04d679090f"

var chrome157CfTTrustAnchors = func() []byte {
	data, err := hex.DecodeString(chrome157CfTTrustAnchorsCapture)
	if err != nil {
		panic(err)
	}
	return data
}()

var customTransportProfiles = map[string]profiles.ClientProfile{
	"chrome_152":     profiles.Chrome_152,
	"chrome_152_cft": chromeForTestingProfile(profiles.Chrome_152, "152", chrome152CfTTrustAnchors),
	// The 157 Linux capture has the base 152 anchor set and server_padding.
	// The local destination comparison verifies the complete normalized shape.
	"chrome_157_cft":     chromeForTestingProfile(profiles.Chrome_152, "157", chrome157CfTTrustAnchors),
	"chrome_157_cft_PSK": chromeForTestingProfile(profiles.Chrome_152_PSK, "157_PSK", chrome157CfTTrustAnchors),
}

func profileTrustAnchors(profile profiles.ClientProfile) []byte {
	spec, err := profile.GetClientHelloSpec()
	if err != nil {
		panic(err)
	}
	for _, extension := range spec.Extensions {
		if generic, ok := extension.(*tls.GenericExtension); ok && generic.Id == 0xca34 {
			return generic.Data
		}
	}
	panic("base Chrome profile has no trust anchors")
}

func chromeForTestingProfile(base profiles.ClientProfile, version string, anchors []byte) profiles.ClientProfile {
	id := base.GetClientHelloId()
	id.Version = version
	id.SpecFactory = func() (tls.ClientHelloSpec, error) {
		spec, err := base.GetClientHelloSpec()
		if err != nil {
			return spec, err
		}
		paddingIndex := len(spec.Extensions)
		for i, extension := range spec.Extensions {
			if generic, ok := extension.(*tls.GenericExtension); ok && generic.Id == 0xca34 && anchors != nil {
				spec.Extensions[i] = &tls.GenericExtension{Id: 0xca34, Data: anchors}
			}
			if _, grease := extension.(*tls.UtlsGREASEExtension); grease {
				paddingIndex = i
			}
		}
		// Preserve terminal GREASE and the PSK extension's required last position.
		spec.Extensions = append(spec.Extensions, nil)
		copy(spec.Extensions[paddingIndex+1:], spec.Extensions[paddingIndex:])
		spec.Extensions[paddingIndex] = &tls.GenericExtension{Id: 4832, Data: []byte{0, 0}}
		return spec, nil
	}
	return profiles.NewClientProfile(id, base.GetSettings(), base.GetSettingsOrder(), base.GetPseudoHeaderOrder(),
		base.GetConnectionFlow(), base.GetPriorities(), base.GetHeaderPriority(), base.GetStreamID(), base.GetAllowHTTP(),
		nil, nil, 0, nil, false)
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
