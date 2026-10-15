package collect

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PromMatcher is one PromQL label matcher; Value is unquoted.
type PromMatcher struct {
	Name, Op, Value string
}

const promSelectorSnippetBytes = 20

// promOps is longest first so "=~" is not read as "=".
var promOps = []string{"=~", "!~", "!=", "="}

var promLabelNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*`)

// ParsePromSelector parses comma-separated PromQL label matchers such as `cluster="prod-a",job=~"kubelet|cadvisor"`,
// with or without surrounding braces. Values are double-quoted Go string literals; regex values must compile
// anchored as Prometheus anchors them. A blank selector yields no matchers.
func ParsePromSelector(s string) ([]PromMatcher, error) {
	p := promSelParser{src: s}
	p.skipSpace()
	braced := p.consume("{")
	if braced {
		p.skipSpace()
		if p.consume("}") {
			return nil, p.expectEnd()
		}
	} else if p.pos == len(s) {
		return nil, nil
	}
	var out []PromMatcher
	for {
		m, err := p.matcher()
		if err != nil {
			return nil, err
		}
		out = append(out, m)
		p.skipSpace()
		if p.consume(",") {
			p.skipSpace()
			continue
		}
		if !braced {
			if p.pos != len(s) {
				return nil, p.fail("want ',' or end of selector")
			}
			return out, nil
		}
		if !p.consume("}") {
			return nil, p.fail("want ',' or '}'")
		}
		if err := p.expectEnd(); err != nil {
			return nil, err
		}
		return out, nil
	}
}

type promSelParser struct {
	src string
	pos int
}

func (p *promSelParser) matcher() (PromMatcher, error) {
	name := promLabelNameRE.FindString(p.src[p.pos:])
	if name == "" {
		return PromMatcher{}, p.fail("want a label name")
	}
	if name == "__name__" {
		return PromMatcher{}, p.fail("__name__ is set by crossaz")
	}
	p.pos += len(name)
	p.skipSpace()
	op, ok := p.op()
	if !ok {
		return PromMatcher{}, p.fail("want one of = != =~ !~")
	}
	p.skipSpace()
	start := p.pos
	value, err := p.quoted()
	if err != nil {
		return PromMatcher{}, err
	}
	if op == "=~" || op == "!~" {
		if _, err := regexp.Compile("^(?:" + value + ")$"); err != nil {
			return PromMatcher{}, fmt.Errorf("offset %d: invalid regex %q: %w", start, value, err)
		}
	}
	return PromMatcher{Name: name, Op: op, Value: value}, nil
}

func (p *promSelParser) op() (string, bool) {
	for _, op := range promOps {
		if p.consume(op) {
			return op, true
		}
	}
	return "", false
}

// quoted reads a double-quoted Go string literal; a raw newline ends it as unterminated.
func (p *promSelParser) quoted() (string, error) {
	start := p.pos
	if !strings.HasPrefix(p.src[start:], `"`) {
		return "", p.fail("want a double-quoted value")
	}
	for i := start + 1; i < len(p.src); i++ {
		switch p.src[i] {
		case '\\':
			i++
		case '\n':
			return "", fmt.Errorf("offset %d: unterminated string", start)
		case '"':
			lit := p.src[start : i+1]
			v, err := strconv.Unquote(lit)
			if err != nil {
				return "", fmt.Errorf("offset %d: invalid string %q: %w", start, lit, err)
			}
			p.pos = i + 1
			return v, nil
		}
	}
	return "", fmt.Errorf("offset %d: unterminated string", start)
}

func (p *promSelParser) consume(tok string) bool {
	if !strings.HasPrefix(p.src[p.pos:], tok) {
		return false
	}
	p.pos += len(tok)
	return true
}

func (p *promSelParser) skipSpace() {
	for p.pos < len(p.src) && strings.ContainsRune(" \t\r\n", rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *promSelParser) expectEnd() error {
	p.skipSpace()
	if p.pos != len(p.src) {
		return p.fail("want end of selector")
	}
	return nil
}

// fail names the offset and the text found there.
func (p *promSelParser) fail(msg string) error {
	rest := p.src[p.pos:]
	if rest == "" {
		return fmt.Errorf("offset %d: %s, got end of selector", p.pos, msg)
	}
	if len(rest) > promSelectorSnippetBytes {
		rest = rest[:promSelectorSnippetBytes] + "..."
	}
	return fmt.Errorf("offset %d: %s, got %q", p.pos, msg, rest)
}

// promRenderMatchers renders matchers as `,name op "value"` pairs to append inside a vector selector.
// Values are re-quoted, so no input can close the selector.
func promRenderMatchers(ms []PromMatcher) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString("," + m.Name + m.Op + strconv.Quote(m.Value))
	}
	return b.String()
}
