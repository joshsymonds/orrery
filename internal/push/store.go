// Package push holds the registration store for Web Push subscriptions and encrypts payloads for them.
package push

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// DefaultPushHost is the push relay host allowed when none is configured.
const DefaultPushHost = "ntfy.sh"

const (
	storeDirPerm   = 0o700
	p256PointBytes = 65
	authSecretSize = 16
)

// Sentinel errors returned by the store.
var (
	// ErrEndpointNotAllowed reports an endpoint that is not https or whose host is not on the allow list.
	ErrEndpointNotAllowed = errors.New("push endpoint not allowed")
	// ErrInvalidSubscription reports a p256dh or auth value that does not decode to a valid key or secret.
	ErrInvalidSubscription = errors.New("invalid push subscription")
	// ErrNotRegistered reports a Remove for an endpoint that is not stored.
	ErrNotRegistered = errors.New("push endpoint not registered")
)

// Subscription is one Web Push subscription: the push endpoint and the client's key material.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// DefaultStorePath resolves the push store file: $XDG_STATE_HOME/orrery/push.json, else
// $HOME/.local/state/orrery/push.json. It returns an error when neither variable is set.
func DefaultStorePath(getenv func(string) string) (string, error) {
	if stateHome := getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "orrery", "push.json"), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", "orrery", "push.json"), nil
	}
	return "", errors.New("no push store path: set XDG_STATE_HOME or HOME")
}

// Store keeps push subscriptions in one JSON file. Endpoints must be https URLs on an allowed host.
// A Store serializes its own reads and writes, so one process can share it across goroutines.
type Store struct {
	mu    sync.Mutex
	path  string
	hosts []string
}

// NewStore returns a Store backed by the JSON file at path. It accepts endpoints only on allowedHosts.
func NewStore(path string, allowedHosts []string) (*Store, error) {
	if path == "" {
		return nil, errors.New("push store path is empty")
	}
	if len(allowedHosts) == 0 {
		return nil, errors.New("push store has no allowed hosts")
	}
	return &Store{path: path, hosts: slices.Clone(allowedHosts)}, nil
}

// Add registers sub, replacing any stored subscription with the same endpoint. The keys are checked
// and stored as unpadded base64url. Nothing is written when the subscription is refused.
func (s *Store) Add(sub Subscription) error {
	if !s.endpointAllowed(sub.Endpoint) {
		return ErrEndpointNotAllowed
	}
	p256dh, err := canonicalP256dh(sub.P256dh)
	if err != nil {
		return err
	}
	auth, err := canonicalAuth(sub.Auth)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.readLocked()
	if err != nil {
		return err
	}
	subs = slices.DeleteFunc(subs, func(existing Subscription) bool { return existing.Endpoint == sub.Endpoint })
	subs = append(subs, Subscription{Endpoint: sub.Endpoint, P256dh: p256dh, Auth: auth})
	return s.writeLocked(subs)
}

// Remove deletes the subscription stored for endpoint. It returns ErrNotRegistered when none is stored.
func (s *Store) Remove(endpoint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.readLocked()
	if err != nil {
		return err
	}
	index := slices.IndexFunc(subs, func(existing Subscription) bool { return existing.Endpoint == endpoint })
	if index < 0 {
		return ErrNotRegistered
	}
	return s.writeLocked(slices.Delete(subs, index, index+1))
}

// List returns the stored subscriptions whose endpoint host is still allowed. A missing file lists empty.
func (s *Store) List() ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	allowed := make([]Subscription, 0, len(subs))
	for _, sub := range subs {
		if s.endpointAllowed(sub.Endpoint) {
			allowed = append(allowed, sub)
		}
	}
	return allowed, nil
}

// endpointAllowed reports whether endpoint is an https URL whose host exactly matches an allowed host.
func (s *Store) endpointAllowed(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := parsed.Hostname()
	return host != "" && slices.ContainsFunc(s.hosts, func(allowed string) bool {
		return strings.EqualFold(allowed, host)
	})
}

// readLocked loads every stored subscription. A missing file yields no subscriptions.
func (s *Store) readLocked() ([]Subscription, error) {
	data, err := os.ReadFile(filepath.Clean(s.path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read push store: %w", err)
	}
	var subs []Subscription
	if err = json.Unmarshal(data, &subs); err != nil {
		return nil, fmt.Errorf("parse push store %s: %w", s.path, err)
	}
	return subs, nil
}

// writeLocked replaces the store file with subs. It writes a temporary file in the same directory and
// renames it into place, so a reader never sees a partial file.
func (s *Store) writeLocked(subs []Subscription) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, storeDirPerm); err != nil {
		return fmt.Errorf("create push store directory: %w", err)
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode push store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "push-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create push store temp file: %w", err)
	}
	tmpName := tmp.Name()
	if err = writeAndClose(tmp, data); err != nil {
		return errors.Join(err, removeTemp(tmpName))
	}
	if err = os.Rename(tmpName, s.path); err != nil {
		return errors.Join(fmt.Errorf("replace push store: %w", err), removeTemp(tmpName))
	}
	return nil
}

// writeAndClose writes data to file, syncs it to disk, and closes it. The file is closed on every path.
func writeAndClose(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return errors.Join(fmt.Errorf("write push store temp file: %w", err), file.Close())
	}
	if err := file.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync push store temp file: %w", err), file.Close())
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close push store temp file: %w", err)
	}
	return nil
}

// removeTemp deletes a leftover temporary file. A file that is already gone is not an error.
func removeTemp(name string) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove push store temp file: %w", err)
	}
	return nil
}

// canonicalP256dh validates an uncompressed P-256 point and returns it as unpadded base64url.
func canonicalP256dh(value string) (string, error) {
	raw, err := decodeBase64URL(value)
	if err != nil {
		return "", fmt.Errorf("%w: p256dh is not base64url: %w", ErrInvalidSubscription, err)
	}
	if len(raw) != p256PointBytes {
		return "", fmt.Errorf("%w: p256dh is %d bytes, want %d", ErrInvalidSubscription, len(raw), p256PointBytes)
	}
	if _, err = ecdh.P256().NewPublicKey(raw); err != nil {
		return "", fmt.Errorf("%w: p256dh is not a P-256 point: %w", ErrInvalidSubscription, err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// canonicalAuth validates a 16-byte auth secret and returns it as unpadded base64url.
func canonicalAuth(value string) (string, error) {
	raw, err := decodeBase64URL(value)
	if err != nil {
		return "", fmt.Errorf("%w: auth is not base64url: %w", ErrInvalidSubscription, err)
	}
	if len(raw) != authSecretSize {
		return "", fmt.Errorf("%w: auth is %d bytes, want %d", ErrInvalidSubscription, len(raw), authSecretSize)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// decodeBase64URL decodes base64url input with or without padding.
func decodeBase64URL(value string) ([]byte, error) {
	if raw, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return raw, nil
	}
	raw, err := base64.URLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode base64url: %w", err)
	}
	return raw, nil
}
