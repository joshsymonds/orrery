// Package hostrepo makes a repository's checkout available on a host that engine work runs on, and reads the origin of
// a checkout there. It fetches only. Ensure never checks out, resets or changes the branch of a checkout on a host: it
// fetches into the checkout, so a checkout the owner works in keeps its branch, its index and its files.
package hostrepo

import (
	"context"
	"errors"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// RootLister lists a host's roots: the absolute directories whose folders the host's agent reports, under which a
// checkout may be found. *node.Client satisfies it.
type RootLister interface {
	Roots(ctx context.Context) ([]string, error)
}

// Request describes the checkout that Ensure makes available. Host is the orrery name of the host the checkout is on.
// Origin is the URL of the repository's origin, which the checkout fetches from. Preferred is the directory of a
// checkout the work should use, when that directory is under one of the roots. Base is the commit the checkout must
// hold after the fetch.
type Request struct {
	Host, Origin, Preferred, Base string
}

// OriginURL returns the URL of the origin remote of the checkout at repository on host. It is not implemented yet.
func OriginURL(_ context.Context, _ hostexec.Host, _ string) (string, error) {
	return "", errors.ErrUnsupported
}

// Ensure makes the checkout that req describes available on host, and returns the path of that checkout. It fetches
// into a checkout under one of roots, and never checks out, resets or changes the branch of a checkout. It is not
// implemented yet.
func Ensure(_ context.Context, _ hostexec.Host, _ RootLister, _ Request) (string, error) {
	return "", errors.ErrUnsupported
}
