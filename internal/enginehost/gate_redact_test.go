package enginehost

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// gateRedactRow pairs a line a gate prints with the line its tail must keep.
type gateRedactRow struct{ in, want string }

// gateRedactRows returns one row per listed credential shape, each with a synthetic, obviously fake credential. A
// PEM block is one row too, and its want is the placeholder for the whole block.
func gateRedactRows() []gateRedactRow {
	return []gateRedactRow{
		{"Authorization: Bearer EXAMPLEBEARERTOKEN", "Authorization: [redacted]"},
		{"Proxy-Authorization: Basic EXAMPLEBASICCRED==", "Proxy-Authorization: [redacted]"},
		{"X-Api-Key: EXAMPLEAPIKEY0001", "X-Api-Key: [redacted]"},
		{"OPENAI_KEY=sk-EXAMPLEKEY0123456789abcd", "OPENAI_KEY=[redacted]"},
		{"ANTHROPIC_API_KEY=sk-ant-api03-EXAMPLEKEY0123456789ab", "ANTHROPIC_API_KEY=[redacted]"},
		{"token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok", "token [redacted] ok"},
		{"token gho_EXAMPLE0123456789abcdefABCDEF0123 ok", "token [redacted] ok"},
		{"token ghu_EXAMPLE0123456789abcdefABCDEF0123 ok", "token [redacted] ok"},
		{"token ghs_EXAMPLE0123456789abcdefABCDEF0123 ok", "token [redacted] ok"},
		{"token ghr_EXAMPLE0123456789abcdefABCDEF0123 ok", "token [redacted] ok"},
		{"GITHUB_TOKEN=github_pat_EXAMPLE0123456789_abcdef", "GITHUB_TOKEN=[redacted]"},
		{"aws_access_key_id = AKIAEXAMPLEEXAMPLE12", "aws_access_key_id = [redacted]"},
		{"aws_access_key_id = ASIAEXAMPLEEXAMPLE34", "aws_access_key_id = [redacted]"},
		{"aws_secret_access_key = EXAMPLESECRETKEY/0123456789+abcdefghijk", "aws_secret_access_key = [redacted]"},
		{"SLACK_TOKEN=xoxb-EXAMPLE-0123456789-abcdef", "SLACK_TOKEN=[redacted]"},
		{
			"-----BEGIN RSA PRIVATE KEY-----\nEXAMPLEKEYBODY\n-----END RSA PRIVATE KEY-----",
			"[redacted]",
		},
	}
}

// gateRedactSecrets returns the credentials of gateRedactRows. None may survive in a stored, shown, judged or sent text.
func gateRedactSecrets() []string {
	return []string{
		"EXAMPLEBEARERTOKEN",
		"EXAMPLEBASICCRED==",
		"EXAMPLEAPIKEY0001",
		"sk-EXAMPLEKEY0123456789abcd",
		"sk-ant-api03-EXAMPLEKEY0123456789ab",
		"ghp_EXAMPLE0123456789abcdefABCDEF0123",
		"gho_EXAMPLE0123456789abcdefABCDEF0123",
		"ghu_EXAMPLE0123456789abcdefABCDEF0123",
		"ghs_EXAMPLE0123456789abcdefABCDEF0123",
		"ghr_EXAMPLE0123456789abcdefABCDEF0123",
		"github_pat_EXAMPLE0123456789_abcdef",
		"AKIAEXAMPLEEXAMPLE12",
		"ASIAEXAMPLEEXAMPLE34",
		"EXAMPLESECRETKEY/0123456789+abcdefghijk",
		"xoxb-EXAMPLE-0123456789-abcdef",
		"EXAMPLEKEYBODY",
	}
}

// gateRedactStream returns the text a gate prints to one stream, every row's input then plain, and the text the tail
// must keep for it.
func gateRedactStream(plain string) (in, want string) {
	rows := gateRedactRows()
	inputs := make([]string, 0, len(rows)+1)
	wants := make([]string, 0, len(rows)+1)
	for _, row := range rows {
		inputs = append(inputs, row.in)
		wants = append(wants, row.want)
	}
	inputs = append(inputs, plain)
	wants = append(wants, plain)
	return strings.Join(inputs, "\n") + "\n", strings.Join(wants, "\n") + "\n"
}

