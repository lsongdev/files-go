# iOS 客户端设计

> 状态：**设计提案，尚未实现**。2026-11-02。现有索引和增强流程见 [architecture.md](architecture.md)。

## 1. 定位

iOS App 是 `files-go` 的另一个客户端，用来改善手机上的浏览和播放体验。它按真实目录浏览文件，也把已识别的电影、电视剧、专辑和图书展示成媒体库。

原则：

1. **能复用就不改后端**：服务端只增加第 3 节列出的三处改动。
2. **媒体库尽力而为**：只展示增强器识别出的内容，不保证准确和完整。没有进入媒体 Tab 的内容，仍可在文件 Tab 找到。
3. **App 不重新识别**：不解析文件名或 NFO，也不维护独立的媒体数据。
4. **文件 Tab 永远真实**：严格按 Root → 目录 → 子项展示，不去重。

技术选型：SwiftUI + AVPlayer，不做 Android。

## 2. 页面

底部固定五个 Tab，切换时保留各自的导航栈和滚动位置。

| Tab | 数据来源 | 列表 | 打开条目 |
| --- | --- | --- | --- |
| 文件 | `/api/roots` → `/api/files/{id}/children` | 目录列表 | 进入目录，或打开文件 |
| 电影 | `/api/library?kind=movie` | 海报网格 | 详情页 |
| 电视剧 | `/api/library?kind=show` | 海报网格 | 详情页 |
| 音乐 | `/api/library?kind=album` | 封面网格 | 详情页，点击曲目后按当前列表顺序播放 |
| 图书 | `/api/library?kind=book` | 封面网格 | 下载后用系统预览或分享打开 |

搜索复用 `/api/search`，从页面顶部进入，范围是全局。设置页放服务器地址和账号。

**详情页只有一种**：`GET /api/files/{id}` 提供标题、封面和简介，`/children` 提供子项。点目录就进入下一层，点音视频就播放。季、集和多碟专辑都按实际目录结构逐层进入，不新增层级查询。

列表和详情都展示现有的文件 View：

- 标题和副标题的规则与 Web 相同，见 [web/app.js](../web/app.js) 的 `title` 和 `subtitle`。
- 封面地址也与 Web 相同：`/api/files/{id}/thumb?v={version}-{artworkVersion}-{poster}`。

## 3. 后端改动

只有下面三处。

### 3.1 `GET /api/library`

现有 `/api/search` 按 `files.kind` 过滤，而且必须带搜索词，因此需要一个按 `meta.kind` 列出内容的接口。

| 参数 | 说明 |
| --- | --- |
| `kind` | 必填，`meta.kind` 的值 |
| `root` | 可选，限定 Root |
| `sort` | `title`（默认）或 `recent`（文件 mtime 倒序） |
| `offset`、`limit` | 与 `/children` 相同，默认 200，上限 1000 |

响应也与 `/children` 相同：`{"items": [View...], "more": bool}`。

```sql
SELECT <columns> FROM files f
WHERE json_extract(f.meta,'$.kind') = :kind
  AND NOT EXISTS (SELECT 1 FROM files p WHERE p.id = f.parent
                  AND json_extract(p.meta,'$.kind') = :kind)
ORDER BY COALESCE(NULLIF(f.title,''), f.name) COLLATE NOCASE, f.id
LIMIT :limit OFFSET :offset;
```

- **去重**：父目录与当前条目的 `meta.kind` 相同时，隐藏当前条目。所以电影目录里的视频不会重复出现。这一规则依赖目录聚合：目录被标记为 `movie` 时，它的主要视频已确认是同一作品。
- **排序**：`files.title` 在标题与文件名相同时为空，因此要回退到 `name`。

### 3.2 索引

加入 [store/store.go](../store/store.go) 的 `triggers`，启动时自动创建，不升级 schema 版本：

```sql
CREATE INDEX IF NOT EXISTS files_library ON files(
  json_extract(meta,'$.kind'),
  COALESCE(NULLIF(title,''),name) COLLATE NOCASE,
  id);
```

### 3.3 PDF 按 `type: books` 判定

`Document` 增强器读取媒体提示：提示为 `books` 时，PDF 的 `meta.kind` 设为 `book`；否则仍是 `document`。不根据文件内容猜测。

新增或修改 `type` 会改变增强版本标签，后台会按现有机制重新增强。如果已有 Root 配置了 `type: books`，上线时需要提升增强版本，其中的 PDF 才会更新。

## 4. iOS 实现

### 连接

- 一次连接一台服务器，切换服务器时清空本地缓存。
- 账号密码存 Keychain，API 请求带 Basic Auth。
- 配置 ATS 例外和本地网络权限，以允许局域网 HTTP。外网访问走 HTTPS 或 VPN。

### 播放

**音视频都走 `GET /api/files/{id}/play`**，把返回的 HLS 地址交给 AVPlayer：

- HLS 地址由不可猜测的会话 key 保护，不需要 Basic Auth，也就不用解决 AVPlayer 携带凭据的问题。
- `play` 返回相对路径，需要拼上服务器地址。
- 播放失败时用 `?transcode=1` 重试一次，用来处理 VP9、AV1 等设备解不了的编码。
- 结束或切换时调用 `DELETE /api/hls/{key}`。没调用成功也没关系，服务端会按空闲超时清理。

音乐用同一条播放路径，开启后台音频并接入系统媒体控制。专辑曲目优先按 `meta.info.Track` 在客户端排序，没有曲目号时保持接口返回的顺序。

### 下载和图书

用 URLSession 带 Basic Auth 请求 `/api/files/{id}/raw?download` 并保存到临时目录，再交给系统预览或分享面板。App 内不做阅读器。

### 刷新

下拉刷新，回到前台时重新读取当前页面。不接入 `/api/events`。

## 5. 开发顺序

**阶段 A：文件浏览和播放**（不改后端）

连接配置、目录浏览、搜索、缩略图、文件详情、下载和音视频播放。需要在实机上确认 HLS 播放、拖动进度和切后台都正常。

**阶段 B：媒体 Tab**

实现第 3 节的后端改动和回归测试，再加入电影、电视剧、音乐、图书四个 Tab。测试至少覆盖：

- 电影目录与其中的视频只返回目录。
- `meta.kind` 不同的父子目录各自保留。
- `root` 过滤、两种排序和分页。
- PDF 只在 `type: books` 下成为 `book`。

**阶段 C：音乐后台播放**

播放队列、后台音频和系统媒体控制。

## 6. 已知限制

这些情况在当前阶段接受，不做处理：

- 电视剧 Tab 只显示 `show`。松散单集、孤立季包和未确认的目录只能从文件 Tab 进入；同一部剧分散在多个目录时，会显示多个入口。
- 自动模式的 Root 里，未识别的视频不进入电影 Tab。
- `fb2`、`djvu`、`cbz`、`cbr` 目前没有 `meta.kind=book`，不进入图书 Tab。
- 同一作品在不同 Root 各有一份时，显示为多个入口。
- 排序使用 SQLite 的 `NOCASE`，中文不按拼音排序。

## 7. 暂不做

- 媒体 Tab 内的搜索和筛选
- App 内的手动匹配（在 Web 上操作）
- 字幕（AVPlayer 不能像 Web 的 `<track>` 那样直接加载独立 WebVTT）
- 多服务器同时管理
- 收藏、继续观看和播放进度
- 离线缓存
- App 内阅读器
- Android 客户端
