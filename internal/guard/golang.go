package guard

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// goTests reads the Go test functions a file declares: each top-level function with
// no receiver named Test*, Benchmark*, Fuzz* or Example*. Its assertions and skips
// are the calls in its body, closures included.
func goTests(src []byte) ([]Test, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse Go test file: %w", err)
	}
	var tests []Test
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !isTestName(fn.Name.Name) {
			continue
		}
		test := Test{Name: fn.Name.Name}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel {
				return true
			}
			switch sel.Sel.Name {
			case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
				test.Asserts++
			case "Skip", "SkipNow", "Skipf":
				test.Skips++
			}
			return true
		})
		tests = append(tests, test)
	}
	return tests, nil
}

// isTestName reports whether a top-level function name makes it a Go test declaration.
func isTestName(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
