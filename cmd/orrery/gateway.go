package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// gatewayCallerKeyEnv names the variable whose value is the path of the file that holds the caller key the gateway
// requires.
const gatewayCallerKeyEnv = "PATCHBAY_CALLER_KEY_FILE"

// gatewayListeningPrefix starts the stderr line `orrery gateway` writes once its listener is bound, before the address.
const gatewayListeningPrefix = "orrery gateway: listening on "

// runGateway runs `orrery gateway`. It serves the Claude traffic gateway on the loopback address --listen names, which
// is gateway.DefaultListen when it names none, until SIGINT, SIGTERM or the end of ctx. Then it shuts down and returns
// nil.
func runGateway(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	listen, err := parseGatewayFlags(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// The listener is bound first, so an address server.Listen refuses leaves nothing behind: no ledger, no reader.
	ln, err := server.Listen(ctx, listen)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	gw, closeGateway, err := openGateway(ctx, stderr, getenv)
	if err != nil {
		return errors.Join(err, ln.Close())
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	running := make(chan struct{})
	go func() {
		defer close(running)
		gw.Run(runCtx)
	}()
	fmt.Fprintf(stderr, "%s%s\n", gatewayListeningPrefix, ln.Addr())
	serveErr := serveUntilDone(runCtx, ln, gw.Handler(), make(chan struct{}))
	cancelRun()
	// Run reads the route registry the reader holds, so the reader and the ledger close only once Run has returned.
	<-running
	return errors.Join(serveErr, closeGateway())
}

// parseGatewayFlags reads the flags of `orrery gateway` and returns the address it listens on.
func parseGatewayFlags(args []string) (string, error) {
	flags := flag.NewFlagSet("orrery gateway", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	listen := flags.String("listen", gateway.DefaultListen, "loopback address to serve on")
	if err := flags.Parse(args); err != nil {
		return "", usagef("gateway: %w", err)
	}
	if flags.NArg() > 0 {
		return "", usagef("gateway: unexpected argument %q", flags.Arg(0))
	}
	return *listen, nil
}

// openGateway builds the gateway over the route registry in the store that getenv locates, and reads the registry
// once. It returns the gateway and the function that closes the reader and the ledger, which the caller runs once the
// gateway has stopped. A gateway that cannot be built closes what it opened before it returns its error.
func openGateway(
	ctx context.Context,
	stderr io.Writer,
	getenv func(string) string,
) (*gateway.Gateway, func() error, error) {
	storePath, err := store.Path(getenv)
	if err != nil {
		return nil, nil, fmt.Errorf("gateway: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	routes := store.NewReader(storePath)
	// When the ledger cannot open, the gateway still serves each request, with a Recorder whose every write fails, so
	// that each request's failed record is logged.
	var recorder gateway.Recorder
	closeLedger := func() error { return nil }
	if led, ledgerErr := ledger.Open(ctx, ledger.Path(storePath)); ledgerErr != nil {
		logger.ErrorContext(ctx, "open the ledger; every request's record will fail", slog.Any("error", ledgerErr))
		recorder = failingRecorder{err: ledgerErr}
	} else {
		recorder = led
		closeLedger = led.Close
	}
	gw, err := gateway.New(gateway.Config{
		Routes:        routes,
		Ledger:        recorder,
		Logger:        logger,
		CallerKeyFile: getenv(gatewayCallerKeyEnv),
		Getenv:        getenv,
	})
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("gateway: %w", err), closeLedger(), routes.Close())
	}
	if loadErr := gw.Load(ctx); loadErr != nil {
		logger.WarnContext(ctx, "load the route registry; requests get 503 until a read succeeds",
			slog.Any("error", loadErr))
	}
	closeAll := func() error { return errors.Join(closeLedger(), routes.Close()) }

	return gw, closeAll, nil
}

// failingRecorder is the Recorder of a gateway whose ledger did not open. Each write fails with err.
type failingRecorder struct {
	err error
}

// Start fails with the error the ledger did not open with.
func (r failingRecorder) Start(context.Context, ledger.Start) (int64, error) {
	return 0, r.err
}

// Finish fails with the error the ledger did not open with.
func (r failingRecorder) Finish(context.Context, int64, ledger.Finish) error {
	return r.err
}
