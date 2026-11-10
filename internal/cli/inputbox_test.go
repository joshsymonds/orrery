package cli

import (
	"strings"
	"testing"
)

const inputBoxTestWidth = 40

// inputBoxTestRule returns a full rule line for the test width.
func inputBoxTestRule() string {
	return strings.Repeat("─", inputBoxTestWidth)
}

func TestInputBoxVisible(t *testing.T) {
	rule := inputBoxTestRule()
	shortRule := strings.Repeat("─", inputBoxTestWidth-1)
	dashed := strings.Repeat("╌", inputBoxTestWidth)
	noBreak := " "
	mixedRule := strings.Repeat("─", 19) + "x" + strings.Repeat("─", 20)

	tests := []struct {
		name   string
		screen string
		want   bool
	}{
		{
			name:   "idle box with no-break space after the mark",
			screen: "some output\n" + rule + "\n❯" + noBreak + "placeholder text\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name:   "typed draft with space after the mark",
			screen: "some output\n" + rule + "\n❯ typed draft words\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name:   "mark alone at end of line",
			screen: rule + "\n❯\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name: "draft with continuation lines and a blank line",
			screen: rule + "\n❯ first line of draft\n  second line\n\n  third line\n" +
				rule + "\n  footer one",
			want: true,
		},
		{
			name:   "streaming reply above the box",
			screen: "reply line one\nreply line two\n" + rule + "\n❯" + noBreak + "\n" + rule + "\n  streaming footer",
			want:   true,
		},
		{
			name:   "one footer line",
			screen: rule + "\n❯ draft\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name:   "three footer lines",
			screen: rule + "\n❯ draft\n" + rule + "\n  footer one\n  footer two\n  footer three",
			want:   true,
		},
		{
			name:   "trailing blank lines after the footer",
			screen: rule + "\n❯ draft\n" + rule + "\n  footer one\n\n\n",
			want:   true,
		},
		{
			name: "question dialog",
			screen: rule + "\n ☐ Header\n\nInvented question?\n\n❯ 1. Option\n   2. Other\n" +
				rule + "\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel",
			want: false,
		},
		{
			name: "permission dialog",
			screen: "❯ earlier prompt\n" + rule + "\n Create file\n" + dashed +
				"\n Do you want to create note.txt?\n ❯ 1. Yes\n   2. No\n Esc to cancel · Tab to amend",
			want: false,
		},
		{
			name:   "folder trust dialog with one rule",
			screen: rule + "\nTrust this folder?\n❯ 1. Yes\n  2. No\nEnter to confirm",
			want:   false,
		},
		{
			name:   "no rule",
			screen: "❯ draft\n  footer one",
			want:   false,
		},
		{
			name:   "rule one rune shorter than width",
			screen: shortRule + "\n❯ draft\n" + shortRule + "\n  footer one",
			want:   false,
		},
		{
			name:   "rule containing another character",
			screen: mixedRule + "\n❯ draft\n" + rule + "\n  footer one",
			want:   false,
		},
		{
			name:   "unindented line between the rules",
			screen: rule + "\n❯ draft\nunindented text\n" + rule + "\n  footer one",
			want:   false,
		},
		{
			name:   "four footer lines",
			screen: rule + "\n❯ draft\n" + rule + "\n  footer one\n  footer two\n  footer three\n  footer four",
			want:   false,
		},
		{
			name:   "mark followed by another character",
			screen: rule + "\n❯1 option\n" + rule + "\n  footer one",
			want:   false,
		},
		{
			name:   "empty screen",
			screen: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inputBoxVisible(tt.screen, inputBoxTestWidth); got != tt.want {
				t.Errorf("inputBoxVisible() = %v, want %v", got, tt.want)
			}
		})
	}
}
