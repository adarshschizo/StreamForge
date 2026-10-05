CREATE TABLE IF NOT EXISTS subscriptions (
    subscriber_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    creator_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber_id, creator_id),
    CHECK (subscriber_id <> creator_id)
);

CREATE INDEX IF NOT EXISTS subscriptions_creator_idx ON subscriptions(creator_id);
