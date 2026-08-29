package relay

import "testing"

func TestBundledProfilesAreAvailable(t *testing.T) {
	pool := newClientPool()
	for _, profile := range []string{
		"chrome_152",
		"chrome_146",
		"chrome_144",
		"firefox_148",
		"firefox_147",
		"safari_ios_18_5",
		"safari_ios_26_0",
		"okhttp4_android_13",
	} {
		if _, err := pool.get(profile); err != nil {
			t.Fatalf("profile %q is unavailable: %v", profile, err)
		}
	}
}
