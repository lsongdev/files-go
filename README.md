# files-go

A lightweight file index and media browser for a NAS. It indexes any number
of directories into SQLite, so browsing and searching never list the disks,
then enriches files in the background by type, name and embedded metadata:

| Files | Enrichment |
| --- | --- |
| Movies / TV | release-name parsing, folder layout, `movie.nfo` / `tvshow.nfo`, local `poster.jpg` / `fanart.jpg`, TMDB (cached) |
| JAV | exact release-code identification through ThePornDB (cached), local JavBus `<code>.json` records |
| Music folders | album tags, album artist, track count, total duration and artwork; CD/Disc folders aggregate into one album |
| Video / audio | ffprobe: duration, resolution, codecs, tags, embedded cover |
| Photos | dimensions and EXIF (time, camera, lens, exposure, GPS) |
| Books / documents | EPUB, MOBI/AZW3, DOCX/XLSX/PPTX, PDF (with `pdfinfo`) |
| Apps | APK (package, version, SDK, icon), Windows PE version info, ELF, Mach-O |
| Archives | ZIP entry count and size |

Videos and music use the browser's native controls. Opening a detail page
shows a play button; media, subtitles and streaming are requested only after
clicking it. Supported files play directly. Other files use ffmpeg to remux
(or, as a last resort, transcode) into HLS, played natively on Safari/iOS and
through hls.js elsewhere.

The reusable [`pkg/hls`](pkg/hls) backend publishes a complete VOD playlist
without generating media. Requested fragments are generated on demand and
cached, so the native progress bar knows the full duration and can seek to an
uncached position without encoding everything before it. Concurrent requests
share work; the backend does not encode ahead. The default completed-fragment
cache is 512 MiB, with LRU eviction and 30-minute idle expiry, and at most two
probe/encoding jobs run concurrently. Copying video follows source keyframes;
each independently generated fMP4 fragment has its own initialization section
and discontinuity. Transcoding uses Intel/AMD GPUs through VAAPI when available.
Embedded text subtitles and external `.srt` / `.ass` files next to a video are
offered as WebVTT tracks on the full timeline. Thumbnails are generated on first
view and cached.

The service is read-only: it opens indexed files read-only and has no
upload, rename or delete operations.

## Design

- **SQLite.** `files` holds one row per file or folder, with derived JSON
  metadata (`meta`); FTS covers names and titles. `cache` stores catalog
  responses and shared show resolutions. `overrides` separately preserves
  manual matches. Schema upgrades migrate existing data; unsupported versions
  fail without rebuilding the database.
- **The table is the queue.** `meta_ver` is the enrichment version a row was
  processed with. Rows below the current version are pending, so changed
  files, new enrichers or configuration changes (TMDB on/off, root types)
  re-enrich automatically. Per-file retries, delays and errors survive restart;
  provider failures cool down independently and successful probes are cached.
- **Scanning** reconciles one directory at a time against the index and only
  writes what changed. Missing roots leave the index untouched. Changes to
  device/optional marker stop reads and scanning; an unexpectedly empty root
  without a marker requires `POST /api/roots/{root}/scan?force=1` to accept the
  current mount/empty view. Filesystem events trigger background incremental
  scans; periodic full scans reconcile missed events. Browsing reads only the index.
- **Enrichers** share one interface (`Match`, `Enrich`) and run in order on
  the same metadata, so the movie lookup can build on ffprobe's result.
  Files identify content through TMDB or ThePornDB. Folders only aggregate
  file results and local artwork; they never query catalogs. Only settled
  identities rise to a folder: a file awaiting review keeps its guess, and is
  listed for review once. A show's doubts are reviewed on its show folder.
  Changed results
  invalidate their parent for aggregation, with revision checks rejecting stale
  workers. Shared show matching avoids searching separately for every episode.
  Manual matches survive re-enrichment, restarts and schema migration.
