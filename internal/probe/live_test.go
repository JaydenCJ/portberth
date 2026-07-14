// Tests for the loopback bind probe. These bind an ephemeral port on
// 127.0.0.1 only — purely local, no packets leave the machine.
package probe

import (
	"net"
	"testing"
)

func TestPortInUseDetectsALiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // kernel picks a free port
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if !PortInUse(port) {
		t.Fatalf("port %d has a live listener but PortInUse said free", port)
	}
}

func TestPortInUseReportsFreedPortAsFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if PortInUse(port) {
		t.Fatalf("port %d was released but PortInUse said busy", port)
	}
}
