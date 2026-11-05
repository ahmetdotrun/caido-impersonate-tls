package relay

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var browserVersionPattern = regexp.MustCompile(`(Chrome|Chromium|Firefox|Version)/([0-9]{1,4})(?:\.|\s|$)`)
var operaVersionPattern = regexp.MustCompile(`OPR/([0-9]{1,4})(?:\.|\s|$)`)
var chromiumHintPattern = regexp.MustCompile(`"(?:Chromium|Google Chrome)"\s*;\s*v="([0-9]{1,4})"`)

func profileCoherenceWarning(profileName string, request *incomingRequest) string {
	profile, found := profileMetadata(profileName)
	if !found {
		return ""
	}
	ua := request.firstHeader("User-Agent")
	family, major := userAgentIdentity(ua)
	expectedMajor := strings.Split(profile.Version, ".")[0]
	var warnings []string
	if family != "" && profile.Family != "application" && family != profile.Family {
		warnings = append(warnings, "User-Agent browser family does not match the selected transport profile")
	} else if major != "" && expectedMajor != "" && major != expectedMajor &&
		(strings.HasPrefix(profileName, "chrome_") || strings.HasPrefix(profileName, "brave_") || profile.Family == "firefox" || profile.Family == "safari") {
		browser := "Chrome"
		if family == "firefox" {
			browser = "Firefox"
		}
		if family == "safari" {
			browser = "Safari"
		}
		warnings = append(warnings, fmt.Sprintf("User-Agent reports %s %s but the selected transport profile is %s %s", browser, major, browser, expectedMajor))
	}
	if strings.HasPrefix(profileName, "opera_") {
		if match := operaVersionPattern.FindStringSubmatch(ua); len(match) == 2 && match[1] != expectedMajor {
			warnings = append(warnings, "User-Agent Opera version does not match the selected transport profile")
		}
	}
	platform := userAgentPlatform(ua)
	if platform != "" && profile.Platform != "" && platform != profile.Platform &&
		!(profile.Platform == "ios" && platform == "macos") {
		warnings = append(warnings, "User-Agent platform does not match the selected transport profile")
	}
	hint := chromiumHintPattern.FindStringSubmatch(request.firstHeader("Sec-CH-UA"))
	if len(hint) == 2 {
		if profile.Family != "chromium" {
			warnings = append(warnings, "Chromium Client Hints do not match the selected transport family")
		} else if expectedMajor != "" && (strings.HasPrefix(profileName, "chrome_") || strings.HasPrefix(profileName, "brave_")) && hint[1] != expectedMajor {
			warnings = append(warnings, "Client Hints browser version does not match the selected transport profile")
		}
		if family == "chromium" && major != "" && hint[1] != major {
			warnings = append(warnings, "User-Agent and Client Hints browser versions disagree")
		}
	}
	if value, err := strconv.Unquote(request.firstHeader("Sec-CH-UA-Platform")); err == nil {
		hintPlatform := strings.ToLower(value)
		if hintPlatform == "chrome os" {
			hintPlatform = "chromeos"
		}
		if (platform != "" && hintPlatform != platform) || (profile.Platform != "" && hintPlatform != profile.Platform) {
			warnings = append(warnings, "Client Hints platform does not match the browser identity")
		}
	}
	if mobile := request.firstHeader("Sec-CH-UA-Mobile"); (mobile == "?0" && strings.Contains(ua, "Mobile")) ||
		(mobile == "?1" && platform != "" && platform != "android" && platform != "ios") {
		warnings = append(warnings, "Client Hints mobile identity does not match the User-Agent")
	}
	return truncateActivityError(strings.Join(warnings, "; "))
}

func userAgentIdentity(ua string) (string, string) {
	versions := make(map[string]string)
	for _, match := range browserVersionPattern.FindAllStringSubmatch(ua, 8) {
		versions[match[1]] = match[2]
	}
	if versions["Firefox"] != "" {
		return "firefox", versions["Firefox"]
	}
	if versions["Chrome"] != "" {
		return "chromium", versions["Chrome"]
	}
	if versions["Chromium"] != "" {
		return "chromium", versions["Chromium"]
	}
	if strings.Contains(ua, "Safari/") && versions["Version"] != "" {
		return "safari", versions["Version"]
	}
	if strings.Contains(strings.ToLower(ua), "okhttp/") {
		return "okhttp", ""
	}
	return "", ""
}

func userAgentPlatform(ua string) string {
	switch {
	case strings.Contains(ua, "Android"):
		return "android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		return "ios"
	case strings.Contains(ua, "CrOS"):
		return "chromeos"
	case strings.Contains(ua, "Windows"):
		return "windows"
	case strings.Contains(ua, "Macintosh"):
		return "macos"
	case strings.Contains(ua, "Linux"), strings.Contains(ua, "X11"):
		return "linux"
	default:
		return ""
	}
}
