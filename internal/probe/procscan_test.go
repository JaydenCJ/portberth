// Tests for the procfs scanner. Every test builds a fabricated proc tree
// in a temp dir — the real /proc is never read, so results are identical
// on any machine, Linux or not.
package probe

import (
	"os"
	"path/filepath"
	"testing"
)

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

// tcpRow fabricates one kernel-format row of /proc/net/tcp.
func tcpRow(local, state, inode string) string {
	return "   0: " + local + " 00000000:0000 " + state +
		" 00000000:00000000 00:00000000 00000000  1000        0 " + inode + " 1 0000000000000000 100 0 0 10 0\n"
}

// writeProcTree fabricates a proc root with the given net/tcp contents
// and optional processes (pid -> {comm, socket inode}).
func writeProcTree(t *testing.T, tcp, tcp6 string, procs map[string][2]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if tcp != "" {
		os.WriteFile(filepath.Join(root, "net", "tcp"), []byte(tcp), 0o644)
	}
	if tcp6 != "" {
		os.WriteFile(filepath.Join(root, "net", "tcp6"), []byte(tcp6), 0o644)
	}
	for pid, meta := range procs {
		fdDir := filepath.Join(root, pid, "fd")
		if err := os.MkdirAll(fdDir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(root, pid, "comm"), []byte(meta[0]+"\n"), 0o644)
		if meta[1] != "" {
			if err := os.Symlink("socket:["+meta[1]+"]", filepath.Join(fdDir, "3")); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestParseHexAddrIPv4(t *testing.T) {
	// 0100007F is 127.0.0.1 in the kernel's little-endian word encoding;
	// 0BB8 is port 3000.
	addr, port, ok := parseHexAddr("0100007F:0BB8")
	if !ok || addr != "127.0.0.1" || port != 3000 {
		t.Fatalf("got addr=%q port=%d ok=%v", addr, port, ok)
	}
	addr, port, ok = parseHexAddr("00000000:1F90")
	if !ok || addr != "0.0.0.0" || port != 8080 {
		t.Fatalf("unspecified: got addr=%q port=%d ok=%v", addr, port, ok)
	}
}

func TestParseHexAddrIPv6(t *testing.T) {
	addr, port, ok := parseHexAddr("00000000000000000000000001000000:0050")
	if !ok || addr != "::1" || port != 80 {
		t.Fatalf("got addr=%q port=%d ok=%v", addr, port, ok)
	}
	addr, port, ok = parseHexAddr("00000000000000000000000000000000:0BB9")
	if !ok || addr != "::" || port != 3001 {
		t.Fatalf("unspecified: got addr=%q port=%d ok=%v", addr, port, ok)
	}
}

func TestParseHexAddrRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", "0100007F", "0100007F:", "xyz:0050", "0100007F:zz", "01:0050", "0100007F:0000"} {
		if _, _, ok := parseHexAddr(raw); ok {
			t.Errorf("parseHexAddr(%q) should fail", raw)
		}
	}
}

func TestParseTCPTableKeepsListenersOnly(t *testing.T) {
	// State 01 is ESTABLISHED — an outbound connection, not a server.
	text := tcpHeader +
		tcpRow("0100007F:0BB8", "0A", "111") +
		tcpRow("0100007F:1F90", "01", "222")
	got := parseTCPTable(text)
	if len(got) != 1 || got[0].Port != 3000 {
		t.Fatalf("got %+v, want only the LISTEN row on 3000", got)
	}
}

func TestParseTCPTableSkipsMalformedLines(t *testing.T) {
	text := tcpHeader +
		"garbage line\n" +
		tcpRow("NOTHEX:0BB8", "0A", "111") +
		"\n" +
		tcpRow("0100007F:0FA0", "0A", "333")
	got := parseTCPTable(text)
	if len(got) != 1 || got[0].Port != 4000 {
		t.Fatalf("got %+v, want only the valid row on 4000", got)
	}
}

func TestSnapshotMissingRootIsEmptyNotFatal(t *testing.T) {
	// Non-Linux hosts have no procfs; portberth degrades, never crashes.
	got := Snapshot(filepath.Join(t.TempDir(), "no-such-proc"))
	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}
}

func TestSnapshotReadsIPv4AndIPv6Tables(t *testing.T) {
	root := writeProcTree(t,
		tcpHeader+tcpRow("0100007F:0BB8", "0A", "111"),
		tcpHeader+tcpRow("00000000000000000000000001000000:0FA0", "0A", "222"),
		nil)
	got := Snapshot(root)
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2: %+v", len(got), got)
	}
	if got[0].Port != 3000 || got[0].Addr != "127.0.0.1" {
		t.Fatalf("ipv4 row wrong: %+v", got[0])
	}
	if got[1].Port != 4000 || got[1].Addr != "::1" {
		t.Fatalf("ipv6 row wrong: %+v", got[1])
	}
	// OnPort narrows a snapshot to one port.
	if n := len(OnPort(got, 3000)); n != 1 {
		t.Fatalf("OnPort(3000) = %d, want 1", n)
	}
	if n := len(OnPort(got, 5000)); n != 0 {
		t.Fatalf("OnPort(5000) = %d, want 0", n)
	}
}

func TestSnapshotResolvesPIDAndProcessName(t *testing.T) {
	root := writeProcTree(t,
		tcpHeader+tcpRow("0100007F:0BB8", "0A", "424242"),
		"",
		map[string][2]string{"1234": {"node", "424242"}})
	got := Snapshot(root)
	if len(got) != 1 {
		t.Fatalf("got %d listeners, want 1", len(got))
	}
	if got[0].PID != 1234 || got[0].Process != "node" {
		t.Fatalf("attribution wrong: %+v", got[0])
	}
}

func TestSnapshotUnresolvedInodeLeavesPIDZero(t *testing.T) {
	// A socket owned by another user: fd dirs unreadable or absent. The
	// listener must still be reported, just without attribution.
	root := writeProcTree(t,
		tcpHeader+tcpRow("0100007F:0BB8", "0A", "424242"),
		"",
		map[string][2]string{"1234": {"node", "999999"}}) // different inode
	got := Snapshot(root)
	if len(got) != 1 {
		t.Fatalf("got %d listeners, want 1", len(got))
	}
	if got[0].PID != 0 || got[0].Process != "" {
		t.Fatalf("expected unattributed listener, got %+v", got[0])
	}
}

func TestSnapshotIgnoresNonNumericProcEntries(t *testing.T) {
	root := writeProcTree(t,
		tcpHeader+tcpRow("0100007F:0BB8", "0A", "111"),
		"",
		map[string][2]string{"1234": {"node", "111"}})
	// Directories like "self" or "sys" must not be treated as PIDs.
	os.MkdirAll(filepath.Join(root, "self", "fd"), 0o755)
	got := Snapshot(root)
	if len(got) != 1 || got[0].PID != 1234 {
		t.Fatalf("got %+v", got)
	}
}
