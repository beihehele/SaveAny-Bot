// Package inputresult describes source selection without storing URLs or RPC errors.
// Selection results do not imply a completed download or a complete media group.
package inputresult

import "fmt"

const Limit = 100

// Result refers to the one-based position of a submitted link.
type Result struct {
	Index    int    `json:"index"`
	Status   string `json:"status"`
	Code     string `json:"code"`
	NewFiles int    `json:"new_files"`
}

// Report bounds metadata independently of how many links are processed.
type Report struct {
	InputResults        []Result `json:"input_results,omitempty"`
	InputResultsOmitted int      `json:"input_results_omitted,omitempty"`
}

// Add records a selection result, retaining at most Limit entries.
func (r *Report) Add(index int, code string, newFiles int) {
	status := "failed"
	if newFiles > 0 {
		status = "ready"
		if code != "ready" {
			status = "partial"
		}
	} else if code == "ready" {
		status, code = "duplicate", "duplicate"
	}
	if len(r.InputResults) >= Limit {
		r.InputResultsOmitted++
		return
	}
	r.InputResults = append(r.InputResults, Result{Index: index, Status: status, Code: code, NewFiles: newFiles})
}

// Clone prevents response callers from mutating retained metadata.
func (r Report) Clone() Report {
	r.InputResults = append([]Result(nil), r.InputResults...)
	return r
}

// Error carries safe preparation results when no task can be admitted.
type Error struct {
	Report
	Cause error
}

func (e *Error) Error() string { return fmt.Sprintf("source selection failed: %v", e.Cause) }
func (e *Error) Unwrap() error { return e.Cause }
