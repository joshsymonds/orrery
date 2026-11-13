package gates

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const testPath = "gates.toml"

// gateKeys is one valid [gates.alpha] table body, one key per line.
func gateKeys() []string {
	return []string{
		`argv = ["/opt/tools/run", "-c", "check"]`,
		`limit = 1800`,
		`needs = { gate = 1, builds = 2 }`,
	}
}

// keyed returns keys with the line for key replaced by line, or dropped when line is empty.
func keyed(keys []string, key, line string) []string {
	var out []string
	for _, k := range keys {
		if before, _, _ := strings.Cut(k, " "); before == key {
			if line != "" {
				out = append(out, line)
			}
			continue
		}
		out = append(out, k)
	}
	return out
}

// withKey returns gateKeys with the key's line replaced by line.
func withKey(key, line string) []string {
	return keyed(gateKeys(), key, line)
}

// table renders one TOML table with the given section header and key lines.
func table(section string, keys ...string) string {
	return "[" + section + "]\n" + strings.Join(keys, "\n") + "\n"
}

// file renders a gates file: the [gates] header, then the tables.
func file(tables ...string) string {
	return "[gates]\n" + strings.Join(tables, "")
}

// parser is one entry point. A gate table reads the same through either.
type parser struct {
	name  string
	parse func(string, []byte) ([]Gate, error)
}

func parsers() []parser {
	return []parser{{"ParseFile", ParseFile}, {"ParseProject", ParseProject}}
}

type refusal struct {
	name string
	text string
	want []string
}

func gateRefusals() []refusal {
	return []refusal{
		{"missing argv", file(table("gates.alpha", keyed(gateKeys(), "argv", "")...)), []string{"alpha", "argv"}},
		{"missing limit", file(table("gates.alpha", keyed(gateKeys(), "limit", "")...)), []string{"alpha", "limit"}},
		{"empty argv", file(table("gates.alpha", withKey("argv", `argv = []`)...)), []string{"alpha", "argv"}},
		{"empty element", file(table("gates.alpha", withKey("argv", `argv = ["/opt/x", ""]`)...)),
			[]string{"alpha", "argv"}},
		{"argv[0] without slash", file(table("gates.alpha", withKey("argv", `argv = ["run", "-c"]`)...)),
			[]string{"alpha", "argv"}},
		{"argv[0] just", file(table("gates.alpha", withKey("argv", `argv = ["just", "check"]`)...)),
			[]string{"alpha", "just"}},
		{"limit zero", file(table("gates.alpha", withKey("limit", "limit = 0")...)), []string{"alpha", "limit"}},
		{"limit negative", file(table("gates.alpha", withKey("limit", "limit = -1")...)), []string{"alpha", "limit"}},
		{"limit overflows", file(table("gates.alpha", withKey("limit", "limit = 9223372037")...)),
			[]string{"alpha", "limit"}},
		{"count zero", file(table("gates.alpha", withKey("needs", "needs = { gate = 0 }")...)),
			[]string{"alpha", "gate"}},
		{"count above MaxInt32", file(table("gates.alpha", withKey("needs", "needs = { gate = 2147483648 }")...)),
			[]string{"alpha", "gate"}},
		{"empty needs", file(table("gates.alpha", withKey("needs", "needs = {}")...)), []string{"alpha", "needs"}},
		{"bad gate name", file(table(`gates."Alpha"`, gateKeys()...)), []string{"Alpha"}},
		{"bad pool name", file(table("gates.alpha", withKey("needs", `needs = { Gate = 1 }`)...)),
			[]string{"alpha", "Gate"}},
		{"unknown key", file(table("gates.alpha", append(gateKeys(), `timeout = 5`)...)),
			[]string{"alpha", "timeout"}},
		{"key differs only in case", file(table("gates.alpha", withKey("argv", `ARGV = ["/opt/x"]`)...)),
			[]string{"alpha", "ARGV"}},
		{"unknown nested table", file(table("gates.alpha", gateKeys()...), table("gates.alpha.extra", "x = 1")),
			[]string{"alpha", "extra"}},
	}
}

