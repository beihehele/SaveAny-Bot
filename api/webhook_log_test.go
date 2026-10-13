package api

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestWebhookLogErrorOmitsDestinationCredentials(t *testing.T) {
	cause := errors.New("connection refused")
	requestErr := &url.Error{Op: "Post", URL: "https://admin:private-password@example.test/private-path?key=private-token", Err: cause}
	for _, err := range []error{requestErr, fmt.Errorf("delivery failed: %w", requestErr)} {
		logged := webhookLogError(err)
		if !errors.Is(logged, cause) {
			t.Fatal("network cause was lost")
		}
		for _, secret := range []string{"private-password", "private-path", "private-token", "example.test"} {
			if strings.Contains(logged.Error(), secret) {
				t.Fatalf("webhook log retains destination %q", secret)
			}
		}
	}
	if webhookLogError(cause) != cause {
		t.Fatal("plain error changed")
	}
}
