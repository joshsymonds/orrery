package enginehost

import (
	"context"
	"log/slog"
	"time"
)

// storeRetry is how long after a failed store write retryStore tries it again. The store's busy timeout bounds each
// try, so a store that Litestream holds is tried about once each busy timeout plus this.
const storeRetry = time.Second

// retry runs write, the write what of check id, as retryStore does.
func (n *NodeChecks) retry(ctx context.Context, what, id string, write func(ctx context.Context) error) error {
	return retryStore(ctx, n.after, n.logger, what, []any{"check", id}, write)
}

// evidenceRunLogKey is the log key a retried write of an evidence run names the run's id under.
const evidenceRunLogKey = "run"

// retry runs write, the write what of evidence run id, as retryStore does.
func (e *Evidence) retry(ctx context.Context, what, id string, write func(ctx context.Context) error) error {
	return retryStore(ctx, e.after, e.logger, what, []any{evidenceRunLogKey, id}, write)
}

// retryStore runs write, which stores what a drive has learned, until it succeeds. A write the store refuses, a busy one
// among them, is tried again storeRetry after each failure, on the channel after returns, since nothing else would store
// it. Each try runs on ctx with its cancel removed. Only the end of ctx stops the retries, after one last try, and the
// error of that try is returned for the caller to leave its work to a restart. The first failure is logged, naming the
// write what and with attrs, and so is a success after one. It returns nil once write succeeds.
func retryStore(
	ctx context.Context,
	after func(time.Duration) <-chan time.Time,
	logger *slog.Logger,
	what string,
	attrs []any,
	write func(ctx context.Context) error,
) error {
	attrs = append([]any{"write", what}, attrs...)
	storeCtx := context.WithoutCancel(ctx)
	for tries := 1; ; tries++ {
		err := write(storeCtx)
		if err == nil {
			if tries > 1 {
				logger.InfoContext(ctx, "store write stored after a retry", append(attrs, "tries", tries)...)
			}
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if tries == 1 {
			logger.WarnContext(ctx, "store write failed; kept for retry", append(attrs, "err", err)...)
		}
		select {
		case <-ctx.Done():
		case <-after(storeRetry):
		}
	}
}
