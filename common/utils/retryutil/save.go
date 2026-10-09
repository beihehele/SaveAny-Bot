package retryutil

import (
	"context"
	"errors"

	"github.com/duke-git/lancet/v2/retry"

	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

// RetrySave keeps the existing retry policy but stops on a deliberate skip.
func RetrySave(ctx context.Context, attempt retry.RetryFunc, opts ...retry.Option) error {
	var skipped error
	opts = append(opts, retry.Context(ctx))
	err := retry.Retry(func() error {
		err := attempt()
		if errors.Is(err, storagetypes.ErrSaveSkipped) {
			skipped = err
			return nil // stop the library's retry loop without losing the skip
		}
		return err
	}, opts...)
	if skipped != nil {
		return skipped
	}
	return err
}
