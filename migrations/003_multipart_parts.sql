CREATE TABLE IF NOT EXISTS upload_parts (
    upload_id TEXT NOT NULL REFERENCES upload_sessions(upload_id) ON DELETE CASCADE,
    part_number INTEGER NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (upload_id, part_number),
    CONSTRAINT upload_parts_number_check CHECK (part_number BETWEEN 1 AND 10000),
    CONSTRAINT upload_parts_size_check CHECK (size_bytes >= 0)
);
