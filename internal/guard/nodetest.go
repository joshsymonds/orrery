package guard

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

// nodeTests reads the node:test declarations a .test.ts, .test.tsx or .test.js file holds. It is a scanner, not a
// JavaScript parser: it lexes away comments, string, template and regular expression literals, and the text of JSX
// elements, then reads each call to test, it or describe, with an optional .skip, .todo or .only, whose first
// argument is a string or a template literal. Each assert call counts as an assertion, and each
// skip form as a skip, toward the innermost declaration that encloses it.
func nodeTests(src []byte) ([]Test, error) {
	lex := &lexer{src: src}
	if err := lex.code(false); err != nil {
		return nil, err
	}
	closes, err := lex.matchParens()
	if err != nil {
		return nil, err
	}
	return declared(lex.toks, closes), nil
}

// kind is the kind of a lexeme.
type kind uint8

const (
	kindWord     kind = iota // an identifier, keyword or number
	kindPunct                // one punctuation character, or ${ opening a template substitution
	kindQuoted               // a '...' or "..." literal, whose content is its text
	kindTemplate             // a template literal, whose content is its source text, substitutions included
	kindOperand              // a value that is no name: a regex or a JSX element
)

// lexeme is one lexed token: its kind, its text, and the byte offset it starts at.
type lexeme struct {
	kind kind
	text string
	pos  int
}

// lexer holds the source, the position reached in it, and the lexemes lexed so far.
type lexer struct {
	src  []byte
	pos  int
	toks []lexeme
}

// code lexes code from pos. Inside a brace it stops after that brace's closing }; otherwise it runs to the end of the
// source.
func (l *lexer) code(inBrace bool) error {
	depth := 0
	for l.pos < len(l.src) {
		switch ch := l.src[l.pos]; {
		case ch == '}' && inBrace && depth == 0:
			l.pos++
			return nil
		case ch == '{':
			depth++
			l.symbol()
		case ch == '}':
			depth--
			l.symbol()
		default:
			if err := l.atom(ch); err != nil {
				return err
			}
		}
	}
	if inBrace {
		return l.fail(l.pos, "unterminated brace")
	}
	return nil
}

// atom lexes the space, comment, literal or lexeme at pos, whose first byte is ch, and that is not a brace.
func (l *lexer) atom(ch byte) error {
	switch {
	case isSpace(ch):
		l.pos++
	case ch == '\'' || ch == '"':
		return l.quotedLiteral()
	case ch == '`':
		return l.templateLiteral()
	case ch == '/':
		return l.slash()
	case ch == '<' && l.jsxStarts():
		return l.jsxOperand()
	case isWordByte(ch):
		l.wordRun()
	default:
		l.symbol()
	}
	return nil
}

// slash lexes the lexeme at a slash: a line or block comment, a regular expression, or a division.
func (l *lexer) slash() error {
	switch {
	case l.peek(1) == '/':
		l.lineComment()
	case l.peek(1) == '*':
		return l.blockComment()
	case l.exprStart():
		return l.regexpLiteral()
	default:
		l.symbol()
	}
	return nil
}

// quotedLiteral lexes the '...' or "..." literal at pos. A literal does not run past its line.
func (l *lexer) quotedLiteral() error {
	start := l.pos
	quote := l.src[start]
	for i := start + 1; i < len(l.src); i++ {
		switch l.src[i] {
		case '\\':
			i++
		case quote:
			l.emit(kindQuoted, string(l.src[start+1:i]), start)
			l.pos = i + 1
			return nil
		case '\n':
			return l.fail(start, "unterminated string literal")
		}
	}
	return l.fail(start, "unterminated string literal")
}

// templateLiteral lexes the template literal at its opening backtick. Each ${ opens a substitution, which is lexed as
// code up to its closing brace, so its braces and literals cannot end the template early.
func (l *lexer) templateLiteral() error {
	start := l.pos
	first := len(l.toks)
	l.pos++
	for l.pos < len(l.src) {
		switch ch := l.src[l.pos]; {
		case ch == '\\':
			l.pos += 2
		case ch == '`':
			// A template is one lexeme whose text is its source between the backticks, substitutions included, so a
			// test named by a template with a substitution keeps that text as its name.
			l.toks = l.toks[:first]
			l.emit(kindTemplate, string(l.src[start+1:l.pos]), start)
			l.pos++
			return nil
		case ch == '$' && l.peek(1) == '{':
			l.emit(kindPunct, "${", l.pos)
			l.pos += 2
			if err := l.code(true); err != nil {
				return err
			}
		default:
			l.pos++
		}
	}
	return l.fail(start, "unterminated template literal")
}

