# 架构与元数据增强流程

> 状态：**现状说明（as-is）**。基于 2026-10-30 `master` 分支的静态代码阅读；本文不表示这些行为均已通过运行时验证。待改进事项见 [improvements.md](improvements.md)。

## 目标和边界

files-go 是面向 NAS 的**只读**文件索引与媒体浏览器。原文件系统是事实来源；SQLite 主要保存可重建的目录索引与派生元数据，HTTP API 和 Preact UI 从索引读取。当前唯一的进程负责扫描、增强和 Web 服务，FFmpeg/FFprobe 等按需作为子进程执行。

```text
config.yaml / roots
       |
       v
Scanner: 逐目录读取、分类、比对变化
       |
       v
SQLite files: 文件/目录记录 + meta_ver(待处理状态)
       |                                   |
       v                                   v
Pipeline: workers -> Enrichers          HTTP API -> Web UI
       |                                   |
       v                                   +--> /raw 直接访问
SQLite meta/title/kind/meta_err             +--> /thumb 按需生成缓存
                                           +--> /play /hls 按需封装/转码
```

相关实现： [main.go](../main.go)、[store/scan.go](../store/scan.go)、[store/store.go](../store/store.go)、[media/enrich.go](../media/enrich.go)、[server/server.go](../server/server.go)、[web/app.js](../web/app.js)。

## 从新增文件到前端显示

1. **发现**：启动后及每次周期扫描，`Scanner.ScanAll` 逐个扫描 Root。访问文件夹时，`Stale` 根据目录修改时间判断是否需要同步当前层。没有文件系统事件订阅，不能保证内容变化立即可见。
2. **快速分类**：`Classify(name, size)` 仅根据扩展名和少量大小规则给出初始 `files.kind`（如 `video`、`audio`、`image`、`book`、`folder`），不用为初次建索引打开所有文件。
3. **协调入库**：按 `(root, path)` 唯一标识条目，更新文件名、父目录、大小、mtime 等。新记录 `meta_ver=0`；变更记录重新置零；目录内容变化也使该目录待增强。删除索引对应的已消失文件及其子树。
4. **排队**：`Pipeline` 从 `files` 表选出 `meta_ver < Pipeline.Version` 的记录。正在查看的条目通过 `Prioritize` 尝试进入 `hot` 优先通道。`inflight` 只抑制当前进程内的重复派发。
5. **顺序增强**：一个 Worker 取得一个文件快照，创建新的 `Meta`，依次调用每个 `Enricher.Match`；命中才执行 `Enrich`，所有增强器共享 `*store.File` 和 `*Meta`。
6. **保存**：结果先经 `results` 通道，再按最多 200 条或约 1 秒批量写入 `files` 的 `kind`、`title`、`meta`、`meta_ver`、`meta_err`。`meta` 是 JSON 文本，不是另一张媒体表。
7. **读取与展示**：`/api/files/{id}`、`/children`、`/search` 从 SQLite 返回 `store.File` / `View`。UI 根据 `meta.kind`、`meta.title`、`meta.info` 等生成不同列表/详情样式。

当前 `files` 中 `path`、独立的 `title`、`meta_ver` 不直接序列化给 Web API；标题仍通过 `meta.title` 展示。SQLite FTS5 trigram 搜索文件名和独立 `title`，不是任意 `meta.info` 字段。

## 增强器接口、执行顺序与错误

接口非常小：

```go
type Enricher interface {
    Match(f *store.File) bool
    Enrich(ctx context.Context, f *store.File, m *Meta) error
}
```

在 `main.go` 中注册的当前顺序为：

