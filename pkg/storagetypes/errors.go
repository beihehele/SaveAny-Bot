package storagetypes

import "errors"

// ErrSaveSkipped means storage policy deliberately left the input unsaved.
// It must never be counted as a successful save or retried as a transient error.
var ErrSaveSkipped = errors.New("save skipped by storage policy")
