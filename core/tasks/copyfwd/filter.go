package copyfwd

import (
	"regexp"
	"strings"
)

func filterMatches(filter, msgText string) bool {
	if filter == "" {
		return true
	}
	parts := strings.SplitN(filter, ":", 2)
	if len(parts) != 2 {
		return false
	}
	switch parts[0] {
	case "msgre":
		ok, err := regexp.MatchString(parts[1], msgText)
		return err == nil && ok
	default:
		return false
	}
}
