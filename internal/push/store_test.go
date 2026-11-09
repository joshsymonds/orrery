package push_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/push"
)

const (
	testTopic      = "orrery-synthetic-topic"
	testAuthLength = 16
)

// testEndpoint returns an https endpoint on the default push host.
func testEndpoint() string {
	return "https://" + push.DefaultPushHost + "/" + testTopic
}

// testKeys generates a fresh P-256 public key and auth secret, both base64url without padding.
func testKeys(t *testing.T) (string, string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	secret := make([]byte, testAuthLength)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// newTestStore opens a store under a fresh temporary directory that allows only the default push host.
func newTestStore(t *testing.T) (*push.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "push.json")
	store, err := push.NewStore(path, []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, path
}

func mustList(t *testing.T, store *push.Store) []push.Subscription {
	t.Helper()
	subs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return subs
}

func TestPushRegisterRoundTripPersists(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "orrery", "push.json")
	store, err := push.NewStore(path, []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	p256dh, auth := testKeys(t)
	sub := push.Subscription{Endpoint: testEndpoint(), P256dh: p256dh, Auth: auth}
	if err := store.Add(sub); err != nil {
		t.Fatalf("Add: %v", err)
	}

	subs := mustList(t, store)
	if len(subs) != 1 || subs[0] != sub {
		t.Fatalf("List = %+v, want [%+v]", subs, sub)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("store file mode = %o, want 600", mode)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read store directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "push.json" {
		t.Errorf("store directory holds %d entries, want only push.json (temp file must be renamed away)", len(entries))
	}

	reopened, err := push.NewStore(path, []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("NewStore on existing path: %v", err)
	}
	if got := mustList(t, reopened); len(got) != 1 || got[0] != sub {
		t.Fatalf("reopened List = %+v, want [%+v]", got, sub)
	}
}

func TestPushRegisterAcceptsPaddedKeys(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate P-256 key: %v", err)
	}
	secret := make([]byte, testAuthLength)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	paddedP256dh := base64.URLEncoding.EncodeToString(priv.PublicKey().Bytes())
	paddedAuth := base64.URLEncoding.EncodeToString(secret)
	padded := push.Subscription{Endpoint: testEndpoint(), P256dh: paddedP256dh, Auth: paddedAuth}
	if err := store.Add(padded); err != nil {
		t.Fatalf("Add with padded keys: %v", err)
	}

	subs := mustList(t, store)
	wantP256dh := base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	wantAuth := base64.RawURLEncoding.EncodeToString(secret)
	if len(subs) != 1 || subs[0].P256dh != wantP256dh || subs[0].Auth != wantAuth {
		t.Fatalf("List = %+v, want unpadded keys %q and %q", subs, wantP256dh, wantAuth)
	}
}

func TestPushRegisterAddReplacesEndpoint(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	p256dh, auth := testKeys(t)
	if err := store.Add(push.Subscription{Endpoint: testEndpoint(), P256dh: p256dh, Auth: auth}); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	newP256dh, newAuth := testKeys(t)
	replacement := push.Subscription{Endpoint: testEndpoint(), P256dh: newP256dh, Auth: newAuth}
	if err := store.Add(replacement); err != nil {
		t.Fatalf("second Add: %v", err)
	}

	subs := mustList(t, store)
	if len(subs) != 1 || subs[0] != replacement {
		t.Fatalf("List = %+v, want only the replacement [%+v]", subs, replacement)
	}
}

func TestPushRegisterRemove(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	p256dh, auth := testKeys(t)
	if err := store.Add(push.Subscription{Endpoint: testEndpoint(), P256dh: p256dh, Auth: auth}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Remove(testEndpoint()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if subs := mustList(t, store); len(subs) != 0 {
		t.Fatalf("List after Remove = %+v, want empty", subs)
	}
	if err := store.Remove(testEndpoint()); !errors.Is(err, push.ErrNotRegistered) {
		t.Fatalf("Remove of unknown endpoint = %v, want ErrNotRegistered", err)
	}
}

func TestPushRegisterRemoveUnknownOnMissingFile(t *testing.T) {
	t.Parallel()
	store, path := newTestStore(t)
	if err := store.Remove(testEndpoint()); !errors.Is(err, push.ErrNotRegistered) {
		t.Fatalf("Remove on empty store = %v, want ErrNotRegistered", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("store file exists after refused Remove: stat err = %v", err)
	}
}

func TestPushRegisterRefusesDisallowedEndpoint(t *testing.T) {
	t.Parallel()
	endpoints := map[string]string{
		"http scheme":       "http://" + push.DefaultPushHost + "/" + testTopic,
		"other host":        "https://example.test/" + testTopic,
		"look-alike suffix": "https://ntfy.sh.example.test/" + testTopic,
		"look-alike prefix": "https://evil-ntfy.sh/" + testTopic,
		"userinfo trick":    "https://" + push.DefaultPushHost + "@example.test/" + testTopic,
		"empty host":        "https:///" + testTopic,
		"unparseable URL":   "https://[::1/" + testTopic,
		"no scheme":         push.DefaultPushHost + "/" + testTopic,
		"empty endpoint":    "",
		"trailing-dot host": "https://" + push.DefaultPushHost + "./" + testTopic,
	}
	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, path := newTestStore(t)
			p256dh, auth := testKeys(t)
			err := store.Add(push.Subscription{Endpoint: endpoint, P256dh: p256dh, Auth: auth})
			if !errors.Is(err, push.ErrEndpointNotAllowed) {
				t.Fatalf("Add(%q) = %v, want ErrEndpointNotAllowed", endpoint, err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("store file written for refused endpoint: stat err = %v", statErr)
			}
		})
	}
}

func TestPushRegisterRejectsInvalidKeys(t *testing.T) {
	t.Parallel()
	validP256dh, validAuth := testKeys(t)
	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	shortP256dh := base64.RawURLEncoding.EncodeToString(make([]byte, 10))
	offCurveP256dh := base64.RawURLEncoding.EncodeToString(offCurve)
	shortAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 15))
	longAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 17))
	cases := map[string]push.Subscription{
		"p256dh empty":      {Endpoint: testEndpoint(), P256dh: "", Auth: validAuth},
		"p256dh not base64": {Endpoint: testEndpoint(), P256dh: "!!!", Auth: validAuth},
		"p256dh short":      {Endpoint: testEndpoint(), P256dh: shortP256dh, Auth: validAuth},
		"p256dh off curve":  {Endpoint: testEndpoint(), P256dh: offCurveP256dh, Auth: validAuth},
		"auth empty":        {Endpoint: testEndpoint(), P256dh: validP256dh, Auth: ""},
		"auth not base64":   {Endpoint: testEndpoint(), P256dh: validP256dh, Auth: "!!!"},
		"auth too short":    {Endpoint: testEndpoint(), P256dh: validP256dh, Auth: shortAuth},
		"auth too long":     {Endpoint: testEndpoint(), P256dh: validP256dh, Auth: longAuth},
	}
	for name, sub := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store, path := newTestStore(t)
			if err := store.Add(sub); !errors.Is(err, push.ErrInvalidSubscription) {
				t.Fatalf("Add = %v, want ErrInvalidSubscription", err)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("store file written for invalid subscription: stat err = %v", statErr)
			}
		})
	}
}

