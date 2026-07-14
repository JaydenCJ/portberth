// Tests for the curated well-known port table.
package wellknown

import "testing"

func TestLookupKnownPorts(t *testing.T) {
	cases := map[int]string{
		5432:  "postgresql",
		5173:  "vite",
		6379:  "redis",
		3000:  "node-dev",
		27017: "mongodb",
	}
	for port, want := range cases {
		svc, ok := Lookup(port)
		if !ok || svc.Name != want {
			t.Errorf("Lookup(%d) = %+v ok=%v, want name %q", port, svc, ok, want)
		}
	}
}

func TestLookupUnknownPortMisses(t *testing.T) {
	if _, ok := Lookup(3742); ok {
		t.Fatal("3742 should not be well-known")
	}
}

func TestPortsAreSortedValidAndUnique(t *testing.T) {
	ports := Ports()
	if len(ports) < 30 {
		t.Fatalf("table suspiciously small: %d entries", len(ports))
	}
	for i, p := range ports {
		if p < 1 || p > 65535 {
			t.Errorf("port %d out of range", p)
		}
		if i > 0 && ports[i-1] >= p {
			t.Errorf("Ports() not strictly ascending at index %d (%d then %d)", i, ports[i-1], p)
		}
	}
}

func TestEveryEntryHasNameAndDescription(t *testing.T) {
	for _, p := range Ports() {
		svc, _ := Lookup(p)
		if svc.Name == "" || svc.Desc == "" {
			t.Errorf("port %d has an incomplete entry: %+v", p, svc)
		}
	}
}
