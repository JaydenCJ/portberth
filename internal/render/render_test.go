// Tests for the shared output helpers: JSON envelope shape and text
// table alignment.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONEnvelopeShape(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, map[string]int{"port": 3000}); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Tool          string         `json:"tool"`
		Version       string         `json:"version"`
		SchemaVersion int            `json:"schema_version"`
		Data          map[string]int `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if env.Tool != "portberth" || env.SchemaVersion != JSONSchemaVersion || env.Version == "" {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if env.Data["port"] != 3000 {
		t.Fatalf("payload lost: %+v", env.Data)
	}
}

func TestJSONIsIndentedAndNewlineTerminated(t *testing.T) {
	var buf bytes.Buffer
	JSON(&buf, struct{}{})
	out := buf.String()
	if !strings.Contains(out, "\n  \"tool\"") {
		t.Fatalf("expected two-space indentation:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatal("JSON output must end with a newline")
	}
}

func TestTableAlignsColumns(t *testing.T) {
	var buf bytes.Buffer
	Table(&buf, [][]string{
		{"PROJECT", "PORT"},
		{"a", "3000"},
		{"longname", "80"},
	}, nil)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines", len(lines))
	}
	// Column two must start at the same offset on every line.
	idx := strings.Index(lines[0], "PORT")
	if strings.Index(lines[1], "3000") != idx {
		t.Fatalf("column misaligned:\n%s", buf.String())
	}
}

func TestTableRightAlignsMarkedColumns(t *testing.T) {
	var buf bytes.Buffer
	Table(&buf, [][]string{
		{"PORT", "X"},
		{"80", "y"},
	}, map[int]bool{0: true})
	lines := strings.Split(buf.String(), "\n")
	if !strings.HasPrefix(lines[1], "  80") {
		t.Fatalf("numbers should right-align:\n%s", buf.String())
	}
}

func TestTableTrimsTrailingWhitespaceAndHandlesEmpty(t *testing.T) {
	var buf bytes.Buffer
	Table(&buf, [][]string{
		{"a", "b"},
		{"a", ""},
	}, nil)
	for _, line := range strings.Split(buf.String(), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Fatalf("trailing whitespace in %q", line)
		}
	}
	buf.Reset()
	Table(&buf, nil, nil)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty rows, got %q", buf.String())
	}
}
