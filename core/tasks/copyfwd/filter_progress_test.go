package copyfwd

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestGroupedAlternativesScanWithoutLosingAlbumHits(t *testing.T) {
	calls := 0
	album := func(id int) *tg.Message { m := &tg.Message{ID: id, Message: "b c"}; m.SetGroupedID(100); return m }
	client := progressContextClient(t, telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesGetHistoryRequest); !ok {
			return fmt.Errorf("grouped alternatives must not search a single alternative: %T", in)
		}
		calls++
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{
			album(9),
			&tg.Message{ID: 8, Message: "a d"},
			album(7),
			&tg.Message{ID: 6, Message: "a only"},
		}}
		return nil
	}))
	ids, err := collectMatchedIDs(t.Context(), client, 42, "msgre:(a|b)&(c|d)", 20, nil)
	if err != nil || calls != 1 || !reflect.DeepEqual(ids, []int{8, 7}) {
		t.Fatalf("lost non-first alternative or album dedup: ids=%v calls=%d err=%v", ids, calls, err)
	}
}

func TestCopyWebProgressWithoutTelegramTrackerAndLateDeletion(t *testing.T) {
	store := taskhistory.NewStore(10, time.Hour)
	ctx, _, _ := store.Track(t.Context(), "copy-progress", "copy", "test")
	task := NewTask("copy-progress", 42, 42, 43, 0, "msgre:舞团&(king8|k8)", 20, nil)
	client := progressContextClient(t, telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		if _, ok := in.(*tg.MessagesSearchRequest); !ok {
			return fmt.Errorf("unexpected RPC %T", in)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{}
		return nil
	}))
	if err := task.executeWithClient(ctx, func() *ext.Context { return client }); err != nil {
		t.Fatal(err)
	}
	r := store.Snapshots()[0]
	if r.CopyProgress == nil || r.CopyProgress.Stage != "scanning" || r.CopyProgress.Matched != 0 || r.CopyProgress.Requested != 20 {
		t.Fatalf("web submission without Telegram tracker lost progress: %+v", r)
	}
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess, ResultSummary: &taskresult.Counts{}})
	store.Delete(task.ID)
	task.emitProgress(ctx, "forwarding", 1, 1)
	if len(store.Snapshots()) != 0 {
		t.Fatal("late copy progress resurrected deleted record")
	}
}

func TestCopyTitleNamesFilterAndOptionalTopic(t *testing.T) {
	task := NewTask("title", 42, -100111, -100222, 2, "msgre:舞团&(king8|k8)", 20, nil)
	task.SourceName, task.TargetName, task.TopicName = "舞团频道", "收藏群", "舞蹈"
	want := "舞团频道(-100111) msgre:舞团&(king8|k8) → 收藏群(-100222) Topic:舞蹈(2)"
	if task.Title() != want {
		t.Fatalf("title=%q", task.Title())
	}
	task.TargetTopicID = 0
	if strings.Contains(task.Title(), "Topic") || strings.HasSuffix(task.Title(), " ") {
		t.Fatal("missing topic left a label or trailing space")
	}
	task.Filter = ""
	if !strings.Contains(task.Title(), i18n.T(i18nk.BotMsgCopyAllMedia)) {
		t.Fatal("missing filter has no all-media label")
	}
}

func TestCopyTelegramProgressKeepsTitleCountsAndActualSelection(t *testing.T) {
	var texts []string
	client := progressContextClient(t, telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, _ bin.Decoder) error {
		req, ok := in.(*tg.MessagesEditMessageRequest)
		if !ok {
			return fmt.Errorf("unexpected progress RPC %T", in)
		}
		texts = append(texts, req.Message)
		return tgerr.New(400, "MESSAGE_NOT_MODIFIED")
	}))
	ctx := tgutil.ExtWithContext(t.Context(), client)
	p := NewProgressTracker(7, 42, "copy", "来源(1) msgre:a → 收藏(2) Topic:舞蹈(3)", 20)
	p.OnScanDone(ctx, 3, 20, 100)
	p.OnForward(ctx, 1, 3, taskresult.Counts{Total: 3, Succeeded: 1, Pending: 2})
	p.OnDone(ctx, taskresult.Counts{Total: 3, Succeeded: 2, Failed: 1}, nil)
	if len(texts) != 3 {
		t.Fatalf("expected scan, forward and final updates: %v", texts)
	}
	for _, text := range texts {
		if !strings.HasPrefix(text, "来源(1) msgre:a → 收藏(2) Topic:舞蹈(3)\n\n") {
			t.Fatal("task identity disappeared")
		}
	}
	if !strings.Contains(texts[1], "33%") || !strings.Contains(texts[2], "20") || !strings.Contains(texts[2], "3") || strings.Contains(texts[2], "<no value>") {
		t.Fatalf("bad progress/count rendering: %v", texts)
	}
}
