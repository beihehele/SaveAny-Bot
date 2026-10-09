package retryutil

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/duke-git/lancet/v2/retry"

	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

func TestRetrySavePreservesSkipAndTransientPolicy(t *testing.T) {
	for _, skip := range []bool{false, true} {
		calls := 0
		failure := errors.New("transient")
		if skip {
			failure = fmt.Errorf("too large: %w", storagetypes.ErrSaveSkipped)
		}
		err := RetrySave(t.Context(), func() error { calls++; return failure }, retry.RetryTimes(3), retry.RetryWithLinearBackoff(time.Millisecond))
		wantCalls := 3
		if skip {
			wantCalls = 1
		}
		if !errors.Is(err, failure) || skip && err != failure || calls != wantCalls {
			t.Fatalf("skip=%v calls=%d err=%v", skip, calls, err)
		}
	}
}
