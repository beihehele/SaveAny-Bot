package api

import "github.com/krau/SaveAny-Bot/pkg/taskresult"

func cloneResultSummary(summary *taskresult.Counts) *taskresult.Counts {
	if summary == nil {
		return nil
	}
	copy := *summary
	return &copy
}
