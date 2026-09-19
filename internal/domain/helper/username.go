package helper

import (
	"regexp"
	"strings"
)

var nonAlphanumericUnderscoreRegex = regexp.MustCompile(`[^a-z0-9_]`)

func NormalizeUsername(username string) string {

	username = strings.ToLower(strings.TrimSpace(username))

	return nonAlphanumericUnderscoreRegex.ReplaceAllString(username, "")
}
