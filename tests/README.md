# StreamForge validation tests

## API integration flow

The Go API package includes an in-process integration test that exercises the
registered HTTP router and session cookie across registration, authenticated
video creation, playlist membership, and logout. Run it with:

```powershell
go test ./apps/api
```

## Worker retry, recovery, and duplicate-claim integration tests

The Redis integration tests verify retry counts, dead-letter payloads,
trace-context preservation, active claim refresh, and recovery of a job left
pending by a crashed worker. PostgreSQL integration tests verify atomic job
claims, concurrent duplicate suppression, retry exhaustion, and takeover of
stale `PROCESSING` jobs. Point them at disposable Redis 7 and PostgreSQL 16
instances; tests use unique Redis streams and an isolated PostgreSQL schema:

```powershell
$env:STREAMFORGE_TEST_REDIS_URL = "redis://localhost:6379/0"
$env:STREAMFORGE_TEST_DATABASE_URL = "postgres://streamforge:streamforge@localhost:5432/streamforge_test?sslmode=disable"
go test ./internal/queue ./internal/processing -run 'RedisQueue|PostgresStatusStore' -v
Remove-Item Env:STREAMFORGE_TEST_REDIS_URL
Remove-Item Env:STREAMFORGE_TEST_DATABASE_URL
```

The GitHub Actions verification job runs these tests against ephemeral Redis
and PostgreSQL services. Each set skips locally when its environment variable
is unset.

## MinIO and real FFmpeg integration tests

The MinIO integration test exercises presigned uploads, completion/stat,
downloads, media uploads, playback URL signing, and multipart assembly. It
uses a unique bucket and removes its objects and bucket when finished. Start a
disposable MinIO instance and set the test endpoint before running:

```powershell
$env:STREAMFORGE_TEST_MINIO_ENDPOINT = "http://localhost:9000"
$env:STREAMFORGE_TEST_MINIO_ACCESS_KEY = "streamforge"
$env:STREAMFORGE_TEST_MINIO_SECRET_KEY = "streamforge-secret"
go test ./internal/storage -run MinIOIntegration -v
Remove-Item Env:STREAMFORGE_TEST_MINIO_ENDPOINT
Remove-Item Env:STREAMFORGE_TEST_MINIO_ACCESS_KEY
Remove-Item Env:STREAMFORGE_TEST_MINIO_SECRET_KEY
```

The real-media processing test generates a short fixture with FFmpeg, then
verifies probed metadata, the master and rendition playlists, transport-stream
segments, and the JPEG thumbnail. It runs automatically when `ffmpeg` and
`ffprobe` are on `PATH`, and skips when they are unavailable locally. Set
`STREAMFORGE_REQUIRE_FFMPEG_TEST=true` to fail instead of skip:

```powershell
go test ./internal/processing -run RealMediaIntegration -v
```

CI installs FFmpeg and starts a disposable MinIO server before running the Go
suite, so both integration tests execute in the verification job.

## Browser dashboard end-to-end test

The Playwright test runs the Vite dashboard in a real Chromium browser and
exercises registration, upload, search, likes, and playback UI. API and object
storage responses are isolated with deterministic test fixtures; backend
storage and media processing are covered by the Go integration tests above.

```powershell
npm --prefix apps/web ci
npx --prefix apps/web playwright install chromium
npm --prefix apps/web test
```

## k6 API smoke/load test

Install k6, start the API, then run:

```powershell
k6 run tests\load\api-smoke.js
```

To test a Kubernetes port-forward:

```powershell
kubectl -n streamforge port-forward service/streamforge-api 8080:8080
$env:STREAMFORGE_API_URL = "http://localhost:8080"
k6 run tests\load\api-smoke.js
```

The test checks health, readiness, version, and Prometheus metrics endpoints.
It runs one virtual user and spaces iterations to stay below the API's default
120-requests-per-minute limiter. On Windows, the PowerShell runner selects an
ephemeral local port and cleans up the API process it launched. The Bash runner
defaults to `127.0.0.1:8080`; override `STREAMFORGE_API_ADDR` if needed. Set
`STREAMFORGE_API_URL` only when intentionally targeting an already-running API
instead of starting a local one.
