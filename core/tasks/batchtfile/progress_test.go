package batchtfile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/common/i18n"
)

func TestProgressLogsTaskCancellationWithoutHidingFailures(t *testing.T) {
	i18n.Init("en")
	for _, name := range []string{"cancelled", "internal_cancel", "deadline", "upload_failure"} {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			err := fmt.Errorf("upload: %w", context.Canceled)
			switch name {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			case "upload_failure":
				err = errors.New("engine was closed")
			}
			var output bytes.Buffer
			logger := log.New(&output)
			logger.SetFormatter(log.JSONFormatter)
			(&Progress{}).OnDone(log.WithContext(ctx, logger), &Task{ID: "collection"}, err)
			if strings.Contains(output.String(), `"level":"error"`) != (name != "cancelled") {
				t.Fatalf("incorrect terminal log: %s", output.String())
			}
		})
	}
}
