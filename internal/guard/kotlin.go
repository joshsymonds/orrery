package guard

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// kotlinTests reads the JUnit tests a Kotlin file declares. It is a scanner, not a Kotlin parser: it lexes away
// comments, character literals, raw strings and string literals, whose ${...} substitutions are code, then reads each
// fun whose own annotations include @Test or @org.junit.Test, named as written with its backticks removed. Each
// assert call in its body counts as an assertion, and each @Ignore or @Disabled among its own annotations, or on an
// enclosing class declaration, counts as a skip.
func kotlinTests(src []byte) ([]Test, error) {
	lex := &kotlinLexer{src: src}
	if err := lex.code(false); err != nil {
		return nil, err
	}
	return lex.declared()
}

// kindName is a backtick name, whose text is the name between its backticks.
const kindName = kindOperand + 1

// kotlinLexer holds the source, the position reached in it, and the lexemes lexed so far.
type kotlinLexer struct {
	src  []byte
	pos  int
	toks []lexeme
}

// code lexes code from pos. Inside a substitution it stops after the } that closes it; otherwise it runs to the end
// of the source.
func (l *kotlinLexer) code(inSubstitution bool) error {
	depth := 0
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		switch {
		case ch == '}' && inSubstitution && depth == 0:
			l.symbol()
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
	if inSubstitution {
		return l.fail(l.pos, "unterminated string template")
	}
	return nil
}

// atom lexes the space, comment, literal or lexeme at pos, whose first byte is ch, and that is not a brace.
func (l *kotlinLexer) atom(ch byte) error {
	switch {
	case isSpace(ch):
		l.pos++
	case ch == '/' && l.ahead() == '/':
		l.lineComment()
	case ch == '/' && l.ahead() == '*':
		return l.blockComment()
	case ch == '"':
		return l.stringLiteral()
	case ch == '\'':
		return l.charLiteral()
	case ch == '`':
		return l.backtickName()
	case ch == '@':
		l.atSign()
	case isWordByte(ch):
		l.wordRun()
	default:
		l.symbol()
	}
	return nil
}

// lineComment skips the line comment at pos, up to but not including its newline.
func (l *kotlinLexer) lineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.pos++
	}
}

// blockComment skips the block comment at pos. Kotlin block comments nest.
func (l *kotlinLexer) blockComment() error {
	start := l.pos
	depth := 0
	for l.pos < len(l.src) {
		switch {
		case l.src[l.pos] == '/' && l.ahead() == '*':
			depth++
			l.pos += 2
		case l.src[l.pos] == '*' && l.ahead() == '/':
			depth--
			l.pos += 2
			if depth == 0 {
				return nil
			}
		default:
			l.pos++
		}
	}
	return l.fail(start, "unterminated comment")
}

// stringLiteral lexes the string at pos: a raw string when it opens with three quotes, otherwise a string that does
// not run past its line. A ${ in either opens a substitution.
func (l *kotlinLexer) stringLiteral() error {
	if l.atRawQuotes() {
		return l.rawString()
	}
	start := l.pos
	l.pos++
	for l.pos < len(l.src) {
		switch ch := l.src[l.pos]; {
		case ch == '\\':
			l.pos += 2
		case ch == '"':
			l.emit(kindQuoted, string(l.src[start+1:l.pos]), start)
			l.pos++
			return nil
		case ch == '\n':
			return l.fail(start, "unterminated string literal")
		case ch == '$' && l.ahead() == '{':
			if err := l.substitution(); err != nil {
				return err
			}
		default:
			l.pos++
		}
	}
	return l.fail(start, "unterminated string literal")
}

// rawString lexes the raw string at its opening three quotes. It has no escapes, and it closes at the last three
// quotes of the run that ends it, so a longer run ends in quotes the string holds.
func (l *kotlinLexer) rawString() error {
	start := l.pos
	l.pos += 3
	for l.pos < len(l.src) {
		switch {
		case l.src[l.pos] == '$' && l.ahead() == '{':
			if err := l.substitution(); err != nil {
				return err
			}
		case l.atRawQuotes():
			end := l.pos
			for end < len(l.src) && l.src[end] == '"' {
				end++
			}
			l.emit(kindQuoted, string(l.src[start+3:end-3]), start)
			l.pos = end
			return nil
		default:
			l.pos++
		}
	}
	return l.fail(start, "unterminated raw string")
}

