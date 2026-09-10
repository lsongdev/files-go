# files-go NAS File Engine 设计文档

Status: Draft
Target: v1 architecture
Language: Go
Primary database: SQLite
Primary storage backend: Local filesystem

---

## 1. 项目定位

`files-go` 是运行在 NAS 上的文件服务引擎。

它不负责重新实现一个文件系统，也不负责管理 RAID、磁盘块、对象副本或分布式一致性。

它的职责是建立在已有文件系统之上的一层统一服务：

```text
Physical Storage
    │
    ├── /mnt/disk1
    ├── /mnt/disk2
    ├── /mnt/archive
    └── ...
          │
          ▼
      files-go
          │
          ├── Catalog
          ├── Search
          ├── Metadata
          ├── Preview
          ├── Media
          ├── Streaming
          └── API
                │
                ▼
          Web / App / Client
```

产品定位可以概括为：

> files-go is a NAS File Intelligence Layer.

它既是文件管理器后端，也可以逐步承担照片、音乐、电影、电视剧、电子书等媒体服务能力。

---

# 2. 设计目标

files-go 的第一目标不是取代 Plex、Jellyfin、SeaweedFS 或操作系统文件系统。

第一目标是：

**让大量位于不同磁盘中的普通文件，可以被快速、安全、统一地浏览、搜索、访问和增强。**

系统必须满足以下核心目标。

### 2.1 文件浏览不依赖实时磁盘枚举

正常的：

```http
GET /api/v1/entries
GET /api/v1/search
GET /api/v1/libraries
```

必须只访问数据库。

不得因为一次目录浏览执行：

```text
ReadDir
Stat
Walk
```

尤其不能为了验证缓存有效性，对每一个结果执行 `os.Stat()`。

这意味着，即使机械硬盘正在休眠，浏览文件目录仍应该是毫秒级响应。

---

### 2.2 文件系统是真实数据源

SQLite Catalog 是：

```text
cache
index
metadata database
search database
```

而不是文件系统自身。

必须始终满足：

```text
filesystem = source of truth
catalog    = rebuildable derived state
```

删除数据库以后，通过重新扫描应该可以恢复所有基础文件信息。

---

### 2.3 文件索引在磁盘离线时仍然可用

某块硬盘掉线时：

```text
/mnt/disk2 -> unavailable
```

Catalog 不应该立即删除其中的文件。

用户仍然应该能够看到：

```text
Movies
├── Dune.mkv             unavailable
├── Interstellar.mkv     unavailable
└── Oppenheimer.mkv      unavailable
```

缩略图、海报、电影信息等派生数据仍然可以正常显示。

只有真实内容读取操作失败。

---

### 2.4 文件系统结构与媒体结构分离

文件世界：

```text
Movies/
  Interstellar.2014.2160p.mkv
  Interstellar.zh.srt
  poster.jpg
```

媒体世界：

```text
Movie
  Interstellar
    Video
    Subtitle
    Artwork
```

不能简单把：

```text
poster
rating
overview
artist
album
season
episode
```

全部塞进 `File`。

因此系统必须明确存在两个模型：

```text
Filesystem Catalog
Media Catalog
```

---

### 2.5 API First

后端不再负责页面模板渲染。

files-go 只提供：

```text
HTTP API
Static frontend assets
Raw file content
Media streams
Derived artifacts
```

Web UI 使用：

```text
Preact
htm
ESM
No bundle
No build
```

API 与 Web UI 独立。

未来 iOS、Android、CLI、WebDAV gateway 等客户端都使用同一套后端能力。

---

# 3. 非目标

v1 明确不实现：

```text
distributed filesystem
RAID
erasure coding
block storage
storage replication
multi-node consensus
S3-compatible object server
full Plex/Jellyfin protocol compatibility
full video transcoding farm
real-time multi-node catalog synchronization
```

如果未来真的需要这些能力，应通过外部 Storage Backend 提供，而不是在 files-go 内部重新实现。

因此：

```text
SeaweedFS
S3
WebDAV
NFS
```

未来可以成为 Storage Adapter。

但不应该成为 files-go 的架构基础。

---

# 4. 核心架构

系统分成五个主要领域：

```text
                 ┌─────────────────────┐
                 │      API Layer      │
                 └──────────┬──────────┘
                            │
       ┌────────────────────┼────────────────────┐
       │                    │                    │
       ▼                    ▼                    ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│ Catalog      │     │ Media        │     │ Storage      │
│ Engine       │     │ Engine       │     │ Engine       │
└──────┬───────┘     └──────┬───────┘     └──────▲───────┘
       │                    │                    │
       └──────────┬─────────┘                    │
                  ▼                              │
           ┌──────────────┐                      │
           │ Indexer      │──────────────────────┘
           │ Engine       │
           └──────────────┘
```

五层分别承担：

```text
Storage Engine
    实际文件访问

Catalog Engine
    文件数据库、目录、搜索

Indexer Engine
    filesystem -> catalog

Media Engine
    metadata / artwork / ffprobe / media model

API Engine
    HTTP protocol / auth / response
```

这是项目最重要的领域边界。

---

# 5. Storage Engine

Storage Engine 是唯一允许直接访问真实文件系统的组件。

其他模块禁止自行调用：

```go
os.Open
os.Stat
os.ReadDir
os.Remove
os.Rename
filepath.Walk
```

必须通过 Storage Engine。

第一版只实现：

```text
LocalStorage
```

未来可以增加：

```text
S3Storage
SeaweedStorage
WebDAVStorage
```

但第一版不应为了未来 backend 设计庞大抽象。

建议接口保持非常小：

```go
type Storage interface {
    Stat(ctx context.Context, path string) (FileInfo, error)

    Open(ctx context.Context, path string) (io.ReadSeekCloser, error)

    ReadDir(ctx context.Context, path string) ([]FileInfo, error)

    Create(ctx context.Context, path string, r io.Reader) error

    Mkdir(ctx context.Context, path string) error

    Rename(ctx context.Context, oldPath, newPath string) error

    Remove(ctx context.Context, path string) error
}
```

必要时可以增加：

```go
type WatchableStorage interface {
    Storage

    Watch(ctx context.Context, path string) (<-chan Event, error)
}
```

不要把 watcher 强行塞进所有 Storage 实现。

---

# 6. Storage

Storage 代表一个真实存储源。

例如：

```yaml
storages:
  - id: disk1
    type: local
    path: /mnt/disk1

  - id: disk2
    type: local
    path: /mnt/disk2
```

数据库：

```sql
CREATE TABLE storages (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL,

    state           TEXT NOT NULL DEFAULT 'unknown',

    last_seen_at    DATETIME,
    last_scan_at    DATETIME,
    scan_generation INTEGER NOT NULL DEFAULT 0,

    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL
);
```

`path` 等 backend-specific 配置不一定要进入数据库。

第一版可以继续保存在 YAML。

数据库只保存运行状态。

Storage state：

```text
unknown
online
offline
error
scanning
```

---

# 7. Library

Storage 是物理概念。

Library 是用户概念。

例如：

```text
Storage
    disk1
    disk2

Library
    Movies
    TV
    Music
    Photos
    Books
```

一个 Library 可以引用多个目录：

