package relay

import "testing"

func TestProfileCoherenceWarning(t *testing.T) {
	tests := []struct {
		name      string
		profile   string
		userAgent string
		want      string
	}{
		{
			name:      "matching Chrome",
			profile:   "chrome_152",
			userAgent: "Mozilla/5.0 Chrome/152.0.7977.54 Safari/537.36",
		},
		{
			name:      "matching Chromium",
			profile:   "chrome_152",
			userAgent: "Mozilla/5.0 Chromium/152.0.7977.54 Safari/537.36",
		},
		{
			name:      "mismatch",
			profile:   "chrome_146",
			userAgent: "Mozilla/5.0 Chrome/152.0.7977.54 Safari/537.36",
			want:      "User-Agent reports Chrome 152 but the selected transport profile is Chrome 146",
		},
		{
			name:      "Firefox profile",
			profile:   "firefox_148",
			userAgent: "Mozilla/5.0 Chrome/152.0.7977.54 Safari/537.36",
			want:      "User-Agent browser family does not match the selected transport profile",
		},
		{
			name:    "missing User-Agent",
			profile: "chrome_152",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := profileCoherenceWarning(test.profile, &incomingRequest{Headers: []header{{Name: "User-Agent", Value: test.userAgent}}}); got != test.want {
				t.Errorf("warning = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClientHintsAndPlatformCoherence(t *testing.T) {
	for _, test := range []struct {
		name, profile, ua, hints, platform, mobile string
		warning                                    bool
	}{
		{"matching", "chrome_152_cft", "Mozilla/5.0 (X11; Linux x86_64) Chrome/152.0.0.0", `"Not_A Brand";v="99", "Chromium";v="152"`, `"Linux"`, "?0", false},
		{"hint version", "chrome_152_cft", "Chrome/152.0.0.0", `"Chromium";v="157"`, "", "", true},
		{"hint family", "firefox_148", "Firefox/148.0", `"Chromium";v="148"`, "", "", true},
		{"wrong OS", "chrome_152_cft", "Mozilla/5.0 (Windows NT 10.0) Chrome/152.0.0.0", "", "", "", true},
		{"hint OS", "chrome_152_cft", "Mozilla/5.0 (X11; Linux x86_64) Chrome/152.0.0.0", "", `"Windows"`, "", true},
		{"mobile", "chrome_152_cft", "Mozilla/5.0 (X11; Linux x86_64) Chrome/152.0.0.0", "", `"Linux"`, "?1", true},
		{"Firefox matching", "firefox_148", "Firefox/148.0", "", "", "", false},
		{"Safari family", "safari_ios_18_5", "Chrome/152.0.0.0", "", "", "", true},
		{"Safari version", "safari_ios_18_5", "Version/26.0 Safari/605.1.15", "", "", "", true},
		{"Opera matching", "opera_91", "Chrome/105.0.0.0 OPR/91.0", `"Chromium";v="105"`, "", "", false},
		{"Opera version", "opera_91", "Chrome/105.0.0.0 OPR/90.0", "", "", "", true},
		{"native app", "nike_ios_mobile", "Nike/1.0", "", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := &incomingRequest{Headers: []header{{"User-Agent", test.ua}, {"Sec-CH-UA", test.hints}, {"Sec-CH-UA-Platform", test.platform}, {"Sec-CH-UA-Mobile", test.mobile}}}
			warning := profileCoherenceWarning(test.profile, request)
			if (warning != "") != test.warning {
				t.Fatalf("warning = %q", warning)
			}
			if len(warning) > maxActivityErrorBytes {
				t.Fatal("warning exceeds activity limit")
			}
		})
	}
}