// substitution lexes the ${ at pos and the code it holds through its closing }. Its lexemes are the code's, so the
// braces, quotes and comments in it do not end the string around it.
func (l *kotlinLexer) substitution() error {
	l.emit(kindPunct, "${", l.pos)
	l.pos += 2
	return l.code(true)
}

// charLiteral lexes the character literal at pos, which may hold an escape, as a quoted literal.
func (l *kotlinLexer) charLiteral() error {
	start := l.pos
	for i := start + 1; i < len(l.src); i++ {
		switch l.src[i] {
		case '\\':
			i++
		case '\'':
			l.emit(kindQuoted, string(l.src[start+1:i]), start)
			l.pos = i + 1
			return nil
		case '\n':
			return l.fail(start, "unterminated character literal")
		}
	}
	return l.fail(start, "unterminated character literal")
}

// backtickName lexes the backtick name at pos. Its text is the name between the backticks, so a name with a template
// keeps its source text.
func (l *kotlinLexer) backtickName() error {
	start := l.pos
	for i := start + 1; i < len(l.src); i++ {
		switch l.src[i] {
		case '`':
			l.emit(kindName, string(l.src[start+1:i]), start)
			l.pos = i + 1
			return nil
		case '\n':
			return l.fail(start, "unterminated backtick name")
		}
	}
	return l.fail(start, "unterminated backtick name")
}

// atSign lexes the @ at pos. An @ directly after a name or a backtick name is a label's, as in return@forEach, and is
// no token; any other @ opens an annotation.
func (l *kotlinLexer) atSign() {
	if l.pos > 0 && (isWordByte(l.src[l.pos-1]) || l.src[l.pos-1] == '`') {
		l.pos++
		return
	}
	l.symbol()
}

// wordRun lexes the identifier, keyword or number at pos.
func (l *kotlinLexer) wordRun() {
	start := l.pos
	for l.pos < len(l.src) && isWordByte(l.src[l.pos]) {
		l.pos++
	}
	l.emit(kindWord, string(l.src[start:l.pos]), start)
}

// symbol lexes the one punctuation byte at pos.
func (l *kotlinLexer) symbol() {
	l.emit(kindPunct, string(l.src[l.pos]), l.pos)
	l.pos++
}

// emit appends a lexeme.
func (l *kotlinLexer) emit(k kind, text string, pos int) {
	l.toks = append(l.toks, lexeme{kind: k, text: text, pos: pos})
}

// ahead returns the byte after the one at pos, or zero past the end of the source.
func (l *kotlinLexer) ahead() byte {
	if l.pos+1 < len(l.src) {
		return l.src[l.pos+1]
	}
	return 0
}

// atRawQuotes reports whether the three quotes that open or close a raw string start at pos.
func (l *kotlinLexer) atRawQuotes() bool {
	return bytes.HasPrefix(l.src[l.pos:], []byte(`"""`))
}

// fail returns an error naming the line of pos.
func (l *kotlinLexer) fail(pos int, msg string) error {
	return fmt.Errorf("%s at line %d", msg, 1+strings.Count(string(l.src[:pos]), "\n"))
}

