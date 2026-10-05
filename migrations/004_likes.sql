CREATE TABLE IF NOT EXISTS video_likes (
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (video_id, user_id)
);

CREATE INDEX IF NOT EXISTS video_likes_video_idx ON video_likes(video_id);
