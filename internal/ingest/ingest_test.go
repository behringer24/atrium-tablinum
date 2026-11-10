package ingest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/chunk"
)

// countingEmbedder records how many texts it was asked to embed.
type countingEmbedder struct{ texts int }

func (c *countingEmbedder) Model() string              { return "count" }
func (c *countingEmbedder) Dimensions() int            { return 2 }
func (c *countingEmbedder) Ping(context.Context) error { return nil }
func (c *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	c.texts += len(texts)
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func longDoc(sections int) string {
	var b strings.Builder
	for i := 0; i < sections; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n%s\n\n", i, strings.Repeat("word ", 40))
	}
	return b.String()
}

func newService(t *testing.T, emb *countingEmbedder) *Service {
	t.Helper()
	s, err := New(Config{MaxDocumentBytes: 100000, Chunk: chunk.Options{TargetTokens: 50, MaxTokens: 80}, FTSLanguages: []string{"simple"}}, emb)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A long entry is embedded only as far as asked, so that saving does not wait for the embedding
// service to work through every section; the rest is reported as pending.
func TestPrepareUpToEmbedsOnlyTheFirstSections(t *testing.T) {
	emb := &countingEmbedder{}
	s := newService(t, emb)
	doc := longDoc(10)
	total := len(chunk.Split("T", doc, s.cfg.Chunk))
	if total < 8 {
		t.Fatalf("the test document has only %d sections", total)
	}

	p, err := s.PrepareUpTo(context.Background(), "T", doc, 3)
	if err != nil {
		t.Fatal(err)
	}
	if emb.texts != 3 || len(p.Chunks) != total || p.Pending != total-3 {
		t.Fatalf("embedded %d, sections %d (want %d), pending %d (want %d)", emb.texts, len(p.Chunks), total, p.Pending, total-3)
	}
	for i, c := range p.Chunks {
		if (c.Vector != "") != (i < 3) {
			t.Errorf("section %d: vector %q", i, c.Vector)
		}
	}
}

// 0 keeps the old behaviour (every section before returning), and a limit above the number of
// sections leaves nothing pending.
func TestPrepareUpToEmbedsEverythingWhenNotLimited(t *testing.T) {
	for _, inline := range []int{0, 1000} {
		emb := &countingEmbedder{}
		s := newService(t, emb)
		doc := longDoc(6)
		total := len(chunk.Split("T", doc, s.cfg.Chunk))
		p, err := s.PrepareUpTo(context.Background(), "T", doc, inline)
		if err != nil {
			t.Fatal(err)
		}
		if emb.texts != total || p.Pending != 0 {
			t.Errorf("inline %d: embedded %d of %d, pending %d", inline, emb.texts, total, p.Pending)
		}
		if q, err := s.Prepare(context.Background(), "T", doc); err != nil || q.Pending != 0 {
			t.Errorf("Prepare: pending %d, %v", q.Pending, err)
		}
	}
}
