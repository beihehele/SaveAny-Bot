package tfile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

type completionInfo struct{}

func (completionInfo) TaskID() string      { return "completion" }
func (completionInfo) FileName() string    { return "album-photo.jpg" }
func (completionInfo) FileSize() int64     { return 100 }
func (completionInfo) StoragePath() string { return "photos" }
func (completionInfo) StorageName() string { return "local" }

func TestCompletionMessageDistinguishesInternalAndTaskCancellation(t *testing.T) {
	i18n.Init("en")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	deadline, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	internal := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		err    error
		prefix i18nk.Key
	}{
		{"active internal cancellation", t.Context(), internal, i18nk.BotMsgProgressDownloadFailedPrefix},
		{"ordinary error after task cancellation", cancelled, errors.New("connection EOF"), i18nk.BotMsgProgressTaskCanceled},
		{"wrapped error after task cancellation", cancelled, internal, i18nk.BotMsgProgressTaskCanceled},
		{"nil after cancellation", cancelled, nil, i18nk.BotMsgProgressDownloadDonePrefix},
		{"deadline with internal cancellation", deadline, internal, i18nk.BotMsgProgressDownloadFailedPrefix},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, entities, err := completionMessage(tc.ctx, completionInfo{}, tc.err)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(text, i18n.T(tc.prefix)) || !strings.Contains(text, "album-photo.jpg") || len(entities) == 0 {
				t.Fatalf("wrong terminal message: %q entities=%v", text, entities)
			}
			if tc.prefix == i18nk.BotMsgProgressDownloadFailedPrefix && !strings.Contains(text, tc.err.Error()) {
				t.Fatal("failure message lost the original error")
			}
			if tc.err == nil && !strings.Contains(text, "[local]:photos") {
				t.Fatal("success message lost the save path")
			}
		})
	}
}

func TestCompletionMessageReportsPolicySkipWithoutSavePath(t *testing.T) {
	i18n.Init("en")
	text, _, err := completionMessage(t.Context(), completionInfo{}, fmt.Errorf("too large: %w", storagetypes.ErrSaveSkipped))
	if err != nil || !strings.HasPrefix(text, "Not saved: album-photo.jpg") || !strings.Contains(text, "Skip reason:") || strings.Contains(text, "[local]:photos") {
		t.Fatalf("skipped file was displayed as saved: %q %v", text, err)
	}
}
