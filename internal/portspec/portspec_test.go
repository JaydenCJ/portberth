// Tests for the CLI grammar: spec parsing, name validation, port and
// range parsing, and env-var name derivation. These rules are the
// registry's key space, so they are pinned tightly.
package portspec

import (
	"strings"
	"testing"
)

func TestParseSpecBareProjectGetsDefaultService(t *testing.T) {
	s, err := ParseSpec("myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Project != "myapp" || s.Service != DefaultService {
		t.Fatalf("got %+v, want myapp/%s", s, DefaultService)
	}
}

func TestParseSpecProjectAndService(t *testing.T) {
	s, err := ParseSpec("shop/api")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Project != "shop" || s.Service != "api" {
		t.Fatalf("got %+v, want shop/api", s)
	}
}

func TestParseSpecNormalizesToLowercase(t *testing.T) {
	// Users type MyApp; the registry key must stay canonical so the same
	// project never receives two different ports by casing accident.
	s, err := ParseSpec("MyApp/Web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Project != "myapp" || s.Service != "web" {
		t.Fatalf("got %+v, want lowercase myapp/web", s)
	}
}

func TestParseSpecRejectsMalformedInputs(t *testing.T) {
	// Empty, nested slashes, whitespace, and non-ASCII are all refused so
	// registry keys can never be ambiguous.
	for _, raw := range []string{"", "a/b/c", "my app", "app!", "sp√©c", "a\tb"} {
		if _, err := ParseSpec(raw); err == nil {
			t.Errorf("spec %q should be rejected", raw)
		}
	}
}

func TestParseSpecRejectsLeadingPunctuation(t *testing.T) {
	for _, raw := range []string{"-app", ".app", "_app", "app/-svc"} {
		if _, err := ParseSpec(raw); err == nil {
			t.Errorf("spec %q should be rejected: leading punctuation", raw)
		}
	}
}

func TestParseSpecAllowsInnerPunctuation(t *testing.T) {
	s, err := ParseSpec("my-app.v2_final/web-ui")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Project != "my-app.v2_final" || s.Service != "web-ui" {
		t.Fatalf("got %+v", s)
	}
}

func TestParseSpecRejectsOverlongNames(t *testing.T) {
	long := strings.Repeat("a", MaxNameLen+1)
	if _, err := ParseSpec(long); err == nil {
		t.Fatal("overlong project name should be rejected")
	}
	if _, err := ParseSpec("ok/" + long); err == nil {
		t.Fatal("overlong service name should be rejected")
	}
}

func TestSpecStringRoundTrip(t *testing.T) {
	if got := (Spec{Project: "app", Service: DefaultService}).String(); got != "app" {
		t.Fatalf("default service should render bare, got %q", got)
	}
	if got := (Spec{Project: "app", Service: "api"}).String(); got != "app/api" {
		t.Fatalf("named service should render project/service, got %q", got)
	}
}

func TestParseRangeValid(t *testing.T) {
	r, err := ParseRange("3000-3999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Lo != 3000 || r.Hi != 3999 || r.Size() != 1000 {
		t.Fatalf("got %+v", r)
	}
	if !r.Contains(3000) || !r.Contains(3999) || r.Contains(4000) {
		t.Fatal("Contains is wrong at the boundaries")
	}
	// A single-port range is legal and useful for pinning.
	if single, err := ParseRange("8080-8080"); err != nil || single.Size() != 1 {
		t.Fatalf("single-port range: %+v err=%v", single, err)
	}
}

func TestParseRangeRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", "3000", "a-b", "0-100", "1-70000", "3000-", "4000-3000"} {
		if _, err := ParseRange(raw); err == nil {
			t.Errorf("range %q should be rejected", raw)
		}
	}
}

func TestParsePortBounds(t *testing.T) {
	if _, err := ParsePort("1"); err != nil {
		t.Errorf("port 1 should be valid: %v", err)
	}
	if _, err := ParsePort("65535"); err != nil {
		t.Errorf("port 65535 should be valid: %v", err)
	}
	for _, raw := range []string{"0", "65536", "-1", "http", ""} {
		if _, err := ParsePort(raw); err == nil {
			t.Errorf("port %q should be rejected", raw)
		}
	}
}

func TestEnvNameDerivation(t *testing.T) {
	if got := EnvName("myapp", DefaultService); got != "MYAPP_PORT" {
		t.Fatalf("got %q, want MYAPP_PORT", got)
	}
	if got := EnvName("my-app.v2", "web-ui"); got != "MY_APP_V2_WEB_UI_PORT" {
		t.Fatalf("got %q, want MY_APP_V2_WEB_UI_PORT", got)
	}
}
