
# files-go

`files-go` is a NAS file intelligence layer. It indexes local storage into a
rebuildable SQLite catalog so directory browsing and metadata requests never
need to wake or enumerate disks.

The current implementation provides the Phase 1/2 core and substantial parts
of the Phase 3-5 media and playback roadmap described in
[`docs/design.md`](docs/design.md). The design document is a roadmap rather
than a claim that every section is implemented.

- local storage isolation with traversal and symlink-escape protection;
- an opaque-ID filesystem catalog and SQLite migrations;
- generation-based scanning with offline-storage preservation;
- cursor-paginated entry APIs that do not expose physical paths;
- direct file content and HTTP Range support.
- a responsive Preact list/grid UI with persistent light and dark themes;
- SQLite FTS5 file search scoped by library, type, or extension.
- native image, audio, video, PDF, and size-limited text previews.
- storage-first directory creation, recursive copy, rename/move, and safe non-recursive delete.
- streaming multipart uploads that never buffer an entire file in memory.
- a persistent SQLite job queue with deduplication, retries, leases, and a bounded worker pool.
- image dimensions and cached small/medium/large thumbnails;
- ffprobe-backed video, audio, and normalized music metadata;
- bounded EPUB package parsing and pdfinfo-backed PDF metadata.
- separate movie, series/season/episode, track, photo, and book catalog items;
- optional TMDB movie/TV matching with confidence scoring and manual override APIs;
- cached TMDB posters, episode details, and media annotations attached to the
  physical file/folder views.
- capability-based direct play, remux, and single-profile HLS transcoding;
- per-user resume state and a Continue Watching media shelf.

Important design work that is still outstanding includes authentication,
permissions and sharing, health and metrics endpoints, cache GC and supported
SQLite backup tooling, mount identity verification beyond root availability,
and optional extensions such as archive browsing, waveform
generation, and non-local storage adapters.

## Configuration

Copy `config.yaml` to `~/.filesgo/config.yaml` and adjust the storage paths:

```yaml
listen: ":8088"
data: "/var/lib/files-go"

processing:
  workers: 2
  ffprobe: ffprobe
  ffmpeg: ffmpeg
  pdfinfo: pdfinfo

media:
  tmdb:
    token: "${TMDB_TOKEN}"
    language: zh-CN

storages:
  - id: data
    name: Data
    type: local
    path: /mnt/data

libraries:
  - id: movies
    name: Movies
    type: movies
    sources:
      - storage: data
        path: Movies
```

Start the server with `go run .`. Use `-d` to select another configuration
directory.

## API

The HTTP API is rooted at `/api/v1`. Core endpoints include:

```text
GET  /api/v1/system
GET  /api/v1/storages
POST /api/v1/storages/{id}/scan
GET  /api/v1/libraries
GET  /api/v1/search?q={query}
GET  /api/v1/entries/{id}
PATCH /api/v1/entries/{id}
DELETE /api/v1/entries/{id}
GET  /api/v1/entries/{id}/children
POST /api/v1/entries/{id}/directories
POST /api/v1/entries/{id}/files
POST /api/v1/entries/{id}/copies
GET  /api/v1/entries/{id}/content
HEAD /api/v1/entries/{id}/content
GET  /api/v1/entries/{id}/text
GET  /api/v1/entries/{id}/media
GET  /api/v1/entries/{id}/thumbnail?size=medium
GET  /api/v1/media?type=movie&library=movies
GET  /api/v1/media/{id}
GET  /api/v1/media/{id}/poster
GET  /api/v1/entries/{id}/media-item
PUT  /api/v1/entries/{id}/media-item
DELETE /api/v1/entries/{id}/media-item
POST /api/v1/entries/{id}/media-item/rematch
POST /api/v1/playback/{id}
GET  /api/v1/playback/sessions/{session}/{file}
DELETE /api/v1/playback/sessions/{session}
GET  /api/v1/media/{id}/playback-state
PUT  /api/v1/media/{id}/playback-state
GET  /api/v1/playback/continue
```

## Development

Run `go test ./...` to exercise catalog scanning, offline behavior, path
security, path redaction, cursor listing, and HTTP Range streaming.
