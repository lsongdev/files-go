# Files-Go 优化建议文档

## 项目概述

Files-Go 是一个基于 Go 语言的在线文件管理器，支持：
- 多库（Library）管理
- WebDAV 协议支持
- 文件元数据提取（EPUB、MP3、APK、图片）
- 分页浏览
- SQLite 元数据缓存

---

## 架构分析

### 当前架构

```
┌─────────────────────────────────────────────────────────────┐
│                      HTTP Server (8080)                      │
├─────────────┬─────────────┬─────────────┬───────────────────┤
│   IndexView │  ListHandler│  FileHandler│  WebDAV Handler   │
└─────────────┴─────────────┴─────────────┴─────────┬─────────┘
                                                    │
┌───────────────────────────────────────────────────▼─────────┐
│                     FileServer                               │
│  ┌─────────────────┐  ┌─────────────────────────────────┐   │
│  │  SQLite (内存)  │  │  File Processors Chain          │   │
│  │  - files 表     │  │  - EpubProcessor                │   │
│  │  - 无索引优化   │  │  - MusicProcessor               │   │
│  │  - WAL 模式      │  │  - ImageProcessor               │   │
│  │                 │  │  - APKProcessor                 │   │
│  │                 │  │  - DefaultProcessor             │   │
│  └─────────────────┘  └─────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

---

## 性能瓶颈分析

### 1. 数据库层

#### 问题
- ❌ **缺少关键索引**：`path` 字段无索引，`ListFiles` 查询效率低
- ❌ **内存数据库**：重启后元数据丢失，每次启动需重新扫描
- ❌ **无批量插入**：逐文件插入，大量文件时性能极差
- ❌ **无连接池配置**：未设置合理的连接数限制

#### 建议

```sql
-- 添加索引
CREATE INDEX idx_files_path ON files(path);
CREATE INDEX idx_files_dir ON files(dir);
CREATE INDEX idx_files_name ON files(name);