```yaml
libraries:
  - id: movies
    name: Movies
    type: movies

    sources:
      - storage: disk1
        path: Movies

      - storage: disk2
        path: Videos/Movies
```

因此：

```text
Library != Directory
```

Library 本质是：

> 一组文件源的逻辑集合。

---

# 8. Library Source

数据库：

```sql
CREATE TABLE library_sources (
    id          INTEGER PRIMARY KEY,

    library_id  TEXT NOT NULL,
    storage_id  TEXT NOT NULL,

    path        TEXT NOT NULL,

    created_at  DATETIME NOT NULL,

    UNIQUE(library_id, storage_id, path)
);
```

未来可以在 source 级增加：

```text
include patterns
exclude patterns
readonly
follow_symlink
hidden files
scanner policy
```

但第一版不必加入。

---

# 9. Entry：Filesystem Catalog 的核心

`Entry` 是系统中最重要的数据模型。

每一个：

```text
file
directory
symlink
```

对应一个 Entry。

建议结构：

```go
type Entry struct {
    ID string

    StorageID string
    ParentID  *string

    Name string
    Path string

    Type EntryType

    Size  int64
    MTime time.Time

    Mime      string
    Extension string

    Available bool

    CreatedAt time.Time
    UpdatedAt time.Time
}
```

其中：

```text
Path
```

必须是 storage-relative path。

例如真实路径：

```text
/mnt/disk2/Movies/Dune/Dune.mkv
```

数据库只保存：

```text
storage_id = disk2
path       = Movies/Dune/Dune.mkv
```

绝不把：

```text
/mnt/disk2
```

暴露给 API。

---

# 10. Entry ID

API 不应该使用 filesystem path 作为资源标识。

必须使用 opaque ID：

```text
01JX7DW...
```

可以使用：

```text
UUIDv7
ULID
```

两者任选其一。

推荐 UUIDv7，因为已经进入标准 UUID 体系。

最重要的语义是：

> Entry ID 表示 catalog entity identity，而不是 path identity。

rename：

```text
Movies/foo.mkv
        ↓
Movies/bar.mkv
```

Entry ID 不变。

这样：

```text
thumbnail
bookmark
playback position
media mapping
share
favorite
```

都不会因为 rename 丢失。

---

# 11. entries Schema

建议：

```sql
CREATE TABLE entries (
    id              TEXT PRIMARY KEY,

    storage_id      TEXT NOT NULL,
    parent_id       TEXT,

    name            TEXT NOT NULL,
    path            TEXT NOT NULL,

    type            TEXT NOT NULL,

    size            INTEGER NOT NULL DEFAULT 0,
    mtime           DATETIME,

    inode           INTEGER,
    device          INTEGER,

    mime            TEXT,
    extension       TEXT,

    available       INTEGER NOT NULL DEFAULT 1,

    scan_generation INTEGER NOT NULL DEFAULT 0,

    created_at      DATETIME NOT NULL,
    updated_at      DATETIME NOT NULL,

    UNIQUE(storage_id, path)
);

CREATE INDEX idx_entries_parent
ON entries(storage_id, parent_id);

CREATE INDEX idx_entries_path
ON entries(storage_id, path);

CREATE INDEX idx_entries_name
ON entries(name);

CREATE INDEX idx_entries_generation
ON entries(storage_id, scan_generation);
```

注意：

```text
parent_id
```

非常重要。

目录浏览必须是：

```sql
SELECT *
FROM entries
WHERE parent_id = ?
```

而不是：

```sql
WHERE path LIKE '/foo/%'
```

目录树是图结构，不应该通过字符串前缀模拟。

---

# 12. Root Entry

每一个 Storage 应该有一个虚拟 root Entry：

```text
storage: disk1

Entry
    id: ...
    name: ""
    path: ""
    parent_id: null
    type: directory
```

于是所有目录都拥有 parent。

这能极大简化：

```text
listing
breadcrumb
move
permissions
tree
API
```

---

# 13. Directory Listing

目录请求：

```http
GET /api/v1/entries/{id}/children
```

只能执行 SQLite query。

禁止任何实时：

```text
ReadDir
Stat
```

响应：

```json
{
  "items": [
    {
      "id": "01...",
      "name": "Movies",
      "type": "directory",
      "size": 0,
      "available": true,
      "modifiedAt": "2026-09-10T10:00:00Z"
    }
  ],
  "cursor": null
}
```

---

# 14. 分页

不要设计：

```text
page=92
size=100
```

作为长期 API。

大目录中：

```sql
OFFSET 100000
```

会逐渐变慢。

推荐 cursor pagination。

例如：

```http
GET /children?limit=100&after=...
```

默认排序：

```text
directory first
name ascending
id ascending
```

cursor 包含最后：

```text
type
name
id
```

第一版如果为了实现速度先使用 offset 也可以。

但 API contract 最好直接设计成 cursor。

---

# 15. Catalog 不验证实时文件存在性

查询 Entry 时：

```go
Catalog.GetEntry()
Catalog.ListChildren()
```

绝不调用：

```go
os.Stat()
```

Catalog 的：

```text
available
```

字段代表 Indexer 最近已知状态。

真正访问内容时：

```http
GET /entries/:id/content
```

Storage 才检查真实文件。

如果不存在：

```text
404 file_not_found
```

同时可以异步向 Indexer 提交：

```text
entry_missing
```

事件。

---

# 16. Indexer Engine

Indexer 负责：

```text
filesystem
      ↓
Catalog
```

Indexer 有三种数据来源：

```text
Initial Scan
Filesystem Events
Reconciliation Scan
```

---

# 17. Initial Scan

首次添加 Storage / Library source 时：

```text
Storage.ReadDir
      ↓
recursive traversal
      ↓
batch catalog upsert
```

Scanner 只收集：

```text
name
path
type
size
mtime
inode/device
```

不要：

```text
请求 TMDB
运行 ffmpeg
解析 EPUB
生成缩略图
下载海报
```

这些全部进入异步 Job。

目标是：

> filesystem traversal 尽可能接近纯目录扫描速度。

---

# 18. Scan Generation

全量扫描使用 generation，而不是在内存保存：

```text
map[path]bool
```

流程：

```text
generation = storage.scan_generation + 1
```

开始扫描：

```sql
UPDATE storages
SET state = 'scanning'
WHERE id = ?;
```

发现 Entry：

```sql
INSERT INTO entries(...)
VALUES(...)
ON CONFLICT(storage_id, path)
DO UPDATE SET
    ...,
    scan_generation = ?,
    available = 1;
```

扫描成功以后：

```sql
UPDATE entries
SET available = 0
WHERE storage_id = ?
  AND scan_generation < ?;
```

然后：

```sql
UPDATE storages
SET
    scan_generation = ?,
    state = 'online',
    last_scan_at = CURRENT_TIMESTAMP;
```

只有：

```text
完整扫描成功
```

才能执行 stale 标记。

扫描失败：

```text
绝不删除旧 Entry
绝不把整盘标记为空
```

---

# 19. 为什么使用 available=false 而不是 DELETE

发现文件不存在时默认：

```text
available = false
```

而不是立即物理删除数据库记录。

这样保留：

```text
metadata
thumbnail
media match
watch progress
favorites
history
```

