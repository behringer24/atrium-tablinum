package chunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

var opt = Options{TargetTokens: 50, MaxTokens: 80}

func TestSmallDocumentIsOneChunkWithTitle(t *testing.T) {
	cs := Split("Titel", "Ein kurzer Text.", opt)
	if len(cs) != 1 || cs[0].Content != "Titel\n\nEin kurzer Text." || cs[0].Index != 0 {
		t.Fatalf("%+v", cs)
	}
}

func TestEmptyContentKeepsTitle(t *testing.T) {
	cs := Split("Nur Titel", "  \n", opt)
	if len(cs) != 1 || cs[0].Content != "Nur Titel" {
		t.Fatalf("%+v", cs)
	}
}

func TestHeadingPaths(t *testing.T) {
	body := strings.Repeat("Text zum Abschnitt. ", 12)
	doc := "# A\n" + body + "\n## B\n" + body + "\n### C\n" + body + "\n## D\n" + body + "\n# E\n" + body
	cs := Split("T", doc, opt)
	var paths []string
	for _, c := range cs {
		paths = append(paths, c.HeadingPath)
	}
	got := strings.Join(paths, "|")
	want := "A|A > B|A > B > C|A > D|E"
	if got != want {
		t.Fatalf("paths = %q, want %q", got, want)
	}
	for i, c := range cs {
		if c.Index != i {
			t.Errorf("Index %d != %d", c.Index, i)
		}
	}
}

func TestHashInCodeFenceIsNotAHeading(t *testing.T) {
	doc := "# Setup\n```bash\n# kein Heading\necho hi\n```\n\nEnde"
	cs := Split("T", doc, Options{TargetTokens: 500, MaxTokens: 800})
	if len(cs) != 1 || cs[0].HeadingPath != "Setup" {
		t.Fatalf("%+v", cs)
	}
}

func TestLongSectionSplitsAtParagraphs(t *testing.T) {
	var paras []string
	for i := 0; i < 20; i++ {
		paras = append(paras, strings.Repeat("wort ", 20)) // ~25 Tokens
	}
	cs := Split("T", "# Lang\n"+strings.Join(paras, "\n\n"), opt)
	if len(cs) < 5 {
		t.Fatalf("zu wenige Chunks: %d", len(cs))
	}
	for _, c := range cs {
		if c.HeadingPath != "Lang" {
			t.Errorf("path %q", c.HeadingPath)
		}
		if n := EstimateTokens(c.Content); n > opt.MaxTokens+5 {
			t.Errorf("Chunk mit %d Tokens", n)
		}
	}
}

func TestHugeParagraphIsHardSplitWithoutLoss(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("überlang ", 400)) // ein Absatz, ~800 Tokens
	cs := Split("", text, opt)
	if len(cs) < 3 {
		t.Fatalf("chunks = %d", len(cs))
	}
	var words []string
	for _, c := range cs {
		if !utf8.ValidString(c.Content) {
			t.Fatal("ungültiges UTF-8")
		}
		if EstimateTokens(c.Content) > opt.MaxTokens {
			t.Errorf("Chunk zu groß: %d", EstimateTokens(c.Content))
		}
		words = append(words, strings.Fields(c.Content)...)
	}
	if len(words) != 400 {
		t.Errorf("Wörter: %d, erwartet 400", len(words))
	}
}

func TestNoWhitespaceTextStillSplits(t *testing.T) {
	cs := Split("", strings.Repeat("ä", 1000), opt)
	total := 0
	for _, c := range cs {
		if EstimateTokens(c.Content) > opt.MaxTokens {
			t.Errorf("zu groß")
		}
		total += utf8.RuneCountInString(c.Content)
	}
	if total != 1000 {
		t.Errorf("Zeichen verloren: %d", total)
	}
}

func TestTinySectionsAreMerged(t *testing.T) {
	doc := "# A\nkurz\n# B\nauch kurz\n# C\n" + strings.Repeat("Lang genug für einen eigenen Chunk. ", 8)
	cs := Split("T", doc, opt)
	if len(cs) >= 3 {
		t.Fatalf("kleine Abschnitte nicht zusammengeführt: %d Chunks", len(cs))
	}
	if !strings.Contains(cs[0].Content, "auch kurz") {
		t.Error("B fehlt im ersten Chunk")
	}
}

func TestHeadingRecognition(t *testing.T) {
	for line, want := range map[string]int{"# a": 1, "###### f": 6, "####### x": 0, "#nospace": 0, "text # no": 0, "## b ##": 2} {
		if l, _ := heading(line); l != want {
			t.Errorf("%q: %d, want %d", line, l, want)
		}
	}
}
