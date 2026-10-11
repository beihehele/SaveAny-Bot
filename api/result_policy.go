package api

import (
	"fmt"

	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func validateResultPolicy(policy string) error {
	if policy != "" {
		return fmt.Errorf("result_policy is not supported for Telegram saves")
	}
	return nil
}

func cloneResultSummary(summary *taskresult.Counts) *taskresult.Counts {
	if summary == nil {
		return nil
	}
	copy := *summary
	return &copy
}