// closeParen returns the index of the ) that closes the ( at index open, and an error when none does.
func (l *kotlinLexer) closeParen(open int) (int, error) {
	depth := 0
	for i := open; i < len(l.toks); i++ {
		switch {
		case isPunct(l.toks[i], "("):
			depth++
		case isPunct(l.toks[i], ")"):
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, l.fail(l.toks[open].pos, "unbalanced (")
}

// annotation reads the annotation whose @ is at index start: its name, dotted, after an optional use-site target such
// as get:, and its arguments, which it skips. It returns the name and the index of the annotation's last lexeme.
func (l *kotlinLexer) annotation(start int) (string, int, error) {
	toks := l.toks
	j := start + 1
	if isName(at(toks, j)) && punctThenName(toks, j+1, ":") {
		j += 2
	}
	if !isName(at(toks, j)) {
		return "", 0, l.fail(toks[start].pos, "annotation without a name")
	}
	parts := []string{toks[j].text}
	for punctThenName(toks, j+1, ".") {
		j += 2
		parts = append(parts, toks[j].text)
	}
	if isPunct(at(toks, j+1), "(") {
		closing, err := l.closeParen(j + 1)
		if err != nil {
			return "", 0, err
		}
		j = closing
	}
	return strings.Join(parts, "."), j, nil
}

// kotlinFrame is a brace the walk is inside: the test whose body it is, or -1 when none. When it opens a class
// declaration, class is set and skips holds that declaration's @Ignore and @Disabled annotations.
type kotlinFrame struct {
	test  int
	class bool
	skips int
}

// kotlinWalk holds the tests declared so far, the braces open, the annotations since the last token that could not
// precede a declaration, and the frame the next { opens when a declaration's body is next.
type kotlinWalk struct {
	lex    *kotlinLexer
	tests  []Test
	frames []kotlinFrame
	annos  []string
	next   *kotlinFrame
}

// declared returns the tests the lexemes declare, in declaration order.
func (l *kotlinLexer) declared() ([]Test, error) {
	walk := &kotlinWalk{lex: l, frames: []kotlinFrame{{test: -1}}}
	toks := l.toks
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if isPunct(tok, "@") {
			name, end, err := l.annotation(i)
			if err != nil {
				return nil, err
			}
			walk.annos = append(walk.annos, name)
			i = end
			continue
		}
		if isKotlinModifier(tok) {
			continue
		}
		annos := walk.annos
		walk.annos = nil
		end, err := walk.token(i, annos)
		if err != nil {
			return nil, err
		}
		i = end
	}
	if len(walk.frames) > 1 {
		return nil, l.fail(len(l.src), "unterminated brace")
	}
	return walk.tests, nil
}

// token walks the lexeme at index i, which follows the annotations annos, and returns the index the walk continues
// from: its own, or the closing paren of a fun's parameter list.
func (w *kotlinWalk) token(i int, annos []string) (int, error) {
	toks := w.lex.toks
	tok := toks[i]
	switch {
	case isFunDecl(toks, i):
		return w.fun(i, annos)
	case isKeywordAt(toks, i, "class"), isKeywordAt(toks, i, "object"), isKeywordAt(toks, i, "interface"):
		w.next = &kotlinFrame{test: -1, class: true, skips: countSkips(annos)}
	case isKeywordAt(toks, i, "val"), isKeywordAt(toks, i, "var"):
		w.dropTestBody()
	case isPunct(tok, "{"), isPunct(tok, "${"):
		w.push()
	case isPunct(tok, "}"):
		if err := w.pop(tok.pos); err != nil {
			return 0, err
		}
	case isPunct(tok, ";"):
		w.next = nil
	case isAssertCall(toks, i):
		w.assert()
	}
	return i, nil
}

// fun records the test the fun at index i declares, when its own annotations include a test annotation, and makes the
// test's body the next brace. It returns the index of the fun's closing paren, so the parameter list's braces are
// not read as the body.
func (w *kotlinWalk) fun(i int, annos []string) (int, error) {
	w.next = nil
	name := i + 1
	closing, err := w.lex.closeParen(name + 1)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(annos, isTestAnnotation) {
		return closing, nil
	}
	skips := countSkips(annos) + w.enclosingSkips()
	w.tests = append(w.tests, Test{Name: w.lex.toks[name].text, Skips: skips})
	w.next = &kotlinFrame{test: len(w.tests) - 1}
	return closing, nil
}

// dropTestBody forgets a test whose body is next when a property begins. An expression-bodied test has no brace, so
// the brace of a later property's initializer is no test's body.
func (w *kotlinWalk) dropTestBody() {
	if w.next != nil && w.next.test >= 0 {
		w.next = nil
	}
}

// enclosingSkips returns the skips of the innermost class declaration the walk is inside, or zero when it is inside
// none.
func (w *kotlinWalk) enclosingSkips() int {
	for _, frame := range slices.Backward(w.frames) {
		if frame.class {
			return frame.skips
		}
	}
	return 0
}

// push opens the brace the walk reached, as the body of the declaration pending, or as a plain brace.
func (w *kotlinWalk) push() {
	frame := kotlinFrame{test: -1}
	if w.next != nil {
		frame = *w.next
		w.next = nil
	}
	w.frames = append(w.frames, frame)
}

// pop closes the innermost brace at pos, and fails when none is open.
func (w *kotlinWalk) pop(pos int) error {
	if len(w.frames) == 1 {
		return w.lex.fail(pos, "unbalanced }")
	}
	w.frames = w.frames[:len(w.frames)-1]
	w.next = nil
	return nil
}

// assert counts an assertion toward the test whose expression body the walk is in, when a test's body is next and no
// brace has opened it, and otherwise toward the innermost test the walk is inside.
func (w *kotlinWalk) assert() {
	if w.next != nil && w.next.test >= 0 {
		w.tests[w.next.test].Asserts++
		return
	}
	for _, frame := range slices.Backward(w.frames) {
		if frame.test >= 0 {
			w.tests[frame.test].Asserts++
			return
		}
	}
}

// isTestAnnotation reports whether an annotation name marks a JUnit test.
func isTestAnnotation(name string) bool {
	return name == "Test" || name == "org.junit.Test"
}

// countSkips returns how many of the annotation names skip a test.
func countSkips(annos []string) int {
	n := 0
	for _, name := range annos {
		switch name {
		case "Ignore", "org.junit.Ignore", "Disabled", "org.junit.jupiter.api.Disabled":
			n++
		}
	}
	return n
}

// isKotlinModifier reports whether a lexeme is a modifier keyword, which may stand between an annotation and the
// declaration it annotates.
func isKotlinModifier(t lexeme) bool {
	if t.kind != kindWord {
		return false
	}
	switch t.text {
	case "abstract", "actual", "annotation", "companion", "const", "crossinline", "data", "enum", "expect",
		"external", "final", "infix", "inline", "inner", "internal", "lateinit", "noinline", "open", "operator",
		"override", "private", "protected", "public", "sealed", "suspend", "tailrec", "value", "vararg":
		return true
	}
	return false
}

// isKeywordAt reports whether the lexeme at index i is the keyword word, which is not a member name after a dot or
// the class name after a colon pair, as in Foo::class.
func isKeywordAt(toks []lexeme, i int, word string) bool {
	return isWord(toks[i], word) && !isPunct(at(toks, i-1), ".") && !isPunct(at(toks, i-1), ":")
}

// isAssertCall reports whether the lexeme at index i is an identifier starting with assert and followed by a paren.
func isAssertCall(toks []lexeme, i int) bool {
	return toks[i].kind == kindWord && strings.HasPrefix(toks[i].text, "assert") && isPunct(at(toks, i+1), "(")
}

// isName reports whether a lexeme is a name: an identifier or a backtick name.
func isName(t lexeme) bool {
	return (t.kind == kindWord || t.kind == kindName) && t.text != ""
}

// isFunDecl reports whether the lexeme at index i is the fun of a declaration named and then opening its parameters.
func isFunDecl(toks []lexeme, i int) bool {
	return isKeywordAt(toks, i, "fun") && isName(at(toks, i+1)) && isPunct(at(toks, i+1+1), "(")
}

// punctThenName reports whether the lexeme at index i is the punctuation p and the lexeme after it is a name.
func punctThenName(toks []lexeme, i int, p string) bool {
	return isPunct(at(toks, i), p) && isName(at(toks, i+1))
}