// gateRedactHeredoc returns a shell fragment that writes text to stdout, or to stderr when toStderr is set. The heredoc is
// quoted, so no byte of text is expanded.
func gateRedactHeredoc(text string, toStderr bool) string {
	redirect := ""
	if toStderr {
		redirect = " >&2"
	}
	return "cat <<'GATEREDACT'" + redirect + "\n" + text + "GATEREDACT\n"
}

// gateRedactAssertNoSecret fails the test once for each credential of gateRedactSecrets that text holds.
func gateRedactAssertNoSecret(t *testing.T, label, text string) {
	t.Helper()
	for _, secret := range gateRedactSecrets() {
		if strings.Contains(text, secret) {
			t.Errorf("%s holds the secret %q:\n%s", label, secret, text)
		}
	}
}

// TestGateRedactStepGateStoresSendsBackAndEndsWithNoSecret runs a writing step whose gate prints every listed shape on both
// streams and exits 1. The stored check, the send-back, the checks_failed reason and the API step keep no secret, and keep
// each stream's tail redacted, with the plain line byte for byte.
func TestGateRedactStepGateStoresSendsBackAndEndsWithNoSecret(t *testing.T) {
	t.Parallel()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	stdoutIn, stdoutWant := gateRedactStream("stdout plain: 12 packages ok")
	stderrIn, stderrWant := gateRedactStream("stderr plain: 0 warnings")
	gate := stepDoneScript(t, gateRedactHeredoc(stdoutIn, false)+gateRedactHeredoc(stderrIn, true)+"exit 1")
	f := newStepDoneFixture(t, stepDoneWriting(dir, start, []string{gate}, time.Minute, &fakeGatePools{}))
	_, done := f.start(t)

	for range stepLoopMaxContinues + 1 {
		f.run.Return(stepDoneReturn(commit))
	}
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	gateCheck := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopGateName)
	if gateCheck.Stdout != stdoutWant || gateCheck.Stderr != stderrWant {
		t.Errorf("stored gate check stdout %q stderr %q, want %q and %q",
			gateCheck.Stdout, gateCheck.Stderr, stdoutWant, stderrWant)
	}
	gateRedactAssertNoSecret(t, "stored gate check", gateCheck.Stdout+gateCheck.Stderr)

	continued := f.sessions.continuedTexts()
	if len(continued) == 0 {
		t.Fatal("Continue was never called, want the failed gate sent back")
	}
	for _, want := range []string{
		"stdout tail: " + strings.TrimSpace(stdoutWant),
		"stderr tail: " + strings.TrimSpace(stderrWant),
	} {
		if !strings.Contains(continued[0], want) {
			t.Errorf("send-back text does not carry %q:\n%s", want, continued[0])
		}
		if !strings.Contains(row.Reason, want) {
			t.Errorf("reason does not carry %q:\n%s", want, row.Reason)
		}
	}
	gateRedactAssertNoSecret(t, "send-back text", continued[0])
	gateRedactAssertNoSecret(t, "checks_failed reason", row.Reason)

	step, err := stepMessage(row, false)
	if err != nil {
		t.Fatalf("stepMessage: %v", err)
	}
	data, err := protojson.Marshal(step)
	if err != nil {
		t.Fatalf("marshal step: %v", err)
	}
	gateRedactAssertNoSecret(t, "API step", string(data))
}

