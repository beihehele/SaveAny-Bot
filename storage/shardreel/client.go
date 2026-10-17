package shardreel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type httpError struct {
	status int
	code   string
}

func (e *httpError) Error() string { return fmt.Sprintf("ShardReel HTTP %d (%s)", e.status, e.code) }
func isTransferMissing(err error) bool {
	var value *httpError
	return errors.As(err, &value) && value.status == http.StatusNotFound && value.code == "TRANSFER_NOT_FOUND"
}

func isTransferStateConflict(err error) bool {
	var value *httpError
	return errors.As(err, &value) && value.status == http.StatusConflict &&
		(value.code == "TRANSFER_NOT_WRITABLE" || value.code == "TRANSFER_CANCELLED")
}

func query(value string) string { return url.QueryEscape(value) }

// Check performs an explicit read-only connection and Worker check.
func (s *Store) Check(ctx context.Context) (map[string]any, error) {
	var response struct {
		Root          string `json:"root"`
		WorkerEnabled bool   `json:"worker_enabled"`
	}
	if err := s.call(ctx, "GET", "/info", nil, nil, &response); err != nil {
		return nil, err
	}
	if !response.WorkerEnabled {
		return nil, errors.New("ShardReel Worker is disabled")
	}
	return map[string]any{"root": response.Root, "ready": true}, nil
}

func (s *Store) call(ctx context.Context, method, suffix string, body any, headers map[string]string, output any) error {
	var reader io.Reader
	if raw, ok := body.([]byte); ok {
		reader = bytes.NewReader(raw)
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.cfg.URL+"/v1/storage"+suffix, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("ShardReel connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Detail json.RawMessage `json:"detail"`
		}
		var detail struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&failure); err == nil {
			if err := json.Unmarshal(failure.Detail, &detail); err != nil {
				detail.Code = ""
			}
		}
		return &httpError{response.StatusCode, detail.Code}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	return err
}

type remoteResult struct {
	Status    string `json:"status"`
	Path      string `json:"path"`
	Received  int64  `json:"received_bytes"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	ErrorCode string `json:"error_code"`
	CanRetry  bool   `json:"can_retry"`
}
