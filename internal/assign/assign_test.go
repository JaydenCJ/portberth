// Tests for the deterministic port picker — the heart of portberth's
// stability promise. Every test here is pure computation.
package assign

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/portberth/internal/portspec"
)

var testRange = portspec.Range{Lo: 3000, Hi: 3999}

func spec(project, service string) portspec.Spec {
	return portspec.Spec{Project: project, Service: service}
}

func TestPreferredIsDeterministic(t *testing.T) {
	a := Preferred(spec("shop", "web"), testRange)
	b := Preferred(spec("shop", "web"), testRange)
	if a != b {
		t.Fatalf("same spec produced %d then %d", a, b)
	}
}

func TestPreferredStaysInRangeForManySpecs(t *testing.T) {
	names := []string{"a", "shop", "blog", "api-gateway", "my-app.v2", "x_1", "frontend", "backend"}
	for _, p := range names {
		for _, s := range []string{"default", "web", "api", "db"} {
			got := Preferred(spec(p, s), testRange)
			if !testRange.Contains(got) {
				t.Fatalf("Preferred(%s/%s) = %d, outside %s", p, s, got, testRange)
			}
		}
	}
}

func TestPreferredDiffersAcrossServicesOfOneProject(t *testing.T) {
	// Not guaranteed in general (pigeonhole), but pinned for these names:
	// a regression here means the hash key stopped including the service.
	web := Preferred(spec("shop", "web"), testRange)
	api := Preferred(spec("shop", "api"), testRange)
	if web == api {
		t.Fatalf("shop/web and shop/api both hashed to %d", web)
	}
}

func TestPickReturnsPreferredWhenFree(t *testing.T) {
	s := spec("shop", "web")
	got, err := Pick(s, testRange, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := Preferred(s, testRange); got != want {
		t.Fatalf("Pick = %d, want preferred %d", got, want)
	}
}

func TestPickSkipsTakenPortsAndProbesForward(t *testing.T) {
	s := spec("shop", "web")
	start := Preferred(s, testRange)
	taken := map[int]bool{start: true, start + 1: true}
	got, err := Pick(s, testRange, taken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != start+2 {
		t.Fatalf("Pick = %d, want %d (linear probe past two taken ports)", got, start+2)
	}
}

func TestPickWrapsAroundRangeEnd(t *testing.T) {
	// Force the walk to fall off the top of the range and wrap to Lo.
	s := spec("shop", "web")
	start := Preferred(s, testRange)
	taken := map[int]bool{}
	for p := start; p <= testRange.Hi; p++ {
		taken[p] = true
	}
	got, err := Pick(s, testRange, taken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != testRange.Lo {
		t.Fatalf("Pick = %d, want wrap to %d", got, testRange.Lo)
	}
}

func TestPickHonorsAvoidFunc(t *testing.T) {
	s := spec("shop", "web")
	start := Preferred(s, testRange)
	avoid := func(p int) bool { return p == start }
	got, err := Pick(s, testRange, nil, avoid)
	if err != nil {
		t.Fatal(err)
	}
	if got != start+1 {
		t.Fatalf("Pick = %d, want %d (avoided preferred)", got, start+1)
	}
}

func TestPickErrorsWhenRangeExhausted(t *testing.T) {
	rng := portspec.Range{Lo: 4100, Hi: 4102}
	taken := map[int]bool{4100: true, 4101: true, 4102: true}
	_, err := Pick(spec("shop", "web"), rng, taken, nil)
	if err == nil {
		t.Fatal("exhausted range must error, not loop or return junk")
	}
	if !strings.Contains(err.Error(), "no free port in range 4100-4102") {
		t.Fatalf("error should name the range: %v", err)
	}
}

func TestPickStableWhenUnrelatedPortsAreTaken(t *testing.T) {
	// The stability promise: reservations for other projects must not
	// move this spec's port as long as its own slot stays free.
	s := spec("shop", "web")
	mine := Preferred(s, testRange)
	taken := map[int]bool{}
	for p := testRange.Lo; p < testRange.Lo+50; p++ {
		if p != mine {
			taken[p] = true
		}
	}
	got, err := Pick(s, testRange, taken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != mine {
		t.Fatalf("Pick = %d, want %d despite unrelated churn", got, mine)
	}
}
