package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// findKeyValue locates the unique occurrence of `"key": <object-or-array>`
// outside strings and comments (JSONC-safe) and returns the byte range
// [valStart, valEnd) of the value.
func findKeyValue(src []byte, key string) (int, int, error) {
	quoted := `"` + key + `"`
	var matches [][2]int
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '"':
			start := i
			i = skipString(src, i)
			if string(src[start:i]) != quoted {
				continue
			}
			j := skipSpaceAndComments(src, i)
			if j >= len(src) || src[j] != ':' {
				continue // a string value that merely equals the key
			}
			j = skipSpaceAndComments(src, j+1)
			if j >= len(src) || (src[j] != '{' && src[j] != '[') {
				return 0, 0, fmt.Errorf("value of %q is not an object or array", key)
			}
			end, err := matchBracket(src, j)
			if err != nil {
				return 0, 0, fmt.Errorf("key %q: %w", key, err)
			}
			matches = append(matches, [2]int{j, end})
			i = end
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			i++
		}
	}
	switch len(matches) {
	case 0:
		return 0, 0, fmt.Errorf("key %q not found", key)
	case 1:
		return matches[0][0], matches[0][1], nil
	default:
		return 0, 0, fmt.Errorf("key %q found %d times; refusing to guess", key, len(matches))
	}
}

// spliceKey replaces the value of `"key"` with val (marshaled as indented
// JSON), preserving every other byte of the file — including comments.
func spliceKey(src []byte, key string, val any) ([]byte, error) {
	start, end, err := findKeyValue(src, key)
	if err != nil {
		return nil, err
	}
	lineStart := bytes.LastIndexByte(src[:start], '\n') + 1
	indent := src[lineStart:]
	n := 0
	for n < len(indent) && (indent[n] == ' ' || indent[n] == '\t') {
		n++
	}
	prefix := string(src[lineStart : lineStart+n])

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, "  ")
	if err := enc.Encode(val); err != nil {
		return nil, err
	}
	rendered := bytes.TrimRight(buf.Bytes(), "\n")

	var out bytes.Buffer
	out.Grow(len(src) + len(rendered))
	out.Write(src[:start])
	out.Write(rendered)
	out.Write(src[end:])
	return out.Bytes(), nil
}

// extractObject parses the current value of `"key"` into a generic map.
func extractObject(src []byte, key string) (map[string]any, error) {
	start, end, err := findKeyValue(src, key)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(src[start:end], &m); err != nil {
		return nil, fmt.Errorf("parsing current value of %q: %w", key, err)
	}
	return m, nil
}

func skipString(src []byte, i int) int {
	i++ // opening quote
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
		case '"':
			return i + 1
		default:
			i++
		}
	}
	return i
}

func skipSpaceAndComments(src []byte, i int) int {
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		default:
			return i
		}
	}
	return i
}

func matchBracket(src []byte, i int) (int, error) {
	open := src[i]
	var close byte = '}'
	if open == '[' {
		close = ']'
	}
	depth := 0
	for i < len(src) {
		switch {
		case src[i] == '"':
			i = skipString(src, i)
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case src[i] == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		case src[i] == open:
			depth++
			i++
		case src[i] == close:
			depth--
			i++
			if depth == 0 {
				return i, nil
			}
		default:
			i++
		}
	}
	return 0, fmt.Errorf("unbalanced brackets")
}
