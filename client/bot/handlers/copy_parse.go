package handlers

import (
	"errors"
	"strconv"
	"strings"

	"github.com/krau/SaveAny-Bot/pkg/msgfilter"
)

const (
	defaultCopyCount = 500
	maxCopyCount     = 5000
)

var (
	errCopyArgsInvalid          = errors.New("copy args invalid")
	errCopyTargetRequired       = errors.New("copy target required")
	errCopyTargetLocalForbidden = errors.New("copy target 0 forbidden")
	errCopyCountTooLarge        = errors.New("copy count too large")
)

type copyArgs struct {
	SourceArg string
	TargetArg string
	FilterArg string
	Count     int
}

func parseCopyArgs(args []string) (copyArgs, error) {
	if len(args) < 2 {
		return copyArgs{}, errCopyTargetRequired
	}
	out := copyArgs{
		SourceArg: args[0],
		TargetArg: args[1],
		Count:     defaultCopyCount,
	}
	if out.TargetArg == "0" || strings.HasPrefix(out.TargetArg, "0:") {
		return copyArgs{}, errCopyTargetLocalForbidden
	}
	countSet := false
	filterSet := false
	for _, a := range args[2:] {
		switch {
		case strings.HasPrefix(a, msgfilter.Prefix):
			if filterSet {
				return copyArgs{}, errCopyArgsInvalid
			}
			filterSet = true
			out.FilterArg = a
		default:
			if countSet {
				return copyArgs{}, errCopyArgsInvalid
			}
			n, err := strconv.Atoi(a)
			if err != nil || n <= 0 {
				return copyArgs{}, errCopyArgsInvalid
			}
			if n > maxCopyCount {
				return copyArgs{}, errCopyCountTooLarge
			}
			countSet = true
			out.Count = n
		}
	}
	return out, nil
}