func TestParseRefusesBadGates(t *testing.T) {
	for _, p := range parsers() {
		for _, tc := range gateRefusals() {
			t.Run(p.name+" "+tc.name, func(t *testing.T) {
				_, err := p.parse(testPath, []byte(tc.text))
				if err == nil {
					t.Fatalf("%s accepted the file, want an error naming %v", p.name, tc.want)
				}
				for _, want := range append([]string{testPath}, tc.want...) {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
			})
		}
	}
}

func TestParseFileRefusesBadFiles(t *testing.T) {
	refusals := []refusal{
		{"no [gates] table", "# no gates\n", []string{"gates"}},
		{"pools table", file(table("gates.alpha", gateKeys()...)) + table("pools.alpha", `host = "box-a"`),
			[]string{"pools"}},
		{"unknown top-level key", "version = 1\n" + file(table("gates.alpha", gateKeys()...)),
			[]string{"version"}},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFile(testPath, []byte(tc.text))
			if err == nil {
				t.Fatalf("ParseFile accepted the file, want an error naming %v", tc.want)
			}
			for _, want := range append([]string{testPath}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestParseProjectRefusesUnknownTopLevelKey(t *testing.T) {
	text := "version = 1\n" + file(table("gates.alpha", gateKeys()...))
	_, err := ParseProject(testPath, []byte(text))
	if err == nil {
		t.Fatal("ParseProject accepted the file, want an error naming version")
	}
	for _, want := range []string{testPath, "version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestParseProjectAcceptsPoolsTables(t *testing.T) {
	pools := table("pools.alpha", `host = "box-a"`, `cap = 2`, `members = ["m1", "m2"]`) +
		table("pools.x_9", `host = "box-b"`, `cap = 1`, `members = ["m3"]`)
	got, err := ParseProject(testPath, []byte(pools))
	if err != nil {
		t.Fatalf("ParseProject with only pools: %v", err)
	}
	if got != nil {
		t.Fatalf("ParseProject with only pools = %+v, want no gates", got)
	}
	got, err = ParseProject(testPath, []byte(file(table("gates.alpha", gateKeys()...))+pools))
	if err != nil {
		t.Fatalf("ParseProject with gates and pools: %v", err)
	}
	if want := []Gate{alphaGate()}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseProject = %+v, want %+v", got, want)
	}
}

func TestParseAcceptsEmptyGatesTable(t *testing.T) {
	for _, p := range parsers() {
		got, err := p.parse(testPath, []byte("[gates]\n"))
		if err != nil {
			t.Fatalf("%s with an empty [gates] table: %v", p.name, err)
		}
		if got != nil {
			t.Fatalf("%s = %+v, want no gates", p.name, got)
		}
	}
}

func alphaGate() Gate {
	return Gate{
		Name:  "alpha",
		Argv:  []string{"/opt/tools/run", "-c", "check"},
		Limit: 1800 * time.Second,
		Needs: []Need{{Pool: "builds", Count: 2}, {Pool: "gate", Count: 1}},
	}
}

func TestParseReadsEveryKey(t *testing.T) {
	for _, p := range parsers() {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(testPath, []byte(file(table("gates.alpha", gateKeys()...))))
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			if want := []Gate{alphaGate()}; !reflect.DeepEqual(got, want) {
				t.Fatalf("%s = %+v, want %+v", p.name, got, want)
			}
		})
	}
}

func TestParseDefaultsNeedsToOneGateMember(t *testing.T) {
	text := file(table("gates.alpha", keyed(gateKeys(), "needs", "")...))
	for _, p := range parsers() {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(testPath, []byte(text))
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			want := []Need{{Pool: DefaultPool, Count: 1}}
			if len(got) != 1 || !reflect.DeepEqual(got[0].Needs, want) {
				t.Fatalf("%s = %+v, want needs %+v", p.name, got, want)
			}
		})
	}
}

func TestParseSortsGatesAndNeeds(t *testing.T) {
	text := file(
		table("gates.zeta", withKey("needs", `needs = { zeta = 1 }`)...),
		table("gates.alpha", withKey("needs", `needs = { gate = 1, builds = 2, alpha = 3 }`)...),
	)
	for _, p := range parsers() {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(testPath, []byte(text))
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			want := []Gate{
				{Name: "alpha", Argv: alphaGate().Argv, Limit: alphaGate().Limit, Needs: []Need{
					{Pool: "alpha", Count: 3}, {Pool: "builds", Count: 2}, {Pool: "gate", Count: 1},
				}},
				{Name: "zeta", Argv: alphaGate().Argv, Limit: alphaGate().Limit, Needs: []Need{
					{Pool: "zeta", Count: 1},
				}},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s = %+v, want %+v", p.name, got, want)
			}
		})
	}
}

func TestParseAcceptsBoundaryValues(t *testing.T) {
	text := file(
		table("gates.a", `argv = ["./x"]`, `limit = 1`, `needs = { a_1 = 2147483647 }`),
		table("gates.x_9", `argv = ["bin/x", "-flag"]`, `limit = 9223372036`, `needs = { z = 1 }`),
	)
	for _, p := range parsers() {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(testPath, []byte(text))
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			want := []Gate{
				{Name: "a", Argv: []string{"./x"}, Limit: time.Second, Needs: []Need{{Pool: "a_1", Count: 2147483647}}},
				{Name: "x_9", Argv: []string{"bin/x", "-flag"}, Limit: 9223372036 * time.Second,
					Needs: []Need{{Pool: "z", Count: 1}}},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s = %+v, want %+v", p.name, got, want)
			}
		})
	}
}

