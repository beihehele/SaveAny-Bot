package api

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
)

func TestLinkResultsPreserveAlbumSelectionAndExposeSkippedInputs(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	client := sourceClient(t, &sourceInvoker{})
	links := []string{"https://t.me/c/111/42?single", "not-a-link", "https://t.me/c/111/40", "https://t.me/c/111/42", "https://t.me/c/111/52?single", "https://t.me/c/111/53?single", "https://t.me/c/111/54?single", "https://t.me/channel/42?comment=1"}
	files, report, err := extractFilesFromLinksReport(ctx, links, ParseMessageLink, func(_ context.Context, _ int64, id int) (*MessageContext, error) {
		if id == 52 {
			return nil, errors.New("private upstream error that must not enter metadata")
		}
		msg := sourceMessage(id)
		if id == 53 {
			msg.Media = nil
		}
		if id == 54 {
			msg.Media = &tg.MessageMediaContact{}
		}
		return &MessageContext{Message: msg, Client: client}, nil
	})
	if err != nil || len(files) != 3 || files[0].Message().ID != 42 {
		t.Fatalf("selection changed: %v %v", files, err)
	}
	for _, file := range files {
		if file.Dler() != client.Raw {
			t.Fatal("download client changed")
		}
	}
	want := []inputresult.Result{
		{Index: 1, Status: "ready", Code: "ready", NewFiles: 1},
		{Index: 2, Status: "failed", Code: "invalid_link"},
		{Index: 3, Status: "ready", Code: "ready", NewFiles: 2},
		{Index: 4, Status: "duplicate", Code: "duplicate"},
		{Index: 5, Status: "failed", Code: "message_unavailable"},
		{Index: 6, Status: "failed", Code: "no_media"},
		{Index: 7, Status: "failed", Code: "unsupported_media"},
		{Index: 8, Status: "failed", Code: "resolution_failed"},
	}
	if !reflect.DeepEqual(report.InputResults, want) {
		t.Fatalf("results=%+v want=%+v", report.InputResults, want)
	}
}

func TestLinkResultLimitDoesNotLimitSelectedFiles(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	client := sourceClient(t, &sourceInvoker{})
	links := make([]string, inputresult.Limit+7)
	for index := range links {
		links[index] = "https://t.me/c/111/42?single"
	}
	calls := 0
	files, report, err := extractFilesFromLinksReport(ctx, links, ParseMessageLink, func(_ context.Context, _ int64, _ int) (*MessageContext, error) {
		calls++
		return &MessageContext{Message: sourceMessage(calls), Client: client}, nil
	})
	if err != nil || len(files) != len(links) || calls != len(links) || len(report.InputResults) != inputresult.Limit || report.InputResultsOmitted != 7 {
		t.Fatalf("metadata limit truncated actual selection: files=%d calls=%d report=%+v err=%v", len(files), calls, report, err)
	}
}

func TestAllFailedLinksReturnSafeIndexedResults(t *testing.T) {
	files, report, err := extractFilesFromLinksReport(t.Context(), []string{"", "not-a-link"}, func(context.Context, string) (int64, int, error) { t.Fatal("invalid link parsed"); return 0, 0, nil }, nil)
	var selection *inputresult.Error
	if files != nil || !errors.As(err, &selection) || len(report.InputResults) != 2 || selection.InputResults[1].Index != 2 {
		t.Fatalf("all-failed results lost: %v %+v", err, report)
	}
}

func TestLinkResultsPreserveOriginalMessageFallback(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	client := sourceClient(t, &sourceInvoker{fail: true})
	files, report, err := extractFilesFromLinksReport(ctx, []string{"https://t.me/c/111/42"}, ParseMessageLink, func(context.Context, int64, int) (*MessageContext, error) {
		return &MessageContext{Message: sourceMessage(42), Client: client}, nil
	})
	if err != nil || len(files) != 1 || files[0].Message().ID != 42 || report.InputResults[0].NewFiles != 1 {
		t.Fatalf("existing album fallback changed: %v %+v", err, report)
	}
}