可以增加 GC：

```text
offline 30 days
没有用户引用
没有 media association
```

才真正 DELETE。

第一版甚至可以永远不自动 GC。

---

# 20. Filesystem Watcher

LocalStorage 使用 filesystem notification：

```text
create
write
remove
rename
```

事件进入 Indexer。

流程：

```text
filesystem event
      ↓
event debounce
      ↓
stat affected entry
      ↓
catalog transaction
      ↓
schedule metadata jobs
```

Watcher 的作用：

```text
降低 catalog 延迟
```

它不是 consistency guarantee。

原因是：

```text
事件可能丢失
服务可能停止
硬盘可能脱机
队列可能 overflow
```

因此必须保留 reconciliation。

---

# 21. Reconciliation

完整模型：

```text
Watcher
    fast

Reconciliation
    correct
```

建议默认：

```text
initial full scan
filesystem watcher
daily reconciliation
manual rescan API
```

可以提供：

```http
POST /api/v1/storages/:id/scan
```

或：

```http
POST /api/v1/libraries/:id/scan
```

---

# 22. Rename Detection

Local filesystem 可以利用：

```text
device
inode
```

辅助判断 rename。

如果：

```text
old.path missing
new.path created
device/inode same
```

则应该：

```text
UPDATE existing entry
```

而不是：

```text
DELETE + INSERT
```

从而保留 Entry ID。

但 inode 不是跨 backend 的通用 identity。

因此这是：

```text
LocalStorage optimization
```

不是 Storage API 的核心语义。

---

# 23. Scanner 与 Library 的关系

Catalog 应该索引 Storage。

Library 只引用 Catalog 中的 Entry。

不要同一个真实文件因为加入两个 Library 而存两份 Entry。

结构：

```text
Storage
    ↓
Entries
    ↑
Library Source
    ↑
Library
```

因此：

```text
physical file : Entry = 1 : 1
```

而：

```text
Library : Entry = many : many
```

是逻辑关系。

---

# 24. 搜索

基础搜索使用 SQLite FTS5。

建议单独：

```sql
CREATE VIRTUAL TABLE entry_search
USING fts5(
    entry_id UNINDEXED,
    name,
    title,
    metadata
);
```

文件系统基础信息：

```text
name
extension
```

进入 Search Index。

Media metadata：

```text
movie title
artist
album
author
book title
```

也进入 Search Index。

搜索 API：

```http
GET /api/v1/search?q=interstellar
```

过滤：

```http
?library=movies
&type=file
&media=movie
&extension=mkv
```

不要一开始上 Elasticsearch / Meilisearch。

单 NAS SQLite FTS 足够。

---

# 25. File Operations

系统支持：

```text
upload
mkdir
rename
move
copy
delete
```

所有写操作遵循：

```text
Storage first
Catalog second
```

例如 rename：

```text
Storage.Rename()
      ↓ success
Catalog.UpdatePath()
      ↓
return
```

如果 Storage 成功、Catalog 更新失败：

```text
schedule reconciliation
```

Catalog 永远不能先于真实 filesystem 修改。

---

# 26. 文件上传

建议：

```http
POST /api/v1/entries/{parent}/files
Content-Type: multipart/form-data
```

大文件未来可以增加 resumable upload。

第一版只需要普通流式 multipart。

必须保证：

```text
request body
    ↓
stream
    ↓
filesystem
```

不能：

```text
io.ReadAll
```

把完整文件读入内存。

---

# 27. 文件内容 API

```http
GET /api/v1/entries/{id}/content
HEAD /api/v1/entries/{id}/content
```

必须正确实现：

```text
Content-Type
Content-Length
Content-Disposition
ETag
Last-Modified
Accept-Ranges
Range
Content-Range
If-None-Match
If-Modified-Since
```

这套 API 同时承担：

```text
download
image view
audio streaming
video direct play
PDF browser
```

---

# 28. HTTP Range

Range 是媒体能力的基础，不应该等到 Media Engine 再实现。

例如：

```http
Range: bytes=1000000-1999999
```

返回：

```http
206 Partial Content
Content-Range: bytes 1000000-1999999/8432691221
```

这样浏览器和播放器可以直接 seek。

---

# 29. ETag

LocalStorage 第一版可以：

```text
ETag = hash(size + mtime)
```

不必 hash 整个文件。

例如逻辑表示：

```text
"8432691221-1725960000000"
```

不要为了 HTTP cache 对几十 GB 文件计算 SHA256。

---

# 30. MIME

文件 MIME 判断顺序：

```text
known extension
    ↓
stored catalog MIME
    ↓
small prefix sniff
```

普通浏览 API 不读文件内容。

只有首次 metadata processing 时才允许 sniff。

---

# 31. Media Engine

Media Engine 负责：

```text
File
    ↓
technical metadata
    ↓
semantic media entity
```

包括：

```text
video
music
photo
book
document
archive
application
```

---

# 32. Processor 重构

现有 Processor 思路可以保留。

但：

```go
Process(file)
```

不再发生在 Scanner 热路径。

建议：

```go
type Processor interface {
    Match(entry Entry) bool

    Process(
        ctx context.Context,
        entry Entry,
    ) error
}
```

Indexer 只：

```text
发现文件
    ↓
enqueue ProcessEntry
```

Worker 才执行 processor。

---

# 33. Job Engine

第一版不需要 Redis / RabbitMQ。

SQLite 就是 Job Queue。

```sql
CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,

    type        TEXT NOT NULL,
    key         TEXT,
    payload     TEXT,

    state       TEXT NOT NULL,

    attempts    INTEGER NOT NULL DEFAULT 0,
    priority    INTEGER NOT NULL DEFAULT 0,

    run_after   DATETIME,

    created_at  DATETIME NOT NULL,
    started_at  DATETIME,
    finished_at DATETIME,

    error       TEXT
);
```

状态：

```text
pending
running
done
failed
```

典型 job：

```text
probe_media
extract_metadata
generate_thumbnail
match_movie
match_tv
generate_artwork
transcode
scan_directory
```

---

# 34. Job 去重

很多 filesystem events 可能连续触发：

```text
write
write
chmod
write
```

不能生成四个 thumbnail job。

`jobs.key` 用于幂等：

```text
thumbnail:{entry_id}:{mtime}
```

可以建立：

```sql
UNIQUE(type, key)
```

已经存在 pending/done job 时不重复创建。

---

# 35. Worker

第一版在同一个进程内启动 worker。

```text
files-go
   │
   ├── HTTP Server
   ├── Indexer
   └── Workers
```

以后如果 ffmpeg 转码过重，再把：

```text
worker
```

拆成单独进程。

API contract 和数据库设计不需要因此改变。

---

# 36. Media Technical Metadata

视频通过：

```text
ffprobe
```

提取：

```text
duration
container
video codec
audio codec
width
height
fps
bitrate
HDR
audio tracks
subtitle tracks
chapters
```

数据库不要全部平铺在 entries。

建议：

```sql
CREATE TABLE media_files (
    entry_id     TEXT PRIMARY KEY,

    kind         TEXT,

    duration_ms  INTEGER,

    container    TEXT,
    width        INTEGER,
    height       INTEGER,

    video_codec  TEXT,
    audio_codec  TEXT,

    bitrate      INTEGER,

    metadata     TEXT,

    updated_at   DATETIME NOT NULL
);
```

