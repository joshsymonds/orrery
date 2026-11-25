package gateway

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// The stream event types the summary is read from, and the framing a server-sent event uses.
const (
	eventMessageStart = "message_start"
	eventMessageDelta = "message_delta"
	eventMessageStop  = "message_stop"
	sseDataPrefix     = "data:"
	sseDone           = "[DONE]"
	eventStreamType   = "text/event-stream"
)

// nanoUSDExponent is the power of ten between one US dollar and one nano US dollar.
const nanoUSDExponent = 9

// maxInt64Digits is the most decimal digits an int64 can hold.
const maxInt64Digits = 19

// Summary is what a captured Messages answer tells the ledger: the model that served it, its token counts, the cost
// its upstream reported, and whether the whole answer was read.
type Summary struct {
	// ResolvedModel is the model the answer says served it, or empty when it names none.
	ResolvedModel string

	// UsagePresent reports whether the answer returned a usage object at all.
	UsagePresent bool

	// InputTokens, OutputTokens and CacheReadTokens are the counts the answer reported, zero when it reported none.
	InputTokens     int64
	OutputTokens    int64
	CacheReadTokens int64

	// CacheWrite5mTokens and CacheWrite1hTokens split the cache writes by their time to live.
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64

	// CacheWriteTokens is every cache write. When the answer splits its writes it is the 5-minute and 1-hour writes
	// together; otherwise it is cache_creation_input_tokens.
	CacheWriteTokens int64

	// ProviderCostNanoUSD is the cost the upstream reported in its usage, in nano US dollars. It is nil when the usage
	// reported no cost, or reported one that is not a nonnegative JSON number.
	ProviderCostNanoUSD *int64

	// Complete reports whether the whole answer was read. It is false for a body that is not an answer, a stream that
	// did not run from message_start to message_stop, and any field that does not decode.
	Complete bool
}

// Summarize reads a captured Messages answer. A body whose content type is an event stream is read as the
// server-sent events of a stream, and any other body as one JSON answer. Summarize reports what it can read and
// nothing it cannot, and it never fails: a body that is not an answer yields an empty Summary with Complete false.
func Summarize(body []byte, contentType string) Summary {
	if strings.Contains(strings.ToLower(contentType), eventStreamType) {
		return summaryOf(readStream(body))
	}

	return summaryOf(readMessage(body))
}

// answer accumulates what one answer reports. Usage objects merge into it, and the latest figure a field reports wins,
// since a stream revises its output count as it goes and only the final figure is the real one.
type answer struct {
	model        string
	usagePresent bool
	usage        usageReport
	complete     bool
}

// usageReport is the accounting one usage object reports. A nil count is one the object omits.
type usageReport struct {
	input         *int64
	output        *int64
	cacheRead     *int64
	cacheCreation *int64
	cache5m       *int64
	cache1h       *int64

	// costGiven reports whether the object carries a cost. cost is that cost in nano US dollars, and it is nil when
	// the cost is not a nonnegative JSON number.
	costGiven bool
	cost      *int64
}

// lifecycle tracks where a stream is in its message: started by message_start, and stopped by message_stop.
type lifecycle struct {
	started bool
	stopped bool
}

// readMessage reads a complete JSON answer.
func readMessage(body []byte) answer {
	fields, ok := decodeObject(body)
	if !ok {
		return answer{}
	}

	result := answer{complete: true}
	result.absorb(fields)

	return result
}

// readStream reads a captured event stream in order. Each data line is one event: its type says whether it opens the
// message, revises its usage, or closes it, and any other event is skipped.
func readStream(body []byte) answer {
	result := answer{complete: true}
	state := lifecycle{}

	for line := range bytes.Lines(body) {
		payload, isData := bytes.CutPrefix(bytes.TrimRight(line, "\r\n"), []byte(sseDataPrefix))
		if !isData {
			continue
		}

		payload = bytes.TrimSpace(payload)
		if bytes.Equal(payload, []byte(sseDone)) {
			continue
		}

		fields, ok := decodeObject(payload)
		if !ok {
			result.complete = false

			continue
		}

		eventType, ok := decodeString(fields["type"])
		if !ok {
			result.complete = false

			continue
		}

		result.applyEvent(eventType, fields, &state)
	}

	if !state.stopped {
		result.complete = false
	}

	return result
}

