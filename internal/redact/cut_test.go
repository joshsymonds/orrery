package redact

import (
	"strings"
	"testing"
)

// TestCutTail covers a tail whose start was cut bytewise: everything before its first newline is replaced, and the
// lines after it go through Text as they would alone. A row's secrets must not survive in the output, and the output
// must equal want.
func TestCutTail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{
			name:    "first line holding a token is replaced and later lines stay",
			in:      "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok\nsecond line\nthird line",
			secrets: []string{"ghp_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "[redacted]\nsecond line\nthird line",
		},
		{
			name:    "text with no newline is replaced alone",
			in:      "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghp_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "[redacted]",
		},
		{
			name:    "cut PEM body ending in END line keeps the plain line after it",
			in:      "EXAMPLEKEYBODY1\nEXAMPLEKEYBODY2\n-----END RSA PRIVATE KEY-----\nplain line",
			secrets: []string{"EXAMPLEKEYBODY1", "EXAMPLEKEYBODY2"},
			want:    "[redacted]\nplain line",
		},
		{
			name:    "later line credential is redacted as Text does",
			in:      "cut first line\nAuthorization: Bearer EXAMPLEBEARERTOKEN\nHost: example.test\n",
			secrets: []string{"EXAMPLEBEARERTOKEN"},
			want:    "[redacted]\nAuthorization: [redacted]\nHost: example.test\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CutTail(tt.in)
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Errorf("CutTail(%q) = %q, still contains %q", tt.in, got, secret)
				}
			}
			if got != tt.want {
				t.Errorf("CutTail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