// regexpLiteral lexes the regular expression literal at its slash. A slash inside a class does not end it.
func (l *lexer) regexpLiteral() error {
	start := l.pos
	inClass := false
	for i := start + 1; i < len(l.src); i++ {
		switch ch := l.src[i]; {
		case ch == '\\':
			i++
		case ch == '\n':
			return l.fail(start, "unterminated regular expression")
		case ch == '[':
			inClass = true
		case ch == ']':
			inClass = false
		case ch == '/' && !inClass:
			l.pos = i + 1
			for l.pos < len(l.src) && isWordByte(l.src[l.pos]) {
				l.pos++
			}
			l.emit(kindOperand, "", start)
			return nil
		}
	}
	return l.fail(start, "unterminated regular expression")
}

// lineComment skips the line comment at pos, up to but not including its newline.
func (l *lexer) lineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// blockComment skips the block comment at pos.
func (l *lexer) blockComment() error {
	start := l.pos
	l.pos += 2
	end := bytes.Index(l.src[l.pos:], []byte("*/"))
	if end < 0 {
		return l.fail(start, "unterminated comment")
	}
	l.pos += end + len("*/")
	return nil
}

// jsxStarts reports whether the < at pos opens a JSX element: it is in expression position and names a tag or opens a
// fragment.
func (l *lexer) jsxStarts() bool {
	next := l.peek(1)
	return l.exprStart() && (isNameStart(next) || next == '>')
}

// jsxOperand lexes the JSX element whose < is at pos, and emits it as a value.
func (l *lexer) jsxOperand() error {
	start := l.pos
	l.pos++
	if err := l.jsxElement(); err != nil {
		return err
	}
	l.emit(kindOperand, "", start)
	return nil
}

// jsxElement lexes a JSX element from just after its opening <, through its closing tag. Its text is not code, so its
// quotes, slashes and parentheses are skipped; the expressions in its braces are code.
func (l *lexer) jsxElement() error {
	l.skipSpace()
	if l.peek(0) == '>' {
		l.pos++
		return l.jsxChildren()
	}
	l.jsxName()
	for {
		l.skipSpace()
		ch := l.peek(0)
		switch {
		case ch == '/' && l.peek(1) == '>':
			l.pos += 2
			return nil
		case ch == '>':
			l.pos++
			return l.jsxChildren()
		case ch == '{':
			l.pos++
			if err := l.code(true); err != nil {
				return err
			}
		case isNameStart(ch):
			if err := l.jsxAttribute(); err != nil {
				return err
			}
		default:
			return l.fail(l.pos, "unexpected character in JSX tag")
		}
	}
}

// jsxAttribute lexes an attribute's name and, when it has one, its value.
func (l *lexer) jsxAttribute() error {
	l.jsxName()
	l.skipSpace()
	if l.peek(0) != '=' {
		return nil
	}
	l.pos++
	l.skipSpace()
	return l.jsxValue()
}

// jsxValue lexes an attribute value: a quoted string, which has no escapes, or a brace expression.
func (l *lexer) jsxValue() error {
	switch ch := l.peek(0); ch {
	case '{':
		l.pos++
		return l.code(true)
	case '"', '\'':
		l.pos++
		end := bytes.IndexByte(l.src[l.pos:], ch)
		if end < 0 {
			return l.fail(l.pos, "unterminated JSX attribute value")
		}
		l.pos += end + 1
		return nil
	default:
		return l.fail(l.pos, "JSX attribute value is neither a string nor an expression")
	}
}

