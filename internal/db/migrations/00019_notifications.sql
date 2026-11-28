-- +goose Up
-- Notifications (docs/design/notifications.md). The connections (web hooks, SMTP) are in config.yaml with their
-- secrets; the database holds only what an instance admin chooses at runtime (which events go through which
-- connection) and the outbox. Everything stays empty while notifications are off.

-- What happened. No title and no text: the facts are looked up when a message is made. A row is routed once
-- (routed_at), then only kept for the retention period.
CREATE TABLE notification_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind         text        NOT NULL,
    space_id     uuid,
    subject_id   uuid,
    subject_kind text        NOT NULL DEFAULT '',
    actor_id     uuid,
    -- the person an event is about (author, owner of a personal space), nil for events about a space
    recipient_id uuid,
    -- a short fact that is no content: the name of a health check
    detail       text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    routed_at    timestamptz
);
CREATE INDEX notification_events_unrouted_idx ON notification_events (created_at) WHERE routed_at IS NULL;

-- What is to be sent: one row per connection and recipient. Events of the same kind, space and recipient that
-- arrive before the row is sent are merged into it (count). A web hook has no recipient (the nil uuid).
CREATE TABLE notification_deliveries (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connection   text        NOT NULL,
    kind         text        NOT NULL,
    space_id     uuid        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    recipient_id uuid        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    detail       text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    count        int         NOT NULL DEFAULT 1,
    attempts     int         NOT NULL DEFAULT 0,
    send_after   timestamptz NOT NULL DEFAULT now(),
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz
);
CREATE INDEX notification_deliveries_due_idx ON notification_deliveries (send_after) WHERE status IN ('pending', 'sending');
CREATE INDEX notification_deliveries_done_idx ON notification_deliveries (created_at) WHERE status IN ('sent', 'failed');

-- What an instance admin chose for a connection of config.yaml. A connection without a row sends nothing.
CREATE TABLE notification_settings (
    connection  text PRIMARY KEY,
    enabled     boolean     NOT NULL DEFAULT false,
    mode        text        NOT NULL DEFAULT 'instant' CHECK (mode IN ('instant', 'digest')),
    digest_at   text        NOT NULL DEFAULT '08:00',
    space_names boolean     NOT NULL DEFAULT false,
    updated_by  uuid,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_routes (
    connection text NOT NULL,
    event      text NOT NULL,
    PRIMARY KEY (connection, event)
);

-- +goose Down
DROP TABLE notification_routes;
DROP TABLE notification_settings;
DROP TABLE notification_deliveries;
DROP TABLE notification_events;
