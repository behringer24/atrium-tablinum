// Package ingest validates content, guards against secrets, chunks, embeds and writes
// the search index rows (chunks). The surrounding document transaction lives in docs.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/guard"
	"github.com/behringer24/atrium-tablinum/internal/visibility"
)

var (
	ErrInvalid  = errors.New("ungültige eingabe")
	ErrTooLarge = errors.New("inhalt zu groß")
)

const (
	maxTitleRunes = 200
	maxTags       = 20
	maxTagRunes   = 50
)

var langRe = regexp.MustCompile(`^[a-z_]{2,30}$`)

type Config struct {
	MaxDocumentBytes int
	Chunk            chunk.Options
	// FTSLanguages are Postgres text search configurations ("german", "simple" …).
	FTSLanguages []string
}

type Service struct {
	cfg Config
	emb embed.Embedder
}

func New(cfg Config, emb embed.Embedder) (*Service, error) {
	if len(cfg.FTSLanguages) == 0 {
		return nil, errors.New("mindestens eine fts-sprache erforderlich")
	}
	for _, l := range cfg.FTSLanguages {
		// The names are inlined into SQL, so only plain identifiers are accepted.
		if !langRe.MatchString(l) {
			return nil, fmt.Errorf("ungültige fts-sprache %q", l)
		}
	}
	return &Service{cfg: cfg, emb: emb}, nil
}

// Embedder exposes the backend, e.g. for query embedding in the search package.
func (s *Service) Embedder() embed.Embedder { return s.emb }

// Normalized is validated, cleaned input.
type Normalized struct {
	Title   string
	Content string
	Tags    []string
}

// Validate cleans and checks title, content and tags, then runs the secret guard over
// all of them. The returned error is ErrInvalid, ErrTooLarge or *guard.Error.
func (s *Service) Validate(title, content string, tags []string) (Normalized, error) {
	n := Normalized{Title: strings.TrimSpace(title), Content: strings.TrimSpace(content)}
	switch {
	case n.Title == "":
		return n, fmt.Errorf("%w: titel fehlt", ErrInvalid)
	case utf8.RuneCountInString(n.Title) > maxTitleRunes:
		return n, fmt.Errorf("%w: titel länger als %d zeichen", ErrInvalid, maxTitleRunes)
	case n.Content == "":
		return n, fmt.Errorf("%w: inhalt fehlt", ErrInvalid)
	case !utf8.ValidString(n.Title) || !utf8.ValidString(n.Content):
		return n, fmt.Errorf("%w: kein gültiges utf-8", ErrInvalid)
	case strings.ContainsRune(n.Title, 0) || strings.ContainsRune(n.Content, 0):
		return n, fmt.Errorf("%w: nullbytes sind nicht erlaubt", ErrInvalid)
	case len(n.Content) > s.cfg.MaxDocumentBytes:
		return n, fmt.Errorf("%w: %d bytes, erlaubt sind %d", ErrTooLarge, len(n.Content), s.cfg.MaxDocumentBytes)
	}
	var err error
	if n.Tags, err = NormalizeTags(tags); err != nil {
		return n, err
	}
	if err := guard.Check(n.Title + "\n" + n.Content + "\n" + strings.Join(n.Tags, "\n")); err != nil {
		return n, err
	}
	return n, nil
}

// NormalizeTags lowercases, trims and de-duplicates tags.
func NormalizeTags(tags []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if utf8.RuneCountInString(t) > maxTagRunes {
			return nil, fmt.Errorf("%w: tag länger als %d zeichen", ErrInvalid, maxTagRunes)
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxTags {
		return nil, fmt.Errorf("%w: höchstens %d tags", ErrInvalid, maxTags)
	}
	sort.Strings(out)
	return out, nil
}

// PreparedChunk is a chunk with its embedding, ready to insert.
type PreparedChunk struct {
	chunk.Chunk
	Vector string // pgvector text literal
}

type Prepared struct {
	Chunks []PreparedChunk
	Model  string
}

// Prepare chunks and embeds a document. It does network I/O and must run before the
// database transaction is opened.
func (s *Service) Prepare(ctx context.Context, title, content string) (Prepared, error) {
	chunks := chunk.Split(title, content, s.cfg.Chunk)
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vecs, err := s.emb.Embed(ctx, texts)
	if err != nil {
		return Prepared{}, err
	}
	if len(vecs) != len(chunks) {
		return Prepared{}, fmt.Errorf("embedder lieferte %d vektoren für %d chunks", len(vecs), len(chunks))
	}
	out := Prepared{Model: s.emb.Model(), Chunks: make([]PreparedChunk, len(chunks))}
	for i, c := range chunks {
		out.Chunks[i] = PreparedChunk{Chunk: c, Vector: VectorLiteral(vecs[i])}
	}
	return out, nil
}

// VectorLiteral formats v as a pgvector text literal, to be cast with ::vector.
func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", f)
	}
	b.WriteByte(']')
	return b.String()
}