// jsxChildren skips an element's text through its closing tag. The expressions in its braces are code.
func (l *lexer) jsxChildren() error {
	for l.pos < len(l.src) {
		switch l.src[l.pos] {
		case '{':
			l.pos++
			if err := l.code(true); err != nil {
				return err
			}
		case '<':
			l.pos++
			l.skipSpace()
			if l.peek(0) == '/' {
				return l.jsxClosing()
			}
			if err := l.jsxElement(); err != nil {
				return err
			}
		default:
			l.pos++
		}
	}
	return l.fail(l.pos, "unterminated JSX element")
}

// jsxClosing skips a closing tag, from its slash through its >.
func (l *lexer) jsxClosing() error {
	end := bytes.IndexByte(l.src[l.pos:], '>')
	if end < 0 {
		return l.fail(l.pos, "unterminated JSX closing tag")
	}
	l.pos += end + 1
	return nil
}

// exprStart reports whether the lexeme about to be lexed is in expression position, where a slash starts a regular
// expression and a < starts JSX, rather than an operator.
func (l *lexer) exprStart() bool {
	if len(l.toks) == 0 {
		return true
	}
	last := l.toks[len(l.toks)-1]
	switch last.kind {
	case kindWord:
		return exprKeyword(last.text)
	case kindPunct:
		return last.text != ")" && last.text != "]" && last.text != "}"
	default:
		return false
	}
}

// exprKeyword reports whether a keyword is followed by an expression.
func exprKeyword(word string) bool {
	switch word {
	case "return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else",
		"yield", "await":
		return true
	}
	return false
}

// wordRun lexes the identifier, keyword or number at pos.
func (l *lexer) wordRun() {
	start := l.pos
	for l.pos < len(l.src) && isWordByte(l.src[l.pos]) {
		l.pos++
	}
	l.emit(kindWord, string(l.src[start:l.pos]), start)
}

// symbol lexes the one punctuation byte at pos.
func (l *lexer) symbol() {
	l.emit(kindPunct, string(l.src[l.pos]), l.pos)
	l.pos++
}

// skipSpace skips the white space at pos.
func (l *lexer) skipSpace() {
	for l.pos < len(l.src) && isSpace(l.src[l.pos]) {
		l.pos++
	}
}

// jsxName skips a JSX tag or attribute name at pos.
func (l *lexer) jsxName() {
	for l.pos < len(l.src) && isNameByte(l.src[l.pos]) {
		l.pos++
	}
}

// emit appends a lexeme.
func (l *lexer) emit(k kind, text string, pos int) {
	l.toks = append(l.toks, lexeme{kind: k, text: text, pos: pos})
}

// peek returns the byte offset bytes past pos, or zero past the end of the source.
func (l *lexer) peek(offset int) byte {
	if l.pos+offset < len(l.src) {
		return l.src[l.pos+offset]
	}
	return 0
}

// fail returns an error naming the line of pos.
func (l *lexer) fail(pos int, msg string) error {
	return fmt.Errorf("%s at line %d", msg, 1+bytes.Count(l.src[:pos], []byte("\n")))
}

// matchParens returns, for each open paren lexeme, the index of its closing paren, and an error when the parens do
// not balance.
func (l *lexer) matchParens() ([]int, error) {
	closes := make([]int, len(l.toks))
	var open []int
	for i, item := range l.toks {
		switch {
		case isPunct(item, "("):
			open = append(open, i)
		case isPunct(item, ")"):
			if len(open) == 0 {
				return nil, l.fail(item.pos, "unbalanced )")
			}
			closes[open[len(open)-1]] = i
			open = open[:len(open)-1]
		}
	}
	if len(open) > 0 {
		return nil, l.fail(l.toks[open[len(open)-1]].pos, "unbalanced (")
	}
	return closes, nil
}

// frame is a test declaration the scan is inside: the index of its Test, and the index of its call's closing paren.
type frame struct {
	test, end int
}

