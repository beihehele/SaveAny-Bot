package batchtfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type collectionStorage struct {
	storage.Storage
	name   string
	groups [][]int
	onSave func(context.Context) error
}

func (s *collectionStorage) Name() string { return s.name }
func (s *collectionStorage) SaveMessages(ctx context.Context, files []tfile.TGFileMessage) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := make([]int, len(files))
	for i, f := range files {
		ids[i] = f.Message().ID
	}
	s.groups = append(s.groups, ids)
	if s.onSave != nil {
		if err := s.onSave(ctx); err != nil {
			return nil, err
		}
	}
	return []string{"https://t.me/c/222/501"}, nil
}

func collectionElement(id int, group int64, store storage.Storage) TaskElement {
	msg := &tg.Message{ID: id, GroupedID: group, PeerID: &tg.PeerChannel{ChannelID: 111}}
	file := tfile.NewTGFile(nil, nil, 4, "test.bin", tfile.WithMessage(msg))
	return TaskElement{ID: string(rune('a' + id)), Storage: store, File: file}
}

func TestCollectionBatchGroupingAndCancellation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("workers=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "group", true: "cancel"}[cancelled], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store := &collectionStorage{name: "collection"}
			if cancelled {
				store.onSave = func(ctx context.Context) error { cancel(); return ctx.Err() }
			}
			task := NewBatchTGFileTask("collection", []TaskElement{collectionElement(1, 55, store), collectionElement(2, 55, store), collectionElement(3, 0, store)}, nil)
			err := task.Execute(ctx)
			if cancelled {
				if !errors.Is(err, context.Canceled) || len(store.groups) != 1 || task.ResultSummary().Succeeded != 0 {
					t.Fatalf("cancelled collection continued: %v %+v", err, store.groups)
				}
			} else {
				if err != nil || !reflect.DeepEqual(store.groups, [][]int{{1, 2}, {3}}) || task.ResultSummary().Succeeded != 3 || len(task.ResultLinks()) != 2 {
					t.Fatalf("album split or result lost: %v %+v %+v", err, store.groups, task.ResultSummary())
				}
			}
			if len(task.Processing()) != 0 {
				t.Fatal("processing state leaked")
			}
		})
	}
}

func TestCollectionRejectsMixedRuleTargetsBeforeWrites(t *testing.T) {
	one, two := &collectionStorage{name: "one"}, &collectionStorage{name: "two"}
	task := NewBatchTGFileTask("mixed", []TaskElement{collectionElement(1, 55, one), collectionElement(2, 55, two)}, nil)
	if err := task.Execute(t.Context()); err == nil || len(one.groups)+len(two.groups) != 0 {
		t.Fatalf("mixed album was partially sent: %v", err)
	}
}

func TestCollectionCancellationKeepsConfirmedAlbum(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("workers=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := &collectionStorage{name: "collection", onSave: func(context.Context) error { cancel(); return nil }}
	task := NewBatchTGFileTask("confirmed-cancel", []TaskElement{collectionElement(1, 55, store), collectionElement(2, 55, store), collectionElement(3, 0, store)}, nil)
	err := task.Execute(ctx)
	summary := task.ResultSummary()
	if !errors.Is(err, context.Canceled) || len(store.groups) != 1 || summary.Succeeded != 2 || summary.Cancelled != 1 || len(task.ResultLinks()) != 1 {
		t.Fatalf("confirmed album lost or cancellation sent another message: err=%v summary=%+v groups=%v links=%v", err, summary, store.groups, task.ResultLinks())
	}
}
