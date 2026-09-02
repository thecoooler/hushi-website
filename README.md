# hushi-website

Small Go service that combines the Hushi landing page with the Android release
API. It is intentionally separate from `hushi-server`: the release service
does not need access to Herdr, phone session tokens, or the user's home PC.

## Run locally

```sh
go run ./cmd/hushi-website
open http://127.0.0.1:8080
```

The upload API is disabled until a token is configured:

```sh
export HUSHI_WEBSITE_UPLOAD_TOKEN="use-a-long-random-secret"
export HUSHI_WEBSITE_RELEASE_DIR="/var/lib/hushi-website/releases"
go run ./cmd/hushi-website -addr :8080
```

The release directory contains generated APK files and `latest.json`; it is
not part of Git. A new file is fully written before `latest.json` switches to
it, so clients never receive an in-progress upload.

## API

Public endpoints:

```http
GET /api/v1/releases/latest
GET /api/v1/releases/latest/apk
GET /api/v1/server/releases/latest
GET /api/v1/server/releases/latest/<asset>
```

Upload a release with the token in the `Authorization` header:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $HUSHI_WEBSITE_UPLOAD_TOKEN" \
  -F apk=@app-release.apk \
  -F version=0.1.0-m7 \
  -F version_code=2 \
  -F notes="Keyboard and release updates" \
  https://updates.example.com/api/v1/releases
```

The service validates the version, size, and ZIP/APK signature, calculates
SHA-256, and publishes metadata like:

```json
{
  "version": "0.1.0-m7",
  "version_code": 2,
  "notes": "Keyboard and release updates",
  "mandatory": false,
  "published_at": "2026-09-02T12:00:00Z",
  "size_bytes": 37748736,
  "sha256": "…",
  "download_url": "/api/v1/releases/latest/apk"
}
```

Server binaries use the same upload token and are published as one complete
release with repeated `asset` fields. The public installer uses this channel:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $HUSHI_WEBSITE_UPLOAD_TOKEN" \
  -F version=0.3.0 \
  -F asset=@hushi-linux-amd64 \
  -F asset=@hushi-linux-arm64 \
  -F asset=@hushi-darwin-amd64 \
  -F asset=@hushi-darwin-arm64 \
  -F asset=@checksums.txt \
  https://updates.example.com/api/v1/server/releases
```

The installer is available at `/install.sh`; it selects the platform binary,
checks its SHA-256 against `checksums.txt`, installs `hushi`, and runs
`hushi setup`.

## Production notes

Run the service as an unprivileged account with write access only to the
release directory and put it behind an HTTPS reverse proxy. Keep
`HUSHI_WEBSITE_UPLOAD_TOKEN` outside the repository; rotate it if it leaks.
Public download is deliberate so the landing page and the Android updater can
fetch the same artifact. The default maximum APK size is 512 MiB and can be
changed with `HUSHI_WEBSITE_MAX_APK_BYTES`.

An example hardened systemd unit is in
[`deploy/systemd/hushi-website.service.example`](deploy/systemd/hushi-website.service.example).

Before publishing, build the APK with the release URL configured:

```sh
./gradlew -PhushiUpdateBaseUrl=https://updates.example.com \
  :app:assembleRelease
```
