package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strconv"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// Notification is one pane event sent to every registered subscription. Options are the option labels in answer
// order, so option n is key n. Request is the headless request's id.
type Notification struct {
	Pane, Host, Project      string
	Session                  string
	Epic                     string
	Limit                    string
	Alert                    string
	Block                    string
	BlockRef                 string
	State                    protocol.State
	Summary, Label, Question string
	Options                  []string
	Request                  string
	Time                     int64
	Silent                   bool
}

// Clear tells the app that a pane, session, epic, limit or alert was seen at Seen (unix seconds), so notices at or
// before it can be removed. Pane is set for a pane clear, Session for a session clear, Epic for an epic clear, Limit
// for a limit clear and Alert for an alert clear.
type Clear struct {
	Host, Pane, Session, Epic, Limit, Alert string
	Seen                                    int64
}

// Payload keys the app reads.
const (
	keyPane         = "pane"
	keyHost         = "host"
	keyProject      = "project"
	keySession      = "session"
	keyEpic         = "epic"
	keyLimit        = "limit"
	keyAlert        = "alert"
	keyBlock        = "block"
	keyBlockRef     = "block_ref"
	keyState        = "state"
	keySummary      = "summary"
	keyLabel        = "label"
	keyQuestion     = "question"
	keyQuestionHash = "question_hash"
	keyOptions      = "options"
	keyRequest      = "request"
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
	daemon *telemetry.Daemon
}

// NewPublisher returns a Publisher that reads subscriptions from store, posts with client and counts failed posts on
// daemon. A nil daemon counts nothing.
func NewPublisher(store *Store, client *http.Client, daemon *telemetry.Daemon) (*Publisher, error) {
	if store == nil {
		return nil, errors.New("push publisher: nil store")
	}
	if client == nil {
		return nil, errors.New("push publisher: nil HTTP client")
	}
	return &Publisher{store: store, client: client, daemon: daemon}, nil
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

// clearPayload returns the JSON payload for cleared. The pane key is present only when cleared.Pane is set, the
// session key only when cleared.Session is set, the epic key only when cleared.Epic is set, the limit key only
// when cleared.Limit is set, and the alert key only when cleared.Alert is set. A clear carries no notice keys.
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
	if cleared.Limit != "" {
		fields[keyLimit] = cleared.Limit
	}
	if cleared.Alert != "" {
		fields[keyAlert] = cleared.Alert
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode clear payload: %w", err)
	}
	return body, nil
}

// errPayloadTooLarge is the error of a payload that does not fit one record even with an empty question and summary.
var errPayloadTooLarge = fmt.Errorf(
	"push payload exceeds %d bytes even with an empty question and summary", MaxPlaintextBytes)

// boundedPayload returns the JSON payload for note, cut to fit one record. The question is shortened first, then
// the summary, with an empty question, if the payload still does not fit. The options key is an array of the
// option labels, present only when note.Options is set, and it is kept whole while the question and summary are
// cut. When the options do not fit even with an empty question and summary, they are dropped, and the question
// is then cut first and the summary second, from their full text. The question_hash always covers the full
// question. The request key is present only when note.Request is set, and it is kept in every cut. The time key
// is always present. The session key is present only when note.Session is set, the epic key only when note.Epic
// is set, the limit key only when note.Limit is set, the alert key only when note.Alert is set, the block key only
// when note.Block is set, the block_ref key only when note.BlockRef is set, and the silent key only when
// note.Silent is set. A payload that does not fit even with no options, an empty question and an empty summary is
// an error.
func boundedPayload(note Notification) ([]byte, error) {
	if len(note.Options) == 0 {
		return cutPayload(note)
	}
	body, err := cutPayload(note)
	if !errors.Is(err, errPayloadTooLarge) {
		return body, err
	}
	note.Options = nil
	return cutPayload(note)
}

// answerKeys returns the options and request keys of note. The options key is present only when note.Options is
// set, and the request key only when note.Request is set.
func answerKeys(note Notification) map[string]any {
	keys := make(map[string]any, 2)
	if len(note.Options) > 0 {
		keys[keyOptions] = note.Options
	}
	if note.Request != "" {
		keys[keyRequest] = note.Request
	}
	return keys
}

// cutPayload returns the JSON payload for note with its options, cut as boundedPayload describes. It fails with
// errPayloadTooLarge when no cut fits one record.
func cutPayload(note Notification) ([]byte, error) {
	hash := protocol.QuestionHash(note.Question)
	encode := func(summary, question string) ([]byte, error) {
		fields := map[string]any{
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
		maps.Copy(fields, answerKeys(note))
		if note.Session != "" {
			fields[keySession] = note.Session
		}
		if note.Epic != "" {
			fields[keyEpic] = note.Epic
		}
		if note.Limit != "" {
			fields[keyLimit] = note.Limit
		}
		if note.Alert != "" {
			fields[keyAlert] = note.Alert
		}
		if note.Block != "" {
			fields[keyBlock] = note.Block
		}
		if note.BlockRef != "" {
			fields[keyBlockRef] = note.BlockRef
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
		return nil, errPayloadTooLarge
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

// post sends one encrypted body to endpoint and fails on any non-2xx reply. Each failure is counted on the daemon by
// its status, with 0 for no reply.
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
		p.daemon.PushFailed(ctx, 0)
		return fmt.Errorf("post push notification: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		p.daemon.PushFailed(ctx, resp.StatusCode)
		return fmt.Errorf("push endpoint returned %s", resp.Status)
	}
	return nil
}
