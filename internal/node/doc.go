// Package node reaches a session's holder on another of the owner's machines through that machine's agent. The protocol
// is node.v1, spoken over the tailnet. A controller dials an agent and sends one Request line, and the agent answers
// with one Response line. The attach op then turns the connection into a byte splice onto the session holder's socket,
// which carries holder frames unchanged.
//
// The package has these parts:
//   - Version is the protocol version a Request names, and the one an agent serves.
//   - Agent and AgentConfig serve node.v1 on a machine that runs holders. NewAgent checks an AgentConfig, and Serve
//     admits a connection only when WhoIs names its peer and Authorize admits that peer.
//   - Client and ClientConfig are a holderhost.Host for one remote machine. NewClient takes the machine's name and
//     agent address, and each op waits for the agent's answer no longer than its bound.
//   - Authorize admits a peer whose WhoIs answer names the controller and its login.
//   - Tailnet returns the WhoIs of this machine's tailscaled, which Serve asks about each peer.
//   - HostPanes is the panes of one host, which an AgentConfig serves through the panes, watch-panes and load
//     ops. Load, PaneSnapshot, PaneTransition and PaneEvent are the values it reports.
//   - Client's Name, HTTPClient, Panes, WatchPanes and Load reach a remote host's HostPanes through those ops, and
//     PaneWatch is the stream of PaneEvents that WatchPanes opens.
//
// A session is never relaunched on another host, and internal/node never imports internal/enginehost.
package node
