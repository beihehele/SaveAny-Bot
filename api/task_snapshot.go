package api

import (
	"context"
	"errors"
	"sort"

	"github.com/charmbracelet/log"
)

// TaskSnapshots returns consistent progress projections without exposing locks
// or allowing the management server to mutate tracked task records.
func TaskSnapshots(ctx context.Context) []TaskInfoResponse {
	tasks := GetAllTasks()
	result := make([]TaskInfoResponse, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, convertTaskProgressToResponse(task))
	}
	uploads, err := uploadResponses(ctx)
	if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		log.FromContext(ctx).Error("Read durable upload task snapshots", "error", err)
	}
	result = mergeUploadResponses(result, uploads)
	sort.Slice(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].TaskID > result[j].TaskID
	})
	return result
}
