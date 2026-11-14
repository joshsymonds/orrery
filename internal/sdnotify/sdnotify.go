// Package sdnotify sends systemd's readiness notification to the socket named by $NOTIFY_SOCKET, as sd_notify(3)
// does, without linking the go-systemd module.
package sdnotify

import (
	"errors"
	"fmt"
	"net"
)

// Ready sends one READY=1 datagram to addr, a unixgram socket path. An addr starting with '@' names an abstract
// socket, so its '@' is sent as the leading NUL byte. Ready returns an error if addr is empty or the send fails.
func Ready(addr string) (err error) {
	if addr == "" {
		return errors.New("sdnotify: empty socket address")
	}
	name := addr
	if addr[0] == '@' {
		name = "\x00" + addr[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("sdnotify: dial %s: %w", addr, err)
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if _, err = conn.Write([]byte("READY=1")); err != nil {
		return fmt.Errorf("sdnotify: send READY=1 to %s: %w", addr, err)
	}
	return nil
}
