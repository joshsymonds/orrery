package stepmcp

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

func TestEpicPartReadsOneDecisionWhole(t *testing.T) {
	// The decision's text runs to a second line past the item text cap, so an item line would cut it to the first line's
	// first 120 runes. Its question and its id are its own, so the read is of DL3 and not of the decision before it.
	decision := &v1.EpicDecision{
		Seq: 2, Id: "DL3", Text: strings.Repeat("d", 290) + "\nThe second line answers it.", Question: "Q2", By: "owner",
	}
	p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{
		Decisions: []*v1.EpicDecision{{Seq: 1, Id: "DL1", Text: "Build passkey login."}, decision},
	}}})

	var got v1.EpicDecision
	unmarshalResult(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"decision","decision":"DL3"}`), &got)
	if !proto.Equal(&got, decision) {
		t.Fatalf("part decision = %s, want %s", marshalProto(t, &got), marshalProto(t, decision))
	}
}

func TestEpicPartReadsOneTurnWhole(t *testing.T) {
	// The turn's outline runs to three lines, and a turn line would cut it to the first. Its decisions are its own list.
	turn := &v1.EpicTurn{
		Seq: 2, Step: "step-2", State: "succeeded", Outcome: "built",
		Outline:   "Outline 2.\nThe second line of the outline.\nThe third line.",
		Decisions: []string{"DL1", "DL2"},
	}
	p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{
		Turns: []*v1.EpicTurn{{Seq: 1, State: "succeeded", Outline: "Outline 1."}, turn, {Seq: 3, State: "running"}},
	}}})

	var got v1.EpicTurn
	unmarshalResult(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"turn","turn":"2"}`), &got)
	if !proto.Equal(&got, turn) {
		t.Fatalf("part turn = %s, want %s", marshalProto(t, &got), marshalProto(t, turn))
	}
}

func TestEpicPartRefusesDecisionAndTurnArguments(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "decision with part decisions",
			args: `{"id":"` + testEpicID + `","part":"decisions","decision":"DL1"}`,
			want: "decision needs part decision, not part decisions",
		},
		{
			name: "turn with part turns",
			args: `{"id":"` + testEpicID + `","part":"turns","turn":"1"}`,
			want: "turn needs part turn, not part turns",
		},
		{
			name: "decision with part task",
			args: `{"id":"` + testEpicID + `","part":"task","task":"T1","decision":"DL1"}`,
			want: "decision needs part decision, not part task",
		},
		{
			name: "turn with part question",
			args: `{"id":"` + testEpicID + `","part":"question","question":"Q1","turn":"1"}`,
			want: "turn needs part turn, not part question",
		},
		{
			name: "decision with no part",
			args: `{"id":"` + testEpicID + `","decision":"DL1"}`,
			want: "decision needs part decision",
		},
		{
			name: "turn with no part",
			args: `{"id":"` + testEpicID + `","turn":"1"}`,
			want: "turn needs part turn",
		},
		{
			name: "part decision without decision",
			args: `{"id":"` + testEpicID + `","part":"decision"}`,
			want: "part decision needs decision, such as DL3",
		},
		{
			name: "part turn without turn",
			args: `{"id":"` + testEpicID + `","part":"turn"}`,
			want: "part turn needs turn, such as 3",
		},
		{
			name: "turn that is a word",
			args: `{"id":"` + testEpicID + `","part":"turn","turn":"three"}`,
			want: `turn "three" is not a positive integer`,
		},
		{
			name: "turn zero",
			args: `{"id":"` + testEpicID + `","part":"turn","turn":"0"}`,
			want: `turn "0" is not a positive integer`,
		},
		{
			name: "turn negative",
			args: `{"id":"` + testEpicID + `","part":"turn","turn":"-1"}`,
			want: `turn "-1" is not a positive integer`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEpics{}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_get", tc.args)

			if !outcome.isError {
				t.Fatalf("epic_get %s returned %q without isError", tc.args, outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_get error text = %q, want %q", outcome.text, tc.want)
			}
			if n := len(fake.calls().gets); n != 0 {
				t.Fatalf("GetEpic calls = %d, want none before the refusal", n)
			}
		})
	}
}

func TestEpicPartRefusesADecisionOrTurnTheEpicLacks(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "decision the epic lacks",
			args: `{"id":"` + testEpicID + `","part":"decision","decision":"DL9"}`,
			want: "decision: no decision DL9 in epic " + testEpicID,
		},
		{
			name: "turn the epic lacks",
			args: `{"id":"` + testEpicID + `","part":"turn","turn":"9"}`,
			want: "turn: no turn 9 in epic " + testEpicID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: smallEpic()}}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_get", tc.args)

			if !outcome.isError {
				t.Fatalf("epic_get returned %q without isError", outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_get error text = %q, want %q", outcome.text, tc.want)
			}
			if n := len(fake.calls().gets); n != 1 {
				t.Fatalf("GetEpic calls = %d, want one", n)
			}
		})
	}
}
