# 移动 App 设计：文件浏览与语义类型媒体视图

> 状态：**设计提案，尚未实现**。2026-11-02。本文描述未来 App 与最小后端扩展，不代表下述新接口已经可用。现有索引和增强流程见 [architecture.md](architecture.md)，可靠性工作见 [improvements.md](improvements.md)。

## 1. 定位与原则

App 是 `files-go` 的另一种客户端：**同一份文件索引、同一套增强元数据，用不同界面浏览和使用**。既能按真实目录浏览 NAS，也能把已经识别的电影、电视剧、专辑、图书直接作为媒体库浏览。

不额外维护电影、剧集、歌曲、图书等独立数据表；不要求用户手动建立一套与磁盘重复的媒体库，也不改变底层源文件的目录结构。

原则：

1. **文件是基础实体**：媒体作品仍是 `files` 中的文件或目录，沿用已有 `id`、`root`、`parent`、`meta`、`version`、`thumb` 和播放字段。
2. **语义类型决定媒体视图**：媒体 Tab 按 `meta.kind` 筛选，不按 `files.kind` 筛选。增强器负责识别，App 不重新解析文件名或 NFO。
3. **目录视图永远真实**：文件 Tab 严格遵循 Root → 目录 → 子项。即使目录已识别为电影、电视剧或专辑，用户仍能进入并查看其中所有视频、字幕、封面等原始内容。
4. **复用 API 而不复制功能**：新增通用语义类型查询能力，其余详情、目录导航、缩略图、搜索、播放、字幕、手动匹配、事件更新继续复用已有接口。
5. **从简单可用开始**：第一版不做独立媒体数据模型、推荐引擎、动态 Tab 插件、复杂跨目录图数据库或重型客户端同步框架。

## 2. 导航与页面

底部固定五个 Tab；每个 Tab 有自己的导航栈和滚动位置，切换后保留状态。

| Tab | 查询来源 | 第一版界面 | 打开条目 |
| --- | --- | --- | --- |
| 文件 | `GET /api/roots` → `/api/files/{id}/children` | 多 Root、列表／网格、面包屑、目录导航 | 文件／目录详情；原始子项浏览、预览、下载 |
| 电影 | `GET /api/library?kind=movie` | 海报网格，标题、年份、评分；排序和基础筛选 | 电影详情 → 选择可播放文件 → 播放 |
| 电视剧 | `GET /api/library?kind=show` | Show 海报网格 | Show 详情 → Season → Episode → 播放 |
| 音乐 | `GET /api/library?kind=album` | 专辑封面；可切换歌曲列表 | 专辑 → 曲目队列 → 全局播放器 |
| 图书 | `GET /api/library?kind=book` | 封面书架；标题、作者 | 图书详情 → 阅读／下载／打开其他 App |

搜索应是各 Tab 可以调用的通用操作，设置放在页面顶部或独立设置页面，不占用底部 Tab。未来可以开放照片、普通视频、文档等媒体类别，但不必在第一版增加底部导航。

### 类型映射

目前同一条 `files` 记录同时拥有两层分类，不能混淆：

| 示例 | 物理类型 `files.kind` | 语义类型 `meta.kind` | App 中的位置 |
| --- | --- | --- | --- |
| `Interstellar.mkv` | `video` | `movie` | 电影 |
| `Interstellar/`（同一电影目录） | `folder` | `movie` | 电影（与内部同作品文件去重） |
| `The Expanse/` | `folder` | `show` | 电视剧 |
| `Season 01/` | `folder` | `season` | 电视剧详情内部 |
| `S01E01.mkv` | `video` | `episode` | 电视剧详情内部 |
| `Random Access Memories/` | `folder` | `album` | 音乐 |
| `01 - Track.flac` | `audio` | `audio` | 专辑曲目／歌曲列表 |
| `The Little Prince.epub` | `book` | `book` | 图书 |

这些类型来自 [media/enrich.go](../media/enrich.go) 的 `Meta.Kind` 及目录汇总。**PDF 不因为扩展名是 PDF 就自动等于图书**：只有增强器实际将其标记为 `meta.kind=book` 时才进入图书 Tab；普通 `document` 仍可在文件 Tab 浏览。

## 3. 最小后端扩展：按 meta.kind 列表

### 为什么现有搜索还不够

目前 [store/store.go](../store/store.go) 的 `SearchOptions.Kind` 筛选 SQL 列 `files.kind`，并且 `Search` 在搜索词为空时直接返回空列表；因此不能通过 `/api/search?kind=movie` 取得所有电影。

新增一个**列表查询接口**即可，无需设计一整套媒体中心服务：

```http
GET /api/library?kind=movie
GET /api/library?kind=show
GET /api/library?kind=album
GET /api/library?kind=book
GET /api/library?kind=audio
GET /api/library?kind=episode
```

首版参数建议：

