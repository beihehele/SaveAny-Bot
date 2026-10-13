package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/charmbracelet/log"
)

// webhookClient Webhook 客户端
var webhookClient = &http.Client{
	Timeout: 30 * time.Second,
}

// sendWebhook receives the destination captured with the terminal decision.
// History eviction must not suppress delivery or change the retry destination.
func sendWebhook(ctx context.Context, webhookURL string, payload *WebhookPayload) {
	if payload == nil || payload.TaskID == "" || webhookURL == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Async send with retries.
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		logger := log.FromContext(ctx).With("task_id", payload.TaskID)

		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			logger.Errorf("Failed to marshal webhook payload: %v", err)
			return
		}

		// 重试 3 次
		for i := range 3 {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewBuffer(payloadBytes))
			if err != nil {
				logger.Errorf("Failed to create webhook request: %v", webhookLogError(err))
				return
			}

			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "SaveAny-Bot/1.0")

			resp, err := webhookClient.Do(req)
			if err != nil {
				logger.Warnf("Webhook request failed (attempt %d/3): %v", i+1, webhookLogError(err))
				if i < 2 && !waitWebhookRetry(ctx, i) {
					return
				}
				continue
			}
			resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				logger.Debug("Webhook sent successfully")
				return
			}

			logger.Warnf("Webhook returned non-2xx status (attempt %d/3): %d", i+1, resp.StatusCode)
			if i < 2 && !waitWebhookRetry(ctx, i) {
				return
			}
		}

		logger.Errorf("Failed to send webhook after 3 attempts")
	}()
}

func webhookLogError(err error) error {
	// net/http includes the full destination URL in url.Error. Keep its cause
	// for diagnosis without logging URL credentials, path or query parameters.
	var requestErr *url.Error
	if errors.As(err, &requestErr) {
		return requestErr.Err
	}
	return err
}

// CreateWebhookPayload creates a Webhook payload.
func CreateWebhookPayload(taskID string, taskType string, status TaskStatus, storage, path string, err error) *WebhookPayload {
	payload := &WebhookPayload{
		TaskID:  taskID,
		Type:    taskType,
		Status:  status,
		Storage: storage,
		Path:    path,
	}

	if status.terminal() {
		now := time.Now()
		payload.CompletedAt = &now
	}

	if err != nil {
		payload.Error = err.Error()
	}

	return payload
}

func waitWebhookRetry(ctx context.Context, attempt int) bool {
	if attempt == 2 {
		return false
	}
	timer := time.NewTimer(time.Second * time.Duration(attempt+1))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
