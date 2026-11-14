package gates

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// defineCase is one call of Define: the fields a [gates.NAME] table holds, passed by value.
type defineCase struct {
	name    string
	argv    []string
	limit   int64
	needs   []Need
	agent   string
	must    []string
	mustNot []string
}

// argvCase is a valid argv gate whose needs are unsorted, so a match with ParseFile also shows the sort.
func argvCase() defineCase {
	return defineCase{
		name:  "alpha",
		argv:  []string{"/opt/tools/run", "-c", "check"},
		limit: 1800,
		needs: []Need{{Pool: "gate", Count: 1}, {Pool: "builds", Count: 2}},
	}
}

// agentCase is a valid agent gate whose must_not is listed apart from its musts.
func agentCase() defineCase {
	return defineCase{
		name:    "alpha",
		limit:   600,
		agent:   "reviewer",
		must:    []string{"the tests pass", "the diff is small"},
		mustNot: []string{"the diff touches vendor/"},
	}
}

func (c defineCase) define() (Gate, error) {
	return Define(c.name, c.argv, c.limit, c.needs, c.agent, c.must, c.mustNot)
}

func TestDefineMatchesParseFileForArgvGate(t *testing.T) {
	t.Parallel()
	parsed, err := ParseFile(testPath, []byte(file(table("gates.alpha", gateKeys()...))))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	got, err := argvCase().define()
	if err != nil {
		t.Fatalf("Define: %v", err)
	}
	if !reflect.DeepEqual(got, parsed[0]) {
		t.Fatalf("Define = %+v, ParseFile = %+v", got, parsed[0])
	}
	if want := alphaGate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Define = %+v, want %+v", got, want)
	}
}

func TestDefineDefaultsNeedsToOneGateMember(t *testing.T) {
	t.Parallel()
	parsed, err := ParseFile(testPath, []byte(file(table("gates.alpha", keyed(gateKeys(), "needs", "")...))))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, needs := range [][]Need{nil, {}} {
		c := argvCase()
		c.needs = needs
		got, err := c.define()
		if err != nil {
			t.Fatalf("Define with needs %#v: %v", needs, err)
		}
		if !reflect.DeepEqual(got, parsed[0]) {
			t.Fatalf("Define with needs %#v = %+v, ParseFile = %+v", needs, got, parsed[0])
		}
		if want := []Need{{Pool: DefaultPool, Count: 1}}; !reflect.DeepEqual(got.Needs, want) {
			t.Fatalf("Define with needs %#v took %+v, want %+v", needs, got.Needs, want)
		}
	}
}

func TestDefineMatchesParseFileForAgentGate(t *testing.T) {
	t.Parallel()
	parsed, err := ParseFile(testPath, []byte(file(table("gates.alpha", agentKeys()...))))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	got, err := agentCase().define()
	if err != nil {
		t.Fatalf("Define: %v", err)
	}
	if !reflect.DeepEqual(got, parsed[0]) {
		t.Fatalf("Define = %+v, ParseFile = %+v", got, parsed[0])
	}
	if want := agentGate(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Define = %+v, want %+v", got, want)
	}
}

