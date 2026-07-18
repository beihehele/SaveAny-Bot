package copyfwd

import "testing"

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