// applyEvent folds one stream event into the answer. An event out of the message's lifecycle marks the answer
// incomplete, but whatever it decodes is still kept.
func (a *answer) applyEvent(eventType string, fields map[string]json.RawMessage, state *lifecycle) {
	switch eventType {
	case eventMessageStart:
		if state.started || state.stopped {
			a.complete = false
		}

		state.started = true

		message, ok := decodeObject(fields["message"])
		if !ok {
			a.complete = false

			return
		}

		a.absorb(message)
	case eventMessageDelta:
		if !state.started || state.stopped {
			a.complete = false
		}

		a.absorb(fields)
	case eventMessageStop:
		if !state.started || state.stopped {
			a.complete = false
		}

		state.stopped = true
	}
}

// absorb folds the model and usage an object carries into the answer.
func (a *answer) absorb(fields map[string]json.RawMessage) {
	if raw, found := fields["model"]; found && !isNull(raw) {
		model, ok := decodeString(raw)
		if !ok {
			a.complete = false
		} else {
			a.model = model
		}
	}

	if raw, found := fields["usage"]; found && !isNull(raw) {
		a.usagePresent = true

		report, ok := decodeUsage(raw)
		a.usage.merge(report)
		if !ok {
			a.complete = false
		}
	}
}

// summaryOf turns what an answer accumulated into its Summary. The cache writes are the 5-minute and 1-hour writes
// when the answer splits them, and cache_creation_input_tokens when it does not.
func summaryOf(result answer) Summary {
	report := result.usage

	cacheWrites := valueOf(report.cacheCreation)
	if report.cache5m != nil || report.cache1h != nil {
		cacheWrites = valueOf(report.cache5m) + valueOf(report.cache1h)
	}

	return Summary{
		ResolvedModel:       result.model,
		UsagePresent:        result.usagePresent,
		InputTokens:         valueOf(report.input),
		OutputTokens:        valueOf(report.output),
		CacheReadTokens:     valueOf(report.cacheRead),
		CacheWrite5mTokens:  valueOf(report.cache5m),
		CacheWrite1hTokens:  valueOf(report.cache1h),
		CacheWriteTokens:    cacheWrites,
		ProviderCostNanoUSD: report.cost,
		Complete:            result.complete,
	}
}

// merge folds a later usage object into the report: each count it gives replaces the earlier one, and so does a cost.
func (r *usageReport) merge(next usageReport) {
	r.input = keepLatest(r.input, next.input)
	r.output = keepLatest(r.output, next.output)
	r.cacheRead = keepLatest(r.cacheRead, next.cacheRead)
	r.cacheCreation = keepLatest(r.cacheCreation, next.cacheCreation)
	r.cache5m = keepLatest(r.cache5m, next.cache5m)
	r.cache1h = keepLatest(r.cache1h, next.cache1h)

	if next.costGiven {
		r.cost = next.cost
	}
}

// decodeUsage decodes the accounting fields of one usage object. It reports false when any of them does not decode,
// and keeps the ones that did.
func decodeUsage(raw json.RawMessage) (usageReport, bool) {
	fields, ok := decodeObject(raw)
	if !ok {
		return usageReport{}, false
	}

	var report usageReport
	valid := true

	counts := []struct {
		name   string
		target **int64
	}{
		{name: "input_tokens", target: &report.input},
		{name: "output_tokens", target: &report.output},
		{name: "cache_read_input_tokens", target: &report.cacheRead},
		{name: "cache_creation_input_tokens", target: &report.cacheCreation},
	}
	for _, count := range counts {
		value, countOK := decodeToken(fields, count.name)
		*count.target = value
		valid = valid && countOK
	}

	if creation, found := fields["cache_creation"]; found && !isNull(creation) {
		fiveMinute, oneHour, splitOK := decodeCacheCreation(creation)
		report.cache5m = fiveMinute
		report.cache1h = oneHour
		valid = valid && splitOK
	}

	if rawCost, found := fields["cost"]; found && !isNull(rawCost) {
		report.costGiven = true

		cost, costOK := parseNanoUSD(string(bytes.TrimSpace(rawCost)))
		if costOK {
			report.cost = &cost
		}
		valid = valid && costOK
	}

	return report, valid
}

// decodeCacheCreation decodes the 5-minute and 1-hour cache writes of a cache_creation object.
func decodeCacheCreation(raw json.RawMessage) (*int64, *int64, bool) {
	fields, ok := decodeObject(raw)
	if !ok {
		return nil, nil, false
	}

	fiveMinute, fiveMinuteOK := decodeToken(fields, "ephemeral_5m_input_tokens")
	oneHour, oneHourOK := decodeToken(fields, "ephemeral_1h_input_tokens")

	return fiveMinute, oneHour, fiveMinuteOK && oneHourOK
}