细节不常查询的内容放：

```text
metadata JSON
```

避免表越来越宽。

---

# 37. Media Item

Media Item 表示语义对象。

例如：

```text
Movie
TV Series
Episode
Artist
Album
Track
Book
Photo
```

建议：

```sql
CREATE TABLE media_items (
    id           TEXT PRIMARY KEY,

    type         TEXT NOT NULL,

    title        TEXT NOT NULL,
    sort_title   TEXT,

    year         INTEGER,

    external_id  TEXT,

    metadata     TEXT,

    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);
```

---

# 38. Media File Association

一个媒体对象可以对应多个文件。

例如：

```text
Dune
├── Dune.1080p.mkv
├── Dune.2160p.mkv
├── Dune.zh.srt
└── Dune.en.srt
```

数据库：

```sql
CREATE TABLE media_item_files (
    media_id   TEXT NOT NULL,
    entry_id   TEXT NOT NULL,

    role       TEXT NOT NULL,

    PRIMARY KEY(media_id, entry_id, role)
);
```

role：

```text
video
audio
subtitle
artwork
book
attachment
```

---

# 39. TV 数据模型

电视剧不要为：

```text
Series
Season
Episode
```

建立三套完全不同的核心表。

可以统一为 MediaItem：

```text
media_items

type = series
type = season
type = episode
```

增加：

```text
parent_id
index_number
```

如果以后证明这个模型不够，再拆。

第一版优先简单。

---

# 40. 音乐模型

同理：

```text
Artist
Album
Track
```

可以用：

```text
media_items
media_relations
```

表达。

不过 v1 不必一次实现万能 metadata graph。

音乐第一版甚至可以只：

```text
Track
album
artist
```

存 JSON。

设计重点是：

> 不污染 Entry。

---

# 41. Metadata Provider

外部 metadata provider：

```text
TMDB
MusicBrainz
OpenLibrary
...
```

不应该属于 Scanner。

建议：

```go
type MetadataProvider interface {
    Search(ctx context.Context, query Query) ([]Candidate, error)

    Fetch(ctx context.Context, id string) (Metadata, error)
}
```

Provider failure：

```text
不能影响文件扫描
不能影响目录浏览
不能影响原文件访问
```

---

# 42. Media Match

流程：

```text
filename
    ↓
parser
    ↓
candidate title/year/season/episode
    ↓
metadata provider
    ↓
confidence
```

必须允许：

```text
automatic match
manual override
unmatch
rematch
```

否则自动识别错误以后用户没有修正能力。

---

# 43. Artifact

所有派生文件统一叫 Artifact：

```text
thumbnail
poster
backdrop
preview
waveform
transcode segment
generated cover
```

数据库：

```sql
CREATE TABLE artifacts (
    id          TEXT PRIMARY KEY,

    entry_id    TEXT,
    media_id    TEXT,

    type        TEXT NOT NULL,
    variant     TEXT,

    key         TEXT NOT NULL,
    mime        TEXT,

    size        INTEGER,

    created_at  DATETIME NOT NULL,

    UNIQUE(type, key)
);
```

---

# 44. Artifact Storage

不要把生成文件放在原始媒体目录。

使用：

```text
/var/lib/files-go/

    catalog.db

    cache/
        thumbnails/
        posters/
        previews/
        transcodes/
```

例如：

```text
cache/thumbnails/ab/cd/abcdef.webp
```

Artifact storage 可以随时删除并重新生成。

因此：

```text
artifact cache = disposable
```

---

# 45. Thumbnail

图片：

```text
decode
resize
encode WebP/JPEG
```

视频：

```text
ffmpeg
```

电子书：

```text
cover extraction
```

API：

```http
GET /api/v1/entries/:id/thumbnail
```

支持：

```http
?size=small
?size=medium
?size=large
```

内部映射固定尺寸。

不要允许：

```text
?width=813&height=592
```

导致无限 cache cardinality。

---

# 46. 视频播放

播放优先级：

```text
Direct Play
    ↓
Remux
    ↓
Transcode
```

---

# 47. Direct Play

客户端支持原始文件时：

```text
GET /entries/:id/content
```

直接播放。

这是默认路径。

---

# 48. Remux

例如：

```text
MKV + H264 + AAC
```

浏览器不支持 MKV container，但支持 codec。

可以：

```text
ffmpeg -c copy
```

remux 成：

```text
MP4/fMP4
```

几乎不消耗 CPU。

---

# 49. Transcode

只有 codec 不兼容才转码。

例如：

```text
HEVC -> H264
DTS -> AAC
```

v1 可以不实现完整 adaptive transcoding。

推荐阶段：

```text
v1
Direct Play

v1.1
Remux

v1.2
single-profile HLS transcode

later
adaptive bitrate
hardware acceleration
```

---

# 50. HLS

如果进入 transcoding：

```http
POST /api/v1/playback/:entry
```

返回：

```json
{
  "mode": "hls",
  "url": "/api/v1/playback/sessions/xxx/master.m3u8"
}
```

Session 负责生命周期。

缓存文件：

```text
cache/transcodes/{session}/
```

闲置一段时间自动 GC。

---

# 51. Playback State

如果希望逐渐具备 Plex/Jellyfin UX，用户状态应该独立：

```sql
CREATE TABLE playback_states (
    user_id      TEXT NOT NULL,
    media_id     TEXT NOT NULL,

    position_ms  INTEGER NOT NULL,
    played       INTEGER NOT NULL,

    updated_at   DATETIME NOT NULL,

    PRIMARY KEY(user_id, media_id)
);
```

这样支持：

```text
Continue Watching
Watched
Resume
```

---

# 52. User 与认证

File Engine 与认证系统不要紧耦合。

内部只需要抽象：

```go
type Principal struct {
    ID string
}
```

HTTP middleware 负责：

```text
request
   ↓
auth
   ↓
Principal
```

可支持：

```text
anonymous
session
Bearer JWT
OIDC
reverse proxy auth
```

核心业务层只认识：

```text
Principal.ID
```

---

# 53. Permission

第一版建议权限模型保持简单：

```text
Library access
    read
    write
    admin
```

不要一开始做 POSIX ACL clone。

数据库：

```sql
library_permissions (
    library_id
    subject_id
    role
)
```

role：

```text
viewer
editor
owner
```

---

# 54. Share

对外文件访问以后很可能需要分享链接。

不要直接暴露 Entry ID 作为匿名访问能力。

设计：

```sql
shares (
    id
    token_hash

    entry_id

    expires_at
    password_hash

    created_by
    created_at
)
```

访问：

```text
/s/{token}
```

真实 Entry ID 可以继续隐藏在服务端。

---

# 55. Path Security

客户端永远不能向 raw content API 提交：

```text
/mnt/disk1/foo
../../etc/passwd
```

所有访问必须：

```text
entry_id
      ↓
Catalog
      ↓
storage_id + relative path
      ↓
Storage
```

Storage 负责最终路径解析。

LocalStorage 必须确保：

```text
resolved path
```

始终位于 storage root 内。

---

# 56. Symlink

这是必须明确的安全策略。

默认：

