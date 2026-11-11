package push

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

const secretEndpoint = "https://push.example.test/send/s3cr3t-token"

func urlError(endpoint string) *url.Error {
	return &url.Error{Op: "Post", URL: endpoint, Err: errors.New("connection refused")}
}

// assertRedacted checks the message names the placeholder and carries no part of the endpoint.
func assertRedacted(t *testing.T, msg string) {
	t.Helper()
	if !strings.Contains(msg, `"<endpoint>"`) {
		t.Errorf("redacted message %q lacks the endpoint placeholder", msg)
	}
	for _, part := range []string{secretEndpoint, "push.example.test", "s3cr3t-token"} {
		if strings.Contains(msg, part) {
			t.Errorf("redacted message %q still contains %q", msg, part)
		}
	}
}

func TestRedactEndpointsSingleURLError(t *testing.T) {
	err := urlError(secretEndpoint)
	msg := RedactEndpoints(err)
	assertRedacted(t, msg)
	if want := err.Error(); msg == want {
		t.Errorf("message was not changed: %q", msg)
	}
}

func TestRedactEndpointsWrappedURLError(t *testing.T) {
	err := fmt.Errorf("post push notification: %w", urlError(secretEndpoint))
	assertRedacted(t, RedactEndpoints(err))
}

func TestRedactEndpointsJoinedURLErrors(t *testing.T) {
	err := errors.Join(
		urlError(secretEndpoint),
		urlError("https://push.example.test/send/other-token"),
	)
	msg := RedactEndpoints(err)
	assertRedacted(t, msg)
	if strings.Contains(msg, "other-token") {
		t.Errorf("redacted message %q still contains the second endpoint", msg)
	}
	if n := strings.Count(msg, `"<endpoint>"`); n != 2 {
		t.Errorf("placeholder count = %d, want 2 in %q", n, msg)
	}
}

func TestRedactEndpointsNoURLErrorUnchanged(t *testing.T) {
	err := fmt.Errorf("list push subscriptions: %w", errors.New("disk full"))
	if msg := RedactEndpoints(err); msg != err.Error() {
		t.Errorf("RedactEndpoints = %q, want %q", msg, err.Error())
	}
}
