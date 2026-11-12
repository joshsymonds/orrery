package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Wire constants restated in the test so the decryption checks the RFC 8188 framing on its own.
const (
	pubSaltLen    = 16
	pubRecordSize = 4096
	pubKeyIDLen   = 65
	pubHeaderLen  = pubSaltLen + 4 + 1 + pubKeyIDLen
	pubKeyLen     = 16
	pubNonceLen   = 12
	pubIKMLen     = 32
	pubDelimiter  = 0x02
	pubAuthLen    = 16
	pubTopic      = "synthetic-topic"
)

// pubRecipient is a synthetic user agent: its P-256 key pair and auth secret.
type pubRecipient struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newPubRecipient(t *testing.T) pubRecipient {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate recipient key: %v", err)
	}
	auth := make([]byte, pubAuthLen)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return pubRecipient{priv: priv, auth: auth}
}

// pubRequest is one POST an httptest endpoint received.
type pubRequest struct {
	method string
	header http.Header
	body   []byte
}

// pubEndpoint is an httptest TLS push endpoint that records every request and answers with status.
type pubEndpoint struct {
	srv       *httptest.Server
	recipient pubRecipient
	mu        sync.Mutex
	requests  []pubRequest
}

func newPubEndpoint(t *testing.T, status int) *pubEndpoint {
	t.Helper()
	ep := &pubEndpoint{recipient: newPubRecipient(t)}
	ep.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		ep.mu.Lock()
		ep.requests = append(ep.requests, pubRequest{method: r.Method, header: r.Header.Clone(), body: body})
		ep.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(ep.srv.Close)
	return ep
}

func (ep *pubEndpoint) received() []pubRequest {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return slices.Clone(ep.requests)
}

func (ep *pubEndpoint) subscription() Subscription {
	return Subscription{
		Endpoint: ep.srv.URL + "/" + pubTopic,
		P256dh:   base64.RawURLEncoding.EncodeToString(ep.recipient.priv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(ep.recipient.auth),
	}
}

// newPubPublisher returns a Publisher whose store holds one subscription per endpoint. The store
// allows only 127.0.0.1, where the httptest servers listen.
func newPubPublisher(t *testing.T, client *http.Client, endpoints ...*pubEndpoint) *Publisher {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "push.json"), []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	for _, ep := range endpoints {
		if err := store.Add(ep.subscription()); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	p, err := NewPublisher(store, client)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return p
}

func pubNotification(question string) Notification {
	return Notification{
		Pane:     "synthetic-pane",
		Host:     "synthetic-host",
		Project:  "synthetic-project",
		State:    protocol.StateWaiting,
		Summary:  "synthetic summary",
		Label:    "synthetic label",
		Question: question,
		Time:     1700000000,
	}
}

// pubEscapedQuestion returns n runes that JSON escapes or widens: quotes, backslashes, newlines,
// control bytes, two-byte and four-byte runes.
func pubEscapedQuestion(n int) string {
	units := []rune{'"', '\\', '\n', '\x01', 'é', '😀', 'a'}
	var b strings.Builder
	for i := range n {
		b.WriteRune(units[i%len(units)])
	}
	return b.String()
}

// onePush asserts ep received exactly one POST and returns it.
func onePush(t *testing.T, ep *pubEndpoint) pubRequest {
	t.Helper()
	reqs := ep.received()
	if len(reqs) != 1 {
		t.Fatalf("endpoint received %d requests, want 1", len(reqs))
	}
	return reqs[0]
}

func assertPushHeaders(t *testing.T, req pubRequest) {
	t.Helper()
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.method)
	}
	if got := req.header.Get("Content-Encoding"); got != "aes128gcm" {
		t.Errorf("Content-Encoding = %q, want aes128gcm", got)
	}
	if got := req.header.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := req.header.Get(pushTTLHeader); got != strconv.Itoa(pushTTLSeconds) {
		t.Errorf("%s = %q, want %d", pushTTLHeader, got, pushTTLSeconds)
	}
}