// TSVExpr returns the SQL expression that builds the tsvector of the text in the given
// parameter placeholder (e.g. "$4") for all configured languages.
func (s *Service) TSVExpr(param string) string {
	parts := make([]string, len(s.cfg.FTSLanguages))
	for i, l := range s.cfg.FTSLanguages {
		parts[i] = fmt.Sprintf("to_tsvector('%s'::regconfig, %s)", l, param)
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

// ReplaceChunks swaps all chunks of a document inside tx.
func (s *Service) ReplaceChunks(ctx context.Context, tx pgx.Tx, docID uuid.UUID, p Prepared) error {
	if _, err := tx.Exec(ctx, `DELETE FROM chunks WHERE document_id = $1`, docID); err != nil {
		return err
	}
	q := `INSERT INTO chunks (document_id, chunk_index, heading_path, content, embedding, embedding_model, tsv)
		VALUES ($1, $2, $3, $4, $5::vector, $6, ` + s.TSVExpr("$4") + `)`
	batch := &pgx.Batch{}
	for _, c := range p.Chunks {
		batch.Queue(q, docID, c.Index, c.HeadingPath, c.Content, c.Vector, p.Model)
	}
	res := tx.SendBatch(ctx, batch)
	defer res.Close()
	for range p.Chunks {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return res.Close()
}

// Duplicate is an existing document that is very similar to new content.
type Duplicate struct {
	ID         uuid.UUID
	Title      string
	Similarity float64
}

// Scope describes what the caller may see; it feeds the visibility predicate.
type Scope struct {
	Visible []uuid.UUID
	Actor   uuid.UUID
	Curator []uuid.UUID
}

// FindDuplicates looks for knowledge documents within one space whose chunks are at
// least threshold similar (cosine) to any of the new chunks. The space must be one the
// caller can see; results are limited by the same visibility rules as search, so no
// information about unseen documents leaks.
func (s *Service) FindDuplicates(ctx context.Context, pool *pgxpool.Pool, sc Scope, space uuid.UUID, p Prepared, threshold float64, exclude *uuid.UUID) ([]Duplicate, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`); err != nil {
		return nil, err
	}

	q := `SELECT d.id, d.title, 1 - (c.embedding <=> $1::vector)
		FROM chunks c JOIN documents d ON d.id = c.document_id
		WHERE ` + visibility.Clause("d", 2, 3, 4) + `
		  AND d.kind = 'knowledge' AND d.space_id = $5 AND ($6::uuid IS NULL OR d.id <> $6::uuid)
		ORDER BY c.embedding <=> $1::vector
		LIMIT 3`
	best := map[uuid.UUID]Duplicate{}
	for _, c := range p.Chunks {
		rows, err := tx.Query(ctx, q, c.Vector, nonNil(sc.Visible), sc.Actor, nonNil(sc.Curator), space, exclude)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d Duplicate
			if err := rows.Scan(&d.ID, &d.Title, &d.Similarity); err != nil {
				rows.Close()
				return nil, err
			}
			if d.Similarity >= threshold && d.Similarity > best[d.ID].Similarity {
				best[d.ID] = d
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]Duplicate, 0, len(best))
	for _, d := range best {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Similarity > out[j].Similarity })
	return out, nil
}

func nonNil(ids []uuid.UUID) []uuid.UUID {
	if ids == nil {
		return []uuid.UUID{}
	}
	return ids
}