- **Identification** scores TMDB candidates on title (exact, partial or
  near, including TMDB's alternative titles such as romanizations), year,
  popularity and local evidence: the main video's duration against the movie
  runtime, and episode files, highest episode and season against the show's
  totals. TMDB's own leading results are verified against their details even
  when no listed title matches. A search without the year still rewards an
  agreeing year. NFO ids are trusted unless the files contradict them. Close
  calls, contradictions and misses are listed for review.

```
config/   configuration
store/    SQLite index and scanner
media/    enrichers, the background pipeline and the TMDB adapter
           (github.com/lsongdev/tmdb-go)
pkg/hls/  independent on-demand HLS VOD timelines, generation and cache
server/   HTTP API, thumbnails, codec/hardware policy and HLS adapter
web/      Preact UI (embedded)
```

## Configuration

```yaml
listen: ":8088"
workers: 4                    # concurrent enrichment jobs
scan_interval: 6h
hwaccel: auto                 # auto | vaapi | none; checked with a test encode at startup
# hwaccel_device: /dev/dri/renderD128
auth:                         # HTTP basic auth; omit to run without auth
  username: me
  password: secret
theporndb:
  token: "${THEPORNDB_TOKEN}" # optional; used only for roots/subpaths with type: jav
tmdb:
  key: "${TMDB_KEY}"          # v3 API key or v4 read token; omit to stay offline
  language: zh-CN
# exclude: [".*", "node_modules", ...]   # name patterns, these are the defaults
roots:
  - name: Videos
    path: /data/Videos
    # mount_marker: .disk-id   # optional existing file with a stable, disk-specific value
    types:                    # media hints for subfolders
      Movies: movies
      TV Shows: tv
  - name: JAV
    path: /data/porn/JAV
    type: jav
  - name: Music
    path: /data/Music
    type: music
  - name: Documents
    path: /data/Documents
```

`type: jav` enables ThePornDB identification by exact release code (filename or parent folder),
with cached metadata and artwork. Similar codes are never accepted automatically.
A JavBus scraper record next to the video (`<code>.json` or `<video>.json`)
confirms the code, picks between catalog entries sharing a code by release
date, adds director, label and series, and describes works ThePornDB lacks.

`type` is a hint for identification. Under `movies`, videos are identified
as films and dedicated folders aggregate their common work; under `tv`, the folders directly below are
shows, `S01` / `Season 1` folders are seasons, and files are episodes.
Elsewhere, only release-style names (title, year and a quality tag) are
looked up, and plain videos stay plain videos.

The UI scripts are embedded locally with pinned versions and licenses. Metadata
and remote artwork still need their configured online services. Timeline caches
survive restarts (32 MiB/1024 entries); fragment caches remain temporary and their
directory is exclusively locked. Thumbnail/subtitle generation has bounded
concurrency and input/output budgets; see [the audit status](docs/improvements.md).

Run with `files-go -d dir` to load `dir/config.yaml` (default: `~/.filesgo`).
Everything the program writes stays in that directory: the database, thumbnail,
subtitle and HLS caches, and temporary files. `TMPDIR` and `XDG_CACHE_HOME` are
pointed at `dir/tmp` and `dir/cache/xdg` for SQLite and the external tools, and
`dir/tmp` is cleared at startup. The former `data:` setting is rejected unless it
names the same directory.
File changes are indexed in the background using filesystem notifications.
Events for the same directory are merged until it has been quiet for two seconds;
new or moved-in directory trees are discovered and watched recursively. Browsing
and pagination read only the database and never check the media filesystem.
Startup scans, `scan_interval` scans, and recovery scans after watcher overflow
reconcile missed changes. Watch registration failures are logged and exposed as
`watchError` in root status; periodic scans remain active. Network mounts such as
NFS/SMB may not deliver notifications and rely on periodic or manual scans.
A configured marker must exist and remain readable, including during forced
scans. A changed identity is reported as `mount_changed`; force accepts a new
identity only after checking the marker. Without a marker, an unexpectedly empty
root is retained even on the same device.
Open pages follow the index live through server-sent events (`/api/events`):
a folder reloads when a change names it, and the sidebar status opens a live
activity page with scan state, files being identified and recent results.
Events are coalesced once a second and are only hints; pages re-read data through
the API, and fall back to polling folder versions while the stream is down.
The scan interval starts after each periodic scan completes.

Go 1.25+ is required to build. Source files are opened through `os.Root`;
external tools receive inherited seekable descriptors on Linux/macOS rather
than reopening media paths. Windows builds support rooted Go readers, but the
external-tool descriptor input is not supported there.

`ffmpeg` / `ffprobe` are needed for video
and audio; `pdfinfo` / `pdftoppm` (poppler) are optional for PDFs.

See [`deploy/`](deploy) for a systemd unit that additionally enforces
read-only access to the media volume; it joins the `render` group for VAAPI.
Create `/data/files-go` owned by the service user and put the binary and
`config.yaml` there; the service writes nothing outside that directory, so a
small SD/USB system disk is never used.

## API

```text
GET    /api/roots                    configured roots and scan state
POST   /api/roots/{root}/scan        rescan a root
GET    /api/status                   counts, scan and enrichment progress, running files
GET    /api/events                   server-sent events: status, changes, activity
GET    /api/activity                 recent activity (newest first) and status
GET    /api/search?q=&root=&kind=
GET    /api/issues?type=review|error      matches to confirm / failed files
GET    /api/files/{id}               file with metadata and ancestors
GET    /api/files/{id}/children?offset=&limit=
POST   /api/files/{id}/refresh       resync a folder / re-enrich a file
GET    /api/files/{id}/raw           content, supports Range; ?download
GET    /api/files/{id}/thumb         thumbnail or poster
GET    /api/files/{id}/backdrop
GET    /api/files/{id}/play          create complete HLS VOD timeline; ?transcode=1
GET    /api/hls/{session}/{file}     playlist and on-demand fragments (session key is the credential)
DELETE /api/hls/{session}            release playback token; retain cached fragments
GET    /api/files/{id}/subtitles     subtitle tracks (embedded and external)
GET    /api/files/{id}/subtitles/{track}.vtt
GET    /api/files/{id}/match?q=&provider=tmdb|theporndb&type=movie|tv|jav
PUT    /api/files/{id}/match         {"match":{"provider":"tmdb","type":"movie","id":"123"}}
DELETE /api/files/{id}/match         unlock and re-identify
```

Matches use the same `{provider,type,id}` identity in `meta.match` and candidate
results. Directory matches apply to their media subtree; matching a file within
an identified common work targets that work's scope (returned with candidates).
More specific choices take precedence. Choices remain associated with the same
root ID and path after deletion/reappearance or removing a root; renames/moves do
not transfer them. Schema v1 locks are migrated automatically. The old `tmdb`
request/metadata shape has been replaced.

## Development

```sh
go test ./...
node web/app_test.cjs
node web/metadata_test.cjs
go run . -d .
```
