package collect

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePromSelector(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []PromMatcher
		wantErr string // substring; "" = success
	}{
		{name: "empty is a no-op", in: "", want: nil},
		{name: "blank is a no-op", in: "  ", want: nil},
		{name: "empty braces are a no-op", in: " { } ", want: nil},
		{name: "all four operators without braces", in: `a="1",b!="2",c=~"x|y",d!~"z.*"`,
			want: []PromMatcher{{"a", "=", "1"}, {"b", "!=", "2"}, {"c", "=~", "x|y"}, {"d", "!~", "z.*"}}},
		{name: "braces and spaces", in: ` { cluster = "prod-a" , job=~"kubelet|cadvisor" } `,
			want: []PromMatcher{{"cluster", "=", "prod-a"}, {"job", "=~", "kubelet|cadvisor"}}},
		{name: "Go escapes are unquoted", in: `a="q\"b\\c}\u00e9"`, want: []PromMatcher{{"a", "=", "q\"b\\c}é"}}},
		{name: "underscore label name", in: `_x_1=""`, want: []PromMatcher{{"_x_1", "=", ""}}},
		{name: "label name cannot start with a digit", in: `1a="x"`, wantErr: `offset 0: want a label name, got "1a=\"x\""`},
		{name: "missing operator", in: `a "x"`, wantErr: `offset 2: want one of = != =~ !~, got "\"x\""`},
		{name: "unknown operator", in: `a=="x"`, wantErr: `offset 2: want a double-quoted value, got "=\"x\""`},
		{name: "single-quoted value", in: `a='x'`, wantErr: `offset 2: want a double-quoted value`},
		{name: "unterminated string", in: `a="x`, wantErr: `offset 2: unterminated string`},
		{name: "raw newline in string", in: "a=\"x\ny\"", wantErr: `offset 2: unterminated string`},
		{name: "invalid escape", in: `a="\q"`, wantErr: `offset 2: invalid string "\"\\q\""`},
		{name: "missing comma", in: `a="1" b="2"`, wantErr: `offset 6: want ',' or end of selector, got "b=\"2\""`},
		{name: "trailing comma", in: `a="1",`, wantErr: `offset 6: want a label name, got end of selector`},
		{name: "trailing garbage after braces", in: `{a="1"} x`, wantErr: `offset 8: want end of selector, got "x"`},
		{name: "missing closing brace", in: `{a="1"`, wantErr: `offset 6: want ',' or '}', got end of selector`},
		{name: "closing brace without opening", in: `a="1"}`, wantErr: `offset 5: want ',' or end of selector, got "}"`},
		{name: "regex that does not compile", in: `a=~"("`, wantErr: `offset 3: invalid regex "("`},
		{name: "regex is checked anchored like Prometheus", in: `a!~"x)(?:y"`, want: []PromMatcher{{"a", "!~", "x)(?:y"}}},
		{name: "__name__ is reserved for the metric", in: `__name__="up"`, wantErr: `offset 0: __name__ is set by crossaz`},
		{name: "equality value is not a regex", in: `a="("`, want: []PromMatcher{{"a", "=", "("}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			got, err := ParsePromSelector(tt.in)

			// Assert
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
