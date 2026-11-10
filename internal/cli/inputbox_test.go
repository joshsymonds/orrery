package cli

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const inputBoxTestWidth = 40

// inputBoxTestRule returns a full rule line for the test width.
func inputBoxTestRule() string {
	return strings.Repeat("─", inputBoxTestWidth)
}

// inputBoxTestTitledRule returns a titled rule for the test width: rule characters, a space,
// the title, a space, and one rule character.
func inputBoxTestTitledRule(title string) string {
	lead := inputBoxTestWidth - utf8.RuneCountInString(title) - 3
	return strings.Repeat("─", lead) + " " + title + " ─"
}

func TestInputBoxVisible(t *testing.T) {
	rule := inputBoxTestRule()
	shortRule := strings.Repeat("─", inputBoxTestWidth-1)
	dashed := strings.Repeat("╌", inputBoxTestWidth)
	noBreak := " "
	mixedRule := strings.Repeat("─", 19) + "x" + strings.Repeat("─", 20)
	mode := "  ⏵⏵ invented mode text · ↓ to manage"
	status := "  invented status line"
	selectMode := "  ↑/↓ to select"
	mainRow := "  ● main"
	agentHollow := "  ◯ invented-agent   Invented description   10s · ↓ 40k tokens"
	agentFilled := "  ● other-agent   Other invented text   2m · ↓ 1k tokens"

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
			name:   "two-line footer, blank line, main row and one agent",
			screen: rule + "\n❯ draft\n" + rule + "\n" + status + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow,
			want:   true,
		},
		{
			name:   "several agents with filled and hollow dots",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow + "\n" + agentFilled + "\n" + agentHollow,
			want:   true,
		},
		{
			name:   "one-line footer with main row and two agents",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow + "\n" + agentFilled,
			want:   true,
		},
		{
			name:   "agent rows followed by trailing blank lines",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow + "\n\n\n",
			want:   true,
		},
		{
			name:   "selected row in the list",
			screen: rule + "\n❯ draft\n" + rule + "\n" + status + "\n" + selectMode + "\n\n❯ ● main\n" + agentHollow,
			want:   false,
		},
		{
			name:   "selected row as the last line",
			screen: rule + "\n❯ draft\n" + rule + "\n" + selectMode + "\n\n❯ ● main",
			want:   false,
		},
		{
			name:   "rows with no blank line after the footer",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n" + mainRow + "\n" + agentHollow,
			want:   false,
		},
		{
			name:   "two blank lines before the rows",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n\n\n" + mainRow + "\n" + agentHollow,
			want:   false,
		},
		{
			name:   "non-row line after the rows",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow + "\n  trailing text",
			want:   false,
		},
		{
			name:   "rows above the footer",
			screen: rule + "\n❯ draft\n" + rule + "\n" + mainRow + "\n" + agentHollow + "\n\n" + mode,
			want:   false,
		},
		{
			name:   "four footer lines then a blank and rows",
			screen: rule + "\n❯ draft\n" + rule + "\n" + status + "\n" + mode + "\n" + status + "\n" + mode + "\n\n" + mainRow + "\n" + agentHollow,
			want:   false,
		},
		{
			name: "question dialog with agent rows below it",
			screen: rule + "\n ☐ Header\n\nInvented question?\n\n❯ 1. Option\n   2. Other\n" +
				rule + "\n  4. Chat about this\n\nEnter to select · ↑/↓ to navigate · Esc to cancel\n\n" +
				mainRow + "\n" + agentHollow,
			want: false,
		},
		{
			name:   "titled upper rule with a short title",
			screen: inputBoxTestTitledRule("invented") + "\n❯ draft\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name: "titled upper rule with a long title",
			screen: inputBoxTestTitledRule("invented long label for the border") +
				"\n❯ draft\n" + rule + "\n  footer one",
			want: true,
		},
		{
			name:   "titled upper rule with a two-word title",
			screen: inputBoxTestTitledRule("invented label") + "\n❯ draft\n" + rule + "\n  footer one",
			want:   true,
		},
		{
			name:   "titled upper rule with no leading rule",
			screen: "invented title" + strings.Repeat(" ", 24) + " ─\n❯ draft\n" + rule + "\n  footer one",
			want:   false,
		},
		{
			name: "titled upper rule with no trailing rule",
			screen: strings.Repeat("─", 3) + " invented title" + strings.Repeat(" ", 22) +
				"\n❯ draft\n" + rule + "\n  footer one",
			want: false,
		},
		{
			name: "titled upper rule with the title mid-border",
			screen: strings.Repeat("─", 10) + " invented title " + strings.Repeat("─", 14) +
				"\n❯ draft\n" + rule + "\n  footer one",
			want: false,
		},
		{
			name:   "titled upper rule with one wide character filling 40 cells",
			screen: strings.Repeat("─", 35) + " 漢 ─\n❯ draft\n" + rule + "\n  footer one",
			want:   false,
		},
		{
			name:   "plain upper rule with a titled lower rule",
			screen: rule + "\n❯ draft\n" + inputBoxTestTitledRule("invented title") + "\n  footer one",
			want:   false,
		},
		{
			name:   "dialog whose last row is titled",
			screen: rule + "\n❯ 1. Option\n  2. Other\n" + inputBoxTestTitledRule("invented title"),
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