// declared returns the tests the lexemes declare, in declaration order. A lexeme counts toward the innermost
// declaration whose call encloses it, and a declaration's own callee is enclosed by it.
func declared(toks []lexeme, closes []int) []Test {
	var tests []Test
	var open []frame
	for i := range toks {
		for len(open) > 0 && open[len(open)-1].end < i {
			open = open[:len(open)-1]
		}
		if name, end, ok := declaration(toks, closes, i); ok {
			tests = append(tests, Test{Name: name})
			open = append(open, frame{test: len(tests) - 1, end: end})
		}
		if len(open) == 0 {
			continue
		}
		top := open[len(open)-1].test
		if isAssert(toks, i) {
			tests[top].Asserts++
		}
		if isSkip(toks, i) {
			tests[top].Skips++
		}
	}
	return tests
}

// declaration reports whether the call at index i declares a test: its callee is test, it or describe, optionally with
// .skip, .todo or .only, and its first argument is a string or a template literal that ends the argument. It returns the test's name and the index of the call's closing paren.
func declaration(toks []lexeme, closes []int, i int) (string, int, bool) {
	if toks[i].kind != kindWord || !isDeclarer(toks[i].text) || isPunct(at(toks, i-1), ".") {
		return "", 0, false
	}
	j := i + 1
	if modifierAt(toks, j) {
		j = dotted(toks, j)
	}
	if !isPunct(at(toks, j), "(") {
		return "", 0, false
	}
	argument := toks[j+1:]
	name := at(argument, 0)
	if (name.kind != kindQuoted && name.kind != kindTemplate) || !isArgumentEnd(at(argument, 1)) {
		return "", 0, false
	}
	return name.text, closes[j], true
}

// isAssert reports whether the lexeme at i starts an assert call: assert( or assert.<name>(, and is not a member of
// another object.
func isAssert(toks []lexeme, i int) bool {
	if !isWord(toks[i], "assert") || isPunct(at(toks, i-1), ".") {
		return false
	}
	return isPunct(at(toks, dotted(toks, i+1)), "(")
}

// isSkip reports whether the lexeme at i is a skip: the dot of a .skip(, .todo( or .only( call, or the key of
// an option named skip, todo or only in an object literal.
func isSkip(toks []lexeme, i int) bool {
	if modifierAt(toks, i) {
		return isPunct(at(toks, dotted(toks, i)), "(")
	}
	if isModifier(toks[i]) && isPunct(at(toks, i+1), ":") {
		return isPunct(at(toks, i-1), "{") || isPunct(at(toks, i-1), ",")
	}
	return false
}

// modifierAt reports whether a dot and a skip, todo or only name start at index i.
func modifierAt(toks []lexeme, i int) bool {
	return isPunct(at(toks, i), ".") && isModifier(at(toks, i+1))
}

// dotted returns the index just past the .name that starts at index i, or i when no member name starts there.
func dotted(toks []lexeme, i int) int {
	if isPunct(at(toks, i), ".") && at(toks, i+1).kind == kindWord {
		i += 2
	}
	return i
}

// isDeclarer reports whether a callee name declares a test.
func isDeclarer(name string) bool {
	return name == "test" || name == "it" || name == "describe"
}

// isModifier reports whether a lexeme is one of the skip modifiers.
func isModifier(t lexeme) bool {
	return t.kind == kindWord && (t.text == "skip" || t.text == "todo" || t.text == "only")
}

// isArgumentEnd reports whether a lexeme ends a call's first argument.
func isArgumentEnd(t lexeme) bool {
	return isPunct(t, ",") || isPunct(t, ")")
}

// isWord reports whether a lexeme is the word text.
func isWord(t lexeme, text string) bool {
	return t.kind == kindWord && t.text == text
}

// isPunct reports whether a lexeme is the punctuation text.
func isPunct(t lexeme, text string) bool {
	return t.kind == kindPunct && t.text == text
}

// at returns the lexeme at index i, or a zero lexeme outside the slice.
func at(toks []lexeme, i int) lexeme {
	if i < 0 || i >= len(toks) {
		return lexeme{}
	}
	return toks[i]
}

// isSpace reports whether a byte is white space.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// isNameStart reports whether a byte can start an identifier, including any non-ASCII byte.
func isNameStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

// isWordByte reports whether a byte can continue an identifier or number.
func isWordByte(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

// isNameByte reports whether a byte can continue a JSX tag or attribute name.
func isNameByte(c byte) bool {
	return isWordByte(c) || c == '-' || c == ':' || c == '.'
}
