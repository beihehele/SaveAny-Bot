package api

import (
	"fmt"

	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func validateResultPolicy(typ tasktype.TaskType, policy taskresult.Policy) error {
	if policy == "" {
		return nil
	}
	if policy != taskresult.Legacy && policy != taskresult.Strict {
		return fmt.Errorf("unsupported result_policy %q", policy)
	}
	if typ != tasktype.TaskTypeTransfer {
		return fmt.Errorf("result_policy is currently supported only for transfer tasks")
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
