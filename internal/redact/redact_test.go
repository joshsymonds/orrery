package redact

import (
	"strings"
	"testing"
)

// TestText covers each listed credential shape with synthetic, obviously fake credentials, plus the plain output
// that must come back byte for byte. A row's secrets must not survive in the output, and the output must equal want.
func TestText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		secrets []string
		want    string
	}{
		{
			name:    "authorization header keeps its name and loses its value to end of line",
			in:      "GET /v1 HTTP/1.1\nAuthorization: Bearer EXAMPLEBEARERTOKEN\nHost: example.test\n",
			secrets: []string{"EXAMPLEBEARERTOKEN"},
			want:    "GET /v1 HTTP/1.1\nAuthorization: [redacted]\nHost: example.test\n",
		},
		{
			name:    "proxy-authorization header matched case-insensitively",
			in:      "proxy-authorization: Basic EXAMPLEBASICCRED==\n",
			secrets: []string{"EXAMPLEBASICCRED=="},
			want:    "proxy-authorization: [redacted]\n",
		},
		{
			name:    "x-api-key header keeps its name",
			in:      "X-Api-Key: EXAMPLEAPIKEY0001\n",
			secrets: []string{"EXAMPLEAPIKEY0001"},
			want:    "X-Api-Key: [redacted]\n",
		},
		{
			name:    "sk- key",
			in:      "OPENAI_KEY=sk-EXAMPLEKEY0123456789abcd end",
			secrets: []string{"sk-EXAMPLEKEY0123456789abcd"},
			want:    "OPENAI_KEY=[redacted] end",
		},
		{
			name:    "sk-ant key",
			in:      "ANTHROPIC_API_KEY=sk-ant-api03-EXAMPLEKEY0123456789ab",
			secrets: []string{"sk-ant-api03-EXAMPLEKEY0123456789ab"},
			want:    "ANTHROPIC_API_KEY=[redacted]",
		},
		{
			name:    "ghp token",
			in:      "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghp_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "token [redacted] ok",
		},
		{
			name:    "gho token",
			in:      "token gho_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"gho_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "token [redacted] ok",
		},
		{
			name:    "ghu token",
			in:      "token ghu_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghu_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "token [redacted] ok",
		},
		{
			name:    "ghs token",
			in:      "token ghs_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghs_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "token [redacted] ok",
		},
		{
			name:    "ghr token",
			in:      "token ghr_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghr_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "token [redacted] ok",
		},
		{
			name:    "github fine-grained token",
			in:      "GITHUB_TOKEN=github_pat_EXAMPLE0123456789_abcdef",
			secrets: []string{"github_pat_EXAMPLE0123456789_abcdef"},
			want:    "GITHUB_TOKEN=[redacted]",
		},
		{
			name:    "AKIA key id",
			in:      "aws_access_key_id = AKIAEXAMPLEEXAMPLE12\n",
			secrets: []string{"AKIAEXAMPLEEXAMPLE12"},
			want:    "aws_access_key_id = [redacted]\n",
		},
		{
			name:    "ASIA key id",
			in:      "aws_access_key_id = ASIAEXAMPLEEXAMPLE34\n",
			secrets: []string{"ASIAEXAMPLEEXAMPLE34"},
			want:    "aws_access_key_id = [redacted]\n",
		},
		{
			name:    "aws_secret_access_key keeps its name and separator",
			in:      "aws_secret_access_key = EXAMPLESECRETKEY/0123456789+abcdefghijk\n",
			secrets: []string{"EXAMPLESECRETKEY/0123456789+abcdefghijk"},
			want:    "aws_secret_access_key = [redacted]\n",
		},
		{
			name:    "quoted aws_secret_access_key in JSON keeps its name, separator and quotes",
			in:      `{"aws_secret_access_key": "EXAMPLESECRETKEY0123"}`,
			secrets: []string{"EXAMPLESECRETKEY0123"},
			want:    `{"aws_secret_access_key": "[redacted]"}`,
		},
		{
			name:    "slack xox token",
			in:      "SLACK_TOKEN=xoxb-EXAMPLE-0123456789-abcdef",
			secrets: []string{"xoxb-EXAMPLE-0123456789-abcdef"},
			want:    "SLACK_TOKEN=[redacted]",
		},
		{
			name:    "PEM RSA private key block replaced whole",
			in:      "before\n-----BEGIN RSA PRIVATE KEY-----\nEXAMPLEKEYBODY\n-----END RSA PRIVATE KEY-----\nafter\n",
			secrets: []string{"EXAMPLEKEYBODY"},
			want:    "before\n[redacted]\nafter\n",
		},
		{
			name:    "PEM unprefixed private key block replaced whole",
			in:      "-----BEGIN PRIVATE KEY-----\nEXAMPLEKEYBODY\n-----END PRIVATE KEY-----",
			secrets: []string{"EXAMPLEKEYBODY"},
			want:    "[redacted]",
		},
		{
			name:    "PEM block cut at end replaced from BEGIN to end of text",
			in:      "log start\n-----BEGIN OPENSSH PRIVATE KEY-----\nEXAMPLEBODYLINE1\nEXAMPLEBODYLINE2",
			secrets: []string{"EXAMPLEBODYLINE1", "EXAMPLEBODYLINE2"},
			want:    "log start\n[redacted]",
		},
		{
			name:    "PEM block cut at start replaced from start through END",
			in:      "EXAMPLEBODYLINE1\nEXAMPLEBODYLINE2\n-----END RSA PRIVATE KEY-----\nnext line",
			secrets: []string{"EXAMPLEBODYLINE1", "EXAMPLEBODYLINE2"},
			want:    "[redacted]\nnext line",
		},
		{
			name:    "gh token after an underscore",
			in:      "MY_TOKEN_ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			secrets: []string{"ghp_EXAMPLE0123456789abcdefABCDEF0123"},
			want:    "MY_TOKEN_[redacted] ok",
		},
		{
			name:    "github fine-grained token after an underscore",
			in:      "MY_github_pat_EXAMPLE0123456789_abcdef",
			secrets: []string{"github_pat_EXAMPLE0123456789_abcdef"},
			want:    "MY_[redacted]",
		},
		{
			name:    "AKIA key id after an underscore",
			in:      "KEY_AKIAEXAMPLEEXAMPLE12\n",
			secrets: []string{"AKIAEXAMPLEEXAMPLE12"},
			want:    "KEY_[redacted]\n",
		},
		{
			name:    "sk- key after an underscore",
			in:      "MY_TOKEN_sk-EXAMPLEKEY0123456789abcd",
			secrets: []string{"sk-EXAMPLEKEY0123456789abcd"},
			want:    "MY_TOKEN_[redacted]",
		},
		{
			name:    "slack xox token after an underscore",
			in:      "SLACK_xoxb-EXAMPLE-0123456789-abcdef",
			secrets: []string{"xoxb-EXAMPLE-0123456789-abcdef"},
			want:    "SLACK_[redacted]",
		},
		{
			name:    "authorization header name after an underscore",
			in:      "MY_Authorization: Bearer EXAMPLEBEARERTOKEN\n",
			secrets: []string{"EXAMPLEBEARERTOKEN"},
			want:    "MY_Authorization: [redacted]\n",
		},
		{
			name: "build and test lines",
			in: "go: downloading example.test v1.0.0\nok  \texample.test/pkg\t0.012s\n" +
				"--- PASS: TestThing (0.00s)\n",
			want: "go: downloading example.test v1.0.0\nok  \texample.test/pkg\t0.012s\n" +
				"--- PASS: TestThing (0.00s)\n",
		},
		{
			name: "sk-learn is not a key",
			in:   "sk-learn is a library",
			want: "sk-learn is a library",
		},
		{
			name: "sk- inside a longer word is not a key",
			in:   "risk-management-policy-documents-review",
			want: "risk-management-policy-documents-review",
		},
		{
			name: "task-123 is not a key",
			in:   "task-123 finished",
			want: "task-123 finished",
		},
		{
			name: "bare AKIA is not a key id",
			in:   "AKIA alone",
			want: "AKIA alone",
		},
		{
			name: "ghp_ alone is not a token",
			in:   "ghp_ alone",
			want: "ghp_ alone",
		},
		{
			name: "CERTIFICATE block is untouched",
			in:   "-----BEGIN CERTIFICATE-----\nEXAMPLECERTBODY\n-----END CERTIFICATE-----\n",
			want: "-----BEGIN CERTIFICATE-----\nEXAMPLECERTBODY\n-----END CERTIFICATE-----\n",
		},
		{
			name: "multi-line text is untouched",
			in:   "first line\nsecond line\n\nthird line",
			want: "first line\nsecond line\n\nthird line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Text(tt.in)
			for _, secret := range tt.secrets {
				if strings.Contains(got, secret) {
					t.Errorf("Text(%q) = %q, still contains %q", tt.in, got, secret)
				}
			}
			if got != tt.want {
				t.Errorf("Text(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