| 参数 | 说明 |
| --- | --- |
| `kind` | 必填，`meta.kind` 的精确值；允许未来新增类型 |
| `root` | 可选，限定某个已配置 Root |
| `q` | 可选，文件名／索引标题搜索；空值表示不按文本过滤 |
| `sort` | 可选：`title`（默认）、`year`、`recent` |
| `limit` | 可选，默认 40，设置上限（如 200） |
| `cursor` | 可选，供下一页使用，必须由服务端生成和解析 |

示意：

```http
GET /api/library?kind=movie&sort=year&limit=40
GET /api/library?kind=album&root=music&q=Daft
```

返回**与现有 API 一致的文件 View**，不再引入 `Movie`、`Show` 等各自独立的 JSON DTO：

```json
{
  "items": [
    {
      "id": 123,
      "name": "Interstellar (2014)",
      "dir": true,
      "kind": "folder",
      "meta": {
        "kind": "movie",
        "title": "星际穿越",
        "year": 2014
      },
      "thumb": true,
      "version": 7
    }
  ],
  "nextCursor": null
}
```

示例数据仅说明结构；`/api/library` 和 `nextCursor` 目前未实现。

查询基础是：

```sql
SELECT *
FROM files
WHERE json_extract(meta, '$.kind') = ?
ORDER BY title COLLATE NOCASE, id
LIMIT ?;
```

实际实现应复用已有 `columns`、`scanFile`、`View`，补充 root、关键词、稳定排序、去重、分页等必要条件，使用参数化 SQL，并验证 `sort` 白名单。排序字段加 `id` 形成确定性顺序；`recent` 先约定是**文件 mtime**，不要误称“最近入库”（当前没有独立的创建索引时间字段）。

为该查询添加轻量 SQLite 表达式索引即可，结合实际 SQL 的 `EXPLAIN QUERY PLAN` 再决定是否需要复合索引：

```sql
CREATE INDEX IF NOT EXISTS files_meta_kind
ON files(json_extract(meta, '$.kind'));
```

不增加 `library`、`movies`、`albums` 等表；不改变源文件，也不要求另起增强任务。可以在当前 schema 上作为兼容的索引增量加入，但必须有迁移／启动建索引和回归测试。

### 去重规则：目录优先，确认同一作品才隐藏子文件

现有聚合允许目录和内部文件同时被标记为 `movie`。直接过滤会把同一作品显示两次。

```text
Movies/
  Interstellar/                  meta.kind=movie
    Interstellar.mkv             meta.kind=movie
```

**电影库显示 `Interstellar/` 一次**。其内部视频仍然保留，在电影详情的文件列表中用于播放；文件 Tab 不去重。

具体规则：

1. 若当前条目存在某个已索引的祖先目录，且祖先 `meta.kind` 与当前媒体种类相同、**能够确定是同一个作品**，媒体列表只返回代表作品的最外层有效目录。
2. 判断同一作品优先依据有效 `meta.match`（provider/type/id）及目录汇总的共享作品关系，不能单凭同名、路径前缀或类型相同就隐藏文件。
3. 如果没有可证明的对应关系（尚待识别／候选冲突／不同匹配／多个电影合辑），保留独立条目，避免误丢资源。
4. `show` 首页仅查询 `show`，不把每一季 `season`、每一集 `episode` 显示为独立 Show；`album` 首页不把每一首 `audio` 作为专辑。
5. 电影目录内可能有多段视频、不同版本、花絮或字幕；去重仅影响**媒体库入口**，不删文件、不合并索引、不改变详情子项。
6. 去重必须在分页的有效结果集上完成，不能先 `LIMIT 40` 再简单过滤掉 20 个重复项、却仍声称该页满 40 条。可通过 SQL/批量父级判断完成，避免 N+1 查询。

同一个作品在不同 Root 里各有一份拷贝，首版可保留为两个独立入口；跨 Root 去重或版本合并留待真实需求出现后再设计。

## 4. 从媒体卡片到详情：复用现有能力

建议将“媒体卡片”理解为一个 `File View` 的表现形式：

- 标题：优先 `meta.title`，否则 `name`。
- 副标题：根据 `meta.kind` 拼出年份、评分、作者、艺术家、时长等，沿用 Web 的 [title/subtitle](../web/app.js) 思路；不新增数据库 `line1/line2/line3` 字符串列。
- 海报：`GET /api/files/{id}/thumb`；背景：`/backdrop`。根据 `version`、`artworkVersion` 设计缓存失效。
- 普通详情：`GET /api/files/{id}`；目录子项：`GET /api/files/{id}/children`（带分页）。
- 搜索：仍可先用 `/api/search` 做全局搜索；媒体 Tab 内筛选应通过未来 `/api/library?kind=...&q=...` 保持类型范围。
- 人工识别：复用已有 `GET/PUT/DELETE /api/files/{id}/match`。
- 更新：利用 `GET /api/events` 的变化提示，重新读取当前列表/详情；断线时按需刷新，切 Tab 时不重置整个应用。

