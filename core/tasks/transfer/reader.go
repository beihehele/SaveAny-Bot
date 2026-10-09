package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// sourceReader verifies EOF and closes the source before exposing successful
// EOF to a destination that publishes its file when copying finishes.
type sourceReader struct {
	ctx      context.Context
	reader   io.ReadCloser
	expected int64
	read     int64
	terminal error
	close    sync.Once
	closeErr error
}

func (r *sourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.terminal != nil {
		return 0, r.terminal
	}
	n, err := r.reader.Read(p)
	r.read += int64(n)
	if r.expected >= 0 && r.read > r.expected {
		err = fmt.Errorf("source exceeds expected size %d: read %d", r.expected, r.read)
	}
	if err == io.EOF {
		if r.expected >= 0 && r.read != r.expected {
			err = fmt.Errorf("source size %d, expected %d: %w", r.read, r.expected, io.ErrUnexpectedEOF)
		} else {
			err = nil
		}
		err = errors.Join(err, r.Close())
		if err == nil {
			err = io.EOF
		}
	}
	if cancelled := r.ctx.Err(); cancelled != nil {
		err = errors.Join(err, cancelled)
	}
	if err != nil {
		r.terminal = err
	}
	return n, err
}

func (r *sourceReader) Close() error {
	r.close.Do(func() { r.closeErr = r.reader.Close() })
	return r.closeErr
}

func (r *sourceReader) finish() error {
	// Once EOF and Close succeeded, a later cancellation must not reclassify
	// a destination's successful save. The task still reports parent cancellation.
	if r.terminal == io.EOF {
		return nil
	}
	if r.terminal != nil {
		return r.terminal
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	// Size-aware uploaders may read exactly ContentLength without probing
	// EOF. Verify the source's final result; never silently drain a source
	// that the destination stopped consuming early.
	var extra [1]byte
	n, err := r.Read(extra[:])
	if err != nil && err != io.EOF {
		return err
	}
	if n != 0 || err == nil {
		return fmt.Errorf("destination did not consume the complete source: %w", io.ErrUnexpectedEOF)
	}
	return nil
}
