package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/leegun2000/crossaz/internal/model"
)

// JSON writes the scan result as 2-space indented JSON with a trailing newline.
// Nil lists are emitted as [] so consumers (jq) never see null for a list.
func JSON(w io.Writer, r model.ScanResult) error {
	out := r
	out.Zones = orEmpty(r.Zones)
	out.Layers = orEmpty(r.Layers)
	out.Paths = orEmpty(r.Paths)
	out.Excluded = orEmpty(r.Excluded)
	out.Uncertain = orEmpty(r.Uncertain)

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode scan result: %w", err)
	}
	return nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
