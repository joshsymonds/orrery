package holder

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// PeerUID returns the uid of the process at the other end of conn. The kernel records it when the
// connection is made, so a caller can refuse a peer that is not its own user.
func PeerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peer uid: %w", err)
	}

	var cred *unix.Xucred
	var sockErr error
	ctlErr := raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if ctlErr != nil {
		return 0, fmt.Errorf("peer uid: %w", ctlErr)
	}
	if sockErr != nil {
		return 0, fmt.Errorf("peer uid: %w", sockErr)
	}
	return int(cred.Uid), nil
}
