// Package engine_test fails when a file under internal/engine/ holds a private detail: a home path,
// a tailnet host name, a network or email address, an account or session id, or a token.
package engine_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	ruleHome  = "home path other than /home/user"
	ruleTSNet = "tailnet host name"
	ruleIPv4  = "IPv4 literal outside 127.0.0.0/8 and 192.0.2.0/24"
	ruleEmail = "email address outside example.test"
	ruleUUID  = "UUID-shaped id other than the fixed form"
	ruleAnt   = "sk-ant- token"
	ruleMsg   = "msg_ token"
)

// rule names one private-detail shape and returns the leaking matches on a line.
type rule struct {
	name  string
	leaks func(line string) []string
}

// finding is one line of one file that a rule flagged.
type finding struct {
	path string
	line int
	rule string
}

func rules() []rule {
	return []rule{
		matchRule(ruleHome, `/(?:home|Users)/[^/\s"'\x60]+`, func(m string) bool { return m == "/home/user" }),
		matchRule(ruleTSNet, `(?i)\.ts\.net\b`, admitNone),
		{name: ruleIPv4, leaks: ipv4Leaks()},
		matchRule(ruleEmail, `[-A-Za-z0-9._%+]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, admitExampleTest),
		{name: ruleUUID, leaks: uuidLeaks()},
		matchRule(ruleAnt, `sk-ant-[A-Za-z0-9_-]+`, admitNone),
		matchRule(ruleMsg, `msg_[A-Za-z0-9_]*[0-9][A-Za-z0-9]{6,}`, admitNone),
	}
}

// matchRule builds a rule that flags every match of pattern that admit rejects.
func matchRule(name, pattern string, admit func(match string) bool) rule {
	re := regexp.MustCompile(pattern)
	return rule{name: name, leaks: func(line string) []string {
		var out []string
		for _, m := range re.FindAllString(line, -1) {
			if !admit(m) {
				out = append(out, m)
			}
		}
		return out
	}}
}

func admitNone(string) bool { return false }

func admitExampleTest(match string) bool {
	domain := match[strings.LastIndex(match, "@")+1:]
	return domain == "example.test" || strings.HasSuffix(domain, ".example.test")
}

const (
	mask8       = 0xff000000
	mask24      = 0xffffff00
	loopbackNet = 0x7f000000 // 127.0.0.0/8
	docNet      = 0xc0000200 // 192.0.2.0/24
)

// ipv4Leaks returns a detector that flags each dotted quad outside the loopback and documentation
// networks. The pattern needs no word boundary, so an address right after a letter or an escape
// such as \n is still found. Octets are parsed as numbers, so leading zeros cannot hide an address.
func ipv4Leaks() func(line string) []string {
	re := regexp.MustCompile(`(?:\d{1,3}\.){3}\d{1,3}`)
	return func(line string) []string {
		var out []string
		for _, m := range re.FindAllString(line, -1) {
			v, ok := ipv4Value(m)
			if !ok {
				continue
			}
			if v&mask8 == loopbackNet || v&mask24 == docNet {
				continue
			}
			out = append(out, m)
		}
		return out
	}
}

// ipv4Value packs a dotted quad into a uint32. It reports false when an octet exceeds 255.
func ipv4Value(m string) (uint32, bool) {
	var v uint32
	for _, part := range strings.Split(m, ".") {
		n, err := strconv.ParseUint(part, 10, 8)
		if err != nil {
			return 0, false
		}
		v = v<<8 | uint32(n)
	}
	return v, true
}

const uuidPattern = `[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`

// uuidLeaks returns a detector that flags each UUID-shaped id that is not the fixed form. A match
// embedded in a longer hex run is a different id, so it is flagged even when its first 36
// characters are the fixed form.
func uuidLeaks() func(line string) []string {
	re := regexp.MustCompile(uuidPattern)
	fixed := regexp.MustCompile(`^00000000-0000-4000-8000-00000000000[0-9]$`)
	return func(line string) []string {
		var out []string
		for _, loc := range re.FindAllStringIndex(line, -1) {
			m := line[loc[0]:loc[1]]
			embedded := (loc[0] > 0 && isHex(line[loc[0]-1])) || (loc[1] < len(line) && isHex(line[loc[1]]))
			if embedded || !fixed.MatchString(m) {
				out = append(out, m)
			}
		}
		return out
	}
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// scan walks every regular file under root, any extension and testdata included, and returns each
// line that a rule flags.
func scan(root string, rs []rule) ([]finding, error) {
	var out []finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, r := range rs {
				if len(r.leaks(line)) > 0 {
					out = append(out, finding{path: path, line: i + 1, rule: r.name})
				}
			}
		}
		return nil
	})
	return out, err
}

// cat joins sample fragments at runtime so that no sample is a literal this file would match.
func cat(parts ...string) string { return strings.Join(parts, "") }

func ruleNamed(t *testing.T, name string) rule {
	t.Helper()
	for _, r := range rules() {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no rule named %q", name)
	return rule{}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEngineHoldsNoPrivateDetail(t *testing.T) {
	findings, err := scan(".", rules())
	if err != nil {
		t.Fatalf("walk internal/engine: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s:%d: %s", f.path, f.line, f.rule)
	}
}

func TestRulesFireOnPlantedSamplesAndAdmitAllowedForms(t *testing.T) {
	cases := []struct {
		rule     string
		planted  []string
		admitted []string
	}{
		{
			rule:     ruleHome,
			planted:  []string{cat("/ho", "me/alice/.config"), cat("/Us", "ers/bob/notes"), cat("/ho", "me/username")},
			admitted: []string{"/home/user", "/home/user/.config/orrery/state.json"},
		},
		{
			rule:     ruleTSNet,
			planted:  []string{cat("node.example-tailnet.", "ts.net"), cat("host.example-tailnet.ts", ".net")},
			admitted: []string{"host.example.test"},
		},
		{
			rule: ruleIPv4,
			planted: []string{
				cat("198.51.", "100.7"),
				cat("203.0.", "113.9"),
				cat("192.0.3", ".1"),
				cat("010.0.0", ".1"),
				cat(`a\n198.51.`, "100.7"),
				cat("ip198.51.", "100.7"),
			},
			admitted: []string{"127.0.0.1", "127.255.0.9", "192.0.2.10", "192.0.2.255"},
		},
		{
			rule:     ruleEmail,
			planted:  []string{cat("ops", "@", "mx.example", ".net"), cat("user", "@", "example.test", ".evil")},
			admitted: []string{"user@example.test", "ops@lab.example.test"},
		},
		{
			rule: ruleUUID,
			planted: []string{
				cat("abcdef01-2345-4678-9abc-", "def012345678"),
				cat("00000000-0000-4000-8000-", "000000000010"),
				cat("00000000-0000-4000-8000-", "0000000000001"),
			},
			admitted: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000009"},
		},
		{
			rule:     ruleAnt,
			planted:  []string{cat("sk-", "ant-api03-EXAMPLE_token")},
			admitted: []string{"sk-antelope", "sk-ant"},
		},
		{
			rule:     ruleMsg,
			planted:  []string{cat("msg", "_01AAAAAAAAAAAAAAAAAAAAAA"), cat("msg", "_bdrk_01AAAAAAAAAA")},
			admitted: []string{"msg_received", "msg_count"},
		},
	}
	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			r := ruleNamed(t, c.rule)
			for _, s := range c.planted {
				if len(r.leaks(s)) == 0 {
					t.Errorf("planted sample %q not flagged", s)
				}
			}
			for _, s := range c.admitted {
				if got := r.leaks(s); len(got) > 0 {
					t.Errorf("allowed form %q flagged: %v", s, got)
				}
			}
		})
	}
}

func TestScanReportsEveryRegularFileByLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.md"), "clean line\n"+cat("/ho", "me/alice/notes")+"\n")
	writeFile(t, filepath.Join(dir, "testdata", "fixture.golden"), "a\nb\n"+cat("198.51.", "100.7")+"\n")
	writeFile(t, filepath.Join(dir, "clean.go"), "package clean\n")

	got, err := scan(dir, rules())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []finding{
		{path: filepath.Join(dir, "notes.md"), line: 2, rule: ruleHome},
		{path: filepath.Join(dir, "testdata", "fixture.golden"), line: 3, rule: ruleIPv4},
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}
}
