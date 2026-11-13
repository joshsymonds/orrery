// Package help renders the one-screen help card and the static help page from the keymap table.
package help

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/joshsymonds/orrery/internal/keymap"
	"github.com/joshsymonds/orrery/internal/palette"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// labelWidth is the column where a row's text starts, counted after its two-space indent.
const labelWidth = 14

// row is one label and the text that explains it.
type row struct {
	Label string
	Text  string
}

// section is one headed group of rows.
type section struct {
	Heading string
	Rows    []row
}

// document is the help content that Card and Page both render.
type document struct {
	Title    string
	Sections []section
}

// content builds the help content. prefix is tmux's prefix key; "" means it is unknown.
func content(prefix string) document {
	tmux := section{Heading: "tmux"}
	if prefix != "" {
		tmux.Heading = fmt.Sprintf("tmux (prefix is %s)", prefix)
	}
	for _, binding := range keymap.For(keymap.Tmux) {
		for _, key := range binding.Keys {
			tmux.Rows = append(tmux.Rows, row{Label: "prefix " + key, Text: binding.Help})
		}
	}

	tree := section{Heading: "tree"}
	for _, binding := range keymap.For(keymap.Tree) {
		tree.Rows = append(tree.Rows, row{Label: strings.Join(binding.Keys, ", "), Text: binding.Help})
	}

	return document{
		Title: "orrery daily use",
		Sections: []section{
			{
				Heading: "p",
				Rows: []row{
					{Label: "p", Text: "attach the most recently active session, then open the tree"},
					{
						Label: "p .",
						Text:  "the session for the current directory, else the picker to resume or start fresh",
					},
					{
						Label: "p PATH",
						Text:  "the session for directory PATH (one with / or ..), else the picker to resume or start fresh",
					},
					{
						Label: "p WORD",
						Text:  "a session named WORD, else the known directory named WORD (several: choose one), else the picker",
					},
					{
						Label: "p new NAME",
						Text:  "create project directory NAME and a session for it (--root DIR sets the parent)",
					},
					{
						Label: "p WORDS...",
						Text:  "open the picker with WORDS as the intent, over known directories and the roots' directories",
					},
				},
			},
			tmux,
			{
				Heading: "kitty",
				Rows: []row{
					{Label: "kitty_mod+p", Text: "run p; nix-config's kitty binding, not set by orrery"},
				},
			},
			tree,
		},
	}
}

// painter colors text with c. The plain card uses one that returns text unchanged.
type painter func(c palette.Color, text string) string

// plain returns text unchanged, for the uncolored card.
func plain(_ palette.Color, text string) string {
	return text
}

// Card writes the help card to out as plain text. prefix is tmux's prefix key; "" means it is unknown.
func Card(out io.Writer, prefix string) error {
	return writeCard(out, prefix, plain)
}

// ColorCard writes the help card to out with palette colors: the title and headings in Mauve, the labels in Lavender,
// and the text in the terminal's default color. prefix is tmux's prefix key; "" means it is unknown. Stripped of its
// escape sequences it is Card's output.
func ColorCard(out io.Writer, prefix string) error {
	return writeCard(out, prefix, palette.Fg)
}

// writeCard writes the help card to out, painting each part with paint.
func writeCard(out io.Writer, prefix string, paint painter) error {
	doc := content(prefix)
	var card strings.Builder

	card.WriteString(paint(palette.Mauve, doc.Title) + "\n\n")
	for i, sec := range doc.Sections {
		if i > 0 {
			card.WriteString("\n")
		}
		card.WriteString(paint(palette.Mauve, sec.Heading) + "\n")
		for _, r := range sec.Rows {
			writeRow(&card, paint, r.Label, r.Text)
		}
	}

	if _, err := io.WriteString(out, card.String()); err != nil {
		return fmt.Errorf("write help card: %w", err)
	}
	return nil
}

// pageSource is the help page. Every label and text reaches it through html/template, which escapes it. Its colors
// come from the palette, so the page is always Catppuccin Mocha; the constant conversions keep them out of template
// escaping.
const pageSource = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: dark; }
body { margin: 0 auto; max-width: 56rem; padding: 2rem 1rem; font: 15px/1.5 system-ui, sans-serif; background: ` + string(palette.Base) + `; color: ` + string(palette.Text) + `; }
h1 { font-size: 1.4rem; color: ` + string(palette.Mauve) + `; }
h2 { font-size: 1.05rem; margin: 1.6rem 0 0.4rem; color: ` + string(palette.Mauve) + `; }
table { width: 100%; border-collapse: collapse; }
th, td { text-align: left; vertical-align: top; padding: 0.2rem 1rem 0.2rem 0; }
th { font-family: ui-monospace, monospace; font-weight: 600; white-space: nowrap; color: ` + string(palette.Lavender) + `; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
{{range .Sections}}<h2>{{.Heading}}</h2>
<table>
{{range .Rows}}<tr><th>{{.Label}}</th><td>{{.Text}}</td></tr>
{{end}}</table>
{{end}}</body>
</html>
`

// Page writes the help page to out: the same content as Card, as one static HTML document with inline CSS.
func Page(out io.Writer, prefix string) error {
	page, err := template.New("page").Parse(pageSource)
	if err != nil {
		return fmt.Errorf("parse help page: %w", err)
	}
	if err = page.Execute(out, content(prefix)); err != nil {
		return fmt.Errorf("write help page: %w", err)
	}
	return nil
}

// writeRow appends one indented row: the label left-justified to labelWidth and painted, then the text.
func writeRow(card *strings.Builder, paint painter, label, text string) {
	fmt.Fprintf(card, "  %s%s\n", paint(palette.Lavender, fmt.Sprintf("%-*s", labelWidth, label)), text)
}

// Prefix returns tmux's global prefix key on c's server, trimmed. It returns "" when the server cannot be read.
func Prefix(ctx context.Context, c *tmuxctl.Client) string {
	out, err := c.Run(ctx, "show-options", "-gv", "prefix")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
