package copyfwd

import "testing"

func TestStaleCopyCleanupKeepsNewOwner(t *testing.T) {
	const user int64 = 424244
	if !TryBegin(user, "old") {
		t.Fatal("cannot acquire old slot")
	}
	End(user, "old")
	if !TryBegin(user, "new") {
		t.Fatal("cannot acquire new slot")
	}
	defer End(user, "new")
	End(user, "old")
	EndByTaskID("old")
	if TryBegin(user, "overlap") {
		t.Fatal("stale cleanup released new copy")
	}
}

func TestTryBeginEndByTaskIDReleasesSlot(t *testing.T) {
	const user int64 = 424242
	const taskID = "copy-task-1"
	// ensure clean slate
	End(user, taskID)

	if !TryBegin(user, taskID) {
		t.Fatal("TryBegin should succeed")
	}
	if TryBegin(user, "other") {
		t.Fatal("second TryBegin should fail while active")
	}
	EndByTaskID(taskID)
	if !TryBegin(user, "after-cancel") {
		t.Fatal("TryBegin should succeed after EndByTaskID")
	}
	End(user, "after-cancel")
}

func TestEndIgnoresMismatchedTaskID(t *testing.T) {
	const user int64 = 424243
	End(user, "x")
	if !TryBegin(user, "a") {
		t.Fatal("TryBegin failed")
	}
	End(user, "b") // wrong id
	if TryBegin(user, "c") {
		t.Fatal("slot should still be held by a")
	}
	End(user, "a")
}