1. `Probe`：对视频/音频运行 ffprobe，提取时长、视频/音频编码、音轨、字幕和音频标签，也可能纠正 `f.Kind`。
2. `Videos`：对视频解析发布名、NFO、目录提示，利用 TMDB、年份、时长等信息识别电影和剧集单集。
3. `Folders`：对非 Root 的文件夹寻找本地海报、NFO，识别电影目录、电视剧目录和季度目录。
4. `JAV`：对标记为 `jav` 的位置执行对应作品识别。
5. `Photo`：解析图片尺寸及 JPEG/TIFF EXIF；无法直接解析的格式可交给 ffprobe。
6. `Document`：解析 EPUB、MOBI、Office 文件、PDF 等元数据。
7. `App`：读取部分应用程序/可执行文件信息。
8. `Archive`：读取受支持压缩包的文件数与总体积。

**不是每个文件都会执行八个处理器。** `Match` 不命中便跳过；视频通常先经 `Probe` 再经 `Videos`；FLAC 通常只经过 `Probe`；EPUB 由 `Document` 处理。尚无独立的 `Music`/专辑聚合增强器。

一个核心区分：

| 字段 | 意义 | 例子 |
| --- | --- | --- |
| `files.kind` | 文件本身的物理/粗分类（用于播放、下载等） | `video`、`audio`、`image`、`folder` |
| `meta.kind` | 增强后的语义分类（用于展示） | `movie`、`show`、`season`、`episode`、`photo`、`book` |

同一电影可以是 `files.kind=video`，同时 `meta.kind=movie`。不要把前者直接改成 `movie`，否则现有播放判断可能失效。

错误语义（当前实现）：

- 一般增强错误追加到 `meta_err`；其他匹配的增强器继续执行，已取得的部分结果仍可保存，并将该文件标记为当前增强版本。
- `ErrTransient` 或上下文取消导致本次处理不保存；后台 Pipeline 使用全局约五分钟暂停，然后再重新尝试。这是当前实现，不是理想的逐任务退避方案。
- `meta_ver` 标记**处理版本**，不是文件内容的并发修订号；无法保证旧任务结果不会覆盖扫描后的新状态（见改进清单）。

## 示例 1：松散电影文件

```text
Videos/Movies/Interstellar.2014.1080p.BluRay.mkv
```

- Scanner：保存一个初始 `kind=video` 的文件。
- Probe：例如读取 `duration`、`width`、`height`、`video=h264`、`audio=aac`。
- Videos：由扩展名和媒体提示进入识别，从文件名解析 `Interstellar`、`2014`、`1080p`、`BluRay`，查询 TMDB；依据标题、年份、流行度、时长等证据评估候选，结果可能是 `meta.kind=movie`。
- 结果仅存储 TMDB 海报引用（如 `tmdb:/xxx.jpg`），不是把封面原始字节塞到 SQLite。海报 API 在首次访问时生成/缓存内容。

示意结果（ID、时长和海报路径均为例子，非实际查询结果）：

```json
{
  "kind": "movie",
  "title": "星际穿越",
  "original": "Interstellar",
  "year": 2014,
  "duration": 10149,
  "width": 1920,
  "height": 1080,
  "video": "h264",
  "audio": "aac",
  "tmdb": "movie/123",
  "poster": "tmdb:/example.jpg",
  "info": {"Release": "1080p · BluRay"}
}
```

TMDB 未配置或没有可靠候选时，不应推断所有这些字段必然存在；自动识别不确定会标记 `meta.review`（如 `unmatched`、`ambiguous`、`conflict`）。

## 示例 2：带 NFO、海报和字幕的电影文件夹

```text
Movies/Interstellar (2014)/
  movie.nfo
  poster.jpg
  fanart.jpg
  Interstellar.mkv
  Interstellar.zh.srt
```

每一个文件和文件夹都是独立索引行：

- 文件夹的 `Folders` 增强器发现 `poster.jpg` / `fanart.jpg`，写入 `poster=file:<文件ID>` 和 `backdrop=file:<文件ID>`。
- `movie.nfo` 提供本地标题、年份、简介及可能的 TMDB ID；电影目录的主视频可以提供片长证据；本地 NFO ID 和实际证据冲突时会重新评估。
- `Interstellar.mkv` 有自己的 `Probe` 和 `Videos` 增强结果，不直接与父级共享同一 JSON。
- 外挂 `.srt` 依然是独立文件。播放时 `server/subtitle.go` 才查找附近字幕并按需转换 WebVTT；不是在扫描时将字幕全文写进文件夹的 `meta`。

