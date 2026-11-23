package enginehost

import (
	"context"
	"errors"
	"fmt"
)

// ProjectRunner is one runner a project names: the name a capture asks for it by, the argv it runs, and a note on what
// it is for.
type ProjectRunner struct {
	Name string
	Argv []string
	Note string
}

// unsupportedError returns the error the functions of this file return until a later task gives them a body: it wraps
// errors.ErrUnsupported and names method.
func unsupportedError(method string) error {
	return fmt.Errorf("%s: %w", method, errors.ErrUnsupported)
}

// ParseProjectRunners returns the runners that a project's runners file in data names, in the order the file lists
// them. It returns an error when data is not a runners file.
func ParseProjectRunners(_ []byte) ([]ProjectRunner, error) {
	return nil, unsupportedError("ParseProjectRunners")
}

// ReadProjectRunners returns the runners that the project of repo names at commit. It returns an error when the commit
// has no runners file that parses.
func ReadProjectRunners(_ context.Context, _, _ string) ([]ProjectRunner, error) {
	return nil, unsupportedError("ReadProjectRunners")
}
