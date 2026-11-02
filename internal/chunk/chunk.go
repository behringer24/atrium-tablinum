// Package chunk splits Markdown documents into embedding-sized pieces along headings
// and paragraphs. Token counts are estimated as runes/4.
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type Options struct {
	TargetTokens int // preferred chunk size
	MaxTokens    int // hard upper bound (the title prefix of the first chunk may exceed it slightly)
}

type Chunk struct {
	Index       int
	HeadingPath string // "Section > Subsection" of the chunk's first heading, "" before any heading
	Content     string
}

func EstimateTokens(s string) int { return (utf8.RuneCountInString(s) + 3) / 4 }

type piece struct {
	path string
	text string
}

// Split chunks content; the document title is prepended to the first chunk.
func Split(title, content string, opt Options) []Chunk {
	if opt.TargetTokens < 1 {
		opt.TargetTokens = 500
	}
	if opt.MaxTokens < opt.TargetTokens {
		opt.MaxTokens = opt.TargetTokens
	}

	var pieces []piece
	for _, s := range sections(content) {
		pieces = append(pieces, splitSection(s, opt)...)
	}
	pieces = merge(pieces, opt)

	if len(pieces) == 0 {
		return []Chunk{{Index: 0, Content: strings.TrimSpace(title)}}
	}
	out := make([]Chunk, len(pieces))
	for i, p := range pieces {
		out[i] = Chunk{Index: i, HeadingPath: p.path, Content: p.text}
	}
	if t := strings.TrimSpace(title); t != "" {
		out[0].Content = t + "\n\n" + out[0].Content
	}
	return out
}

// sections splits at ATX headings (outside code fences), keeping the heading line
// at the start of its section's text.
func sections(content string) []piece {
	var (
		out     []piece
		stack   []string // heading titles by level-1
		cur     []string
		curPath string
		fence   string
	)
	flush := func() {
		if text := strings.TrimSpace(strings.Join(cur, "\n")); text != "" {
			out = append(out, piece{curPath, text})
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		inFence := fence != ""
		fence = trackFence(fence, line)
		if !inFence && fence == "" {
			if level, title := heading(line); level > 0 {
				flush()
				if level-1 < len(stack) {
					stack = stack[:level-1]
				}
				for len(stack) < level-1 {
					stack = append(stack, "")
				}
				stack = append(stack, title)
				curPath = joinPath(stack)
			}
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// trackFence returns the open fence marker after line ("" = outside a fence).
func trackFence(fence, line string) string {
	f := fenceMarker(line)
	switch {
	case f == "":
		return fence
	case fence == "":
		return f
	case strings.HasPrefix(f, fence):
		return ""
	}
	return fence
}

func heading(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || len(line) <= n || (line[n] != ' ' && line[n] != '\t') {
		return 0, ""
	}
	return n, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[n:]), "#"))
}

func fenceMarker(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 {
		return ""
	}
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(t, m) {
			return m
		}
	}
	return ""
}

func joinPath(stack []string) string {
	var parts []string
	for _, s := range stack {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " > ")
}

// splitSection leaves small sections whole and cuts big ones at paragraph borders.
func splitSection(s piece, opt Options) []piece {
	if EstimateTokens(s.text) <= opt.MaxTokens {
		return []piece{s}
	}
	var (
		out  []piece
		buf  []string
		size int
	)
	emit := func() {
		if len(buf) > 0 {
			out = append(out, piece{s.path, strings.Join(buf, "\n\n")})
		}
		buf, size = nil, 0
	}
	for _, para := range paragraphs(s.text) {
		pt := EstimateTokens(para)
		if pt > opt.MaxTokens {
			emit()
			for _, part := range hardSplit(para, opt.MaxTokens) {
				out = append(out, piece{s.path, part})
			}
			continue
		}
		if size > 0 && size+pt > opt.TargetTokens {
			emit()
		}
		buf = append(buf, para)
		size += pt
	}
	emit()
	return out
}

// paragraphs splits on blank lines, but never inside a code fence.
func paragraphs(text string) []string {
	var (
		out   []string
		cur   []string
		fence string
	)
	flush := func() {
		if p := strings.TrimSpace(strings.Join(cur, "\n")); p != "" {
			out = append(out, p)
		}
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		fence = trackFence(fence, line)
		if fence == "" && strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// hardSplit cuts an oversized block at line breaks, else whitespace, else mid-word.
func hardSplit(s string, maxTokens int) []string {
	limit := maxTokens * 4
	var out []string
	rs := []rune(s)
	for len(rs) > limit {
		cut := limit
		if i := lastBreak(rs[:limit]); i > limit/2 {
			cut = i
		}
		if p := strings.TrimSpace(string(rs[:cut])); p != "" {
			out = append(out, p)
		}
		rs = rs[cut:]
	}
	if p := strings.TrimSpace(string(rs)); p != "" {
		out = append(out, p)
	}
	return out
}

func lastBreak(rs []rune) int {
	for _, pred := range []func(rune) bool{
		func(r rune) bool { return r == '\n' },
		unicode.IsSpace,
	} {
		for i := len(rs) - 1; i > 0; i-- {
			if pred(rs[i]) {
				return i + 1
			}
		}
	}
	return -1
}

// merge folds undersized pieces into their successor so tiny sections do not become
// poor standalone embeddings.
func merge(in []piece, opt Options) []piece {
	minTokens := opt.TargetTokens / 4
	var out []piece
	for _, p := range in {
		if n := len(out); n > 0 {
			last := out[n-1]
			lt := EstimateTokens(last.text)
			if lt < minTokens && lt+EstimateTokens(p.text) <= opt.MaxTokens {
				out[n-1] = piece{last.path, last.text + "\n\n" + p.text}
				continue
			}
		}
		out = append(out, p)
	}
	return out
}
