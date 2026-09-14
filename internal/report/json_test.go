package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/leegun2000/crossaz/internal/model"
)

func TestJSONRoundTripsScanResult(t *testing.T) {
	// Arrange
	in := likelyResult()
	var buf bytes.Buffer

	// Act
	err := JSON(&buf, in)

	// Assert
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	var got model.ScanResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip mismatch:\ngot  %+v\nwant %+v", got, in)
	}
}

func TestJSONFormatting(t *testing.T) {
	// Arrange
	in := model.ScanResult{Cluster: "a<b>&c", Exposure: model.ExposureNone}
	var buf bytes.Buffer

	// Act
	err := JSON(&buf, in)

	// Assert
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	out := buf.String()
	checks := []struct {
		name string
		ok   bool
	}{
		{"two-space indent", strings.Contains(out, "\n  \"cluster\": \"a<b>&c\",")},
		{"trailing newline", strings.HasSuffix(out, "}\n")},
		{"nil paths as empty list", strings.Contains(out, "\"paths\": [],")},
		{"nil zones as empty list", strings.Contains(out, "\"zones\": [],")},
		{"unknown total is null", strings.Contains(out, "\"estimatedTotalUsd\": null")},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: got\n%s", c.name, out)
		}
	}
	if in.Paths != nil || in.Zones != nil {
		t.Error("JSON mutated its input")
	}
}

func TestJSONReturnsWriteError(t *testing.T) {
	// Act
	err := JSON(failingWriter{}, likelyResult())

	// Assert
	if !errors.Is(err, errWrite) {
		t.Errorf("JSON() error = %v, want wrapped %v", err, errWrite)
	}
}
