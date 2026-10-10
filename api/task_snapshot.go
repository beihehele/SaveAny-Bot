package api

import "sort"

// TaskSnapshots returns consistent progress projections without exposing locks
// or allowing the management server to mutate tracked task records.
func TaskSnapshots() []TaskInfoResponse {
	tasks := GetAllTasks()
	result := make([]TaskInfoResponse, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, convertTaskProgressToResponse(task))
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].TaskID > result[j].TaskID
	})
	return result
}
