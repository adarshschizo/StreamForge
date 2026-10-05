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
are written to `video-processing-dlq`. Jobs left pending by a crashed worker
are reclaimed after 30 seconds. Active processing jobs refresh their Redis
claim every 10 seconds so long transcodes are not reclaimed as abandoned.
Retries and dead-letter publication happen before acknowledging the failed
message to avoid losing the job if publishing fails. This provides at-least-
once delivery. With PostgreSQL enabled, an atomic job-status claim prevents
concurrent duplicate deliveries from processing the same active/completed job;
a reclaimed job can take over the previous worker's stale `PROCESSING` state.
Object storage writes remain keyed by video and should be idempotent.
The worker uses FFprobe to inspect the source and FFmpeg to generate a
thumbnail, 360p/720p/1080p HLS renditions supported by the source height, and
a master playlist. Configure executable paths with
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

### Watch history

Authenticated playback records the video in watch history. Use
`GET /history?limit=50` to list entries and `DELETE /history/{videoID}` to
remove one. Apply `migrations/006_watch_history.sql` to existing PostgreSQL
volumes.

### Analytics

Authenticated playback records view events. Owners can query aggregated counts
with `GET /analytics/videos`. Apply `migrations/007_analytics.sql` to existing
PostgreSQL volumes.

### Playlists

Authenticated users can create and manage playlists with `GET /playlists`,
`POST /playlists`, `GET /playlists/{id}`, `POST /playlists/{id}/videos`, and
`DELETE /playlists/{id}/videos`. Apply `migrations/008_playlists.sql` to
existing PostgreSQL volumes.

### Subscriptions

Authenticated users can follow creators with
`POST /subscriptions/{creatorID}`, list followed creators with
`GET /subscriptions`, and unfollow with `DELETE /subscriptions/{creatorID}`.
Apply `migrations/009_subscriptions.sql` to existing PostgreSQL volumes.

### Production containers

Production images are defined by `Dockerfile.api`, `Dockerfile.worker`, and
`apps/web/Dockerfile`. Start the local production-shaped stack with:

```powershell
docker compose -f docker-compose.yml -f docker-compose.prod.yml up --build
```

The API is exposed on port `8080`, the web container on port `5173`, and the
worker uses the Redis queue and FFmpeg runtime. GitHub Actions in
`.github/workflows/ci.yml` runs Go tests, frontend checks, and all three
container builds. Version tags publish the images to GitHub Container Registry
under `ghcr.io/adarshschizo/streamforge-*`.

### Kubernetes delivery

Kubernetes templates are under `k8s/`. Before deployment, replace
`REPLACE_WITH_RELEASE_TAG` in `k8s/api.yaml`, `k8s/worker.yaml`, and
`k8s/web.yaml` with a version tag that has been published to GHCR. Create the
namespace and a private copy of the secret environment template:

```powershell
kubectl apply -f k8s/namespace.yaml
Copy-Item k8s/streamforge-secrets.env.example k8s/streamforge-secrets.env
```

Edit `k8s/streamforge-secrets.env` with real credentials before creating the
Secret. The completed file is git-ignored and must not be committed.

```powershell
kubectl create secret generic streamforge-secrets --namespace streamforge --from-env-file=k8s/streamforge-secrets.env
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/api.yaml -f k8s/worker.yaml -f k8s/web.yaml -f k8s/hpa.yaml
```

Do not apply the example environment file as credentials. For repeat
deployments, update the Secret with:

```powershell
kubectl create secret generic streamforge-secrets --namespace streamforge --from-env-file=k8s/streamforge-secrets.env --dry-run=client -o yaml | kubectl apply -f -
```

PostgreSQL, Redis, and object storage should be supplied as managed production
services or added as separate stateful workloads; the templates intentionally
do not package development credentials into Kubernetes.

### Metrics

The API exposes Prometheus-compatible metrics at `GET /metrics`, including
request totals by method and status, cumulative request duration, and current
in-flight requests. Scrape this endpoint from the API Service in Kubernetes.

When `STREAMFORGE_QUEUE_MODE=redis`, the API also uses Redis-backed
per-client rate-limit counters so limits are shared across API replicas. If
Redis is unavailable, it falls back to the existing process-local limiter.

Monitoring assets are in `monitoring/`. `prometheus.yml` scrapes the API
metrics endpoint and `grafana-dashboard.json` provides request-rate,
latency, and in-flight-request panels. `k8s/servicemonitor.yaml` is provided
for clusters running the Prometheus Operator; apply it only when that CRD is
installed.

### Distributed tracing

The API creates OpenTelemetry HTTP server spans and injects W3C trace context
into processing jobs. The worker extracts that context and creates a consumer
span around video processing, allowing an upload request and its asynchronous
processing job to be correlated. Request logs include the trace ID.

Tracing is disabled by default. To enable OTLP/HTTP export, set
`STREAMFORGE_OTEL_ENABLED=true` and configure
`STREAMFORGE_OTEL_EXPORTER_OTLP_ENDPOINT` as the collector's base URL (for
example, `http://otel-collector:4318`). Set
`STREAMFORGE_OTEL_SERVICE_NAME` to a shared service prefix; the API and worker
append `-api` and `-worker`. The collector must accept OTLP over HTTP on port
4318 and be reachable from both services. Kubernetes keeps tracing disabled
until the operator supplies a reachable collector and enables it in
`k8s/configmap.yaml`. If tracing is enabled but exporter initialization fails,
the service exits rather than silently running without traces.

### Container publishing

The CI workflow publishes API, worker, and web images to GitHub Container
Registry when a version tag such as `v1.0.0` is pushed. Pull requests and
regular branch pushes only build and validate images; they do not publish.

```powershell
git tag v1.0.0
git push origin v1.0.0
```

Published images:

```text
ghcr.io/adarshschizo/streamforge-api:v1.0.0
ghcr.io/adarshschizo/streamforge-worker:v1.0.0
ghcr.io/adarshschizo/streamforge-web:v1.0.0
```

### Load validation

A small k6 smoke/load test is available at `tests/load/api-smoke.js`. It
checks the health, readiness, version, and metrics endpoints with a 1% error
budget and a 500 ms p95 latency target. See `tests/README.md` for local and
Kubernetes port-forward commands, plus the MinIO, real FFmpeg, and browser
end-to-end validation instructions.

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
