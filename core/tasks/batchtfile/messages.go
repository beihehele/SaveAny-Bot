package batchtfile

import (
	"context"
	"fmt"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

// workGroups keeps existing file units, grouping only message-based storage.
func (t *Task) workGroups() ([][]int, error) {
	var units [][]int
	albums := make(map[[2]int64]int)
	for i, elem := range t.elems {
		_, messageStore := elem.Storage.(storage.MessageStorage)
		if !messageStore {
			units = append(units, []int{i})
			continue
		}
		file, ok := elem.File.(tfile.TGFileMessage)
		if !ok || file.Message() == nil {
			return nil, fmt.Errorf("collection requires original messages")
		}
		if file.Message().GroupedID == 0 {
			units = append(units, []int{i})
			continue
		}
		key := tfile.AlbumKey(file.Message())
		if group, ok := albums[key]; ok {
			units[group] = append(units[group], i)
		} else {
			albums[key] = len(units)
			units = append(units, []int{i})
		}
	}
	// A rule may route siblings differently. Reject that case before any writes,
	// rather than silently publishing an incomplete TG album.
	for _, elem := range t.elems {
		file, ok := elem.File.(tfile.TGFileMessage)
		if !ok || file.Message() == nil || file.Message().GroupedID == 0 {
			continue
		}
		if group, ok := albums[tfile.AlbumKey(file.Message())]; ok {
			if elem.Storage.Name() != t.elems[units[group][0]].Storage.Name() {
				return nil, fmt.Errorf("rules must send all members of a TG collection album to the same storage")
			}
		}
	}
	return units, nil
}

func (t *Task) processGroup(ctx context.Context, indices []int) error {
	elem := t.elems[indices[0]]
	store, ok := elem.Storage.(storage.MessageStorage)
	if !ok {
		return t.processElement(ctx, elem)
	}
	files := make([]tfile.TGFileMessage, len(indices))
	for i, index := range indices {
		files[i] = t.elems[index].File.(tfile.TGFileMessage)
	}
	links, err := store.SaveMessages(ctx, files)
	t.processingMu.Lock()
	t.links = append(t.links, links...)
	t.processingMu.Unlock()
	return err
}

func (t *Task) emitFileCounts(ctx context.Context) {
	summary := t.ResultSummary()
	taskevent.Emit(ctx, taskevent.Event{TaskID: t.ID, Phase: taskevent.PhaseProgress, TotalFiles: len(t.elems), DownloadedFiles: summary.Succeeded, DownloadedBytes: t.downloaded.Load()})
}
