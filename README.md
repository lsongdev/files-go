
# files-go

`files-go` is a NAS file intelligence layer. It indexes local storage into a
rebuildable SQLite catalog so directory browsing and metadata requests never
need to wake or enumerate disks.

The current implementation provides the Phase 1 core and the first Phase 2
file-manager features described in [`docs/design.md`](docs/design.md):

- local storage isolation with traversal and symlink-escape protection;
- an opaque-ID filesystem catalog and SQLite migrations;
- generation-based scanning with offline-storage preservation;
- cursor-paginated entry APIs that do not expose physical paths;
- direct file content and HTTP Range support.
- a responsive Preact list/grid UI with persistent light and dark themes;
- SQLite FTS5 file search scoped by library, type, or extension.
- native image, audio, video, PDF, and size-limited text previews.
- storage-first directory creation, rename/move, and safe non-recursive delete.

## Configuration

Copy `config.yaml` to `~/.filesgo/config.yaml` and adjust the storage paths:

```yaml
listen: ":8088"
data: "/var/lib/files-go"

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
GET  /api/v1/entries/{id}/content
HEAD /api/v1/entries/{id}/content
GET  /api/v1/entries/{id}/text
```

## Development

Run `go test ./...` to exercise catalog scanning, offline behavior, path
security, path redaction, cursor listing, and HTTP Range streaming.