// decodeToken decodes one token count. An omitted or null count is absent, which is not an error, and a count that is
// not a nonnegative integer is an error.
func decodeToken(fields map[string]json.RawMessage, name string) (*int64, bool) {
	raw, found := fields[name]
	if !found || isNull(raw) {
		return nil, true
	}

	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value < 0 {
		return nil, false
	}

	return &value, true
}

// parseNanoUSD rounds a nonnegative JSON number of US dollars to whole nano US dollars, half to even. It reads the
// number as text and never as a float, so no amount is rounded by floating-point error. It reports false for a value
// that is not a nonnegative number, or that does not fit an int64 of nano US dollars.
func parseNanoUSD(text string) (int64, bool) {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(text), "e")
	whole, fraction, hasPoint := strings.Cut(mantissa, ".")
	if !isDigits(whole) || (hasPoint && !isDigits(fraction)) || (len(whole) > 1 && whole[0] == '0') {
		return 0, false
	}

	// The amount is the digits times ten to the power, in US dollars. Nano US dollars are a further factor of 10^9.
	power := big.NewInt(nanoUSDExponent - int64(len(fraction)))
	if hasExponent {
		exponent, ok := parseExponent(exponentText)
		if !ok {
			return 0, false
		}

		power.Add(power, exponent)
	}

	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return 0, true
	}

	return scaleNanoUSD(digits, power)
}

// parseExponent reads the signed decimal exponent of a JSON number.
func parseExponent(text string) (*big.Int, bool) {
	digits, negative := text, false
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		digits, negative = text[1:], text[0] == '-'
	}

	if !isDigits(digits) {
		return nil, false
	}

	exponent, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}

	if negative {
		exponent.Neg(exponent)
	}

	return exponent, true
}

// scaleNanoUSD returns digits times ten to the power, rounded half to even to an int64. Only the digits that can change
// the result are read, so an exponent of any size costs nothing to read.
func scaleNanoUSD(digits string, power *big.Int) (int64, bool) {
	if power.Sign() >= 0 {
		if !power.IsInt64() || power.Int64() > maxInt64Digits || int64(len(digits))+power.Int64() > maxInt64Digits {
			return 0, false
		}

		return parseInt64(digits + strings.Repeat("0", int(power.Int64())))
	}

	dropCount := new(big.Int).Neg(power)
	if !dropCount.IsInt64() || dropCount.Int64() > int64(len(digits)) {
		// Every digit is dropped, and what is left is under one tenth, which rounds to zero.
		return 0, true
	}

	keep := len(digits) - int(dropCount.Int64())

	kept := int64(0)
	if keep > 0 {
		value, ok := parseInt64(digits[:keep])
		if !ok {
			return 0, false
		}

		kept = value
	}

	if !roundsUp(digits[keep:], kept) {
		return kept, true
	}

	if kept == math.MaxInt64 {
		return 0, false
	}

	return kept + 1, true
}

// roundsUp reports whether the dropped digits of a half-even rounding round the kept amount up.
func roundsUp(dropped string, kept int64) bool {
	switch {
	case dropped[0] > '5':
		return true
	case dropped[0] < '5':
		return false
	case strings.Trim(dropped[1:], "0") != "":
		return true
	default:
		return kept%2 == 1
	}
}

// parseInt64 reads a decimal int64, and reports false when the text is not one.
func parseInt64(text string) (int64, bool) {
	value, err := strconv.ParseInt(text, 10, 64)

	return value, err == nil
}

// isDigits reports whether text is one or more ASCII decimal digits.
func isDigits(text string) bool {
	if text == "" {
		return false
	}

	for index := range len(text) {
		if text[index] < '0' || text[index] > '9' {
			return false
		}
	}

	return true
}

// keepLatest returns next when it is present, and previous otherwise.
func keepLatest(previous, next *int64) *int64 {
	if next != nil {
		return next
	}

	return previous
}

// valueOf returns the count a pointer holds, or zero when it is absent.
func valueOf(count *int64) int64 {
	if count == nil {
		return 0
	}

	return *count
}

// decodeObject decodes exactly one JSON object, and reports false for anything else, null included.
func decodeObject(raw []byte) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, false
	}

	return fields, true
}

// decodeString decodes a JSON string, and reports false for null and for every other shape.
func decodeString(raw json.RawMessage) (string, bool) {
	if isNull(raw) {
		return "", false
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}

	return value, true
}

// isNull reports whether raw is the JSON null, ignoring surrounding whitespace.
func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
