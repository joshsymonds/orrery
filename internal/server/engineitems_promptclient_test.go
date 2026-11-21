package server

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestPromptClientPromptItemCarriesTheSendingClient checks that a session's prompt item carries the client of the
// request that sent its prompt: orrery-android for a prompt sent from the phone, and orrery web, which is no phone
// client, for a prompt sent from orrery web.
func TestPromptClientPromptItemCarriesTheSendingClient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "prompt sent from the phone carries orrery-android",
			payload: `{"text":"fix the build","promptId":"p1","client":"orrery-android"}`,
			want:    "orrery-android",
		},
		{
			name:    "prompt sent from orrery web carries orrery web, not the phone client",
			payload: `{"text":"fix the build","promptId":"p2","client":"orrery web"}`,
			want:    "orrery web",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := sessionItem(store.EngineEvent{
				Seq: 1, Kind: store.EventEnginePrompt, At: 100, Payload: tt.payload,
			})
			if err != nil || !ok {
				t.Fatalf("sessionItem ok = %v, err = %v, want a prompt item", ok, err)
			}
			if got.GetKind() != "prompt" || got.GetClient() != tt.want {
				t.Fatalf("item kind %q client %q, want kind %q client %q",
					got.GetKind(), got.GetClient(), "prompt", tt.want)
			}
		})
	}
}
