package storagetypes

import "context"

type skipExistingKey struct{}

// WithSkipExisting preserves the user's skip policy until remote admission.
func WithSkipExisting(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipExistingKey{}, true)
}

// SkipExisting reports whether an existing completed file should be kept.
func SkipExisting(ctx context.Context) bool {
	value, _ := ctx.Value(skipExistingKey{}).(bool)
	return value
}