func TestPushRegisterListOmitsDisallowedHost(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "push.json")
	original, err := push.NewStore(path, []string{"old.example.test"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	p256dh, auth := testKeys(t)
	sub := push.Subscription{Endpoint: "https://old.example.test/" + testTopic, P256dh: p256dh, Auth: auth}
	if err := original.Add(sub); err != nil {
		t.Fatalf("Add: %v", err)
	}

	narrowed, err := push.NewStore(path, []string{push.DefaultPushHost})
	if err != nil {
		t.Fatalf("NewStore narrowed: %v", err)
	}
	if subs := mustList(t, narrowed); len(subs) != 0 {
		t.Fatalf("List with host no longer allowed = %+v, want empty", subs)
	}
}

func TestPushRegisterListMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	store, _ := newTestStore(t)
	subs := mustList(t, store)
	if len(subs) != 0 {
		t.Fatalf("List on missing file = %+v, want empty", subs)
	}
}

func TestPushRegisterNewStoreValidates(t *testing.T) {
	t.Parallel()
	if _, err := push.NewStore("", []string{push.DefaultPushHost}); err == nil {
		t.Error("NewStore with empty path: want error")
	}
	if _, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), nil); err == nil {
		t.Error("NewStore with nil allow list: want error")
	}
	if _, err := push.NewStore(filepath.Join(t.TempDir(), "push.json"), []string{}); err == nil {
		t.Error("NewStore with empty allow list: want error")
	}
}

func TestPushRegisterDefaultStorePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		env    map[string]string
		want   string
		wantOK bool
	}{
		{
			name:   "XDG_STATE_HOME wins over HOME",
			env:    map[string]string{"XDG_STATE_HOME": "/synthetic/state", "HOME": "/synthetic/home"},
			want:   filepath.Join("/synthetic/state", "orrery", "push.json"),
			wantOK: true,
		},
		{
			name:   "HOME fallback",
			env:    map[string]string{"HOME": "/synthetic/home"},
			want:   filepath.Join("/synthetic/home", ".local", "state", "orrery", "push.json"),
			wantOK: true,
		},
		{
			name:   "neither set",
			env:    map[string]string{},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			getenv := func(key string) string { return tc.env[key] }
			got, err := push.DefaultStorePath(getenv)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("DefaultStorePath: %v", err)
				}
				if got != tc.want {
					t.Fatalf("DefaultStorePath = %q, want %q", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("DefaultStorePath = %q, want error", got)
			}
		})
	}
}
