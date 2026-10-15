package retryutil

import (
	"context"
	"errors"

	"github.com/duke-git/lancet/v2/retry"

	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

// RetrySave stops retrying when a file is skipped or durably staged.
func RetrySave(ctx context.Context, attempt retry.RetryFunc, opts ...retry.Option) error {
	var handled error
	opts = append(opts, retry.Context(ctx))
	err := retry.Retry(func() error {
		err := attempt()
		if errors.Is(err, storagetypes.ErrSaveSkipped) || errors.Is(err, storagetypes.ErrSaveQueued) {
			handled = err
			return nil // stop retries while preserving the handoff result
		}
		return err
	}, opts...)
	if handled != nil {
		return handled
	}
	return err
}
