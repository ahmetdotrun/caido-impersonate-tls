package relay

import (
	"fmt"
	"regexp"
)

var chromeProfileMajorPattern = regexp.MustCompile(`^chrome_([0-9]+)(?:_|$)`)
var chromeUserAgentMajorPattern = regexp.MustCompile(`(?:Chrome|Chromium)/([0-9]+)(?:\.|\s|$)`)

func profileCoherenceWarning(profileName, userAgent string) string {
	profileMatch := chromeProfileMajorPattern.FindStringSubmatch(profileName)
	userAgentMatch := chromeUserAgentMajorPattern.FindStringSubmatch(userAgent)
	if len(profileMatch) != 2 || len(userAgentMatch) != 2 {
		return ""
	}
	if profileMatch[1] == userAgentMatch[1] {
		return ""
	}

	return fmt.Sprintf(
		"User-Agent reports Chrome %s but the selected transport profile is Chrome %s",
		userAgentMatch[1],
		profileMatch[1],
	)
}
