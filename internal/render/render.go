// Package render holds the small, dependency-free output helpers shared
// by every subcommand: aligned text tables and the stable JSON envelope.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/portberth/internal/version"
)

// JSONSchemaVersion versions the machine-readable envelope; bump only on
// breaking shape changes.
const JSONSchemaVersion = 1

// Envelope wraps every JSON payload with tool identity so downstream
// scripts can sanity-check what they parsed.
type Envelope struct {
	Tool          string `json:"tool"`
	Version       string `json:"version"`
	SchemaVersion int    `json:"schema_version"`
	Data          any    `json:"data"`
}

// JSON writes v inside the standard envelope, two-space indented, with a
// trailing newline. Map iteration never occurs: all payloads are structs
// and slices, so output is byte-deterministic.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Envelope{
		Tool:          "portberth",
		Version:       version.Version,
		SchemaVersion: JSONSchemaVersion,
		Data:          v,
	})
}

// Table writes rows as space-aligned columns. rightAlign marks columns
// (by index) that should be right-aligned — numbers, mostly. Cells are
// separated by two spaces; trailing whitespace is trimmed so output is
// diff-friendly.
func Table(w io.Writer, rows [][]string, rightAlign map[int]bool) {
	if len(rows) == 0 {
		return
	}
	widths := map[int]int{}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			if rightAlign[i] {
				b.WriteString(strings.Repeat(" ", widths[i]-len(cell)))
				b.WriteString(cell)
			} else if i == len(row)-1 {
				b.WriteString(cell) // last column: no padding
			} else {
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", widths[i]-len(cell)))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}