本地封面引用通常优先于远端 TMDB 封面。`file:<ID>`、`tmdb:<path>`、`zip:<entry>`、`self` 都是内部 artwork 引用，实际图片由 `server/thumb.go` 按需提供。

## 示例 3：电视剧、季度和单集

```text
TV Shows/The Expanse/
  tvshow.nfo
  Season 01/
    The.Expanse.S01E01.mkv
    The.Expanse.S01E02.mkv
```

在 `type: tv` 的配置提示下，系统可以形成不同层级的语义：

| 索引行 | `files.kind` | 典型 `meta.kind` | 主要来源 |
| --- | --- | --- | --- |
| `The Expanse/` | `folder` | `show` | 目录名、NFO、TMDB、下层单集证据 |
| `Season 01/` | `folder` | `season` | 目录名提取季号，可能继承父级海报引用 |
| `S01E01.mkv` | `video` | `episode` | ffprobe、文件名、祖先目录、TMDB 分集信息 |

单集先从文件名提取季号/集号，再沿祖先查找 Show 文件夹；可使用已经增强的父级 TMDB ID，也可独立查找。由于任务并行，**不保证父目录总是先于子文件增强完成**。父级后来得到更准确匹配时，目前没有完整的依赖传播体系来自动刷新所有子项。

## 示例 4：音乐、照片、电子书

- `Music/Artist/Album/01 - Track.flac`：Scanner 标记 `audio`；`Probe` 读取内嵌 Tag（标题、艺术家、专辑、年份、曲目号等）、时长和音频编码；UI 显示标题及 `艺术家 · 专辑 · 时长`。当前不会独立生成 `artist` / `album` 聚合对象。
- `Photos/IMG_0001.jpg`：Scanner 标记 `image`；`Photo` 读取图片尺寸、拍摄时间、相机、镜头和可能的 GPS 信息，令 `meta.kind=photo`；缩略图延迟生成。无 EXIF 不是增强失败。
- `Books/The Little Prince.epub`：Scanner 标记 `book`；`Document` 从 EPUB OPF 读取标题、作者、简介、出版社、语言等；`poster=zip:<entry>` 指向 EPUB 内封面图片。

## 前端如何拼装三行信息与详情

[web/app.js](../web/app.js) 的 `title(item)`、`subtitle(item)`、`Details` 根据 `meta.kind`、`meta.title` 和 `meta.info` 拼装标题、副标题、详情字段；当前**没有持久化的 `line1/line2/line3` 列**。如需更加可配置的三行展示，优先保留结构化元数据，只在展示层派生这些行，不要将翻译好的展示字符串当作唯一事实来源。

文件详情 API 可立即返回原始条目，即使后台仍在增强；界面随后读取到更新的 `meta`，才显示识别标题、简介、海报等信息。`/api/search` 使用 `files_fts` 检索文件名与提取标题，不搜索全部 `meta` 字段。

## 当前边界与后续方向

- 扫描由周期任务和浏览时的目录 mtime 检测驱动，未采用实时文件监控；文件内容原地修改不会必然更新父目录 mtime。
- `Match + Enrich` 和同一个 `Meta` 已满足当前内部可组合需求；无需引入动态插件、独立任务队列、Redis 或复杂 DAG 引擎。
- 缺少“文件版本与后台写回”的并发校验、父子元数据失效传播、用户覆盖数据的独立持久化；这些是下一步的正确性改进重点。
- 元数据增强、海报/字幕缓存和视频转码是不同阶段，不能把查看列表理解为一定已下载封面或转码了整部视频。

具体问题、修复策略、测试及分阶段顺序参见 [improvements.md](improvements.md)。