// agentKeys is one valid [gates.alpha] agent table body, one key per line. must_not sits above must, so the file's
// key order differs from the order the assertions come out in.
func agentKeys() []string {
	return []string{
		`agent = "reviewer"`,
		`limit = 600`,
		`must_not = ["the diff touches vendor/"]`,
		`must = ["the tests pass", "the diff is small"]`,
	}
}

func agentGate() Gate {
	return Gate{
		Name:  "alpha",
		Agent: "reviewer",
		Limit: 600 * time.Second,
		Assertions: []Assertion{
			{Text: "the tests pass", Kind: KindMust},
			{Text: "the diff is small", Kind: KindMust},
			{Text: "the diff touches vendor/", Kind: KindMustNot},
		},
	}
}

func TestParseReadsAgentGate(t *testing.T) {
	for _, p := range parsers() {
		t.Run(p.name, func(t *testing.T) {
			got, err := p.parse(testPath, []byte(file(table("gates.alpha", agentKeys()...))))
			if err != nil {
				t.Fatalf("%s: %v", p.name, err)
			}
			if want := []Gate{agentGate()}; !reflect.DeepEqual(got, want) {
				t.Fatalf("%s = %+v, want %+v", p.name, got, want)
			}
		})
	}
}

func TestParseReadsAgentGateWithOneKind(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		want []Assertion
	}{
		{"only must", keyed(agentKeys(), "must_not", ""), []Assertion{
			{Text: "the tests pass", Kind: KindMust},
			{Text: "the diff is small", Kind: KindMust},
		}},
		{"only must_not", keyed(agentKeys(), "must", ""), []Assertion{
			{Text: "the diff touches vendor/", Kind: KindMustNot},
		}},
	}
	for _, p := range parsers() {
		for _, tc := range cases {
			t.Run(p.name+" "+tc.name, func(t *testing.T) {
				got, err := p.parse(testPath, []byte(file(table("gates.alpha", tc.keys...))))
				if err != nil {
					t.Fatalf("%s: %v", p.name, err)
				}
				want := agentGate()
				want.Assertions = tc.want
				if !reflect.DeepEqual(got, []Gate{want}) {
					t.Fatalf("%s = %+v, want %+v", p.name, got, want)
				}
			})
		}
	}
}

func agentRefusals() []refusal {
	return []refusal{
		{"argv beside agent", file(table("gates.alpha", append(agentKeys(), `argv = ["/opt/x"]`)...)),
			[]string{"alpha", "argv", "agent"}},
		{"needs beside agent", file(table("gates.alpha", append(agentKeys(), `needs = { gate = 1 }`)...)),
			[]string{"alpha", "needs", "agent"}},
		{"must beside argv", file(table("gates.alpha", append(gateKeys(), `must = ["the tests pass"]`)...)),
			[]string{"alpha", "must", "argv"}},
		{"must_not beside argv", file(table("gates.alpha", append(gateKeys(), `must_not = ["the diff is small"]`)...)),
			[]string{"alpha", "must_not", "argv"}},
		{"neither argv nor agent", file(table("gates.alpha", keyed(agentKeys(), "agent", "")...)),
			[]string{"alpha", "argv"}},
		{"empty agent", file(table("gates.alpha", keyed(agentKeys(), "agent", `agent = ""`)...)),
			[]string{"alpha", "agent"}},
		{"empty must element", file(table("gates.alpha",
			keyed(agentKeys(), "must", `must = ["the tests pass", ""]`)...)), []string{"alpha", "must"}},
		{"empty must_not element", file(table("gates.alpha", keyed(agentKeys(), "must_not", `must_not = [""]`)...)),
			[]string{"alpha", "must_not"}},
		{"no assertion keys", file(table("gates.alpha", keyed(keyed(agentKeys(), "must", ""), "must_not", "")...)),
			[]string{"alpha", "must"}},
		{"empty assertion arrays", file(table("gates.alpha",
			keyed(keyed(agentKeys(), "must", `must = []`), "must_not", `must_not = []`)...)),
			[]string{"alpha", "must"}},
		{"missing limit", file(table("gates.alpha", keyed(agentKeys(), "limit", "")...)),
			[]string{"alpha", "limit"}},
	}
}

func TestParseRefusesBadAgentGates(t *testing.T) {
	for _, p := range parsers() {
		for _, tc := range agentRefusals() {
			t.Run(p.name+" "+tc.name, func(t *testing.T) {
				_, err := p.parse(testPath, []byte(tc.text))
				if err == nil {
					t.Fatalf("%s accepted the file, want an error naming %v", p.name, tc.want)
				}
				for _, want := range append([]string{testPath}, tc.want...) {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err, want)
					}
				}
			})
		}
	}
}
