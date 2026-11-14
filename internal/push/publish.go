package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Notification is one pane event sent to every registered subscription.
type Notification struct {
	Pane, Host, Project      string
	Session                  string
	Epic                     string
	State                    protocol.State
	Summary, Label, Question string
	Time                     int64
	Silent                   bool
}

// Clear tells the app that a pane or session was seen at Seen (unix seconds), so notices at or before it
// can be removed. Pane is set for a pane clear, Session for a session clear and Epic for an epic clear.
type Clear struct {
	Host, Pane, Session, Epic string
	Seen                      int64
}

// Payload keys the app reads.
const (
	keyPane         = "pane"
	keyHost         = "host"
	keyProject      = "project"
	keySession      = "session"
	keyEpic         = "epic"
	keyState        = "state"
	keySummary      = "summary"
	keyLabel        = "label"
	keyQuestion     = "question"
	keyQuestionHash = "question_hash"
	keyTime         = "time"
	keyKind         = "kind"
	keySeen         = "seen"
	keySilent       = "silent"
)

// kindClear is the kind value of a clear payload.
const kindClear = "clear"

// pushTTLHeader names the Web Push TTL header.
const pushTTLHeader = "TTL"

// pushTTLSeconds is how long the push service may hold an undelivered notification.
const pushTTLSeconds = 3600

// Publisher sends notifications to every stored subscription as encrypted Web Push POSTs.
type Publisher struct {
	store  *Store
	client *http.Client
}

// NewPublisher returns a Publisher that reads subscriptions from store and posts with client.
func NewPublisher(store *Store, client *http.Client) (*Publisher, error) {
	if store == nil {
		return nil, errors.New("push publisher: nil store")
	}
	if client == nil {
		return nil, errors.New("push publisher: nil HTTP client")
	}
	return &Publisher{store: store, client: client}, nil
}

// Publish encrypts note for every stored subscription and POSTs each body. Every body is built before the
// first POST, so a payload that cannot fit posts nothing. A failed POST does not stop the rest; the
// returned error joins every failure.
func (p *Publisher) Publish(ctx context.Context, note Notification) error {
	return p.publishPayload(ctx, func() ([]byte, error) { return boundedPayload(note) })
}

// PublishClear sends the clear cleared to every stored subscription, with the same encryption, headers and error
// handling as Publish.
func (p *Publisher) PublishClear(ctx context.Context, cleared Clear) error {
	return p.publishPayload(ctx, func() ([]byte, error) { return clearPayload(cleared) })
}

// publishPayload builds the payload with build, encrypts it for every stored subscription, and POSTs each body.
// build runs only when a subscription exists, so an empty store posts nothing and never fails on a payload.
func (p *Publisher) publishPayload(ctx context.Context, build func() ([]byte, error)) error {
	subs, err := p.store.List()
	if err != nil {
		return fmt.Errorf("list push subscriptions: %w", err)
	}
	if len(subs) == 0 {
		return nil
	}
	payload, err := build()
	if err != nil {
		return err
	}
	bodies := make([][]byte, len(subs))
	for i, sub := range subs {
		body, encErr := encryptFor(sub, payload)
		if encErr != nil {
			return fmt.Errorf("encrypt push notification: %w", encErr)
		}
		bodies[i] = body
	}
	var errs []error
	for i, sub := range subs {
		if postErr := p.post(ctx, sub.Endpoint, bodies[i]); postErr != nil {
			errs = append(errs, postErr)
		}
	}
	return errors.Join(errs...)
}

// clearPayload returns the JSON payload for cleared. The pane key is present only when cleared.Pane is set,
// the session key only when cleared.Session is set and the epic key only when cleared.Epic is set. A clear
// carries no notice keys.
func clearPayload(cleared Clear) ([]byte, error) {
	fields := map[string]string{
		keyKind: kindClear,
		keyHost: cleared.Host,
		keySeen: strconv.FormatInt(cleared.Seen, 10),
	}
	if cleared.Pane != "" {
		fields[keyPane] = cleared.Pane
	}
	if cleared.Session != "" {
		fields[keySession] = cleared.Session
	}
	if cleared.Epic != "" {
		fields[keyEpic] = cleared.Epic
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode clear payload: %w", err)
	}
	return body, nil
}

// boundedPayload returns the JSON payload for note. The question is shortened first, then the summary if
// the payload still does not fit with an empty question. The question_hash always covers the full question.
// The time key is always present. The session key is present only when note.Session is set, the epic key only
// when note.Epic is set, and the silent key only when note.Silent is set.
func boundedPayload(note Notification) ([]byte, error) {
	hash := protocol.QuestionHash(note.Question)
	encode := func(summary, question string) ([]byte, error) {
		fields := map[string]string{
			keyPane:         note.Pane,
			keyHost:         note.Host,
			keyProject:      note.Project,
			keyState:        string(note.State),
			keySummary:      summary,
			keyLabel:        note.Label,
			keyQuestion:     question,
			keyQuestionHash: hash,
			keyTime:         strconv.FormatInt(note.Time, 10),
		}
		if note.Session != "" {
			fields[keySession] = note.Session
		}
		if note.Epic != "" {
			fields[keyEpic] = note.Epic
		}
		if note.Silent {
			fields[keySilent] = "true"
		}
		return json.Marshal(fields)
	}
	fits := func(summary, question string) bool {
		body, err := encode(summary, question)
		return err == nil && len(body) <= MaxPlaintextBytes
	}

	summary, question := note.Summary, note.Question
	if !fits(summary, question) {
		question = longestFittingPrefix(note.Question, func(q string) bool { return fits(summary, q) })
	}
	if !fits(summary, question) {
		summary = longestFittingPrefix(note.Summary, func(s string) bool { return fits(s, "") })
		question = ""
	}
	if !fits(summary, question) {
		return nil, fmt.Errorf(
			"push payload exceeds %d bytes even with an empty question and summary", MaxPlaintextBytes)
	}
	return encode(summary, question)
}

// longestFittingPrefix returns the longest prefix of s, cut at a rune boundary, for which fits holds.
// It returns "" when no prefix fits. fits must be monotone: once a prefix fails, every longer one fails.
func longestFittingPrefix(text string, fits func(string) bool) string {
	cuts := make([]int, 0, len(text)+1)
	for i := range text {
		cuts = append(cuts, i)
	}
	cuts = append(cuts, len(text))
	index := sort.Search(len(cuts), func(i int) bool { return !fits(text[:cuts[i]]) }) - 1
	if index < 0 {
		return ""
	}
	return text[:cuts[index]]
}

// encryptFor encrypts payload for one subscription under a fresh ephemeral key and a fresh salt.
func encryptFor(sub Subscription, payload []byte) ([]byte, error) {
	uaPublic, err := decodeBase64URL(sub.P256dh)
	if err != nil {
		return nil, fmt.Errorf("%w: decode p256dh: %w", ErrInvalidSubscription, err)
	}
	authSecret, err := decodeBase64URL(sub.Auth)
	if err != nil {
		return nil, fmt.Errorf("%w: decode auth: %w", ErrInvalidSubscription, err)
	}
	asPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}
	salt := make([]byte, saltLen)
	if _, err = rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return Encrypt(payload, uaPublic, authSecret, asPrivate, salt)
}

// post sends one encrypted body to endpoint and fails on any non-2xx reply.
func (p *Publisher) post(ctx context.Context, endpoint string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(pushTTLHeader, strconv.Itoa(pushTTLSeconds))
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("post push notification: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("push endpoint returned %s", resp.Status)
	}
	return nil
}
