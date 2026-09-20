package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// yamlToJSON converts the subset of YAML used by this service configuration
// into JSON, so the rest of the loader can lean on encoding/json.
//
// Supported: block mappings, block sequences, flow sequences ([a, b]) and flow
// mappings ({a: 1}), single/double quoted scalars, comments, and the usual
// scalar types. Anchors, aliases, multi-document files, block scalars (| and
// >) and tags are not supported: the config format has no need for them, and
// a small hand-written parser keeps the dependency list empty.
//
// A document that already looks like JSON is passed straight through.
func yamlToJSON(src string) ([]byte, error) {
	if t := strings.TrimSpace(src); strings.HasPrefix(t, "{") {
		return []byte(t), nil
	}
	lines, err := scanLines(src)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return []byte("{}"), nil
	}
	p := &yamlParser{lines: lines}
	v, err := p.parseBlock(lines[0].indent)
	if err != nil {
		return nil, err
	}
	if p.i < len(p.lines) {
		return nil, fmt.Errorf("line %d: unexpected indentation", p.lines[p.i].num)
	}
	return json.Marshal(v)
}

type yamlLine struct {
	num    int // 1-based line number in the source, for error messages
	indent int
	text   string
}

func scanLines(src string) ([]yamlLine, error) {
	var out []yamlLine
	for n, raw := range strings.Split(src, "\n") {
		raw = strings.TrimRight(raw, " \t\r")
		lead := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
		if strings.Contains(lead, "\t") {
			return nil, fmt.Errorf("line %d: tabs may not be used for indentation", n+1)
		}
		indent := len(lead)
		text := strings.TrimRight(stripComment(raw[indent:]), " ")
		if text == "" || text == "---" {
			continue
		}
		out = append(out, yamlLine{num: n + 1, indent: indent, text: text})
	}
	return out, nil
}

// stripComment removes a trailing "#" comment that is not inside quotes.
func stripComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t'):
			return s[:i]
		}
	}
	return s
}

type yamlParser struct {
	lines []yamlLine
	i     int
}

func (p *yamlParser) peek() (yamlLine, bool) {
	if p.i < len(p.lines) {
		return p.lines[p.i], true
	}
	return yamlLine{}, false
}

// parseBlock parses whatever node starts at the current line at the given
// indentation: either a sequence or a mapping.
func (p *yamlParser) parseBlock(indent int) (any, error) {
	ln, ok := p.peek()
	if !ok {
		return nil, nil
	}
	if isSeqItem(ln.text) {
		return p.parseSequence(indent)
	}
	return p.parseMapping(indent)
}

func isSeqItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ")
}

func (p *yamlParser) parseMapping(indent int) (any, error) {
	m := map[string]any{}
	for {
		ln, ok := p.peek()
		if !ok || ln.indent < indent {
			return m, nil
		}
		if ln.indent > indent {
			return nil, fmt.Errorf("line %d: unexpected indentation in mapping", ln.num)
		}
		if isSeqItem(ln.text) {
			return nil, fmt.Errorf("line %d: sequence item where a mapping key was expected", ln.num)
		}
		key, rest, err := splitKey(ln.text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", ln.num, err)
		}
		p.i++
		if rest != "" {
			v, err := parseScalarOrFlow(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", ln.num, err)
			}
			m[key] = v
			continue
		}
		// An empty value means a nested block, or an explicit null.
		next, ok := p.peek()
		switch {
		case !ok:
			m[key] = nil
		case next.indent > indent:
			v, err := p.parseBlock(next.indent)
			if err != nil {
				return nil, err
			}
			m[key] = v
		case next.indent == indent && isSeqItem(next.text):
			// YAML allows a sequence to sit at its key's indentation.
			v, err := p.parseSequence(indent)
			if err != nil {
				return nil, err
			}
			m[key] = v
		default:
			m[key] = nil
		}
	}
}

func (p *yamlParser) parseSequence(indent int) (any, error) {
	items := []any{}
	for {
		ln, ok := p.peek()
		if !ok || ln.indent != indent || !isSeqItem(ln.text) {
			return items, nil
		}
		rest := strings.TrimSpace(strings.TrimPrefix(ln.text, "-"))
		if rest == "" {
			p.i++
			next, ok := p.peek()
			if !ok || next.indent <= indent {
				items = append(items, nil)
				continue
			}
			v, err := p.parseBlock(next.indent)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
			continue
		}
		if _, _, err := splitKey(rest); err == nil {
			// "- key: value" starts a mapping whose remaining keys are
			// indented to the column of the text after the dash. Rewrite the
			// line without the dash and parse a mapping at that column.
			col := ln.indent + len(ln.text) - len(rest)
			p.lines[p.i] = yamlLine{num: ln.num, indent: col, text: rest}
			v, err := p.parseMapping(col)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
			continue
		}
		v, err := parseScalarOrFlow(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", ln.num, err)
		}
		p.i++
		items = append(items, v)
	}
}

// splitKey splits "key: value" at the first colon that is not inside quotes or
// a flow collection. It reports an error when the line is not a mapping entry.
func splitKey(s string) (key, rest string, err error) {
	var quote byte
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ':' && depth == 0:
			if i+1 < len(s) && s[i+1] != ' ' {
				continue // part of a value such as a "http://host" URL
			}
			key = strings.TrimSpace(s[:i])
			if k, quoted := unquote(key); quoted {
				key = k
			}
			if key == "" {
				return "", "", fmt.Errorf("empty mapping key")
			}
			return key, strings.TrimSpace(s[i+1:]), nil
		}
	}
	return "", "", fmt.Errorf("not a mapping entry: %q", s)
}

func parseScalarOrFlow(s string) (any, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "["):
		return parseFlowSeq(s)
	case strings.HasPrefix(s, "{"):
		return parseFlowMap(s)
	default:
		return parseScalar(s), nil
	}
}

func parseFlowSeq(s string) (any, error) {
	if !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("unterminated flow sequence: %q", s)
	}
	parts, err := splitFlow(s[1 : len(s)-1])
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		v, err := parseScalarOrFlow(p)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func parseFlowMap(s string) (any, error) {
	if !strings.HasSuffix(s, "}") {
		return nil, fmt.Errorf("unterminated flow mapping: %q", s)
	}
	parts, err := splitFlow(s[1 : len(s)-1])
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, p := range parts {
		k, rest, err := splitKey(p)
		if err != nil {
			return nil, err
		}
		v, err := parseScalarOrFlow(rest)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// splitFlow splits on top-level commas, respecting quotes and nesting.
func splitFlow(s string) ([]string, error) {
	var (
		out   []string
		cur   strings.Builder
		quote byte
		depth int
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else if c == quote {
				quote = 0
			}
			continue
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 || depth != 0 {
		return nil, fmt.Errorf("unbalanced flow collection: %q", s)
	}
	if last := strings.TrimSpace(cur.String()); last != "" {
		out = append(out, last)
	}
	return out, nil
}

func parseScalar(s string) any {
	if v, quoted := unquote(s); quoted {
		return v
	}
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	case "null", "~", "":
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// unquote strips matching surrounding quotes, reporting whether the input
// actually was quoted.
func unquote(s string) (string, bool) {
	if len(s) < 2 {
		return s, false
	}
	switch {
	case s[0] == '"' && s[len(s)-1] == '"':
		if v, err := strconv.Unquote(s); err == nil {
			return v, true
		}
		return s[1 : len(s)-1], true
	case s[0] == '\'' && s[len(s)-1] == '\'':
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), true
	}
	return s, false
}
