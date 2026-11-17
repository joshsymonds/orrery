package guard

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// The DOM assertion helpers a node test uses in place of node:assert's equal family when a value is a DOM node. They
// count as assertions (see isAssert).
const (
	domAssertNone = "assertNone"
	domAssertSame = "assertSame"
)

// ruleDOMAssert is the rule an added assert.<equal family> call breaks when one of its first two arguments is a DOM
// node. node:assert formats a failing assertion's values with util.inspect at depth 1000 with getters on, and a DOM node
// formatted that way walks its whole window and document until memory runs out, so such a call that fails does not
// fail: it takes the host's memory. The %s is the call, as assert.<name>.
const ruleDOMAssert = "%s at line %d compares a DOM node, which node:assert formats in full when it fails: " +
	"use assertNone or assertSame from web/src/testdom.ts, or compare a boolean such as el === null"

// domAssertArgs is how many leading arguments of an equal-family call node:assert formats when the call fails: actual
// and expected.
const domAssertArgs = 2

// equalFamilyOpen is how far an equal-family call's open paren is from its assert: assert, ., the name, then (.
const equalFamilyOpen = 3

// domAssertName reports whether name is one of the node:assert calls that format their actual and expected values when
// they fail.
func domAssertName(name string) bool {
	switch name {
	case "equal", "strictEqual", "notEqual", "notStrictEqual", "deepEqual", "deepStrictEqual":
		return true
	}
	return false
}

// domAssert is one assert.<equal family> call whose first or second argument is a DOM node: its call, as
// assert.<name>, the line it starts on, and its key, the text of its lexemes, which names the call wherever it moves.
type domAssert struct {
	call string
	line int
	key  string
}

// domAssertsAdded returns the refusals of the DOM asserts a node test file's change adds: each call at the head whose
// key the head holds more often than the base does. A call that only moves, or that the base already held, is not
// refused, so a file that held such calls before the change is refused only for the ones it gains. A side that is not a
// node test file, or that cannot be lexed, reads no calls; a guarded file that cannot be lexed is refused by its own
// rule.
func domAssertsAdded(change Change) []Refusal {
	if !nodeTestPath(change.Path) || !change.AtHead {
		return nil
	}
	head, err := domAsserts(change.Head)
	if err != nil {
		return nil
	}
	held := map[string]int{}
	if change.AtBase {
		base, baseErr := domAsserts(change.Base)
		if baseErr != nil {
			return nil
		}
		for _, call := range base {
			held[call.key]++
		}
	}
	var refusals []Refusal
	for _, call := range head {
		if held[call.key] > 0 {
			held[call.key]--
			continue
		}
		refusals = append(refusals, Refusal{File: change.Path, Rule: fmt.Sprintf(ruleDOMAssert, call.call, call.line)})
	}
	return refusals
}

// nodeTestPath reports whether a path is a node test file, as parserFor reads one.
func nodeTestPath(path string) bool {
	return strings.HasSuffix(path, ".test.ts") || strings.HasSuffix(path, ".test.tsx") ||
		strings.HasSuffix(path, ".test.js")
}

// domAsserts returns the DOM asserts of a node test file's source, in source order.
func domAsserts(src []byte) ([]domAssert, error) {
	toks, closes, err := lexNode(src)
	if err != nil {
		return nil, err
	}
	var calls []domAssert
	for i := range toks {
		open, ok := equalFamilyCall(toks, i)
		if !ok {
			continue
		}
		args := callArgs(toks, open, closes[open])
		if len(args) > domAssertArgs {
			args = args[:domAssertArgs]
		}
		if !slices.ContainsFunc(args, func(arg span) bool { return domValued(toks, closes, arg) }) {
			continue
		}
		parts := make([]string, 0, closes[open]-i+1)
		for _, tok := range toks[i : closes[open]+1] {
			parts = append(parts, tok.text)
		}
		calls = append(calls, domAssert{
			call: "assert." + toks[open-1].text,
			line: 1 + bytes.Count(src[:toks[i].pos], []byte("\n")),
			key:  strings.Join(parts, " "),
		})
	}
	return calls, nil
}

// equalFamilyCall reports whether the lexemes at i start assert.<name>( with name one of domAssertName's, not as a
// member of another object, and returns the index of the call's open paren.
func equalFamilyCall(toks []lexeme, i int) (int, bool) {
	if !isWord(toks[i], "assert") || isPunct(at(toks, i-1), ".") || !isPunct(at(toks, i+1), ".") {
		return 0, false
	}
	open := i + equalFamilyOpen
	name := at(toks, open-1)
	if name.kind != kindWord || !domAssertName(name.text) || !isPunct(at(toks, open), "(") {
		return 0, false
	}
	return open, true
}

// span is one call argument: the lexemes from index start up to, not including, index end.
type span struct {
	start, end int
}

// callArgs returns the arguments of the call whose parens are at open and closing, split at the commas outside any
// nested paren, bracket or brace. A trailing comma adds no argument.
func callArgs(toks []lexeme, open, closing int) []span {
	var args []span
	depth, start := 0, open+1
	for j := open + 1; j < closing; j++ {
		tok := toks[j]
		switch {
		case isPunct(tok, "(") || isPunct(tok, "[") || isPunct(tok, "{"):
			depth++
		case isPunct(tok, ")") || isPunct(tok, "]") || isPunct(tok, "}"):
			depth--
		case isPunct(tok, ",") && depth == 0:
			args = append(args, span{start: start, end: j})
			start = j + 1
		}
	}
	if start < closing {
		args = append(args, span{start: start, end: closing})
	}
	return args
}

// domValued reports whether an argument is a DOM node by its form: document.activeElement, or an expression that ends
// in a .querySelector(…) or .closest(…) call, optional chaining included. A trailing non-null assertion (!) or a
// trailing `as Type` cast does not change it. closes holds toks' paren matches.
func domValued(toks []lexeme, closes []int, arg span) bool {
	end := withoutCast(toks, arg)
	if end-arg.start == 3 && isWord(toks[arg.start], "document") && isPunct(toks[arg.start+1], ".") &&
		isWord(toks[arg.start+2], "activeElement") {
		return true
	}
	last := end - 1
	if last < arg.start || !isPunct(toks[last], ")") {
		return false
	}
	// The argument ends in a call when its last lexeme closes the paren that follows a callee name.
	for open := last - 1; open >= arg.start+2; open-- {
		if isPunct(toks[open], "(") && closes[open] == last {
			callee := toks[open-1]
			return (isWord(callee, "querySelector") || isWord(callee, "closest")) && isPunct(toks[open-2], ".")
		}
	}
	return false
}

// withoutCast returns the end of arg without a trailing non-null assertion or `as Type` cast, as many as it ends in.
func withoutCast(toks []lexeme, arg span) int {
	end := arg.end
	for {
		switch {
		case end > arg.start && isPunct(toks[end-1], "!"):
			end--
		case end-2 > arg.start && isWord(toks[end-2], "as") && toks[end-1].kind == kindWord:
			end -= 2
		default:
			return end
		}
	}
}
