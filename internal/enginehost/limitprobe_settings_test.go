package enginehost

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestLimitsProbeSettingsEnv checks that a probe's --settings carries the gateway as ANTHROPIC_BASE_URL and its
// account pin as ANTHROPIC_CUSTOM_HEADERS. claude applies a settings file's env over its process environment, so a user
// settings file that sets either, as one setting ANTHROPIC_CUSTOM_HEADERS to another proxy's key does, would otherwise
// send the probe unpinned on the caller's own login.
func TestLimitsProbeSettingsEnv(t *testing.T) {
	now := idleProbeStart()
	accounts := idleProbeAccounts{accounts: []store.Account{{ID: idleProbeNone}}}
	rec := &idleProbeRecorder{}
	cfg := idleProbeConfig(t, accounts, idleProbeReadings{}, func() time.Time { return now }, rec)
	NewLimitProber(cfg).Pass(t.Context())
	if len(rec.cmds) != 1 {
		t.Fatalf("pass ran %d probes, want 1", len(rec.cmds))
	}
	raw, ok := idleProbeFlag(rec.cmds[0].Args, "--settings")
	if !ok {
		t.Fatalf("probe args %q have no --settings", rec.cmds[0].Args)
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		t.Fatalf("decode --settings %q: %v", raw, err)
	}
	if got := settings.Env["ANTHROPIC_BASE_URL"]; got != idleProbeGateway {
		t.Errorf("--settings env ANTHROPIC_BASE_URL = %q, want %q", got, idleProbeGateway)
	}
	if got, want := settings.Env["ANTHROPIC_CUSTOM_HEADERS"], "X-Orrery-Account: "+idleProbeNone; got != want {
		t.Errorf("--settings env ANTHROPIC_CUSTOM_HEADERS = %q, want %q", got, want)
	}
}