func TestDefineAcceptsBoundaryValues(t *testing.T) {
	t.Parallel()
	got, err := defineCase{
		name:  "a",
		argv:  []string{"./x"},
		limit: 1,
		needs: []Need{{Pool: "a_1", Count: math.MaxInt32}},
	}.define()
	if err != nil {
		t.Fatalf("Define at the low limit and the largest count: %v", err)
	}
	want := Gate{
		Name:  "a",
		Argv:  []string{"./x"},
		Limit: time.Second,
		Needs: []Need{{Pool: "a_1", Count: math.MaxInt32}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Define = %+v, want %+v", got, want)
	}
	got, err = defineCase{
		name:  "x_9",
		argv:  []string{"bin/x"},
		limit: 9223372036,
		needs: []Need{{Pool: "z", Count: 1}},
	}.define()
	if err != nil {
		t.Fatalf("Define at the largest limit: %v", err)
	}
	if got.Limit != 9223372036*time.Second {
		t.Fatalf("Define limit = %v, want %v", got.Limit, 9223372036*time.Second)
	}
}

// defineRefusal is one Define call that must fail with an error that starts with the gates.go form of its name and
// contains want.
type defineRefusal struct {
	name string
	c    defineCase
	want string
}

func defineRefusals() []defineRefusal {
	argv := func(mutate func(*defineCase)) defineCase {
		c := argvCase()
		mutate(&c)
		return c
	}
	agent := func(mutate func(*defineCase)) defineCase {
		c := agentCase()
		mutate(&c)
		return c
	}
	return []defineRefusal{
		{"bad name", argv(func(c *defineCase) { c.name = "Alpha" }), "name must match [a-z][a-z0-9_]*"},
		{"empty name", argv(func(c *defineCase) { c.name = "" }), "name must match [a-z][a-z0-9_]*"},
		{"argv empty", argv(func(c *defineCase) { c.argv = nil }), "argv is empty"},
		{
			"argv empty element",
			argv(func(c *defineCase) { c.argv = []string{"/opt/x", ""} }),
			"argv has an empty element",
		},
		{"argv[0] without slash", argv(func(c *defineCase) { c.argv = []string{"run", "-c"} }),
			"must be a path with a / in it"},
		{"argv[0] just", argv(func(c *defineCase) { c.argv = []string{"just", "check"} }),
			"must be a path with a / in it"},
		{"limit zero", argv(func(c *defineCase) { c.limit = 0 }), "limit 0 is outside 1 to"},
		{"limit negative", argv(func(c *defineCase) { c.limit = -1 }), "limit -1 is outside 1 to"},
		{"limit overflows", argv(func(c *defineCase) { c.limit = 9223372037 }), "limit 9223372037 is outside 1 to"},
		{"must beside argv", argv(func(c *defineCase) { c.must = []string{"the tests pass"} }),
			"must cannot sit beside argv"},
		{"must_not beside argv", argv(func(c *defineCase) { c.mustNot = []string{"the diff is small"} }),
			"must_not cannot sit beside argv"},
		{"needs pool bad", argv(func(c *defineCase) { c.needs = []Need{{Pool: "Gate", Count: 1}} }),
			`pool "Gate" in needs must match`},
		{"count zero", argv(func(c *defineCase) { c.needs = []Need{{Pool: "gate", Count: 0}} }),
			"needs gate count 0 is outside 1 to"},
		{
			"count above MaxInt32",
			argv(func(c *defineCase) { c.needs = []Need{{Pool: "gate", Count: math.MaxInt32 + 1}} }),
			fmt.Sprintf("needs gate count %d is outside 1 to", math.MaxInt32+1),
		},
		{
			"pool twice",
			argv(func(c *defineCase) { c.needs = []Need{{Pool: "gate", Count: 1}, {Pool: "gate", Count: 2}} }),
			`pool "gate" named twice in needs`,
		},
		{
			"argv beside agent",
			agent(func(c *defineCase) { c.argv = []string{"/opt/x"} }),
			"argv cannot sit beside agent",
		},
		{"needs beside agent", agent(func(c *defineCase) { c.needs = []Need{{Pool: "gate", Count: 1}} }),
			"needs cannot sit beside agent"},
		{"agent with no assertion", agent(func(c *defineCase) { c.must = nil; c.mustNot = nil }),
			"needs a must or must_not assertion"},
		{"agent empty must element", agent(func(c *defineCase) { c.must = []string{"the tests pass", ""} }),
			"must has an empty element"},
		{"agent empty must_not element", agent(func(c *defineCase) { c.mustNot = []string{""} }),
			"must_not has an empty element"},
		{"agent limit zero", agent(func(c *defineCase) { c.limit = 0 }), "limit 0 is outside 1 to"},
	}
}

func TestDefineRefusesBadGates(t *testing.T) {
	t.Parallel()
	for _, tc := range defineRefusals() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.c.define()
			if err == nil {
				t.Fatalf("Define accepted the gate, want an error containing %q", tc.want)
			}
			if prefix := fmt.Sprintf("gate %q: ", tc.c.name); !strings.HasPrefix(err.Error(), prefix) {
				t.Errorf("error %q does not start with %q", err, prefix)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}
