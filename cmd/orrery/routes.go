package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// routesImportVerb is the verb of `orrery routes` that stores a routes file.
const routesImportVerb = "import"

// routesForm is the form `orrery routes` takes, for the usage error of a wrong argument list.
const routesForm = "import FILE"

// runRoutes runs `orrery routes import FILE`. It calls the Accounts service on the session socket beside the daemon
// socket, as runContract does, so the command opens no listener and no store.
func runRoutes(ctx context.Context, opts globals, args []string, stdout io.Writer, getenv func(string) string) error {
	socket, err := protocol.SocketPath(opts.daemonSocket, getenv)
	if err != nil {
		return fmt.Errorf("routes: %w", err)
	}
	accounts := enginev1connect.NewAccountsClient(
		cli.SessionHTTPClient(protocol.SessionSocketPath(socket)), cli.SessionBaseURL)
	return routesCommand(ctx, accounts, args, stdout)
}

// routesCommand implements `orrery routes`. Its one form, import FILE, parses the routes file FILE with
// llmroutes.ParseFile and stores the registry through ReplaceRoutes, which holds it to the rules of Routes.Replace. It
// prints the seat and context counts of the registry the store holds. A file that the parser or orreryd refuses returns
// an error and stores nothing. A wrong argument list is a usage error and calls nothing.
func routesCommand(
	ctx context.Context, accounts enginev1connect.AccountsClient, args []string, stdout io.Writer,
) error {
	if len(args) != 2 || args[0] != routesImportVerb {
		return usagef("routes: want %s", routesForm)
	}
	data, err := os.ReadFile(filepath.Clean(args[1]))
	if err != nil {
		return fmt.Errorf("routes import: %w", err)
	}
	reg, err := llmroutes.ParseFile(data)
	if err != nil {
		return fmt.Errorf("routes import: %w", err)
	}
	reply, err := accounts.ReplaceRoutes(ctx, connect.NewRequest(&v1.ReplaceRoutesRequest{
		Registry: llmroutes.ToProto(reg),
	}))
	if err != nil {
		return fmt.Errorf("routes import: %w", err)
	}
	stored := reply.Msg.GetRegistry()
	if _, err = fmt.Fprintf(stdout, "stored %d seats and %d contexts\n",
		len(stored.GetSeats()), len(stored.GetContexts())); err != nil {
		return fmt.Errorf("routes import: %w", err)
	}
	return nil
}