// decryptPush is the user-agent side of RFC 8291 for one aes128gcm record. It returns the decoded
// JSON object and fails the test on any framing or key error.
func decryptPush(t *testing.T, body []byte, recipient pubRecipient) map[string]string {
	t.Helper()
	if len(body) <= pubHeaderLen {
		t.Fatalf("body is %d bytes, not longer than the %d-byte header", len(body), pubHeaderLen)
	}
	if rs := binary.BigEndian.Uint32(body[pubSaltLen:]); rs != pubRecordSize {
		t.Fatalf("rs = %d, want %d", rs, pubRecordSize)
	}
	salt := body[:pubSaltLen]
	keyID := body[pubSaltLen+5 : pubHeaderLen]
	asPublic, err := ecdh.P256().NewPublicKey(keyID)
	if err != nil {
		t.Fatalf("keyid is not a P-256 point: %v", err)
	}
	secret, err := recipient.priv.ECDH(asPublic)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	uaPublic := recipient.priv.PublicKey().Bytes()
	ikm, err := hkdf.Key(sha256.New, secret, recipient.auth,
		"WebPush: info\x00"+string(uaPublic)+string(keyID), pubIKMLen)
	if err != nil {
		t.Fatalf("ikm: %v", err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", pubKeyLen)
	if err != nil {
		t.Fatalf("cek: %v", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", pubNonceLen)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	padded, err := gcm.Open(nil, nonce, body[pubHeaderLen:], nil)
	if err != nil {
		t.Fatalf("open record: %v", err)
	}
	if len(padded) == 0 || padded[len(padded)-1] != pubDelimiter {
		t.Fatal("record does not end with the final-record delimiter")
	}
	var payload map[string]string
	if err := json.Unmarshal(padded[:len(padded)-1], &payload); err != nil {
		t.Fatalf("payload is not a JSON object of strings: %v", err)
	}
	return payload
}

func TestPushPublishDeliversEncryptedPayload(t *testing.T) {
	first := newPubEndpoint(t, http.StatusCreated)
	second := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, first.srv.Client(), first, second)

	n := pubNotification("synthetic question?")
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          n.Pane,
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
	}
	for _, ep := range []*pubEndpoint{first, second} {
		req := onePush(t, ep)
		assertPushHeaders(t, req)
		if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
			t.Errorf("payload = %v, want %v", got, want)
		}
	}

	a := onePush(t, first).body
	b := onePush(t, second).body
	if bytes.Equal(a[:pubHeaderLen], b[:pubHeaderLen]) {
		t.Error("two POSTs share a salt and key ID; each needs a fresh ephemeral key and salt")
	}
}

func TestPushPublishCarriesSessionKey(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Pane = ""
	n.Session = "s1"
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          "",
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
		"session":       "s1",
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}

func TestPushPublishOmitsSessionKeyWithoutSession(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          n.Pane,
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want the pane keys %v", got, want)
	}
}

func TestPushPublishKeepsSessionWhenSummaryShortened(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Pane = ""
	n.Session = "s1"
	n.Summary = pubEscapedQuestion(3000)
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	if len(req.body) > pubRecordSize {
		t.Errorf("body is %d bytes, want at most %d", len(req.body), pubRecordSize)
	}
	payload := decryptPush(t, req.body, ep.recipient)
	if payload["session"] != "s1" {
		t.Errorf("session = %q, want it kept whole as %q", payload["session"], "s1")
	}
	if payload["question"] != "" {
		t.Errorf("question = %q, want it emptied before the summary is shortened", payload["question"])
	}
	got := payload["summary"]
	if !utf8.ValidString(got) || !strings.HasPrefix(n.Summary, got) || len(got) >= len(n.Summary) {
		t.Errorf("summary is %d bytes, want a valid truncated prefix of the %d-byte original", len(got), len(n.Summary))
	}
	if payload["question_hash"] != protocol.QuestionHash(n.Question) {
		t.Error("question_hash does not hash the original question")
	}
}

