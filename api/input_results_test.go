package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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

func TestAlbumFallbackMetadataPreservesSelection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		links         []string
		invoker       sourceInvoker
		wantIDs       []int
		wantStatus    []string
		fallback      []bool
		unsupportedAt int
		wantError     bool
	}{
		{"query error", []string{"https://t.me/c/111/42"}, sourceInvoker{fail: true}, []int{42}, []string{"ready"}, []bool{true}, 0, false},
		{"empty result", []string{"https://t.me/c/111/42"}, sourceInvoker{emptyAlbum: true}, []int{42}, []string{"ready"}, []bool{true}, 0, false},
		{"duplicate fallback", []string{"https://t.me/c/111/42?single", "https://t.me/c/111/42"}, sourceInvoker{fail: true}, []int{42}, []string{"ready", "duplicate"}, []bool{false, true}, 0, false},
		{"repeated fallback", []string{"https://t.me/c/111/42", "https://t.me/c/111/42"}, sourceInvoker{fail: true}, []int{42}, []string{"ready", "duplicate"}, []bool{true, true}, 0, false},
		{"explicit single", []string{"https://t.me/c/111/42?single"}, sourceInvoker{fail: true}, []int{42}, []string{"ready"}, []bool{false}, 0, false},
		{"normal album", []string{"https://t.me/c/111/42"}, sourceInvoker{}, []int{40, 42, 44}, []string{"ready"}, []bool{false}, 0, false},
		{"successful single member", []string{"https://t.me/c/111/42"}, sourceInvoker{onlySelected: true}, []int{42}, []string{"ready"}, []bool{false}, 0, false},
		{"unsupported fallback", []string{"https://t.me/c/111/42"}, sourceInvoker{fail: true}, nil, []string{"failed"}, []bool{true}, 42, true},
		{"mixed supported and unsupported", []string{"https://t.me/c/111/42?single", "https://t.me/c/111/44"}, sourceInvoker{fail: true}, []int{42}, []string{"ready", "failed"}, []bool{false, true}, 44, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := sourceRequest(t)
			defer cancel()
			client := sourceClient(t, &tc.invoker)
			files, report, err := extractFilesFromLinksReport(ctx, tc.links, ParseMessageLink, func(_ context.Context, _ int64, msgID int) (*MessageContext, error) {
				msg := sourceMessage(msgID)
				if msgID == tc.unsupportedAt {
					msg.Media = &tg.MessageMediaContact{}
				}
				return &MessageContext{Message: msg, Client: client}, nil
			})
			var selectionErr *inputresult.Error
			if (err != nil) != tc.wantError || (tc.wantError && !errors.As(err, &selectionErr)) || len(files) != len(tc.wantIDs) || len(report.InputResults) != len(tc.links) {
				t.Fatalf("selection changed: files=%d report=%+v err=%v", len(files), report, err)
			}
			ids := map[int]bool{}
			for _, file := range files {
				ids[file.Message().ID] = true
				if file.Dler() != client.Raw {
					t.Fatal("download client changed")
				}
			}
			for _, id := range tc.wantIDs {
				if !ids[id] {
					t.Fatalf("selected member lost: %d", id)
				}
			}
			payload, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Results []map[string]any `json:"input_results"`
			}
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			for index, result := range decoded.Results {
				flag, present := result["album_fallback"]
				if result["status"] != tc.wantStatus[index] || present != tc.fallback[index] || (present && flag != true) {
					t.Fatalf("selection status or known fallback missing: %s", payload)
				}
				wantNew := 0
				if tc.wantStatus[index] == "ready" {
					wantNew = len(tc.wantIDs)
				}
				if report.InputResults[index].NewFiles != wantNew || (tc.wantStatus[index] == "failed" && report.InputResults[index].Code != "unsupported_media") {
					t.Fatalf("selection counts or failure reason changed: %s", payload)
				}
			}
			if strings.Contains(string(payload), "source inaccessible") || strings.Contains(string(payload), "https://") || client.Err() != nil {
				t.Fatal("metadata leaked upstream details or cancelled shared client")
			}
			if tc.name == "explicit single" && tc.invoker.queries != 0 {
				t.Fatal("single selection queried the album")
			}
		})
	}
}

func TestCancelledAlbumLookupDiscardsFilesAndDoesNotReportFallback(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	invoker := &sourceInvoker{cancel: cancel, cancelAlbum: true}
	client := sourceClient(t, invoker)
	files, report, err := extractFilesFromLinksReport(ctx, []string{"https://t.me/c/111/42?single", "https://t.me/c/111/44"}, ParseMessageLink, func(_ context.Context, _ int64, msgID int) (*MessageContext, error) {
		return &MessageContext{Message: sourceMessage(msgID), Client: client}, nil
	})
	if !errors.Is(err, context.Canceled) || files != nil || invoker.groups == 0 || len(report.InputResults) != 1 || report.InputResults[0].AlbumFallback || client.Err() != nil {
		t.Fatalf("cancelled album lookup returned fallback or partial files: files=%v report=%+v err=%v", files, report, err)
	}
}
