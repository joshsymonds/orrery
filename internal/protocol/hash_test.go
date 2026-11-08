package protocol_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

func TestQuestionHash(t *testing.T) {
	cases := []struct {
		name, question, want string
	}{
		{
			"synthetic question",
			"Example question for the user?",
			"7f593eeb8a031e6a99b02a9b65b8e03a17a82a20ca4862ecda37bd06c479a5eb",
		},
		{
			"unset question",
			"",
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := protocol.QuestionHash(tc.question); got != tc.want {
				t.Errorf("QuestionHash(%q) = %s, want %s", tc.question, got, tc.want)
			}
		})
	}
}

func TestQuestionHashKeepsWhitespace(t *testing.T) {
	const trimmed = "Example question for the user?"
	for _, padded := range []string{" " + trimmed, trimmed + "\n", "\t" + trimmed + " \n"} {
		if protocol.QuestionHash(padded) == protocol.QuestionHash(trimmed) {
			t.Errorf("QuestionHash(%q) equals the trimmed form's hash; whitespace must not be normalized", padded)
		}
	}
}
