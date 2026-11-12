package pools

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const testPath = "pools.toml"

// poolKeys is one valid [pools.alpha] table body, one key per line.
func poolKeys() []string {
	return []string{
		`host = "box-a"`,
		`cap = 2`,
		`members = ["m1", "m2"]`,
		`wake = ["wake-m1", "--flag"]`,
		`wake_limit = 5`,
		`status = ["status-m1"]`,
		`status_limit = 3`,
		`admit = ["admit-m1"]`,
		`admit_limit = 4`,
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

// table renders one TOML table with the given section header and key lines.
func table(section string, keys ...string) string {
	return "[" + section + "]\n" + strings.Join(keys, "\n") + "\n"
}

// withKey returns poolKeys with the key's line replaced by line.
func withKey(key, line string) []string {
	return keyed(poolKeys(), key, line)
}

type refusal struct {
	name string
	text string
	want []string
}

func fixedRefusals() []refusal {
	return []refusal{
		{"syntax error", table("pools.alpha", poolKeys()...) + "[pools.beta\n", []string{"line 12"}},
		{"unknown key", table("pools.alpha", append(poolKeys(), `nickname = "x"`)...), []string{"alpha", "nickname"}},
		{"unknown section", table("other", "x = 1"), []string{"other"}},
		{"unknown top-level key", "version = 1\n" + table("pools.alpha", poolKeys()...), []string{"version"}},
		{"key differs only in case", table("pools.alpha", withKey("host", `HOST = "box-a"`)...),
			[]string{"alpha", "HOST"}},
		{"unknown nested table", table("pools.alpha", poolKeys()...) + table("pools.alpha.extra", "x = 1"),
			[]string{"alpha", "extra"}},
		{"empty table", table("pools.alpha"), []string{"alpha", "host"}},
	}
}

func nameRefusals() []refusal {
	names := []string{"Alpha", "1a", "_a", "a-b", "a b"}
	out := make([]refusal, 0, len(names))
	for _, name := range names {
		out = append(out, refusal{"pool name " + name, table(`pools."`+name+`"`, poolKeys()...), []string{name}})
	}
	return out
}

func missingRefusals() []refusal {
	keys := poolKeys()
	out := make([]refusal, 0, len(keys))
	for _, line := range keys {
		key, _, _ := strings.Cut(line, " ")
		out = append(out, refusal{"missing " + key, table("pools.alpha", keyed(keys, key, "")...),
			[]string{"alpha", key}})
	}
	return out
}

func keyRefusals() []refusal {
	return slices.Concat(fixedRefusals(), nameRefusals(), missingRefusals())
}

func typeRefusals() []refusal {
	lines := []struct{ key, line string }{
		{"host", `host = 1`},
		{"cap", `cap = "2"`},
		{"cap", `cap = 2.5`},
		{"members", `members = "m1"`},
		{"members", `members = [1]`},
		{"wake", `wake = "x"`},
		{"wake_limit", `wake_limit = "5"`},
		{"status", `status = [true]`},
		{"admit_limit", `admit_limit = 1.5`},
	}
	out := make([]refusal, 0, len(lines))
	for _, l := range lines {
		out = append(out, refusal{"wrong type " + l.line, table("pools.alpha", withKey(l.key, l.line)...),
			[]string{"line", "pools.alpha." + l.key}})
	}
	return out
}

func boundRefusals() []refusal {
	return []refusal{
		{"cap zero", table("pools.alpha", withKey("cap", "cap = 0")...), []string{"alpha", "cap"}},
		{"cap negative", table("pools.alpha", withKey("cap", "cap = -3")...), []string{"alpha", "cap"}},
		{"wake_limit zero", table("pools.alpha", withKey("wake_limit", "wake_limit = 0")...),
			[]string{"alpha", "wake_limit"}},
		{"status_limit negative", table("pools.alpha", withKey("status_limit", "status_limit = -1")...),
			[]string{"alpha", "status_limit"}},
		{"admit_limit zero", table("pools.alpha", withKey("admit_limit", "admit_limit = 0")...),
			[]string{"alpha", "admit_limit"}},
		{"wake_limit overflows", table("pools.alpha", withKey("wake_limit", "wake_limit = 9223372037")...),
			[]string{"alpha", "wake_limit"}},
		{"members empty", table("pools.alpha", withKey("members", "members = []")...), []string{"alpha", "members"}},
		{"duplicate member", table("pools.alpha", withKey("members", `members = ["m1", "m1"]`)...),
			[]string{"alpha", "members", "m1"}},
	}
}

func memberRefusals() []refusal {
	bads := []string{"-m1", ".m1", "m 1", "m/1", ""}
	out := make([]refusal, 0, len(bads))
	for _, bad := range bads {
		line := `members = ["` + bad + `"]`
		out = append(out, refusal{"member " + line, table("pools.alpha", withKey("members", line)...),
			[]string{"alpha", "members"}})
	}
	return out
}

func argvRefusals() []refusal {
	keys := []string{"wake", "status", "admit"}
	out := make([]refusal, 0, 2*len(keys))
	for _, key := range keys {
		out = append(out,
			refusal{key + " empty", table("pools.alpha", withKey(key, key+" = []")...), []string{"alpha", key}},
			refusal{key + " empty element", table("pools.alpha", withKey(key, key+` = ["x", ""]`)...),
				[]string{"alpha", key}},
		)
	}
	return out
}

func valueRefusals() []refusal {
	return slices.Concat(boundRefusals(), memberRefusals(), argvRefusals())
}

// gatesRefusals are other top-level keys and tables refused beside a [gates] table, which pools leaves to
// internal/gates.
func gatesRefusals() []refusal {
	gates := gateTables()
	return []refusal{
		{"unknown top-level key beside gates", "version = 1\n" + gates, []string{"version"}},
		{"unknown section beside gates", gates + table("other", "x = 1"), []string{"other"}},
		{"gates as a value", "gates = 1\n" + table("pools.alpha", poolKeys()...), []string{"gates"}},
		{"section named like gates beside gates", gates + table("gate.unit", `argv = ["/bin/true"]`),
			[]string{"gate"}},
	}
}

func refusals() []refusal {
	return slices.Concat(keyRefusals(), typeRefusals(), valueRefusals(), gatesRefusals())
}

func TestParseRefusesBadFiles(t *testing.T) {
	for _, tc := range refusals() {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(testPath, []byte(tc.text))
			if err == nil {
				t.Fatalf("Parse accepted the file, want an error naming %v", tc.want)
			}
			for _, want := range append([]string{testPath}, tc.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestParseReadsEveryKey(t *testing.T) {
	got, err := Parse(testPath, []byte(table("pools.alpha", poolKeys()...)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []Pool{{
		Name:        "alpha",
		Host:        "box-a",
		Cap:         2,
		Members:     []string{"m1", "m2"},
		Wake:        []string{"wake-m1", "--flag"},
		WakeLimit:   5 * time.Second,
		Status:      []string{"status-m1"},
		StatusLimit: 3 * time.Second,
		Admit:       []string{"admit-m1"},
		AdmitLimit:  4 * time.Second,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse = %+v, want %+v", got, want)
	}
}

// gateTables is a project file's [gates] table with one gate that names argv, limit and an inline needs table.
func gateTables() string {
	return table("gates") + table("gates.unit", `argv = ["/bin/true"]`, `limit = 5`, `needs = { alpha = 1 }`)
}

func TestParseLeavesGatesTablesToGates(t *testing.T) {
	pools := table("pools.alpha", poolKeys()...)
	want, err := Parse(testPath, []byte(pools))
	if err != nil {
		t.Fatalf("Parse without gates: %v", err)
	}
	if len(want) != 1 {
		t.Fatalf("Parse without gates returned %d pools, want 1", len(want))
	}
	got, err := Parse(testPath, []byte(gateTables()+pools))
	if err != nil {
		t.Fatalf("Parse with gates: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse with gates = %+v, want %+v", got, want)
	}
}

func TestParseReadsGatesOnlyFileAsNoPools(t *testing.T) {
	gate := table("gates.unit", `argv = ["/bin/true"]`, `limit = 5`, `needs = { alpha = 1 }`)
	for _, text := range []string{gateTables(), gate} {
		got, err := Parse(testPath, []byte(text))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("Parse = %+v, want no pools", got)
		}
	}
}

func TestParseSortsByName(t *testing.T) {
	text := table("pools.zeta", withKey("members", `members = ["z1"]`)...) +
		table("pools.alpha", withKey("members", `members = ["a1"]`)...)
	got, err := Parse(testPath, []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := make([]string, 0, len(got))
	for _, p := range got {
		names = append(names, p.Name)
	}
	if want := []string{"alpha", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestParseAcceptsBoundaryValues(t *testing.T) {
	text := table("pools.a",
		`host = "h"`,
		`cap = 1`,
		`members = ["A", "9.x-y_z"]`,
		`wake = ["w"]`,
		`wake_limit = 9223372036`,
		`status = ["s"]`,
		`status_limit = 1`,
		`admit = ["d"]`,
		`admit_limit = 1`,
	) + table("pools.x_9", poolKeys()...)
	got, err := Parse(testPath, []byte(text))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Parse returned %d pools, want 2", len(got))
	}
	if got[0].Cap != 1 || got[0].WakeLimit != 9223372036*time.Second {
		t.Fatalf("boundary pool = %+v", got[0])
	}
	if want := []string{"A", "9.x-y_z"}; !reflect.DeepEqual(got[0].Members, want) {
		t.Fatalf("members = %v, want %v", got[0].Members, want)
	}
}

func testPool(name, member string) Pool {
	return Pool{
		Name:        name,
		Host:        "box-a",
		Cap:         1,
		Members:     []string{member},
		Wake:        []string{"wake"},
		WakeLimit:   time.Second,
		Status:      []string{"status"},
		StatusLimit: time.Second,
		Admit:       []string{"admit"},
		AdmitLimit:  time.Second,
	}
}

func TestMergeKeepsOneCopyOfIdenticalPools(t *testing.T) {
	got, err := Merge([]Pool{testPool("alpha", "m1")}, []Pool{testPool("alpha", "m1")})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if want := []Pool{testPool("alpha", "m1")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %+v, want %+v", got, want)
	}
}

func TestMergeRefusesDifferingSameNamePools(t *testing.T) {
	other := testPool("alpha", "m1")
	other.Cap = 2
	_, err := Merge([]Pool{testPool("alpha", "m1")}, []Pool{other})
	if !errors.Is(err, ErrDefinedTwice) {
		t.Fatalf("Merge error = %v, want ErrDefinedTwice", err)
	}
}

func TestMergeRefusesMemberInTwoPools(t *testing.T) {
	_, err := Merge([]Pool{testPool("alpha", "m1")}, []Pool{testPool("beta", "m1")})
	if !errors.Is(err, ErrDefinedTwice) {
		t.Fatalf("Merge error = %v, want ErrDefinedTwice", err)
	}
}

func TestMergeSortsByName(t *testing.T) {
	got, err := Merge([]Pool{testPool("zeta", "z1")}, []Pool{testPool("alpha", "a1")})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := []Pool{testPool("alpha", "a1"), testPool("zeta", "z1")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge = %+v, want %+v", got, want)
	}
}

func TestEqualComparesEveryField(t *testing.T) {
	base := testPool("alpha", "m1")
	if !base.Equal(testPool("alpha", "m1")) {
		t.Fatal("identical pools compare unequal")
	}
	changes := []struct {
		field  string
		change func(*Pool)
	}{
		{"Name", func(p *Pool) { p.Name = "beta" }},
		{"Host", func(p *Pool) { p.Host = "box-b" }},
		{"Cap", func(p *Pool) { p.Cap = 2 }},
		{"Members", func(p *Pool) { p.Members = []string{"m9"} }},
		{"Wake", func(p *Pool) { p.Wake = []string{"other"} }},
		{"WakeLimit", func(p *Pool) { p.WakeLimit = 2 * time.Second }},
		{"Status", func(p *Pool) { p.Status = []string{"other"} }},
		{"StatusLimit", func(p *Pool) { p.StatusLimit = 2 * time.Second }},
		{"Admit", func(p *Pool) { p.Admit = []string{"other"} }},
		{"AdmitLimit", func(p *Pool) { p.AdmitLimit = 2 * time.Second }},
	}
	for _, c := range changes {
		other := testPool("alpha", "m1")
		c.change(&other)
		if base.Equal(other) {
			t.Errorf("pools differing in %s compare equal", c.field)
		}
	}
}
