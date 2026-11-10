// Package session runs headless Claude Code sessions for orreryd. A Session starts one claude process
// that speaks the stream-json protocol, writes prompts and interrupts to it, and delivers what the
// process reports as Events, in the order the process wrote them. When the process ends, the last
// event is Exited, carrying the exit error, the stream error and the stderr of the process.
//
// Control requests from claude that wait on the host arrive as Request events, each with the kind of
// request it is. A request the package cannot answer arrives as a Refused request, which the host must
// not answer. A consumer that stops reading Events pauses only its own session.
//
// Unverified: no recording holds the answer shapes beyond one question with one chosen option. Several
// questions, multi-select questions and free-text answers have no recorded shape. The error control
// response used to refuse a request has no recording either.
package session