```text
follow_symlink = false
```

尤其不能：

```text
/mnt/disk1/share/etc -> /etc
```

然后通过 files-go 读 `/etc`。

第一版最简单策略：

```text
symlink 作为 Entry 展示
禁止通过 symlink 进入 storage root 外部
```

---

# 57. API Namespace

全部 API 使用：

```text
/api/v1
```

Web frontend：

```text
/
```

Static：

```text
/assets/*
```

这样未来 API breaking change 有明确升级空间。

---

# 58. API 资源

核心资源：

```text
storages
libraries
entries
search
media
artifacts
jobs
playback
shares
system
```

---

# 59. System API

```http
GET /api/v1/system
```

返回：

```json
{
  "version": "0.1.0",
  "features": {
    "media": true,
    "transcode": false
  }
}
```

可以让 Web UI 根据服务器能力渲染。

---

# 60. Storage API

```http
GET /api/v1/storages
GET /api/v1/storages/:id

POST /api/v1/storages/:id/scan
```

普通用户不一定可以看到 filesystem mount path。

---

# 61. Library API

```http
GET /api/v1/libraries
GET /api/v1/libraries/:id

GET /api/v1/libraries/:id/entries
```

例如：

```json
{
  "id": "movies",
  "name": "Movies",
  "type": "movies"
}
```

---

# 62. Entry API

```http
GET    /api/v1/entries/:id
GET    /api/v1/entries/:id/children
GET    /api/v1/entries/:id/content
HEAD   /api/v1/entries/:id/content

POST   /api/v1/entries/:id/files
POST   /api/v1/entries/:id/directories

PATCH  /api/v1/entries/:id
DELETE /api/v1/entries/:id
```

PATCH：

```json
{
  "name": "new-name.mkv"
}
```

move 可以：

```json
{
  "parentId": "..."
}
```

---

# 63. Entry Representation

统一返回：

```json
{
  "id": "0199...",
  "name": "Interstellar.mkv",

  "type": "file",

  "size": 8432691221,
  "mime": "video/x-matroska",
  "extension": "mkv",

  "available": true,

  "modifiedAt": "2026-09-10T10:22:00Z",

  "links": {
    "content": "/api/v1/entries/0199.../content",
    "thumbnail": "/api/v1/entries/0199.../thumbnail"
  }
}
```

不要加入：

```text
line1
line2
line3
icon
```

这些属于前端表现逻辑。

---

# 64. API Errors

统一格式：

```json
{
  "error": {
    "code": "entry_not_found",
    "message": "entry not found"
  }
}
```

常见 code：

```text
invalid_request
unauthorized
forbidden
entry_not_found
storage_offline
conflict
not_supported
internal_error
```

前端不应该解析 human-readable `message` 判断行为。

---

# 65. API 不暴露内部 Error

禁止：

```json
{
  "error": "open /mnt/disk2/private/foo: permission denied"
}
```

外部返回：

```text
storage_error
```

真实错误进入 log。

---

# 66. Config

配置建议重新整理：

```yaml
listen: ":8088"

data: "/var/lib/files-go"

storages:
  - id: disk1
    name: Disk 1
    type: local
    path: /mnt/disk1

  - id: disk2
    name: Disk 2
    type: local
    path: /mnt/disk2

libraries:
  - id: movies
    name: Movies
    type: movies

    sources:
      - storage: disk1
        path: Movies

      - storage: disk2
        path: Movies

media:
  tmdb:
    api_key: "${TMDB_API_KEY}"

scan:
  reconcile_interval: 24h
```

Secret 不写进 repository。

---

# 67. Database

保持 SQLite。

推荐：

```text
WAL
foreign_keys=ON
busy_timeout
```

一个 NAS 单实例完全不需要引入 PostgreSQL。

SQLite 的优点：

```text
single file
zero administration
excellent read performance
transactional
FTS5
backup simple
```

---

# 68. Database Migration

不要继续把：

```sql
CREATE TABLE IF NOT EXISTS
```

长期塞进启动函数。

引入极小 migration system：

```text
migrations/
  001_init.sql
  002_media.sql
  003_search.sql
```

数据库：

```sql
schema_migrations (
    version INTEGER PRIMARY KEY
)
```

启动：

```text
current version
      ↓
apply pending migrations
```

不需要引入大型 ORM。

---

# 69. Repository Layer

不要 ORM。

SQL 已经足够清晰。

建议：

```go
type EntryStore struct {
    db *sql.DB
}
```

例如：

```go
func (s *EntryStore) Get(ctx context.Context, id string) (*Entry, error)

func (s *EntryStore) Children(
    ctx context.Context,
    parent string,
    opts ListOptions,
) ([]Entry, error)

func (s *EntryStore) Upsert(...)
```

保留 SQL 的可见性。

---

# 70. Package Structure

推荐：

```text
files-go/

cmd/
  files-go/
    main.go

internal/

  api/
    server.go
    middleware.go
    entries.go
    libraries.go
    media.go
    search.go

  catalog/
    catalog.go
    entries.go
    libraries.go
    search.go

  storage/
    storage.go

    local/
      local.go
      watch.go

  indexer/
    indexer.go
    scanner.go
    watcher.go

  jobs/
    queue.go
    worker.go

  media/
    media.go

    probe/
      probe.go

    thumbnail/
      thumbnail.go

    metadata/
      metadata.go
      tmdb.go

  model/
    entry.go
    library.go
    media.go

  database/
    database.go
    migrations.go

web/
  index.html
  app.js

migrations/

docs/
```

不要为了“clean architecture”制造：

```text
controller
usecase
repository interface
repository implementation
service
manager
provider factory
```

这样的层层包装。

每个 package 应该有一个非常明确的理由存在。

---

# 71. Dependency Direction

理想依赖：

```text
api
 │
 ▼
catalog / media
 │
 ▼
model

indexer
 │
 ├── catalog
 └── storage

media workers
 │
 ├── catalog
 └── storage
```

禁止：

```text
catalog -> api
storage -> api
model -> everything
```

`model` 应保持接近纯数据定义。

---

# 72. Application

可以有一个很薄的 Application struct：

```go
type App struct {
    Catalog *catalog.Catalog
    Storage *storage.Registry
    Indexer *indexer.Indexer
    Jobs    *jobs.Queue
    Media   *media.Engine
}
```

不要发展成：

```text
God Object
```

HTTP handler 可以直接持有所需依赖。

---

# 73. Storage Registry

因为有多个磁盘，需要 Registry：

```go
type Registry struct {
    stores map[string]Storage
}
```

接口：

```go
func (r *Registry) Get(id string) (Storage, bool)
```

除此之外不要加复杂 service locator。

---

# 74. Context

所有：

```text
HTTP
Storage
DB
Job
External API
```

操作都应该传：

```go
context.Context
```

这样：

```text
client disconnect
server shutdown
timeout
```

可以正确取消工作。

---

# 75. Logging

第一版标准库：

`log/slog`

足够。

日志结构至少包含：

```text
component
storage
entry
job
duration
error
```

不要把每个正常文件扫描记录成 INFO。

建议：

```text
INFO  scan start/finish
INFO  storage offline/online
WARN  processing failure
ERROR unrecoverable database/storage error
DEBUG per-file details
```

