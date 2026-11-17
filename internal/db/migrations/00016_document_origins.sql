-- +goose Up
-- Where the knowledge of an entry comes from: references to the original documents (a link, a path,
-- an address in some other system). Atrium stores only the reference and a few facts about the
-- reading (title, date, a hash of the text that was taken over), never the file. A change is
-- metadata like the owner and the review date: it writes no version.
ALTER TABLE documents ADD COLUMN origins jsonb NOT NULL DEFAULT '[]'
    CHECK (jsonb_typeof(origins) = 'array');

-- +goose Down
ALTER TABLE documents DROP COLUMN origins;
