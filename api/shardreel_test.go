package api

import "testing"

func TestUploadMergeUsesDurableStateAndKeepsOrdinaryTasks(t *testing.T) {
	tasks := []TaskInfoResponse{{TaskID: "upload", Status: TaskStatusRunning}, {TaskID: "ordinary", Status: TaskStatusCompleted}}
	uploads := []TaskInfoResponse{{TaskID: "upload", Status: TaskStatusCancelled, Path: "/caption/file.bin"}, {TaskID: "new", Status: TaskStatusQueued}}
	result := mergeUploadResponses(tasks, uploads)
	if len(result) != 3 || result[0].Status != TaskStatusCancelled || result[0].Path != "/caption/file.bin" ||
		result[1].TaskID != "ordinary" || result[1].Status != TaskStatusCompleted || result[2].TaskID != "new" {
		t.Fatalf("incorrect merged observations: %+v", result)
	}
}
