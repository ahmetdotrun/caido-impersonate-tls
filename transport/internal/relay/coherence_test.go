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
		},
		{
			name:    "missing User-Agent",
			profile: "chrome_152",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := profileCoherenceWarning(test.profile, test.userAgent); got != test.want {
				t.Errorf("warning = %q, want %q", got, test.want)
			}
		})
	}
}