---

# 76. Graceful Shutdown

退出过程：

```text
stop accepting HTTP
      ↓
cancel scanners
      ↓
stop watchers
      ↓
wait active workers
      ↓
close DB
```

不能简单让 goroutine 被进程强杀。

---

# 77. Concurrency

目录扫描不应该根据：

```go
runtime.NumCPU()
```

无限增加磁盘并发。

机械盘的瓶颈是 seek，不是 CPU。

建议 Scanner：

```text
directory traversal concurrency: low
metadata processing concurrency: configurable
```

例如：

```text
scanner workers = 2
metadata workers = 4
ffmpeg workers = 1
```

尤其 transcode 必须有限制。

---

# 78. Backpressure

Job queue 本身提供 backpressure。

Scanner：

```text
discover 2,000,000 files
```

不应该同时创建 2,000,000 goroutine。

始终：

```text
bounded channels
batch writes
persistent jobs
worker pool
```

---

# 79. SQLite Write Model

SQLite 是 single-writer friendly。

因此：

```text
scanner workers
      ↓
batch
      ↓
single catalog writer / short transactions
```

比很多 goroutine 同时写 SQLite 更合理。

批量：

```text
100 ~ 1000 entries / transaction
```

可以根据性能测试调整。

---

# 80. Offline Storage

Storage mount 不存在：

```text
Storage.State = offline
```

行为：

```text
Catalog browse        YES
Search                YES
Metadata              YES
Thumbnail cache       YES

Content               NO
Rename                NO
Delete                NO
Upload                NO
Transcode             NO
```

API：

```http
503 Service Unavailable
```

或者资源层返回：

```text
storage_offline
```

---

# 81. Mount Identity

不要只靠：

```text
/mnt/disk1
```

判断硬盘 identity。

否则错误的磁盘被 mount 到同一路径时可能污染 catalog。

LocalStorage 可以配置：

```yaml
device_uuid: ...
```

未来启动时验证。

第一版如果实现复杂，可以先暂缓，但数据模型要允许。

---

# 82. Library Offline

一个 Library 有：

```text
disk1
disk2
disk3
```

其中 disk2 offline。

Library 本身仍：

```text
available
```

只是部分 Entry：

```text
available=false
```

不要把整个 Library 标记 offline。

---

# 83. File Views 与 Media Views

API 必须提供两个正交入口：

```text
Filesystem Browser

/api/v1/libraries/:id/entries
/api/v1/entries/:id/children
```

以及：

```text
Media Browser

/api/v1/media/movies
/api/v1/media/shows
/api/v1/media/music
/api/v1/media/photos
```

因此同一份文件可以：

```text
Files
  /Movies/Dune.mkv
```

同时出现在：

```text
Movies
  Dune
```

这是两个不同 projection。

---

# 84. Frontend

Web UI 不属于后端领域模型。

推荐：

```text
web/

index.html
app.js

lib/
components/
views/
```

入口：

```js
import { html, render } from '...'
import { useState, useEffect } from 'preact/hooks'
```

直接调用：

```js
fetch('/api/v1/libraries')
```

---

# 85. Frontend Routing

可以使用 History API：

```text
/files/:entry
/movies/:media
/photos
/music
/search
```

HTTP server 对非 API route：

```text
fallback -> index.html
```

让 Preact 处理 UI routing。

---

# 86. Static Embedding

Release binary 可以：

```go
//go:embed web/*
```

这样最终部署只有：

```text
files-go
config.yaml
/var/lib/files-go
```

但 Web 与 API 仍保持逻辑解耦。

---

# 87. 前端展示职责

后端提供：

```text
type
mime
metadata
thumbnail URL
available
```

前端决定：

```text
icon
layout
line1
line2
line3
color
card/list/grid
```

因此旧模型中的：

```text
Icon
Line1
Line2
Line3
```

应删除。

---

# 88. Preview

文件 Preview 建议分级。

浏览器原生：

```text
image
audio
video
PDF
text
```

Server generated：

```text
thumbnail
video preview
book cover
audio waveform
```

未知格式：

```text
metadata + download
```

不要尝试服务器端渲染一切。

---

# 89. 文本文件

可以提供：

```http
GET /api/v1/entries/:id/text
```

但必须：

```text
size limit
UTF detection
binary detection
```

例如默认最大：

```text
1 MiB
```

防止浏览器请求一个 50GB 日志。

---

# 90. Archive

ZIP/TAR 第一版只显示 metadata。

未来可以增加：

```text
virtual entries
```

但不建议 v1 实现。

Archive-as-directory 会让：

```text
Entry identity
path semantics
write semantics
```

复杂很多。

---

# 91. Photo

照片阶段建议：

```text
EXIF
taken_at
camera
dimensions
GPS
thumbnail
```

以后可以：

```text
timeline
albums
map
```

原始 EXIF 可以放 JSON。

高频字段如：

```text
taken_at
```

单独列。

---

# 92. Books

EPUB：

```text
title
author
cover
language
publisher
```

PDF：

```text
title
author
page count
thumbnail
```

阅读器本身尽可能由 Web client 完成。

Server 主要提供：

```text
content
metadata
cover
```

---

# 93. Music

支持：

```text
ID3
FLAC metadata
album art
duration
codec
bitrate
```

第一阶段即可形成：

```text
Artists
Albums
Tracks
```

无需 transcoding 就已经非常有用。

---

# 94. Security Boundary

files-go 被设计为可暴露到网络，因此必须假设：

```text
所有 HTTP 输入不可信
文件名不可信
metadata 不可信
media 文件不可信
archive 不可信
external provider response 不可信
```

尤其 ffmpeg、图片 decoder、EPUB parser 等处理器都属于潜在攻击面。

---

# 95. External Process

调用：

```text
ffprobe
ffmpeg
```

不要通过：

```text
sh -c
```

必须：

```go
exec.CommandContext(
    ctx,
    "ffprobe",
    "-i",
    filename,
)
```

filename 永远作为独立 argv。

---

# 96. Resource Limits

processor 必须有限制：

```text
timeout
concurrency
input size where relevant
output size
```

例如：

```text
ffprobe timeout = 30s
thumbnail timeout = 60s
TMDB request timeout = 10s
```

---

# 97. API Rate Limit

v1 可以暂时不内置复杂 rate limiter。

但：

```text
login
share
search
thumbnail generation
transcode
```

以后可以单独限制。

如果只部署在可信 reverse proxy 后面，可以由 proxy 处理普通 rate limit。

---

# 98. Reverse Proxy

推荐部署：

```text
Internet
    ↓
Caddy / nginx / Cloudflare Tunnel
    ↓
files-go
```

files-go 本身仍然必须做好认证和授权。

不能把：

```text
“只有反代能访问”
```

当作唯一安全边界。

---

# 99. Health API

```http
GET /healthz
```

只检查：

```text
process alive
database reachable
```

不要因为一块 storage offline 返回 unhealthy。

否则 NAS 掉一块 USB HDD 时 orchestration 会反复重启服务。

---

# 100. Status

可以：

```http
GET /api/v1/status
```

展示：

```text
storage states
last scan
queue depth
active workers
database size
cache size
```

供 UI 的系统管理页使用。

---

# 101. Metrics