func TestPushPublishCarriesSilentKey(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Silent = true
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	want := map[string]string{
		"pane":          n.Pane,
		"host":          n.Host,
		"project":       n.Project,
		"state":         string(n.State),
		"summary":       n.Summary,
		"label":         n.Label,
		"question":      n.Question,
		"question_hash": protocol.QuestionHash(n.Question),
		"time":          strconv.FormatInt(n.Time, 10),
		"silent":        "true",
	}
	if got := decryptPush(t, onePush(t, ep).body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}

func TestPushPublishOmitsSilentKeyWithoutSilent(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	payload := decryptPush(t, onePush(t, ep).body, ep.recipient)
	if value, ok := payload["silent"]; ok {
		t.Errorf("payload carries silent = %q, want no silent key", value)
	}
}

func TestPushPublishKeepsSilentWhenSummaryShortened(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Silent = true
	n.Summary = pubEscapedQuestion(3000)
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	if len(req.body) > pubRecordSize {
		t.Errorf("body is %d bytes, want at most %d", len(req.body), pubRecordSize)
	}
	payload := decryptPush(t, req.body, ep.recipient)
	if payload["silent"] != "true" {
		t.Errorf("silent = %q, want %q after the summary is shortened", payload["silent"], "true")
	}
	if got := payload["summary"]; len(got) >= len(n.Summary) {
		t.Errorf("summary is %d bytes, want it shortened from %d", len(got), len(n.Summary))
	}
}

func TestPushPublishTruncatesQuestionToFitRecord(t *testing.T) {
	first := newPubEndpoint(t, http.StatusOK)
	second := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, first.srv.Client(), first, second)

	question := pubEscapedQuestion(6000)
	n := pubNotification(question)
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	for _, ep := range []*pubEndpoint{first, second} {
		req := onePush(t, ep)
		assertPushHeaders(t, req)
		if len(req.body) > pubRecordSize {
			t.Errorf("body is %d bytes, want at most %d", len(req.body), pubRecordSize)
		}
		payload := decryptPush(t, req.body, ep.recipient)
		got := payload["question"]
		if !utf8.ValidString(got) {
			t.Error("decrypted question is not valid UTF-8")
		}
		if !strings.HasPrefix(question, got) {
			t.Error("decrypted question is not a prefix of the original question")
		}
		if len(got) >= len(question) {
			t.Errorf("decrypted question is %d bytes, want it truncated below %d", len(got), len(question))
		}
		if payload["question_hash"] != protocol.QuestionHash(question) {
			t.Error("question_hash does not hash the original, untruncated question")
		}
		if payload["summary"] != n.Summary {
			t.Errorf("summary = %q, want the unchanged %q", payload["summary"], n.Summary)
		}
	}
}

func TestPushPublishShortensSummaryAfterQuestion(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	n := pubNotification("synthetic question?")
	n.Summary = pubEscapedQuestion(3000)
	if err := p.Publish(context.Background(), n); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	if len(req.body) > pubRecordSize {
		t.Errorf("body is %d bytes, want at most %d", len(req.body), pubRecordSize)
	}
	payload := decryptPush(t, req.body, ep.recipient)
	if payload["question"] != "" {
		t.Errorf("question = %q, want it emptied before the summary is shortened", payload["question"])
	}
	got := payload["summary"]
	if !utf8.ValidString(got) || !strings.HasPrefix(n.Summary, got) || len(got) >= len(n.Summary) {
		t.Errorf("summary is %d bytes, want a valid truncated prefix of the %d-byte original", len(got), len(n.Summary))
	}
	if payload["question_hash"] != protocol.QuestionHash(n.Question) {
		t.Error("question_hash does not hash the original question")
	}
}

func TestPushPublishPostsNothingWhenPayloadCannotFit(t *testing.T) {
	first := newPubEndpoint(t, http.StatusOK)
	second := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, first.srv.Client(), first, second)

	n := pubNotification("synthetic question?")
	n.Label = strings.Repeat("l", 5000)
	if err := p.Publish(context.Background(), n); err == nil {
		t.Fatal("Publish succeeded for a payload that cannot fit one record")
	}
	for _, ep := range []*pubEndpoint{first, second} {
		if got := len(ep.received()); got != 0 {
			t.Errorf("endpoint received %d requests, want none", got)
		}
	}
}

