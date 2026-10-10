package core

import (
	"context"
	"testing"
)

func TestAddTasksRejectsEntireGroupAndReleasesWrappers(t *testing.T) {
	for _, state := range []string{"queued_duplicate", "running_duplicate", "group_duplicate", "closed", "nil"} {
		t.Run(state, func(t *testing.T) {
			resetCoreQueue(t)
			Prepare()
			qe := currentQueue()
			t.Cleanup(qe.CloseAndCancel)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			parent := &admissionParent{Context: ctx}
			first := lifecycleTask{id: "first", run: func(context.Context) error { return nil }}
			last := lifecycleTask{id: "last", run: first.run}
			var wantActive int64
			wantQueued := 0
			if state == "queued_duplicate" || state == "running_duplicate" {
				if err := AddTask(parent, last); err != nil {
					t.Fatal(err)
				}
				wantActive, wantQueued = 1, 1
				if state == "running_duplicate" {
					if _, err := qe.Get(); err != nil {
						t.Fatal(err)
					}
					wantQueued = 0
				}
			}
			group := []Executable{first, last}
			switch state {
			case "group_duplicate":
				last.id = first.id
				group[1] = last
			case "closed":
				qe.Close()
			case "nil":
				group[1] = nil
			}
			for range 16 {
				if err := AddTasks(parent, group...); err == nil {
					t.Fatal("invalid group accepted")
				}
				if qe.ActiveLength() != wantQueued || parent.active.Load() != wantActive || parent.Err() != nil {
					t.Fatalf("rejection published a prefix or damaged ownership: queued=%d active=%d", qe.ActiveLength(), parent.active.Load())
				}
			}
		})
	}
}

func TestAddTasksPreservesContextsAndReleasesEachCompletion(t *testing.T) {
	resetCoreQueue(t)
	if err := AddTasks(t.Context()); err != nil {
		t.Fatal("empty group should not require an initialized queue")
	}
	if err := AddTasks(t.Context(), lifecycleTask{id: "unprepared"}); err == nil {
		t.Fatal("group accepted before Prepare")
	}
	Prepare()
	qe := currentQueue()
	t.Cleanup(qe.CloseAndCancel)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	parent := &admissionParent{Context: ctx}
	first, last := lifecycleTask{id: "first"}, lifecycleTask{id: "last"}
	if err := AddTasks(parent, first, last); err != nil {
		t.Fatal(err)
	}
	qe.Close()
	for index, want := range []string{first.id, last.id} {
		task, err := qe.Get()
		if err != nil || task.ID != want || task.Context().Err() != nil {
			t.Fatalf("admitted order or context changed: %v", err)
		}
		qe.Done(task.ID)
		if parent.active.Load() != int64(1-index) || parent.Err() != nil {
			t.Fatal("completion failed to release only its own wrapper")
		}
	}
}
