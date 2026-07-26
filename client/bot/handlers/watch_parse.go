package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/krau/SaveAny-Bot/pkg/msgfilter"
)

var (
	errWatchFilterFormatInvalid   = errors.New("watch filter format invalid")
	errWatchFilterTypeUnsupported = errors.New("watch filter type unsupported")
	errWatchTargetTopicInvalid    = errors.New("watch target topic invalid")
)

type parsedWatchArgs struct {
	SourceArg     string
	TargetArg     string
	TargetOmitted bool // true → target 语义为 0
	FilterArg     string
}

type parsedTarget struct {
	ChatIDArg string
	TopicID   int // 0 when unspecified
}

func isWatchFilterArg(s string) bool {
	return strings.HasPrefix(s, msgfilter.Prefix)
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
	if isWatchFilterArg(second) {
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

func parseTargetWithTopic(targetArg string) (parsedTarget, error) {
	if !strings.Contains(targetArg, ":") {
		return parsedTarget{ChatIDArg: targetArg}, nil
	}
	parts := strings.SplitN(targetArg, ":", 2)
	if parts[0] == "" || parts[1] == "" {
		return parsedTarget{}, errWatchTargetTopicInvalid
	}
	topicID, err := strconv.Atoi(parts[1])
	if err != nil || topicID <= 0 {
		return parsedTarget{}, errWatchTargetTopicInvalid
	}
	return parsedTarget{ChatIDArg: parts[0], TopicID: topicID}, nil
}

func formatWatchTargetDisplay(targetName, topicName string) string {
	if topicName == "" {
		return targetName
	}
	return targetName + "#" + topicName
}

func formatWatchListLine(id uint, sourceName, targetName, filter string) string {
	line := fmt.Sprintf("[%d] %s -> %s", id, sourceName, targetName)
	if filter != "" {
		line += " " + filter
	}
	return line
}

// validateAndNormalizeFilter validates filter; empty is ok; only msgre: boolean expressions.
func validateAndNormalizeFilter(filterArg string) (string, error) {
	filterArg = strings.TrimSpace(filterArg)
	if filterArg == "" {
		return "", nil
	}
	if !strings.HasPrefix(filterArg, msgfilter.Prefix) {
		return "", errWatchFilterTypeUnsupported
	}
	if _, err := msgfilter.ParseFilter(filterArg); err != nil {
		if strings.TrimSpace(filterArg[len(msgfilter.Prefix):]) == "" {
			return "", errWatchFilterFormatInvalid
		}
		return "", err
	}
	return filterArg, nil
}

// watchFilterMatches reports whether msgText passes the stored watch filter.
func watchFilterMatches(filter, msgText string) bool {
	return msgfilter.Match(filter, msgText)
}