func TestPushPublishDeliversToOthersAfterEndpointFailure(t *testing.T) {
	failing := newPubEndpoint(t, http.StatusInternalServerError)
	healthy := newPubEndpoint(t, http.StatusCreated)
	p := newPubPublisher(t, failing.srv.Client(), failing, healthy)

	n := pubNotification("synthetic question?")
	if err := p.Publish(context.Background(), n); err == nil {
		t.Fatal("Publish succeeded although an endpoint returned 500")
	}
	onePush(t, failing)
	req := onePush(t, healthy)
	assertPushHeaders(t, req)
	if got := decryptPush(t, req.body, healthy.recipient)["question"]; got != n.Question {
		t.Errorf("healthy endpoint question = %q, want %q", got, n.Question)
	}
}

func TestPushPublishWithNoSubscriptionsSendsNothing(t *testing.T) {
	unused := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, unused.srv.Client())

	if err := p.Publish(context.Background(), pubNotification("synthetic question?")); err != nil {
		t.Fatalf("Publish with no subscriptions: %v", err)
	}
	if got := len(unused.received()); got != 0 {
		t.Errorf("unregistered endpoint received %d requests, want none", got)
	}
}

func TestPushPublishClearCarriesPaneKeys(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	c := Clear{Host: "synthetic-host", Pane: "synthetic-pane", Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	want := map[string]string{
		"kind": "clear",
		"host": c.Host,
		"pane": c.Pane,
		"seen": strconv.FormatInt(c.Seen, 10),
	}
	if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want exactly %v", got, want)
	}
}

func TestPushPublishClearCarriesSessionKeys(t *testing.T) {
	ep := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, ep.srv.Client(), ep)

	c := Clear{Host: "synthetic-host", Session: "s1", Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear: %v", err)
	}

	req := onePush(t, ep)
	assertPushHeaders(t, req)
	want := map[string]string{
		"kind":    "clear",
		"host":    c.Host,
		"session": c.Session,
		"seen":    strconv.FormatInt(c.Seen, 10),
	}
	if got := decryptPush(t, req.body, ep.recipient); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want exactly %v", got, want)
	}
}

func TestPushPublishClearWithNoSubscriptionsSendsNothing(t *testing.T) {
	unused := newPubEndpoint(t, http.StatusOK)
	p := newPubPublisher(t, unused.srv.Client())

	c := Clear{Host: "synthetic-host", Pane: "synthetic-pane", Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err != nil {
		t.Fatalf("PublishClear with no subscriptions: %v", err)
	}
	if got := len(unused.received()); got != 0 {
		t.Errorf("unregistered endpoint received %d requests, want none", got)
	}
}

func TestPushPublishClearDeliversToOthersAfterEndpointFailure(t *testing.T) {
	failing := newPubEndpoint(t, http.StatusInternalServerError)
	healthy := newPubEndpoint(t, http.StatusCreated)
	p := newPubPublisher(t, failing.srv.Client(), failing, healthy)

	c := Clear{Host: "synthetic-host", Pane: "synthetic-pane", Seen: 1700000500}
	if err := p.PublishClear(context.Background(), c); err == nil {
		t.Fatal("PublishClear succeeded although an endpoint returned 500")
	}
	onePush(t, failing)
	req := onePush(t, healthy)
	assertPushHeaders(t, req)
	if got := decryptPush(t, req.body, healthy.recipient)["pane"]; got != c.Pane {
		t.Errorf("healthy endpoint pane = %q, want %q", got, c.Pane)
	}
}

func TestPushPublishRejectsNilDependencies(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "push.json"), []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := NewPublisher(nil, http.DefaultClient); err == nil {
		t.Error("NewPublisher accepted a nil store")
	}
	if _, err := NewPublisher(store, nil); err == nil {
		t.Error("NewPublisher accepted a nil client")
	}
}
