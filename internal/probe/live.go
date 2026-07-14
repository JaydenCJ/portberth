package probe

import (
	"net"
	"strconv"
)

// PortInUse reports whether a TCP port is occupied on the loopback
// interface, by attempting to bind it and releasing immediately. This is
// the portable fallback when procfs is unavailable; it never sends a
// packet anywhere — binding 127.0.0.1 is a purely local operation.
//
// Limitation (documented): a server bound to a single non-loopback
// interface only is not detected. The procfs snapshot covers that case
// on Linux.
func PortInUse(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}
