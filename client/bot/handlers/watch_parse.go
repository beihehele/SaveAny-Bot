package handlers

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	errWatchFilterFormatInvalid   = errors.New("watch filter format invalid")
	errWatchFilterTypeUnsupported = errors.New("watch filter type unsupported")
)

type parsedWatchArgs struct {
	SourceArg     string
	TargetArg     string
	TargetOmitted bool // true → target 语义为 0
	FilterArg     string
}

func parseWatchArgs(args []string) (parsedWatchArgs, error) {
	if len(args) == 0 {
		return parsedWatchArgs{}, fmt.Errorf("missing source")
	}
	out := parsedWatchArgs{SourceArg: args[0]}
	if len(args) == 1 {
		out.TargetOmitted = true
		return out, nil
	}
	second := args[1]
	if strings.Contains(second, ":") {
		out.TargetOmitted = true
		out.FilterArg = strings.Join(args[1:], " ")
		return out, nil
	}
	out.TargetArg = second
	if len(args) > 2 {
		out.FilterArg = strings.Join(args[2:], " ")
	}
	return out, nil
}

func formatWatchListLine(id uint, sourceName, targetName, filter string) string {
	line := fmt.Sprintf("[%d] %s -> %s", id, sourceName, targetName)
	if filter != "" {
		line += " " + filter
	}
	return line
}

// validateAndNormalizeFilter validates filter; empty is ok; only msgre: supported.
// Also regexp.Compile the expression body.
func validateAndNormalizeFilter(filterArg string) (string, error) {
	if filterArg == "" {
		return "", nil
	}
	parts := strings.SplitN(filterArg, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errWatchFilterFormatInvalid
	}
	if parts[0] != "msgre" {
		return "", errWatchFilterTypeUnsupported
	}
	if _, err := regexp.Compile(parts[1]); err != nil {
		return "", err
	}
	return parts[0] + ":" + parts[1], nil
}
