package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/ratecard"
)

// answerCaptureBytes caps the answer one request keeps for its ledger row, at 16 MiB. A stream reports its usage at its
// end, so the answer is kept until it completes. The cap keeps one request from holding more than that, since many are
// served at once; the caller is sent the whole answer whatever the cap.
const answerCaptureBytes = 16 << 20

// ledgerRecord is one request's ledger row while the request is served: the id its Start returned, and the resolution
// its Finish is priced from. The record is nil when its Start failed, and then nothing is finished.
type ledgerRecord struct {
	id  int64
	res Resolution
}

// startRecord writes the Start of a request, before the seat serves it. The request's account is the one its pin names
// when the pin applied, else its context's account, else empty; the caller's own account, which metadata.user_id names,
// is recorded beside it. A failed Start is logged with the request's seat and context, and returns nil, so that the
// request is served all the same.
func (g *Gateway) startRecord(
	ctx context.Context,
	header http.Header,
	body []byte,
	res Resolution,
	pin string,
) *ledgerRecord {
	account := res.Context.Account
	if pin != "" {
		account = header.Get(accountPinHeader)
	}
	callerAccount, session := Caller(body)
	start := ledger.Start{
		Account:        account,
		CallerAccount:  callerAccount,
		Seat:           res.SeatID,
		Context:        res.Context.ID,
		RequestedModel: requestModel(body),
		ClaudeSession:  session,
		StartedAt:      g.clock.Now().Unix(),
	}
	if observed := observeSubagent(header); observed != nil {
		start.AgentID = observed.AgentID
		start.AgentType = observed.AgentType
	}

	id, err := g.ledger.Start(ctx, start)
	if err != nil {
		g.logger.WarnContext(ctx, "ledger: start failed",
			slog.String("seat", res.SeatID), slog.String("context", res.Context.ID), slog.Any("error", err))

		return nil
	}

	return &ledgerRecord{id: id, res: res}
}

// finishRecord writes the Finish of the request a record was started for, once the seat has served its answer. The
// answer is the one the capture kept: its usage prices the request, and the headers the caller was sent report its
// claims. A failed Finish is logged with the request's seat and context. A nil record finishes nothing.
func (g *Gateway) finishRecord(ctx context.Context, rec *ledgerRecord, capture *answerCapture) {
	if rec == nil {
		return
	}

	if capture.overflowed {
		g.logger.WarnContext(ctx,
			"ledger: the answer is longer than the capture keeps, so its usage is read from the part kept",
			slog.String("seat", rec.res.SeatID), slog.String("context", rec.res.Context.ID))
	}

	summary := Summarize(capture.kept, capture.Header().Get(headerContentType))
	finish := ledger.Finish{
		CompletedAt:       g.clock.Now().Unix(),
		ResolvedModel:     summary.ResolvedModel,
		InputTokens:       summary.InputTokens,
		OutputTokens:      summary.OutputTokens,
		CacheReadTokens:   summary.CacheReadTokens,
		CacheWriteTokens:  summary.CacheWriteTokens,
		BilledCostNanoUSD: BilledCost(rec.res.Seat, rec.res.Seat.AuthMode == llmroutes.AuthForward, summary),
		Claims:            UnifiedClaims(capture.Header()),
	}
	setListCosts(&finish, summary)

	if err := g.ledger.Finish(ctx, rec.id, finish); err != nil {
		g.logger.WarnContext(ctx, "ledger: finish failed",
			slog.String("seat", rec.res.SeatID), slog.String("context", rec.res.Context.ID), slog.Any("error", err))
	}
}

// setListCosts prices a request at its resolved model's list price, in the four token kinds, from the rate card. A
// model the card does not name has no list cost, so every one of the five costs stays nil.
func setListCosts(finish *ledger.Finish, summary Summary) {
	cost, ok := ratecard.Price(summary.ResolvedModel, ratecard.Usage{
		Input:        summary.InputTokens,
		Output:       summary.OutputTokens,
		CacheRead:    summary.CacheReadTokens,
		CacheWrite5m: summary.CacheWrite5mTokens,
		CacheWrite1h: summary.CacheWrite1hTokens,
	})
	if !ok {
		return
	}

	finish.ListCostNanoUSD = new(cost.Total())
	finish.ListInputCostNanoUSD = new(cost.Input)
	finish.ListOutputCostNanoUSD = new(cost.Output)
	finish.ListCacheReadCostNanoUSD = new(cost.CacheRead)
	finish.ListCacheWriteCostNanoUSD = new(cost.CacheWrite)
}

// answerCapture is the ResponseWriter a seat answers a request through. It sends every byte on to the caller, and keeps
// the bytes the caller was sent, so that the answer's usage and claims can be read once it completes.
type answerCapture struct {
	http.ResponseWriter

	// kept is the answer's first bytes, up to answerCaptureBytes. overflowed reports that some of the answer was not
	// kept.
	kept       []byte
	overflowed bool
}

// Write sends chunk to the caller and keeps the bytes the caller was sent.
func (c *answerCapture) Write(chunk []byte) (int, error) {
	n, err := c.ResponseWriter.Write(chunk)
	c.keep(chunk[:n])
	if err != nil {
		return n, fmt.Errorf("send the answer to the caller: %w", err)
	}

	return n, nil
}

// Flush flushes the caller's writer when it can be flushed, so that a streamed answer reaches the caller as it is
// produced.
func (c *answerCapture) Flush() {
	if flusher, ok := c.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// keep appends chunk to the kept answer. It stops at answerCaptureBytes, and marks the capture overflowed when chunk
// does not fit.
func (c *answerCapture) keep(chunk []byte) {
	room := max(answerCaptureBytes-len(c.kept), 0)
	if len(chunk) > room {
		c.overflowed = true
		chunk = chunk[:room]
	}
	c.kept = append(c.kept, chunk...)
}