// TestGateRedactJudgeInputHoldsNoSecret runs a writing step whose gate prints every listed shape on both streams and passes.
// The judge input carries each stream's tail redacted, and no secret.
func TestGateRedactJudgeInputHoldsNoSecret(t *testing.T) {
	t.Parallel()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	stdoutIn, stdoutWant := gateRedactStream("stdout plain: 12 packages ok")
	stderrIn, stderrWant := gateRedactStream("stderr plain: 0 warnings")
	gate := stepDoneScript(t, gateRedactHeredoc(stdoutIn, false)+gateRedactHeredoc(stderrIn, true))
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "complete")}}
	f := newJudgeLoopFixture(t, haiku, stepDoneWriting(dir, start, []string{gate}, time.Minute, &fakeGatePools{}))
	_, done := f.start(t)

	f.run.Return(stepDoneReturn(commit))
	waitRunEnd(t, done)

	inputs := haiku.callInputs()
	if len(inputs) != 1 {
		t.Fatalf("judge called %d times, want 1", len(inputs))
	}
	for _, want := range []string{"<gate_stdout_tail>\n" + stdoutWant, "<gate_stderr_tail>\n" + stderrWant} {
		if !strings.Contains(inputs[0], want) {
			t.Errorf("judge input does not contain %q:\n%s", want, inputs[0])
		}
	}
	gateRedactAssertNoSecret(t, "judge input", inputs[0])
}

