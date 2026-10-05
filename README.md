# StreamForge

**Distributed Video Processing & Adaptive Streaming Platform**

> Upload once. Process anywhere. Stream everywhere.

This repository contains the first runnable foundation of StreamForge:

- Go API with `/health`, `/ready`, and `/version`
- Authentication endpoints with Argon2id passwords and HTTP-only JWT cookies
- Authenticated video metadata CRUD with ownership enforcement
- Redis Streams worker foundation with consumer groups, retries, and a dead-letter stream
- Upload initiation, completion, abort, and processing-job enqueueing
- Graceful shutdown and structured request logging
- Worker process scaffold with heartbeat logging
- PostgreSQL, Redis, and MinIO local infrastructure
- Initial relational schema migrations
- SvelteKit-compatible web shell

## Run locally

1. Copy `.env.example` to `.env` and adjust values if needed.
2. Start dependencies:

   ```sh
   docker compose up -d postgres redis minio
   ```

3. Start the API:

   ```sh
   go run ./apps/api
   ```

4. In another terminal, start the worker:

   ```sh
   go run ./apps/worker
   ```

The API listens on `http://localhost:8080` by default.

### Authentication endpoints

- `POST /auth/register` with `email`, `username`, and `password`
- `POST /auth/login` with `login` and `password`
- `POST /auth/logout`
- `GET /auth/me` with the session cookie from registration or login

Set `STREAMFORGE_JWT_SECRET` to a unique random value of at least 32 characters outside local development.

### Video metadata endpoints

All video endpoints require the session cookie:

- `POST /videos` with `title`, optional `description`, and optional `visibility`
- `GET /videos`
- `GET /videos/{id}`
- `PATCH /videos/{id}`
- `DELETE /videos/{id}`
- `GET /videos/{id}/stream` returns a one-hour presigned `master.m3u8` URL
  for videos in the `READY` state.

New videos begin in the `UPLOADING` state and default to `PRIVATE` visibility.

### Processing queue

The worker consumes Redis Stream `video-processing` through consumer group
`streamforge-workers`. Jobs contain `job_id`, `video_id`, `input_key`, and
`attempts`. Failed jobs are retried up to three attempts; permanent failures
are written to `video-processing-dlq`. The worker now uses FFprobe to inspect the source and FFmpeg to generate a
thumbnail, 360p/720p/1080p HLS renditions supported by the source height, and
a master playlist. Configure the executable paths with
`STREAMFORGE_FFPROBE_PATH` and `STREAMFORGE_FFMPEG_PATH`.

### Upload endpoints

Authenticated upload lifecycle:

- `POST /videos/{id}/upload/initiate`
- `POST /videos/{id}/upload/complete`
- `POST /videos/{id}/upload/abort`

Development API defaults use in-memory storage and queue implementations. Set
`STREAMFORGE_STORAGE_MODE=s3` and `STREAMFORGE_QUEUE_MODE=redis` to use MinIO
and Redis Streams. The worker always requires `STREAMFORGE_QUEUE_MODE=redis`.
In S3 mode, the worker downloads originals from
`streamforge-originals`, processes them locally, and uploads the thumbnail and
HLS tree to `streamforge-processed`.

Set `STREAMFORGE_DATABASE_MODE=postgres` to use the PostgreSQL repositories.
The Compose PostgreSQL service applies the schema in `migrations/` on its first
startup. Existing database volumes need to be recreated if the schema changes.

The API and worker automatically load `.env` from the project directory. Copy
`.env.example` to `.env` for a fresh setup, then adjust credentials and local
paths. Explicit environment variables take precedence over values in `.env`.

### Resumable multipart uploads

For large files, use the multipart endpoints instead of the single PUT flow:

1. `POST /videos/{id}/upload/multipart/initiate`
2. Upload each binary chunk with `PUT /videos/{id}/upload/multipart/{upload_id}/parts/{part_number}`.
3. Resume by retrying only missing or failed parts.
4. `POST /videos/{id}/upload/multipart/{upload_id}/complete` with
   `{"total_parts": 3}`.
5. Cancel with `DELETE /videos/{id}/upload/multipart/{upload_id}`.

Parts are numbered from 1 to 10,000 and each request is limited to 512 MiB.
The upload ID is bound to the authenticated video's original-object key. In S3
mode, the API temporarily assembles parts before uploading the completed object,
so the multipart workspace needs sufficient local disk space.

On Windows, verify the tools with `Get-Command ffmpeg, ffprobe`. If they are
installed through WinGet but are not on `PATH`, set
`STREAMFORGE_FFMPEG_PATH` and `STREAMFORGE_FFPROBE_PATH` to their absolute
executable paths before starting the worker. On Windows, use
`http://127.0.0.1:9000` for `STREAMFORGE_STORAGE_ENDPOINT` to avoid local
IPv6 resolution issues.

The development Compose file uses the maintained `coollabsio/minio` image
because the former anonymous `minio/minio` Docker Hub image is no longer
available.

### Playback visibility

Owner playback uses `GET /videos/{id}/stream`. Public and unlisted ready
videos can also be played without a session through
`GET /public/videos/{id}/stream`. Private videos return `404` from the public
route. The web dashboard embeds the returned HLS playlist in its player; the
browser must support HLS natively unless an HLS JavaScript player is added.

### Processing progress

Authenticated clients can poll `GET /videos/{id}/processing` for the latest
processing stage and percentage. The worker reports `preparing`, `finalizing`,
and `completed` stages. PostgreSQL deployments apply
`migrations/002_processing_progress.sql`; existing databases should run that
migration before enabling the progress endpoint.

### Durable multipart sessions

PostgreSQL deployments also persist multipart sessions in
`upload_sessions` and successfully received parts in `upload_parts`. Apply
`migrations/003_multipart_parts.sql` to an existing database. Re-uploading a
part updates its recorded size and timestamp, while completing or aborting an
upload updates the session status.

### API security defaults

The API applies security response headers and an in-memory per-client limit of
120 requests per minute. Requests over the limit receive HTTP `429` and a
`Retry-After` header. This limiter is intended for local and single-instance
deployments; production multi-instance deployments should move the counter to
Redis or an API gateway.

### Search

`GET /search/videos?q=term&limit=20` searches ready public and unlisted videos
by title and description. Private videos are never returned. The dashboard
includes a public-library search form.

### Likes

Authenticated users can toggle a like with `POST /videos/{id}/like` and read
their current state plus the total with `GET /videos/{id}/likes`. Apply
`migrations/004_likes.sql` to existing PostgreSQL volumes.

### Comments

Ready public and unlisted videos support comments through
`GET /videos/{id}/comments`, `POST /videos/{id}/comments`, and
`DELETE /videos/{id}/comments/{commentID}`. Apply
`migrations/005_comments.sql` to existing PostgreSQL volumes. Private videos
do not expose comments.

### Web dashboard

Start the Vite dashboard with:

```powershell
npm --prefix apps\web install
npm --prefix apps\web run dev
```

Open `http://localhost:5173`, then register or log in. The dashboard creates
videos, uploads small files through the signed single-upload flow, and uploads
files of 8 MiB or larger through the resumable multipart API. Multipart
progress and the completed part list are stored in browser `localStorage`, so
selecting the same file again resumes an interrupted upload. Failed parts are
retried up to three times.
