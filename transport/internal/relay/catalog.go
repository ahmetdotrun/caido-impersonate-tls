package relay

import (
	_ "embed"
	"encoding/json"
)

//go:embed catalog.json
var catalogJSON []byte

type profileInfo struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Family         string   `json:"family"`
	Version        string   `json:"version,omitempty"`
	Platform       string   `json:"platform,omitempty"`
	Source         string   `json:"source"`
	VerifiedBuilds []string `json:"verifiedBuilds,omitempty"`
	Verification   string   `json:"verification,omitempty"`
	HTTP2Headers   string   `json:"http2Headers"`
}

type profileCatalog struct {
	Version             string                        `json:"version"`
	TLSClientVersion    string                        `json:"tlsClientVersion"`
	Profiles            []profileInfo                 `json:"profiles"`
	HTTP2HeaderProfiles map[string]http2HeaderProfile `json:"http2HeaderProfiles"`
}

var catalog = func() profileCatalog {
	var result profileCatalog
	if err := json.Unmarshal(catalogJSON, &result); err != nil {
		panic(err)
	}
	return result
}()

var Version = catalog.Version

func profileMetadata(id string) (profileInfo, bool) {
	for _, profile := range catalog.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return profileInfo{}, false
}
