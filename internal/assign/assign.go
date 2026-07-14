// Package assign picks ports. The core promise of portberth is
// per-project stability: the same project/service asks for a port on any
// machine, on any day, and — absent collisions — gets the same answer.
// That is achieved by hashing the spec into the range and probing forward
// deterministically from the hashed starting point.
package assign

import (
	"fmt"
	"hash/fnv"

	"github.com/JaydenCJ/portberth/internal/portspec"
)

// AvoidFunc reports ports the picker must skip (well-known ports, live
// listeners, ...). It is composed by the caller so the picker itself
// stays pure and fully deterministic.
type AvoidFunc func(port int) bool

// Preferred returns the deterministic starting port for a spec within a
// range: an FNV-1a hash of the canonical "project/service" key, mapped
// into the range. Two different specs land on different starts with high
// probability; the same spec always lands on the same start.
func Preferred(s portspec.Spec, rng portspec.Range) int {
	h := fnv.New64a()
	// Hash the fully explicit key so "myapp" and "myapp/default" agree.
	h.Write([]byte(s.Project + "/" + s.Service))
	return rng.Lo + int(h.Sum64()%uint64(rng.Size()))
}

// Pick chooses a port for a spec: start at Preferred and walk forward
// (wrapping at the top of the range) until a port is neither taken in the
// registry nor rejected by avoid. Returns an error when the entire range
// is exhausted, so callers can suggest a wider range instead of looping
// forever.
func Pick(s portspec.Spec, rng portspec.Range, taken map[int]bool, avoid AvoidFunc) (int, error) {
	start := Preferred(s, rng)
	size := rng.Size()
	for i := 0; i < size; i++ {
		p := rng.Lo + (start-rng.Lo+i)%size
		if taken[p] {
			continue
		}
		if avoid != nil && avoid(p) {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free port in range %s (%d ports, all taken or avoided); try --range with a wider span", rng, size)
}
