// Package probe answers "is this port actually in use, and by whom?".
// Two independent mechanisms, degrading gracefully:
//
//   - procscan.go (this file) parses <root>/net/tcp and <root>/net/tcp6
//     for LISTEN sockets and maps socket inodes to PIDs and process names
//     via <root>/<pid>/fd. The root is injectable, so tests run against
//     fabricated fixture trees and never touch the real /proc.
//   - live.go binds 127.0.0.1 to detect occupancy even where procfs is
//     unavailable or unreadable.
package probe

import (
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultProcRoot is where the kernel exposes procfs on Linux.
const DefaultProcRoot = "/proc"

// Listener is one live listening TCP socket. PID is 0 and Process empty
// when inode-to-PID resolution was not possible (foreign user, no
// procfs); the port and address are still reported.
type Listener struct {
	Port    int    `json:"port"`
	Addr    string `json:"addr"` // local bind address, e.g. "127.0.0.1" or "::"
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	inode   string
}

// tcpListenState is the kernel's st column value for LISTEN.
const tcpListenState = "0A"

// Snapshot parses the procfs tree under root and returns every listening
// TCP socket, IPv4 and IPv6, with best-effort process attribution. A
// missing or unreadable tree yields an empty slice and no error: on
// non-Linux hosts portberth simply has less provenance to offer.
func Snapshot(root string) []Listener {
	var out []Listener
	inodeIdx := map[string][]int{} // inode -> indexes in out
	for _, name := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(root, "net", name))
		if err != nil {
			continue
		}
		for _, l := range parseTCPTable(string(data)) {
			inodeIdx[l.inode] = append(inodeIdx[l.inode], len(out))
			out = append(out, l)
		}
	}
	if len(out) > 0 {
		resolvePIDs(root, inodeIdx, out)
	}
	return out
}

// OnPort filters a snapshot down to one port.
func OnPort(listeners []Listener, port int) []Listener {
	var out []Listener
	for _, l := range listeners {
		if l.Port == port {
			out = append(out, l)
		}
	}
	return out
}

// parseTCPTable extracts LISTEN rows from the text of /proc/net/tcp or
// /proc/net/tcp6. Malformed lines are skipped, never fatal — the kernel
// format is stable but we refuse to crash on surprises.
func parseTCPTable(text string) []Listener {
	var out []Listener
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 { // header row
			continue
		}
		fields := strings.Fields(line)
		// sl local rem st tx_rx tr_tm retrnsmt uid timeout inode ...
		if len(fields) < 10 || fields[3] != tcpListenState {
			continue
		}
		addr, port, ok := parseHexAddr(fields[1])
		if !ok {
			continue
		}
		out = append(out, Listener{Port: port, Addr: addr, inode: fields[9]})
	}
	return out
}

// parseHexAddr decodes the kernel's "ADDR:PORT" hex encoding. The port is
// big-endian hex; the address is one (IPv4) or four (IPv6) 32-bit words,
// each in host byte order (little-endian on every platform Go supports
// for Linux procfs consumers).
func parseHexAddr(s string) (addr string, port int, ok bool) {
	hexAddr, hexPort, found := strings.Cut(s, ":")
	if !found {
		return "", 0, false
	}
	p, err := strconv.ParseUint(hexPort, 16, 32)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, false
	}
	raw, err := hex.DecodeString(hexAddr)
	if err != nil {
		return "", 0, false
	}
	switch len(raw) {
	case 4, 16:
		ip := make(net.IP, len(raw))
		// Reverse each 4-byte word to undo the kernel's per-word
		// little-endian rendering.
		for w := 0; w < len(raw); w += 4 {
			ip[w], ip[w+1], ip[w+2], ip[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
		}
		return ip.String(), int(p), true
	default:
		return "", 0, false
	}
}

// resolvePIDs walks <root>/<pid>/fd looking for "socket:[inode]" links
// that match the snapshot's inodes, then reads <root>/<pid>/comm for the
// process name. Permission errors are expected (other users' processes)
// and silently skipped.
func resolvePIDs(root string, inodeIdx map[string][]int, out []Listener) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join(root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok {
				continue
			}
			idxs, ok := inodeIdx[inode]
			if !ok {
				continue
			}
			name := readComm(root, e.Name())
			for _, i := range idxs {
				if out[i].PID == 0 {
					out[i].PID = pid
					out[i].Process = name
				}
			}
		}
	}
}

// socketInode extracts N from "socket:[N]".
func socketInode(target string) (string, bool) {
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return "", false
	}
	return target[len("socket:[") : len(target)-1], true
}

// readComm returns the short process name, or "" when unreadable.
func readComm(root, pid string) string {
	data, err := os.ReadFile(filepath.Join(root, pid, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