第一版不强制 Prometheus。

内部先能统计：

```text
entries count
scan duration
jobs pending
jobs failed
storage online/offline
artifact cache size
```

以后 `/metrics` 很容易增加。

---

# 102. Backup

需要备份：

```text
catalog.db
config
```

不需要强制备份：

```text
cache/
```

因为 cache 可以重建。

SQLite WAL 模式备份必须使用：

```text
SQLite backup API
VACUUM INTO
```

或正确 checkpoint 后复制。

不要运行时简单复制 DB 文件。

---

# 103. Cache GC

Artifact cache 可以设置：

```yaml
cache:
  max_size: 50GB
```

第一版可以实现简单：

```text
oldest unused first
```

但 poster/thumbnail 和 transcode cache 应区分：

```text
persistent derived asset
ephemeral playback cache
```

优先 GC：

```text
transcode
preview
thumbnail
poster
```

---

# 104. Media Metadata 与 Cache 的区别

TMDB metadata：

```text
database
```

Poster binary：

```text
artifact cache
```

这样即使 poster 文件被清：

```text
TMDB mapping
title
overview
rating
```

仍然存在。

---

# 105. Consistency Model

系统采用：

> eventual consistency between filesystem and catalog.

这意味着文件通过外部程序创建：

```text
filesystem immediately has it
catalog may see it milliseconds or minutes later
```

这对 NAS 文件管理器完全合理。

files-go 自己执行的修改则尽可能：

```text
read-after-write consistent
```

因为操作成功后立即更新 Catalog。

---

# 106. 核心不变量

整个实现过程中建议把下面这些当成 architecture invariants。

```text
1. Browse never touches filesystem.

2. Filesystem is source of truth.

3. Catalog is rebuildable.

4. Storage path never leaks through API.

5. Filesystem operations go through Storage.

6. Scanner performs no expensive metadata enrichment.

7. Media model never pollutes filesystem Entry.

8. Storage offline never implies catalog deletion.

9. Entry identity survives rename whenever possible.

10. Derived artifacts are disposable.

11. External metadata failure never prevents file access.

12. API model contains data, not presentation.
```

如果某个实现违反这些原则，应该优先重新考虑设计。

---

# 107. SeaweedFS 的位置

未来如果需要：

```text
distributed storage
multi-node capacity
replication
object storage
```

可以增加：

```text
storage/seaweed
```

结构：

```text
files-go
    │
Storage interface
    │
    ├── LocalStorage
    ├── SeaweedStorage
    ├── S3Storage
    └── WebDAVStorage
```

因此 SeaweedFS 是：

> storage provider

而不是：

> files-go architecture foundation

这样现有 NAS 文件完全不需要迁移。

---

# 108. 不要过早抽象 Storage

虽然上面定义了 Storage interface，但第一阶段只有 LocalStorage。

原则：

> interface 必须由实际调用需求塑造，而不是为了想象中的未来 backend。

因此如果实现过程中发现：

```go
Storage
```

只有六个方法就够，就保持六个。

不要因为 S3 可能没有 Rename 就提前引入：

```text
Capabilities
FeatureSet
Provider
Operation
Driver
BackendManager
Strategy
AdapterFactory
```

等层级。

未来真的实现 S3 时再修改接口也完全可以。

---

# 109. 推荐开发阶段

## Phase 1 — Core Catalog

目标：

```text
真正做到浏览不碰磁盘
```

完成：

```text
Storage
Entry
Catalog
SQLite migrations
generation scanner
offline storage
API /entries
API /content
HTTP Range
```

删除：

```text
template rendering
os.Stat in ListFiles
absolute path API
```

完成 Phase 1 后，它已经是一个可靠的 NAS File API。

---

## Phase 2 — Web File Manager

完成：

```text
Preact + htm frontend
directory list/grid
breadcrumb
upload
mkdir
rename
move
delete
search
file preview
storage status
```

此时 files-go 已经可以日常使用。

---

## Phase 3 — Background Processing

完成：

```text
SQLite jobs
worker pool
image metadata
thumbnail
ffprobe
music metadata
EPUB metadata
PDF metadata
```

此时开始成为 File Intelligence Engine。

---

## Phase 4 — Media Catalog

完成：

```text
media_items
media_item_files
TMDB
movie matching
TV matching
posters
movie UI
TV UI
music UI
photo UI
```

---

## Phase 5 — Playback

完成：

```text
Direct Play
compatibility detection
Remux
HLS
Transcode
playback state
Continue Watching
```

---

## Phase 6 — Extensions

根据真实需求决定：

```text
OIDC
sharing
WebDAV
S3 backend
SeaweedFS backend
hardware transcoding
photo timeline
semantic search
remote storage
multiple server workers
```

没有实际需求就不实现。

---

# 110. Phase 1 推荐重构目录

Phase 1 可以先保持非常克制：

```text
files-go/

main.go

model/
  entry.go
  library.go

storage/
  storage.go
  local.go

catalog/
  catalog.go
  entries.go

indexer/
  indexer.go
  scanner.go

api/
  server.go
  entries.go
  libraries.go

database/
  database.go

migrations/
  001_init.sql
```

甚至不必一开始搬到：

```text
internal/
cmd/
```

等核心稳定以后再整理。

目录结构不是架构本身。

---

# 111. Phase 1 最小对象关系

```text
Config
 │
 ├── StorageConfig
 └── LibraryConfig

StorageRegistry
 │
 └── Storage

Catalog
 │
 ├── StorageStore
 └── EntryStore

Indexer
 │
 ├── StorageRegistry
 └── Catalog

API
 │
 ├── Catalog
 └── StorageRegistry
```

这已经足够。

不要再多抽象一层 `Service`。

---

# 112. 第一版 Entry Go Model

建议直接从这里开始：

```go
package model

import "time"

type EntryType string

const (
    EntryFile      EntryType = "file"
    EntryDirectory EntryType = "directory"
    EntrySymlink   EntryType = "symlink"
)

type Entry struct {
    ID        string    `json:"id"`
    StorageID string    `json:"-"`
    ParentID  *string   `json:"parentId,omitempty"`

    Name string `json:"name"`
    Path string `json:"-"`

    Type EntryType `json:"type"`

    Size int64 `json:"size"`

    MIME      string `json:"mime,omitempty"`
    Extension string `json:"extension,omitempty"`

    Available bool `json:"available"`

    ModifiedAt time.Time `json:"modifiedAt"`
    CreatedAt  time.Time `json:"createdAt"`
    UpdatedAt  time.Time `json:"updatedAt"`
}
```

注意：

```text
StorageID
Path
```

默认不 JSON 暴露。

---

# 113. 第一版 Storage 接口

建议不要超过：

```go
type Storage interface {
    Stat(
        ctx context.Context,
        path string,
    ) (FileInfo, error)

    ReadDir(
        ctx context.Context,
        path string,
    ) ([]FileInfo, error)

    Open(
        ctx context.Context,
        path string,
    ) (io.ReadSeekCloser, error)

    Mkdir(
        ctx context.Context,
        path string,
    ) error

    Rename(
        ctx context.Context,
        oldPath,
        newPath string,
    ) error

    Remove(
        ctx context.Context,
        path string,
    ) error
}
```

