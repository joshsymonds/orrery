// Package ratecard prices an LLM request at list price, from the rate card embedded in claude.json. A card entry gives
// a model's price for each token kind in nano-USD per million tokens, and the entry names the page its prices came
// from. Prices are integers throughout, so a request's cost is exact to the nano-USD and never a float.
package ratecard

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// claudeCard is the Claude rate card, a JSON array of entries.
//
//go:embed claude.json
var claudeCard []byte

// Usage is one request's tokens by kind. CacheWrite5m and CacheWrite1h are the cache writes at the 5-minute and the
// 1-hour price.
type Usage struct {
	Input, Output, CacheRead, CacheWrite5m, CacheWrite1h int64
}

// Cost is one request's list cost by kind, in nano-USD. CacheWrite is the 5-minute and the 1-hour writes, each priced
// at its own rate and summed.
type Cost struct {
	Input, Output, CacheRead, CacheWrite int64
}

// Total returns the request's list cost, the sum of its four kinds.
func (c Cost) Total() int64 {
	return c.Input + c.Output + c.CacheRead + c.CacheWrite
}

// entry is one model's record in claude.json. Each rate is nano-USD per million tokens.
type entry struct {
	Model            string `json:"model"`
	Source           string `json:"source"`
	InputRate        int64  `json:"inputNanoUsdPerMtok"`
	OutputRate       int64  `json:"outputNanoUsdPerMtok"`
	CacheReadRate    int64  `json:"cacheReadNanoUsdPerMtok"`
	CacheWrite5mRate int64  `json:"cacheWrite5mNanoUsdPerMtok"`
	CacheWrite1hRate int64  `json:"cacheWrite1hNanoUsdPerMtok"`
}

// rate is one model's price for each token kind, in nano-USD per million tokens.
type rate struct {
	input, output, cacheRead, cacheWrite5m, cacheWrite1h int64
}

// loadCard parses a rate card and returns its rates by model. It fails for a card that does not parse, or that names
// a model twice.
func loadCard(data []byte) (map[string]rate, error) {
	var items []entry
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse rate card: %w", err)
	}
	rates := make(map[string]rate, len(items))
	for _, item := range items {
		if _, dup := rates[item.Model]; dup {
			return nil, fmt.Errorf("rate card names model %q twice", item.Model)
		}
		rates[item.Model] = rate{
			input:        item.InputRate,
			output:       item.OutputRate,
			cacheRead:    item.CacheReadRate,
			cacheWrite5m: item.CacheWrite5mRate,
			cacheWrite1h: item.CacheWrite1hRate,
		}
	}
	return rates, nil
}

// Price returns the list cost of usage at model's list price. It returns false, and a zero Cost, for a model the card
// does not name, so an unknown model has no list cost rather than a zero one. The embedded card is parsed on each call;
// it is small, and TestGatewayListPriceCardLoads fails the build's tests if it does not load, so a broken card never
// prices a request as unknown without a failing test.
func Price(model string, usage Usage) (Cost, bool) {
	rates, err := loadCard(claudeCard)
	if err != nil {
		return Cost{}, false
	}
	card, ok := rates[model]
	if !ok {
		return Cost{}, false
	}
	return Cost{
		Input:      perMTok(usage.Input, card.input),
		Output:     perMTok(usage.Output, card.output),
		CacheRead:  perMTok(usage.CacheRead, card.cacheRead),
		CacheWrite: perMTok(usage.CacheWrite5m, card.cacheWrite5m) + perMTok(usage.CacheWrite1h, card.cacheWrite1h),
	}, true
}

// perMTok returns the cost of tokens at rate nano-USD per million tokens, rounded down to a whole nano-USD. It splits
// tokens into whole millions and a remainder, so the product stays within int64 for any token count a request reports.
func perMTok(tokens, rate int64) int64 {
	return tokens/1_000_000*rate + tokens%1_000_000*rate/1_000_000
}
