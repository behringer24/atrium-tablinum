-- +goose Up
-- A section can wait for its vector: after a change of the embedding dimension all vectors are
-- dropped and rebuilt by the re-embedding task (admin_reembed). Meanwhile the section is found by
-- full-text search only; vector search and the duplicate check use only vectors of the configured
-- model (chunks.embedding_model).
ALTER TABLE chunks ALTER COLUMN embedding DROP NOT NULL;

-- +goose Down
DELETE FROM chunks WHERE embedding IS NULL;
ALTER TABLE chunks ALTER COLUMN embedding SET NOT NULL;
