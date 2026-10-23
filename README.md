# files-go

A lightweight file index and media browser for a NAS. It indexes any number
of directories into SQLite, so browsing and searching never list the disks,
then enriches files in the background by type, name and embedded metadata:

| Files | Enrichment |
| --- | --- |
| Movies / TV | release-name parsing, folder layout, `movie.nfo` / `tvshow.nfo`, local `poster.jpg` / `fanart.jpg`, TMDB (cached) |
| Video / audio | ffprobe: duration, resolution, codecs, tags, embedded cover |
| Photos | dimensions and EXIF (time, camera, lens, exposure, GPS) |
| Books / documents | EPUB, MOBI/AZW3, DOCX/XLSX/PPTX, PDF (with `pdfinfo`) |
| Apps | APK (package, version, SDK, icon), Windows PE version info, ELF, Mach-O |
| Archives | ZIP entry count and size |

Videos and music play in the browser: directly when the browser supports the
file, otherwise remuxed (or, as a last resort, transcoded) by ffmpeg into a
fragmented MP4 stream. Transcoding uses Intel/AMD GPUs through VAAPI when
available. Embedded text subtitles and external `.srt` / `.ass` files next to
a video are offered as WebVTT tracks. Thumbnails are generated on first view
and cached.

The service is read-only: it opens indexed files read-only and has no
upload, rename or delete operations.

## Design

- **One table.** `files` holds one row per file or folder, with the
  enrichment result as JSON in the same row (`meta`). A full-text trigram
  index covers names and titles; a small `cache` table keeps TMDB responses.
  The index is derived data: an incompatible schema is simply rebuilt.
- **The table is the queue.** `meta_ver` is the enrichment version a row was
  processed with. Rows below the current version are pending, so changed
  files, new enrichers or configuration changes (TMDB on/off, root types)
  re-enrich automatically, and nothing is lost on restart.
- **Scanning** reconciles one directory at a time against the index and only
  writes what changed. A missing root, or one that turned empty and now sits
  on another filesystem than before, is treated as an unmounted disk and
  leaves the index untouched (`POST /api/roots/{root}/scan?force=1` confirms
  a root that is really empty). A full rescan runs periodically; when
  a folder is browsed, one `stat` detects whether it changed and resyncs just
  that level.
- **Enrichers** share one interface (`Match`, `Enrich`) and run in order on
  the same metadata, so the movie lookup can build on ffprobe's result.
  Manual TMDB matches are locked and survive re-enrichment.

```
config/   configuration
store/    SQLite index and scanner
media/    enrichers, the background pipeline and the TMDB adapter
           (github.com/lsongdev/tmdb-go)
server/   HTTP API, thumbnails, streaming
web/      Preact UI (embedded)
```

## Configuration

```yaml
listen: ":8088"
data: /var/lib/files-go       # database and thumbnail cache
workers: 4                    # concurrent enrichment jobs
scan_interval: 6h
hwaccel: auto                 # auto | vaapi | none; checked with a test encode at startup
# hwaccel_device: /dev/dri/renderD128
auth:                         # HTTP basic auth; omit to run without auth
  username: me
  password: secret
tmdb:
  key: "${TMDB_KEY}"          # v3 API key or v4 read token; omit to stay offline
  language: zh-CN
# exclude: [".*", "node_modules", ...]   # name patterns, these are the defaults
roots:
  - name: Videos
    path: /data/Videos
    types:                    # media hints for subfolders
      Movies: movies
      TV Shows: tv
  - name: Music
    path: /data/Music
    type: music
  - name: Documents
    path: /data/Documents
```

`type` is a hint for identification. Under `movies`, each folder or loose
video is looked up as a film; under `tv`, the folders directly below are
shows, `S01` / `Season 1` folders are seasons, and files are episodes.
Elsewhere, only release-style names (title, year and a quality tag) are
looked up, and plain videos stay plain videos.

Run with `files-go -c config.yaml`. `ffmpeg` / `ffprobe` are needed for video
and audio; `pdfinfo` / `pdftoppm` (poppler) are optional for PDFs.

See [`deploy/`](deploy) for a systemd unit that additionally enforces
read-only access to the data volume; it joins the `render` group for VAAPI.

## API

```text
GET    /api/roots                    configured roots and scan state
POST   /api/roots/{root}/scan        rescan a root
GET    /api/status                   counts, scan and enrichment progress
GET    /api/search?q=&root=&kind=
GET    /api/files/{id}               file with metadata and ancestors
GET    /api/files/{id}/children?offset=&limit=
POST   /api/files/{id}/refresh       resync a folder / re-enrich a file
GET    /api/files/{id}/raw           content, supports Range; ?download
GET    /api/files/{id}/thumb         thumbnail or poster
GET    /api/files/{id}/backdrop
GET    /api/files/{id}/stream?t=     ffmpeg fMP4 stream from t seconds; &transcode=1
GET    /api/files/{id}/subtitles     subtitle tracks (embedded and external)
GET    /api/files/{id}/subtitles/{track}.vtt
GET    /api/files/{id}/match?q=&type=movie|tv     TMDB candidates
PUT    /api/files/{id}/match         {"tmdb": "movie/123"} lock a match
DELETE /api/files/{id}/match         unlock and re-identify
```

## Development

```sh
go test ./...
go run . -c config.yaml
```
