// Package ntfy posts waiting and done pane notifications to an ntfy topic.
package ntfy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// ErrNotConfigured is returned by New when no ntfy topic URL file is named.
var ErrNotConfigured = errors.New("ntfy: no topic URL file configured")

// Publisher posts pane notifications to one ntfy topic.
type Publisher struct {
	topicURL string
	token    string
	host     string
	client   *http.Client
}

// New reads the topic URL and optional token from the files named by the environment and returns a Publisher.
func New(getenv func(string) string, host string, client *http.Client) (*Publisher, error) {
	urlFile := getenv(protocol.EnvNtfyURLFile)
	if urlFile == "" {
		return nil, ErrNotConfigured
	}
	if client == nil {
		return nil, errors.New("ntfy: nil HTTP client")
	}
	topicURL, err := readTrimmed(urlFile)
	if err != nil {
		return nil, fmt.Errorf("read ntfy topic URL: %w", err)
	}
	if topicURL == "" {
		return nil, fmt.Errorf("ntfy topic URL file %s is empty", urlFile)
	}
	var token string
	if tokenFile := getenv(protocol.EnvNtfyAuthFile); tokenFile != "" {
		token, err = readTrimmed(tokenFile)
		if err != nil {
			return nil, fmt.Errorf("read ntfy token: %w", err)
		}
	}
	return &Publisher{topicURL: topicURL, token: token, host: host, client: client}, nil
}

// Publish posts a notification for a pane in the given state. Working panes are not notified and return an error.
func (p *Publisher) Publish(
	ctx context.Context,
	project, session string,
	window int,
	state protocol.State,
	body string,
) error {
	priority, tag, err := notice(state)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("%s · %s:%d", project, session, window)
	return p.post(ctx, title, priority, tag, body)
}

// PublishSession posts a headless session notification. The session name is used as given. States without a notification return an error.
func (p *Publisher) PublishSession(
	ctx context.Context,
	project, session, state, body string,
) error {
	priority, tag, err := sessionNotice(state)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("%s · session %s", project, session)
	return p.post(ctx, title, priority, tag, body)
}

// post sends one notification to the topic with the given title, priority and tag.
func (p *Publisher) post(ctx context.Context, title, priority, tag, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.topicURL, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ntfy request: %w", err)
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Tags", tag+","+p.host)
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post ntfy notification: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("ntfy returned %s", resp.Status)
	}
	return nil
}

// notice returns the ntfy priority and tag for a pane state.
func notice(state protocol.State) (string, string, error) {
	switch state {
	case protocol.StateWaiting:
		return "5", "question", nil
	case protocol.StateDone:
		return "4", "white_check_mark", nil
	case protocol.StateWorking:
		return "", "", errors.New("pane state \"working\" has no ntfy notification")
	default:
		return "", "", fmt.Errorf("unknown pane state %q", state)
	}
}

// sessionNotice returns the ntfy priority and tag for a headless session state.
func sessionNotice(state string) (string, string, error) {
	switch state {
	case "waiting":
		return "5", "question", nil
	case "done":
		return "4", "white_check_mark", nil
	case "failed":
		return "4", "x", nil
	case "interrupted":
		return "3", "warning", nil
	default:
		return "", "", fmt.Errorf("session state %q has no ntfy notification", state)
	}
}

// readTrimmed returns the contents of path with surrounding whitespace removed.
func readTrimmed(path string) (string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}
