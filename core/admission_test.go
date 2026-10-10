package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// admissionParent observes child registrations through the public AfterFunc
// contract. Hiding context's private value forces that cancellation path,
// avoiding reflection into context's internal children map.
type admissionParent struct {
	context.Context
	active atomic.Int64
}

func (p *admissionParent) Value(any) any { return nil }

func (p *admissionParent) AfterFunc(f func()) func() bool {
	p.active.Add(1)
	var once sync.Once
	release := func() { once.Do(func() { p.active.Add(-1) }) }
	stop := context.AfterFunc(p.Context, func() { release(); f() })
	return func() bool {
		stopped := stop()
		if stopped {
			release()
		}
		return stopped
	}
}

func TestRejectedAdmissionReleasesOnlyItsOwnContext(t *testing.T) {
	for _, state := range []string{"queued_duplicate", "running_duplicate", "closed", "unprepared"} {
		t.Run(state, func(t *testing.T) {
			resetCoreQueue(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			parent := &admissionParent{Context: ctx}
			exe := lifecycleTask{id: "admission", run: func(context.Context) error { return nil }}
			var wantActive int64
			if state != "unprepared" {
				Prepare()
				t.Cleanup(func() { currentQueue().CloseAndCancel() })
				if state == "closed" {
					currentQueue().Close()
				} else {
					if err := AddTask(parent, exe); err != nil {
						t.Fatal(err)
					}
					wantActive = 1
					if state == "running_duplicate" {
						if _, err := currentQueue().Get(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			for range 32 {
				if err := AddTask(parent, exe); err == nil {
					t.Fatal("accepted rejected submission")
				}
				if got := parent.active.Load(); got != wantActive {
					t.Fatalf("rejected submission retained a child or cancelled accepted work: active=%d want=%d", got, wantActive)
				}
				if parent.Err() != nil {
					t.Fatal("rejecting a submission cancelled its caller")
				}
			}
			if wantActive == 1 {
				qe := currentQueue()
				if state == "queued_duplicate" {
					accepted, err := qe.Get()
					if err != nil || accepted.Context().Err() != nil {
						t.Fatalf("rejection damaged the accepted task: %v", err)
					}
				} else if !qe.IsExecuting(exe.id) {
					t.Fatal("rejection removed running work")
				}
				qe.Done(exe.id)
				if got := parent.active.Load(); got != 0 {
					t.Fatalf("completed task retained a child: %d", got)
				}
			}
		})
	}
}

func TestConcurrentRejectedAdmissionKeepsAcceptedTask(t *testing.T) {
	resetCoreQueue(t)
	Prepare()
	qe := currentQueue()
	t.Cleanup(qe.CloseAndCancel)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	parent := &admissionParent{Context: ctx}
	exe := lifecycleTask{id: "shared", run: func(context.Context) error { return nil }}
	if err := AddTask(parent, exe); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			if err := AddTask(parent, exe); err == nil {
				t.Error("concurrent duplicate accepted")
			}
		})
	}
	wg.Wait()
	if got := parent.active.Load(); got != 1 {
		t.Fatalf("concurrent rejects retained children or cancelled accepted work: %d", got)
	}
	accepted, err := qe.Get()
	if err != nil || accepted.Context().Err() != nil {
		t.Fatalf("concurrent rejection damaged accepted work: %v", err)
	}
	qe.Done(exe.id)
	if parent.active.Load() != 0 || parent.Err() != nil {
		t.Fatal("completion leaked its child or cancelled its caller")
	}
}

func TestAdmissionRacingShutdownReleasesAllContexts(t *testing.T) {
	resetCoreQueue(t)
	Prepare()
	qe := currentQueue()
	t.Cleanup(qe.CloseAndCancel)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	parent := &admissionParent{Context: ctx}
	exe := lifecycleTask{id: "shutdown-race", run: func(context.Context) error { return nil }}
	if err := AddTask(parent, exe); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 64 {
		wg.Go(func() {
			<-start
			if err := AddTask(parent, exe); err == nil {
				t.Error("duplicate or shutdown submission accepted")
			}
		})
	}
	wg.Go(func() { <-start; qe.CloseAndCancel() })
	close(start)
	wg.Wait()
	if parent.active.Load() != 0 || parent.Err() != nil || qe.ActiveLength() != 0 {
		t.Fatalf("shutdown race left contexts or cancelled its caller: active=%d err=%v", parent.active.Load(), parent.Err())
	}
}