-- 复合索引优化分页查询
CREATE INDEX idx_files_path_name ON files(path, name);
```

**批量插入优化**：
```go
func (server *FileServer) BatchInsert(files []*types.File) error {
    tx, err := server.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    stmt, err := tx.Prepare(`
        INSERT INTO files (name, path, is_dir, size, icon, title, line1, line2, line3)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()

    for _, f := range files {
        if _, err := stmt.Exec(f.Name, f.Path, f.IsDir, f.Size,
            f.Icon, f.Title, f.Line1, f.Line2, f.Line3); err != nil {
            return err
        }
    }
    return tx.Commit()
}
```

**持久化存储**：
```go
// 使用磁盘数据库，带缓存
db, err := sql.Open("sqlite", "file:metadata.db?_journal_mode=WAL&_cache_size=-64000")
```

---

### 2. 文件扫描层

#### 问题
- ❌ **无增量扫描**：每次启动全量扫描，不支持增量更新
- ❌ **无并发扫描**：单线程 `filepath.Walk`，大量文件时极慢
- ❌ **无扫描进度**：用户无法感知扫描状态
- ❌ **错误处理不当**：`log.Fatal` 导致单个错误终止整个扫描
- ❌ **无文件变更监控**：无法感知文件系统变化

#### 建议

**并发扫描**：
```go
func (server *FileServer) ScanDirectoryConcurrent(root string) error {
    fileChan := make(chan string, 1000)
    errChan := make(chan error, 10)
    var wg sync.WaitGroup

    // 启动多个处理协程
    for i := 0; i < runtime.NumCPU(); i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for path := range fileChan {
                // 处理文件
            }
        }()
    }

    // 遍历目录
    go func() {
        filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
            if err != nil {
                log.Printf("walk error: %v", err)
                return nil // 继续扫描
            }
            fileChan <- path
            return nil
        })
        close(fileChan)
    }()

    wg.Wait()
    close(errChan)
    return nil
}
```

**增量扫描**：
```go
func (server *FileServer) IncrementalScan(root string) error {
    // 获取上次扫描时间
    var lastScan time.Time
    err := server.db.QueryRow(`SELECT MAX(updated_at) FROM files WHERE path LIKE ?`, root+"%").Scan(&lastScan)
    
    return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
        if info.ModTime().Before(lastScan) {
            return nil // 跳过未变更文件
        }
        // 处理变更文件
    })
}
```

**文件监控**：
```go
// 使用 fsnotify 监控文件变化
import "github.com/fsnotify/fsnotify"

watcher, _ := fsnotify.NewWatcher()
watcher.Add("/path/to/library")
go func() {
    for event := range watcher.Events {
        if event.Op&fsnotify.Write == fsnotify.Write {
            server.ProcessFile(event.Name)
        }
    }
}()
```

---

### 3. 文件处理器层

#### 问题
- ❌ **无缓存机制**：每次请求都重新处理文件
- ❌ **临时文件泄漏**：`/tmp/` 文件无清理机制
- ❌ **无超时控制**：大文件处理可能阻塞

#### 建议

**元数据缓存**：
```go
type MetadataCache struct {
    data sync.Map // map[string]*CachedMetadata
}

type CachedMetadata struct {
    Icon      string
    Title     string
    Line1     string
    Line2     string
    Line3     string
    UpdatedAt time.Time
    TTL       time.Duration
}

func (cache *MetadataCache) Get(path string) (*CachedMetadata, bool) {
    if v, ok := cache.data.Load(path); ok {
        meta := v.(*CachedMetadata)
        if time.Since(meta.UpdatedAt) < meta.TTL {
            return meta, true
        }
    }
    return nil, false
}
```

**临时文件清理**：
```go
// 定期清理任务
func (server *FileServer) StartCleanupTask() {
    ticker := time.NewTicker(24 * time.Hour)
    go func() {
        for range ticker.C {
            filepath.Walk("/tmp", func(path string, info os.FileInfo, err error) error {
                if strings.HasPrefix(info.Name(), "files-go-") && 
                   info.ModTime().Before(time.Now().Add(-24*time.Hour)) {
                    os.Remove(path)
                }
                return nil
            })
        }
    }()
}
```

---

### 4. Web 服务层

#### 问题
- ❌ **模板每次解析**：`template.ParseFiles` 在每次请求时执行
- ❌ **无静态资源缓存**：图标等资源无缓存策略
- ❌ **无请求限流**：可能遭受恶意请求
- ❌ **无健康检查**：缺少监控端点
- ❌ **硬编码端口**：端口配置未使用

#### 建议

**模板预编译**：
```go
var templates *template.Template

func init() {
    templates = template.Must(template.ParseFiles(
        "templates/layout.html",
        "templates/index.html",
        "templates/list.html",
    ))
}

func (s *FileServer) Render(w http.ResponseWriter, name string, data H) {
    err := templates.ExecuteTemplate(w, "layout", data)
    // ...
}
```

**静态资源缓存**：
```go
// 添加缓存头
func withCacheMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/file?path=/tmp/") {
            w.Header().Set("Cache-Control", "public, max-age=86400")
        }
        next.ServeHTTP(w, r)
    })
}
```

**请求限流**：
```go
import "golang.org/x/time/rate"

var limiter = rate.NewLimiter(rate.Every(time.Second), 100)

func rateLimitMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !limiter.Allow() {
            http.Error(w, "Too many requests", http.StatusTooManyRequests)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

---

### 5. 前端层

#### 问题
- ❌ **无懒加载优化**：大量文件时 DOM 节点过多
- ❌ **无虚拟滚动**：长列表性能差
- ❌ **无搜索功能**：无法快速定位文件
- ❌ **无面包屑导航**：路径跳转不便
- ❌ **无响应式图片**：图标加载慢

#### 建议

**虚拟滚动**：
```html
<!-- 使用现代虚拟滚动库 -->
<script type="module">
  import { VirtualScroller } from 'https://cdn.jsdelivr.net/npm/virtual-scroller/+esm';
  
  const scroller = new VirtualScroller(document.querySelector('.files'), {
    itemHeight: 80,
    bufferSize: 10
  });
</script>
```

**搜索功能**：
```go
func (server *FileServer) SearchHandler(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("q")
    rows, err := server.db.Query(`
        SELECT name, path, is_dir, icon, title 
        FROM files 
        WHERE name LIKE ? OR title LIKE ?
        LIMIT 50
    `, "%"+query+"%", "%"+query+"%")
    // ...
}
```

---

## 安全性问题

### 1. 路径遍历漏洞 ⚠️

```go
// 当前代码存在风险
http.ServeFile(w, r, path) // path 来自用户输入

// 修复方案
func safePath(base, userPath string) (string, error) {
    clean := filepath.Clean(filepath.Join(base, userPath))
    if !strings.HasPrefix(clean, base) {
        return "", errors.New("invalid path")
    }
    return clean, nil
}
```

### 2. SQL 注入风险

虽然使用了参数化查询，但需确保所有查询都正确使用 `?` 占位符。

### 3. 文件上传风险

WebDAV 允许任意文件上传，应添加：
- 文件类型白名单
- 文件大小限制
- 病毒扫描

---

## 配置优化

### 当前配置问题

```yaml
# config.yaml
listen: ":8080"  # ✅ 但未使用
language: zh-CN  # ✅ 但未使用
tmdb:
  api_key: "..." #  
```

### 建议配置

```yaml
server:
  addr: ":8080"
  read_timeout: 30s
  write_timeout: 30s
  
database:
  driver: sqlite
  dsn: "file:metadata.db?_journal_mode=WAL&_cache_size=-64000"
  max_open_conns: 25
  max_idle_conns: 5
  
scanner:
  workers: 4
  batch_size: 1000
  enable_watcher: true
  
cache:
  metadata_ttl: 24h
  thumbnail_ttl: 7d
  
security:
  allowed_extensions: [".jpg", ".png", ".mp3", ".epub", ".apk"]
  max_upload_size: 104857600 # 100MB
```

---


## 扩展建议

### 1. 功能扩展

| 功能 | 优先级 | 描述 |
|------|--------|------|
| 全文搜索 | 高 | 支持文件内容搜索 |
| 标签系统 | 中 | 用户自定义标签 |
| 分享链接 | 中 | 生成文件分享链接 |
| 回收站 | 高 | 删除文件临时存储 |
| 多用户 | 低 | 用户权限管理 |
| 缩略图生成 | 高 | 图片/视频缩略图 |

### 2. 存储扩展

- 支持 S3 兼容存储
- 支持 FTP/SFTP
- 支持云存储（Google Drive, Dropbox）

### 3. 协议扩展

- FTP 服务器
- SMB/CIFS 共享
- HTTP API (RESTful)

---

## 实施路线图

### Phase 1: 性能优化（1-2 周）
- [ ] 添加数据库索引
- [ ] 实现批量插入
- [ ] 模板预编译
- [ ] 修复 ImageProcessor 重复注册

### Phase 2: 稳定性提升（2-3 周）
- [ ] 增量扫描
- [ ] 文件监控
- [ ] 临时文件清理
- [ ] 错误处理优化

### Phase 3: 功能增强（3-4 周）
- [ ] 搜索功能
- [ ] 面包屑导航
- [ ] 虚拟滚动
- [ ] 缩略图缓存

### Phase 4: 安全加固（1-2 周）
- [ ] 路径遍历修复
- [ ] 请求限流
- [ ] 文件上传限制
- [ ] 敏感信息配置化

---

## 总结

Files-Go 当前版本功能完整，但在**性能**、**安全性**和**可维护性**方面有较大优化空间。建议按优先级逐步实施上述改进，特别是：

1. **数据库索引** - 立竿见影的性能提升
2. **批量插入** - 大幅缩短启动时间
3. **路径安全** - 避免安全漏洞
4. **增量扫描** - 提升用户体验

---

*文档生成时间：2026-03-26*
