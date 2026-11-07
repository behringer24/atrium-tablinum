-- +goose Up
-- What an entry is (binding rule, documentation, recommendation, experience) and who answers for
-- it. review_due_at (migration 1) becomes the review date. Design: docs/design/knowledge-metadata.md.

ALTER TABLE documents
    ADD COLUMN knowledge_type text NOT NULL DEFAULT 'documentation'
        CHECK (knowledge_type IN ('rule', 'documentation', 'recommendation', 'experience')),
    ADD COLUMN owner_id uuid REFERENCES users(id);

CREATE INDEX documents_owner_idx ON documents (owner_id) WHERE owner_id IS NOT NULL;

-- The type is part of a version, so the history shows when an entry became binding.
ALTER TABLE document_versions ADD COLUMN knowledge_type text NOT NULL DEFAULT 'documentation';

-- +goose Down
ALTER TABLE document_versions DROP COLUMN knowledge_type;
DROP INDEX documents_owner_idx;
ALTER TABLE documents DROP COLUMN owner_id, DROP COLUMN knowledge_type;