// TestGateRedactCaptureLogsHoldNoSecret runs an evidence capture that prints every listed shape on both streams. Its two
// stored blobs keep each tail redacted, and the plain line byte for byte.
func TestGateRedactCaptureLogsHoldNoSecret(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	stdoutIn, stdoutWant := gateRedactStream("capture stdout plain: 3 files")
	stderrIn, stderrWant := gateRedactStream("capture stderr plain: no warnings")
	script := gateRedactHeredoc(stdoutIn, false) + gateRedactHeredoc(stderrIn, true)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", script}, Limit: 60}
	got, err := c.run(t.Context(), repo, head, "redact", item, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.NotCaptured != "" || got.Exit != 0 {
		t.Fatalf("capture = %+v, want exit 0 and captured", got)
	}
	if got.LogBytes != int64(len(stdoutWant)) || got.LogHash != evidenceCaptureHash(stdoutWant) {
		t.Fatalf("log = %d bytes hash %s, want the redacted stdout (%d bytes)",
			got.LogBytes, got.LogHash, len(stdoutWant))
	}
	if got.StderrBytes != int64(len(stderrWant)) || got.StderrHash != evidenceCaptureHash(stderrWant) {
		t.Fatalf("stderr log = %d bytes hash %s, want the redacted stderr (%d bytes)",
			got.StderrBytes, got.StderrHash, len(stderrWant))
	}
	for _, blob := range []struct{ hash, want string }{{got.LogHash, stdoutWant}, {got.StderrHash, stderrWant}} {
		data, readErr := c.blobs.read(blob.hash + ".log")
		if readErr != nil {
			t.Fatalf("read blob %s: %v", blob.hash, readErr)
		}
		if string(data) != blob.want {
			t.Errorf("blob %s = %q, want %q", blob.hash, data, blob.want)
		}
		gateRedactAssertNoSecret(t, "capture blob "+blob.hash, string(data))
	}
}

// TestGateRedactPEMBlockCutAtTailStartIsRedactedUpToItsEnd runs a gate whose PEM private key block is longer than the 8 KiB
// tail, so the tail begins inside the block with its BEGIN line beyond the tail. The tail is redacted from its start through
// the block's END line, and the plain line after it is kept.
func TestGateRedactPEMBlockCutAtTailStartIsRedactedUpToItsEnd(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("EXAMPLEKEYBODYLINE0123456789abcdefghij\n", 300)
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + body + "-----END RSA PRIVATE KEY-----\nplain after\n"
	result, err := runGrantedGate(t.Context(), &fakeGatePools{}, gateGrantRequest(), gateRun{
		Host:    gateTestHost(),
		Argv:    []string{"/bin/sh", "-c", gateRedactHeredoc(pem, false)},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv(),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if want := "[redacted]\nplain after\n"; result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
	gateRedactAssertNoSecret(t, "PEM tail", result.Stdout)
}

// gateStraddleToken is a synthetic token that the 8 KiB cut of gateStraddleText falls inside.
const gateStraddleToken = "ghp_EXAMPLE0123456789abcdefABCDEF0123"

// gateStraddleText returns the output of a gate whose newest 8 KiB begin inside gateStraddleToken, which opens its first
// line: the line the token is on, then 816 lines of ten bytes. The cut falls inside the token, so the token's first
// bytes are discarded and its last bytes start the tail.
func gateStraddleText(t *testing.T) string {
	t.Helper()
	text := "token " + gateStraddleToken + " ok\n" + gateTailLines(0, 816)
	cut := len(text) - 8*1024
	start := strings.Index(text, gateStraddleToken)
	if cut <= start || cut >= start+len(gateStraddleToken) {
		t.Fatalf("the 8 KiB cut at byte %d does not fall inside the token at byte %d", cut, start)
	}
	return text
}

// TestGateRedactTailCutInsideTokenDropsItsLineOnBothStreams runs a gate that writes gateStraddleText to stdout and to
// stderr, so the newest 8 KiB of each begin inside a token. Each tail begins with the placeholder, holds no fragment of the
// token, and keeps every later line.
func TestGateRedactTailCutInsideTokenDropsItsLineOnBothStreams(t *testing.T) {
	t.Parallel()
	text := gateStraddleText(t)
	result := runGateScript(t, gateRedactHeredoc(text, false)+gateRedactHeredoc(text, true), 0)
	want := "[redacted]\n" + gateTailLines(0, 816)
	for _, stream := range []struct{ name, got string }{{"Stdout", result.Stdout}, {"Stderr", result.Stderr}} {
		if stream.got != want {
			t.Errorf("%s = %d bytes, want the placeholder and the 816 later lines (%d bytes)",
				stream.name, len(stream.got), len(want))
		}
	}
}

// TestGateRedactTailWithNoNewlineIsPlaceholder runs a gate that writes more than 8 KiB with no newline. The tail is one
// cut line, so it is the placeholder alone.
func TestGateRedactTailWithNoNewlineIsPlaceholder(t *testing.T) {
	t.Parallel()
	result := runGateScript(t, `head -c 20000 /dev/zero | tr '\0' a`, 0)
	if want := "[redacted]"; result.Stdout != want {
		t.Fatalf("Stdout = %d bytes, want %q", len(result.Stdout), want)
	}
}

// TestGateRedactShorterTailIsUnchanged runs a gate whose output is shorter than its 8 KiB tail. Nothing is cut, so the
// first line is kept, and only its credential is redacted.
func TestGateRedactShorterTailIsUnchanged(t *testing.T) {
	t.Parallel()
	text := "token " + gateStraddleToken + " ok\nplain line\n"
	result := runGateScript(t, gateRedactHeredoc(text, false), 0)
	if want := "token [redacted] ok\nplain line\n"; result.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", result.Stdout, want)
	}
}

// TestGateRedactCaptureOverfillingLogTailDropsItsFirstLine runs an evidence capture that writes a line longer than its 1 MiB
// log tail, then a short line. The stored log is the placeholder and the short line, and none of the long line.
func TestGateRedactCaptureOverfillingLogTailDropsItsFirstLine(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	script := fmt.Sprintf(`head -c %d /dev/zero | tr '\0' a; printf '\nline-after\n'`, evidenceCaptureLogTail+100)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", script}, Limit: 60}
	got, err := c.run(t.Context(), repo, head, "overfill", item, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.NotCaptured != "" || got.Exit != 0 {
		t.Fatalf("capture = %+v, want exit 0 and captured", got)
	}
	const want = "[redacted]\nline-after\n"
	if got.LogBytes != int64(len(want)) || got.LogHash != evidenceCaptureHash(want) {
		t.Fatalf("log = %d bytes hash %s, want the placeholder and the short line (%d bytes)",
			got.LogBytes, got.LogHash, len(want))
	}
	data, readErr := c.blobs.read(got.LogHash + ".log")
	if readErr != nil {
		t.Fatalf("read blob %s: %v", got.LogHash, readErr)
	}
	if string(data) != want {
		t.Errorf("blob %s = %q, want %q", got.LogHash, data, want)
	}
}
