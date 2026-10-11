package taskresult

// Provider exposes submitted file results after Execute has returned.
type Provider interface {
	ResultSummary() Summary
}

// Counts is a bounded wire representation; it excludes names and error texts.
// Failed includes storage-skipped (unsaved) files to preserve the wire schema.
type Counts struct {
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Cancelled   int `json:"cancelled"`
	Interrupted int `json:"interrupted"`
}

// Counts returns only the counts in this detached summary.
func (s Summary) Counts() Counts {
	return Counts{Total: s.Total, Pending: s.Pending, Running: s.Running, Succeeded: s.Succeeded,
		Failed: s.Failed + s.Skipped, Cancelled: s.Cancelled, Interrupted: s.Interrupted}
}

// Valid checks that nonnegative counts exactly cover the submitted inputs.
func (c Counts) Valid() bool {
	remaining := c.Total
	if remaining < 0 {
		return false
	}
	for _, count := range []int{c.Pending, c.Running, c.Succeeded, c.Failed, c.Cancelled, c.Interrupted} {
		if count < 0 || count > remaining {
			return false
		}
		remaining -= count
	}
	return remaining == 0
}
