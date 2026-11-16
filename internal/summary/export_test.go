package summary

// InFlight returns how many runs s has started that have not finished. Request decides under s.mu whether a request
// starts or waits, so the count is exact once Request returns.
func (s *Summarizer) InFlight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}
