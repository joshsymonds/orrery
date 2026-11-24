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
	InputRate        int64  `json:"input_nano_usd_per_mtok"`
	OutputRate       int64  `json:"output_nano_usd_per_mtok"`
	CacheReadRate    int64  `json:"cache_read_nano_usd_per_mtok"`
	CacheWrite5mRate int64  `json:"cache_write_5m_nano_usd_per_mtok"`
	CacheWrite1hRate int64  `json:"cache_write_1h_nano_usd_per_mtok"`
}

// rate is one model's price for each token kind, in nano-USD per million tokens.
type rate struct {
	input, output, cacheRead, cacheWrite5m, cacheWrite1h int64
}

// cards holds the rate card by model. The card is embedded in the binary, so a card that does not parse, or that
// names a model twice, is a build error: the package panics at start rather than price from a broken card.
var cards = loadCard(claudeCard)

// loadCard parses a rate card and returns its rates by model.
func loadCard(data []byte) map[string]rate {
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		panic(fmt.Sprintf("ratecard: parse claude.json: %v", err))
	}
	rates := make(map[string]rate, len(entries))
	for _, e := range entries {
		if _, dup := rates[e.Model]; dup {
			panic(fmt.Sprintf("ratecard: claude.json names model %q twice", e.Model))
		}
		rates[e.Model] = rate{
			input:        e.InputRate,
			output:       e.OutputRate,
			cacheRead:    e.CacheReadRate,
			cacheWrite5m: e.CacheWrite5mRate,
			cacheWrite1h: e.CacheWrite1hRate,
		}
	}
	return rates
}

// Price returns the list cost of u at model's list price. It returns false, and a zero Cost, for a model the card does
// not name, so an unknown model has no list cost rather than a zero one.
func Price(model string, u Usage) (Cost, bool) {
	r, ok := cards[model]
	if !ok {
		return Cost{}, false
	}
	return Cost{
		Input:      perMTok(u.Input, r.input),
		Output:     perMTok(u.Output, r.output),
		CacheRead:  perMTok(u.CacheRead, r.cacheRead),
		CacheWrite: perMTok(u.CacheWrite5m, r.cacheWrite5m) + perMTok(u.CacheWrite1h, r.cacheWrite1h),
	}, true
}

// perMTok returns the cost of tokens at rate nano-USD per million tokens, rounded down to a whole nano-USD. It splits
// tokens into whole millions and a remainder, so the product stays within int64 for any token count a request reports.
func perMTok(tokens, rate int64) int64 {
	return tokens/1_000_000*rate + tokens%1_000_000*rate/1_000_000
}
