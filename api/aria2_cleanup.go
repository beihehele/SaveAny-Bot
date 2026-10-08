package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/aria2"
)

func cleanupUnqueuedAria2Download(ctx context.Context, client *aria2.Client, gid string) error {
	// Request or service cancellation must not cancel compensation. All RPCs,
	// including cleanup of generated downloads, share one bounded deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	type cleanupStep struct {
		gid              string
		removeResultOnly bool
		errorCount       int
	}
	pending := []cleanupStep{{gid: gid}}
	seen := make(map[string]bool)
	var errs []error
	for len(pending) != 0 {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, fmt.Errorf("aria2 cleanup interrupted: %w", err))...)
		}
		step := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		current := step.gid
		if step.removeResultOnly {
			// Keep metadata and its child GIDs available for recovery on failure.
			if len(errs) != step.errorCount {
				continue
			}
		} else {
			if seen[current] {
				continue
			}
			seen[current] = true
			if _, err := client.ForceRemove(ctx, current); err != nil {
				// A completed metadata download cannot be force-removed, but it may
				// already have generated torrent/Metalink children. Inspect it before
				// removing the result so those GIDs are not lost.
				status, statusErr := client.TellStatus(ctx, current, "followedBy")
				if statusErr != nil {
					errs = append(errs, fmt.Errorf("failed to stop aria2 download %s: %w", current, errors.Join(err, statusErr)))
					continue
				}
				if len(status.FollowedBy) != 0 {
					pending = append(pending, cleanupStep{gid: current, removeResultOnly: true, errorCount: len(errs)})
					for _, child := range status.FollowedBy {
						pending = append(pending, cleanupStep{gid: child})
					}
					continue
				}
			}
		}
		if _, err := client.RemoveDownloadResult(ctx, current); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove aria2 result %s: %w", current, err))
		}
	}
	return errors.Join(errs...)
}