### 电影

海报网格 → 电影详情（封面、剧情、年份、评分、文件列表）→ 播放对应的视频。目录和单文件都可以成为作品入口；单文件直接播放，目录根据其子项选择播放文件。不要假设目录本身能够播放。

### 电视剧

Show 海报网格 → Show 详情 → Season → Episode → 视频文件。首版优先复用实际目录和已有 `meta.kind=season/episode` 记录。如果单集跨多层文件夹、季度不存在实体文件夹，或节目成员不在直接子级，仅靠 `/children` 不一定能得到完整列表；**实际需要时再增加范围内按 `show/season` 的单集查询**，不为此提前建立第二套电视剧表。

### 音乐

Album 封面网格 → 专辑曲目。另可在音乐页切换“歌曲”，查询 `kind=audio`。曲目排序优先音轨号／碟号，不应只按文件名。全局播放控制器管理队列、上一首／下一首、顺序／随机／循环，跨 Tab 保持播放，并支持后台与系统媒体控制（由目标平台实现）。

### 图书

按 `kind=book` 构建书架，复用作者、封面、简介等字段。首版可从原始 API 读取或下载，具体阅读能力取决于客户端平台；EPUB 阅读进度、书签、复杂分页在后续阶段实施。

## 5. 原生播放与设备能力边界

客户端优先使用目标平台自带的视频/音频播放器：

- 可兼容的资源通过 `GET /api/files/{id}/raw` 直接播放（Range）。
- 否则通过 `GET /api/files/{id}/play` 获取 HLS 会话地址，交由原生 HLS 支持播放；必要时请求 `?transcode=1`。
- 在结束／切换会话时按当前契约调用 `DELETE /api/hls/{key}`，并正确处理网络断开后服务器的自动过期。
- 视频字幕可使用现有 `/subtitles` 接口发现轨道，但原生播放器对独立 WebVTT 的支持与 Web 的 `<track>` 不同；在 iOS/Android 实机上验证后，再决定是否需要 HLS 字幕轨道封装。
- 认证目前是可选 Basic Auth；原生播放器的分段请求不能假定和普通 API 客户端自动共享 Header。需要独立验证 HLS URL、播放器鉴权、有效期与 HTTPS，不能把密码拼在 URL 中。

首版不是重新实现 FFmpeg 客户端，也不需要让 App 下载全片后才能播放。

## 6. 首版开发顺序与验收

**阶段 A：文件浏览 App**

服务器地址与凭据配置；Root 列表；目录导航、列表/网格、搜索、缩略图、文件详情、下载/打开。验证与 Web 相同的源文件及元数据，不改变文件结构。

**阶段 B：最小媒体筛选后端**

实现 `GET /api/library`，支持 `meta.kind`、root、可选关键词、稳定排序和分页；在实际查询下验证索引效果；加入电影目录与子文件去重回归。至少覆盖空库、未增强记录、重复目录、不同作品同名、不同 Root、翻页中新增记录。

**阶段 C：四个媒体 Tab**

电影和 Show 海报墙、专辑和书架。全部沿用同一份 `View`，点击后跳转到现有文件详情/目录 API。电视剧成员关系不足时才补最小单集筛选。

**阶段 D：消费体验**

原生视频播放器、HLS/鉴权/字幕；音乐队列与后台播放；图书预览与下载。收藏、继续观看、跨设备播放/阅读进度、离线缓存和高级筛选作为后续独立设计，**不写入自动增强的 `meta`**。

验收条件：

1. 一个松散电影视频和一个电影目录各能作为电影入口；同目录、同作品的视频在电影 Tab 不重复，但文件 Tab 仍全部可见。
2. `show`、`season`、`episode` 只出现在各自合适的层级；切回文件 Tab 仍是相同目录结构。
3. 专辑目录只出现一次，内部曲目顺序合理；单曲能在歌曲列表单独找到。
4. `book` 正常进入图书 Tab；没有增强为 `book` 的文档不被意外归类。
5. 百万级索引分页不会加载全表到客户端；排序稳定，重复项过滤不破坏分页。
6. 识别结果随后台增强更新；离线 Root 的索引可浏览，源文件不可访问时提示明确，不误判为空库。
7. 播放和缩略图不要求 App 复制 Web 的业务逻辑；不出现播放会话无界泄漏或密钥明文进入 URL。

## 7. 暂不决定的事项

这份文档是**平台中立的 API 与产品设计**。iOS 可选择 SwiftUI + AVPlayer；Android 可选择原生 UI + Media3，但平台方案、最低系统版本、代码仓库结构、离线策略暂不在这里定死。

核心结论：**先做 `meta.kind` 的语义类型筛选，再用现有文件和目录详情承载媒体体验。** 遇到真实的剧集层级或复杂作品关系需求，再增加有边界的关系查询，避免提前构建庞大的媒体数据库。