Upload 是否增加：

```go
Create()
```

可以等实现 upload 时再决定。

---

# 114. 第一版 Catalog API

```go
type Catalog struct {
    db *sql.DB
}

func (c *Catalog) Entry(
    ctx context.Context,
    id string,
) (*model.Entry, error)

func (c *Catalog) Children(
    ctx context.Context,
    parentID string,
    opts ListOptions,
) ([]model.Entry, error)

func (c *Catalog) UpsertEntries(
    ctx context.Context,
    entries []model.Entry,
) error
```

无需定义：

```text
CatalogInterface
CatalogRepository
CatalogRepositoryImpl
```

除非测试真的证明需要。

---

# 115. 第一版 Indexer API

```go
type Indexer struct {
    catalog  *catalog.Catalog
    storages *storage.Registry
}

func (i *Indexer) Scan(
    ctx context.Context,
    storageID string,
) error
```

以后自然增加：

```go
Watch()
Reconcile()
ScanPath()
```

不用现在先定义。

---

# 116. 第一版 HTTP

建议直接使用 Go 1.22+ `http.ServeMux` routing：

```go
mux.HandleFunc("GET /api/v1/entries/{id}", ...)
mux.HandleFunc("GET /api/v1/entries/{id}/children", ...)
mux.HandleFunc("GET /api/v1/entries/{id}/content", ...)
```

没必要为了几个 REST route 引入大型 HTTP framework。

---

# 117. Streaming 实现

LocalStorage 返回：

```text
io.ReadSeekCloser
```

因此可以充分利用：

```go
http.ServeContent
```

处理：

```text
Range
HEAD
Last-Modified
seek
```

避免自己重新实现复杂 HTTP Range parser。

API 层控制：

```text
authorization
MIME
filename
ETag
```

Storage 只负责读文件。

---

# 118. Testing

测试重点不是追求 coverage 数字。

优先测试系统不变量。

Catalog：

```text
children only query DB
rename preserves ID
offline entries preserved
generation marks stale
```

Storage：

```text
path traversal rejected
symlink escape rejected
range-compatible open
```

Indexer：

```text
failed scan does not invalidate catalog
new file inserted
deleted file unavailable
rename detected
```

API：

```text
unauthorized cannot read
path never leaked
range works
offline returns expected error
```

---

# 119. Scanner Integration Test

建议测试真实临时目录：

```text
temp/
  foo/
    a.txt
    b.txt
```

Scan。

修改：

```text
rename a.txt -> c.txt
delete b.txt
create d.txt
```

再次 scan。

验证：

```text
catalog
```

不要大量 mock filesystem。

这类测试真实 filesystem 反而更简单可靠。

---

# 120. 性能目标

v1 可以定义非常现实的目标。

在 Catalog 已建立情况下：

```text
directory listing:
< 20ms typical

entry metadata:
< 10ms typical

search:
< 100ms typical

browse:
zero filesystem IO
```

扫描百万文件时重点不是绝对时间，而是：

```text
bounded memory
bounded goroutines
incremental DB writes
service remains responsive
```

---

# 121. 大目录

必须假设一个目录可能：

```text
100,000+
```

文件。

因此前后端都不能：

```text
return everything
render everything
```

API 必须分页。

前端必须 virtual list 或 progressive rendering。

---

# 122. 文件数量

设计目标建议以：

```text
1M - 10M entries
```

仍能正常工作为标准。

这已经覆盖绝大部分家庭 NAS。

不必为了：

```text
1 billion entries
```

提前引入分布式数据库。

---

# 123. SQLite Index Discipline

不要为所有字段建立 index。

第一版核心：

```text
(storage_id, path)
(storage_id, parent_id)
name
scan_generation
```

FTS 单独负责全文搜索。

数据库 index 越多：

```text
scan write cost
```

越高。

---

# 124. 数据库与真实文件不同步时的哲学

不要追求：

```text
filesystem/catalog strongly consistent
```

那会让架构重新回到每次 `Stat()`。

正确哲学：

```text
Catalog is believed until proven otherwise.
```

发现不一致：

```text
content access failure
watch event
manual rescan
reconciliation
```

再修复 Catalog。

---

# 125. Media 增强不能破坏 File Manager

这是未来开发最重要的产品原则之一。

即使：

```text
TMDB down
ffmpeg missing
metadata corrupted
thumbnail failed
movie match wrong
```

用户仍然必须可以：

```text
browse
download
rename
move
delete
upload
```

Media Engine 永远是 augmentation。

不是 File Engine 的依赖。

---

# 126. 最终架构图

```text
                         ┌────────────────────────┐
                         │      Web / Apps        │
                         │ Preact / iOS / Android │
                         └───────────┬────────────┘
                                     │
                                  HTTP API
                                     │
                         ┌───────────▼────────────┐
                         │       API Engine       │
                         │ Auth / Permissions     │
                         └───┬────────┬────────┬──┘
                             │        │        │
              ┌──────────────┘        │        └──────────────┐
              ▼                       ▼                       ▼
       ┌────────────┐          ┌────────────┐          ┌────────────┐
       │  Catalog   │          │   Media    │          │  Content   │
       │   Engine   │          │   Engine   │          │  Serving   │
       └─────▲──────┘          └──────▲─────┘          └──────┬─────┘
             │                        │                       │
             │                  ┌─────┴─────┐                 │
             │                  │ Job Queue │                 │
             │                  └─────▲─────┘                 │
             │                        │                       │
       ┌─────┴──────┐          ┌──────┴─────┐                 │
       │  Indexer   │          │  Workers   │                 │
       │ Scan/Watch │          │ Probe/etc. │                 │
       └─────┬──────┘          └──────┬─────┘                 │
             │                        │                       │
             └────────────────────────┼───────────────────────┘
                                      │
                               Storage Engine
                                      │
            ┌─────────────────────────┼─────────────────────────┐
            ▼                         ▼                         ▼
        Local FS                 SeaweedFS                    S3
       /mnt/disk1               optional later            optional later
       /mnt/disk2
```

---

# 127. 最终产品结构

files-go 最终面对用户可以自然形成：

```text
Files
    Browse physical/logical files

Photos
    Timeline / Albums

Movies
    Posters / Details / Playback

TV
    Series / Seasons / Episodes

Music
    Artists / Albums / Tracks

Books
    Library / Metadata / Reader

Search
    Across all content
```

但这些不是六套不同的 server。

它们共同建立在：

```text
Storage
Catalog
Entry
Media
Artifact
```

几个稳定核心之上。

---

# 128. 最重要的设计决策总结

files-go 应坚持：

```text
普通文件系统
    是真实数据源

SQLite
    是目录索引与 Catalog

Storage
    隔离真实文件 IO

Indexer
    维护 Catalog

Media
    提供语义增强

Artifacts
    保存可重新生成的数据

HTTP API
    是唯一客户端边界

Preact UI
    完全由 API 驱动
```

SeaweedFS 不进入核心架构。

如果有一天实际需求证明：

```text
LocalStorage
```

已经无法满足存储规模，再把 SeaweedFS 接到 Storage interface 下。

在那一天到来之前：

> Keep the filesystem boring, and make the layer above it smart.

这应该成为 files-go 的核心工程哲学。
