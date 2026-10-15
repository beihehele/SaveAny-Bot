package storagetypes

import (
	"context"
	"errors"
)

// ErrSaveQueued means the complete file was durably handed to ShardReel uploads.
var ErrSaveQueued = errors.New("file queued for ShardReel upload")

type saveKey struct{}

// SaveRequest links a file's durable receipt to its visible download task.
type SaveRequest struct {
	TaskID, FileID, Title, Source string
	ChatID                        int64
	MessageID                     int
	ExpectedFiles                 int
}

func WithSaveRequest(ctx context.Context, value SaveRequest) context.Context {
	return context.WithValue(ctx, saveKey{}, value)
}
func SaveRequestFrom(ctx context.Context) SaveRequest {
	value, _ := ctx.Value(saveKey{}).(SaveRequest)
	return value
}
