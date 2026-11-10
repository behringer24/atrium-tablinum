package docs_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/chunk"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/reembed"
)

// withInline returns the test environment with a store that embeds only the first n sections of an
// entry while saving.
func withInline(t *testing.T, n int) env {
	t.Helper()
	e := setup(t)
	ing, err := ingest.New(ingest.Config{
		MaxDocumentBytes: 2000,
		Chunk:            chunk.Options{TargetTokens: 50, MaxTokens: 80},
		FTSLanguages:     []string{"german", "english", "simple"},
	}, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	e.docs = docs.New(e.pool, e.spaces, ing, docs.Options{Quota: e.quota, HandoffTTLDays: 14, DuplicateThreshold: 0.9, InlineIndexSections: n})
	return e
}

func longEntry(sections int) string {
	var b strings.Builder
	for i := 0; i < sections; i++ {
		fmt.Fprintf(&b, "## Abschnitt %d\n\n%s\n\n", i, strings.Repeat("alpha Wort ", 15))
	}
	return b.String()
}

// A long entry is saved at once with its first sections embedded; the rest waits for the background
// task, is found by full text meanwhile, and gets its vectors from IndexPending without any request.
func TestLongEntryIsSavedAtOnceAndIndexedInTheBackground(t *testing.T) {
	e := withInline(t, 3)
	anna := e.actor(t, "anna@x.de")

	res, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "Lang", Content: longEntry(8)})
	if err != nil || res.Document == nil {
		t.Fatalf("Store: %v %+v", err, res)
	}
	id := res.Document.ID
	total := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1`, id)
	pending := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding IS NULL AND embedding_model = $2`, id, ingest.PendingModel)
	if pending == 0 || total-pending != 3 || res.Document.IndexPending != pending {
		t.Fatalf("sections %d, pending %d, reported %d: want 3 embedded and the rest pending", total, pending, res.Document.IndexPending)
	}
	// The text of a pending section is searchable by full text at once.
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding IS NULL AND tsv @@ plainto_tsquery('simple', 'abschnitt')`, id); n != pending {
		t.Errorf("%d of %d pending sections are in the full-text index", n, pending)
	}

	rs := reembed.New(e.pool, fakeEmbedder{})
	st, err := rs.Status(e.ctx)
	if err != nil || st.Pending != pending || st.Stale != 0 {
		t.Fatalf("status before: pending %d (want %d), stale %d, %v", st.Pending, pending, st.Stale, err)
	}
	if err := rs.IndexPending(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding IS NOT NULL AND embedding_model = 'fake'`, id); n != total {
		t.Errorf("%d of %d sections embedded after the background task", n, total)
	}
	if st, err = rs.Status(e.ctx); err != nil || st.Pending != 0 || st.Stale != 0 {
		t.Errorf("status after: pending %d, stale %d, %v", st.Pending, st.Stale, err)
	}
}

// The background task is the second half of saving, not a rebuild: sections whose vector was dropped
// by a change of the embedding dimension (model "") wait for admin_reembed and stay as they are.
func TestIndexPendingLeavesDroppedVectorsToTheRebuild(t *testing.T) {
	e := withInline(t, 2)
	anna := e.actor(t, "anna@x.de")
	res, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "Lang", Content: longEntry(6)})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Document.ID
	if _, err := e.pool.Exec(e.ctx, `UPDATE chunks SET embedding = NULL, embedding_model = '' WHERE document_id = $1 AND chunk_index = 0`, id); err != nil {
		t.Fatal(err)
	}
	if err := reembed.New(e.pool, fakeEmbedder{}).IndexPending(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding IS NULL AND embedding_model = ''`, id); n != 1 {
		t.Errorf("the dropped vector was rebuilt without a request: %d left", n)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding_model = $2`, id, ingest.PendingModel); n != 0 {
		t.Errorf("%d sections still pending", n)
	}
}

// Changing a long entry saves the new text at once too; the sections of the old text are gone.
func TestUpdateOfALongEntryIndexesInTheBackground(t *testing.T) {
	e := withInline(t, 2)
	anna := e.actor(t, "anna@x.de")
	d := mustStore(t, e, anna, personal(anna), "Kurz", "alpha kurz")
	content := longEntry(6)
	got, err := e.docs.Update(e.ctx, anna, docs.UpdateParams{ID: d.ID, Content: &content})
	if err != nil {
		t.Fatal(err)
	}
	if got.IndexPending == 0 {
		t.Fatal("no section is pending after the update of a long entry")
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE document_id = $1 AND embedding_model = $2`, d.ID, ingest.PendingModel); n != got.IndexPending {
		t.Errorf("%d sections pending in the database, %d reported", n, got.IndexPending)
	}
}

// With 0 every section is embedded before saving answers, as before; nothing is left pending.
func TestWithoutALimitNothingIsLeftPending(t *testing.T) {
	e := withInline(t, 0)
	anna := e.actor(t, "anna@x.de")
	res, err := e.docs.Store(e.ctx, anna, docs.StoreParams{SpaceID: personal(anna), Title: "Lang", Content: longEntry(6)})
	if err != nil || res.Document.IndexPending != 0 {
		t.Fatalf("%v pending %d", err, res.Document.IndexPending)
	}
	if n := queryInt(t, e, `SELECT count(*) FROM chunks WHERE embedding IS NULL`); n != 0 {
		t.Errorf("%d sections without a vector", n)
	}
}
