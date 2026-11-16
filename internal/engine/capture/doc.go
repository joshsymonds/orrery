// Package capture runs the fixed stream-json probes that record how Claude Code behaves under control. It declares
// the probes (Probes, SettingsProbes) and builds each one's isolated command (NewCommand): a fixed argument vector, an
// environment limited to an allow-list, a fresh empty working directory, and a timeout. Run plays a probe's script over
// that command, Sanitiser makes the recordings safe for a public repository, Check reports the expectations a
// recording fails and the message types the wire package does not declare, and Decide answers the permission requests
// a probe receives. DaemonCommand and NewDaemon build the command in the form orreryd starts a session, and Main is the
// enginecapture CLI.
package capture
