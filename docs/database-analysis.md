# 数据库使用合理性分析

## 问题背景

你担心使用 SQLite 存储文件元数据会导致**数据不一致性**，这个担忧是正确的。

---

## 当前架构的不一致风险

### 场景分析

```
时间点 T0: 启动扫描
┌─────────────────────┐      ┌─────────────────────┐
│    文件系统          │      │     SQLite 数据库    │
│  /data/Movies/      │      │  files 表           │
│  ├─ movie1.mp4      │      │  ├─ movie1.mp4 ✓   │
│  └─ movie2.mp4      │      │  └─ movie2.mp4 ✓   │
└─────────────────────┘      └─────────────────────┘
        │                            │
        └────────── 一致状态 ──────────┘

时间点 T1: 用户删除文件（数据库未知）
┌─────────────────────┐      ┌─────────────────────┐
│    文件系统          │      │     SQLite 数据库    │
│  /data/Movies/      │      │  files 表           │
│  └─ movie2.mp4      │      │  ├─ movie1.mp4 ✗   │ ← 脏数据
│                      │      │  └─ movie2.mp4 ✓   │
└─────────────────────┘      └─────────────────────┘
        │                            │
        └──────── 不一致状态 ──────────┘
```

### 当前代码的问题

```go
// main.go
func main() {
    server, _ := NewFileServer()
    
    // ❌ 只在启动时扫描一次
    go server.ScanLibraries()
    
    // ❌ 之后文件系统变化，数据库完全不知道
    // - 用户手动删除文件
    // - 其他程序修改文件
    // - 网络存储断开重连
}
```

---

## 方案对比

### 方案 A: 不使用数据库（实时查询文件系统）

```go
// 每次请求直接读取文件系统
func (server *FileServer) ListFiles(path string) ([]File, error) {
    entries, _ := os.ReadDir(path)
    var files []File
    for _, e := range entries {
        info, _ := e.Info()
        files = append(files, File{
            Name:  e.Name(),
            Size:  info.Size(),
            IsDir: e.IsDir(),
        })
    }
    return files, nil
}
```

| 优点 | 缺点 |
|------|------|
| ✅ 数据永远一致 | ❌ 每次请求都要磁盘 I/O |
| ✅ 无额外存储 | ❌ 大量文件时响应慢（秒级） |
| ✅ 代码简单 | ❌ 无法缓存元数据（封面、标题等） |
| ✅ 无维护成本 | ❌ 无法实现搜索功能 |

**适用场景**：文件数量 < 10,000，对性能要求不高

---

### 方案 B: 使用数据库 + 实时校验（推荐）

```go
// 查询时校验文件是否存在
func (server *FileServer) ListFiles(path string) ([]File, error) {
    rows, _ := server.db.Query(`SELECT * FROM files WHERE path = ?`, path)
    
    var files []File
    var stalePaths []string
    
    for rows.Next() {
        var file File
        rows.Scan(&file)
        
        // ✅ 校验文件是否仍然存在
        if _, err := os.Stat(file.filename()); os.IsNotExist(err) {
            stalePaths = append(stalePaths, file.filename)
            continue // 跳过已删除的文件
        }
        
        files = append(files, file)
    }
    
    // ✅ 异步清理脏数据
    if len(stalePaths) > 0 {
        go server.deleteStaleFiles(stalePaths)
    }
    
    return files, nil
}
```

| 优点 | 缺点 |
|------|------|
| ✅ 查询性能好（毫秒级） | ❌ 需要额外存储 |
| ✅ 可缓存元数据 | ❌ 代码复杂度增加 |
| ✅ 支持搜索 | ❌ 需要处理不一致 |
| ✅ 可接受最终一致性 | |

**适用场景**：文件数量 > 10,000，需要搜索和元数据

---

### 方案 C: 使用数据库 + 文件监控（最佳体验）

```go
// 使用 fsnotify 监控文件系统变化
import "github.com/fsnotify/fsnotify"

func (server *FileServer) StartWatcher() error {
    watcher, _ := fsnotify.NewWatcher()
    defer watcher.Close()
    
    for _, lib := range server.config.Libraries {
        watcher.Add(lib.Path)
    }
    
    for event := range watcher.Events {
        switch {
        case event.Op&fsnotify.Create == fsnotify.Create:
            server.ProcessFile(event.Name) // 添加
        case event.Op&fsnotify.Remove == fsnotify.Remove:
            server.DeleteFile(event.Name)  // 删除
        case event.Op&fsnotify.Write == fsnotify.Write:
            server.ProcessFile(event.Name) // 更新
        }
    }
}
```

| 优点 | 缺点 |
|------|------|
| ✅ 近实时同步 | ❌ 实现复杂度高 |
| ✅ 用户体验好 | ❌ 网络文件系统支持差 |
| ✅ 减少脏数据 | ❌ 可能丢失事件 |

---

### 方案 D: 混合模式（最稳健）

```go
// 数据库作为缓存，文件系统作为真相源
func (server *FileServer) ListFiles(path string, refresh bool) ([]File, error) {
    if refresh {
        // 强制刷新：直接读文件系统
        return server.scanFromFS(path)
    }
    
    // 正常情况：查数据库 + 校验
    files, stale := server.queryWithValidation(path)
    
    if len(stale) > 10 {
        // 大量不一致：触发后台重新扫描
        go server.ScanDirectory(path)
    }
    
    return files, nil
}
```

---

## 推荐方案

### 针对你的场景（处理大量文件）

```
┌─────────────────────────────────────────────────────────┐
│                    推荐架构                              │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  ┌─────────────┐     ┌──────────────┐                  │
│  │  文件系统    │     │  SQLite 缓存  │                  │
│  │  (真相源)   │     │  (加速层)    │                  │
│  └──────┬──────┘     └──────┬───────┘                  │
│         │                   │                          │
│         │  1. 定时扫描      │                          │
│         ├──────────────────►│                          │
│         │                   │                          │
│         │  2. 文件监控      │                          │
│         ├──────────────────►│                          │
│         │  (fsnotify)       │                          │
│         │                   │                          │
│         │  3. 查询时校验    │                          │
│         ◄───────────────────┤                          │
│         │  (发现脏数据)     │                          │
│         │                   │                          │
│  ┌──────▼──────┐     ┌──────▼───────┐                  │
│  │ 实际文件     │     │ 清理脏数据    │                  │
│  └─────────────┘     └──────────────┘                  │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

### 核心原则

1. **文件系统是唯一真相源** (Source of Truth)
2. **数据库是性能缓存层** (Performance Cache)
3. **接受最终一致性** (Eventual Consistency)
4. **查询时校验关键数据** (Query-time Validation)

---

## 实现建议

### 1. 添加校验标志

```go
type File struct {
    Name      string
    Path      string
    Size      int64
    IsDir     bool
    Icon      string
    Title     string
    CheckedAt time.Time  // 最后校验时间
}
```

### 2. 懒校验策略

```go
func (server *FileServer) ListFiles(path string) ([]File, error) {
    rows, _ := server.db.Query(`SELECT * FROM files WHERE path = ?`, path)
    
    var files []File
    for rows.Next() {
        var file File
        rows.Scan(&file)
        
        // 超过 24 小时未校验，检查文件是否存在
        if time.Since(file.CheckedAt) > 24*time.Hour {
            if _, err := os.Stat(file.filename()); os.IsNotExist(err) {
                // 标记删除（异步）
                go server.db.Exec(`DELETE FROM files WHERE id = ?`, file.ID)
                continue
            }
            // 更新校验时间
            go server.db.Exec(`UPDATE files SET checked_at = ? WHERE id = ?`, 
                time.Now(), file.ID)
        }
        
        files = append(files, file)
    }
    return files, nil
}
```

### 3. 定期一致性检查

```go
// 每天凌晨 2 点执行
func (server *FileServer) StartConsistencyCheck() {
    go func() {
        for {
            now := time.Now()
            tomorrow := now.AddDate(0, 0, 1)
            next := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 
                2, 0, 0, 0, now.Location())
            
            time.Sleep(next.Sub(now))
            
            // 执行一致性检查
            server.checkConsistency()
        }
    }()
}

func (server *FileServer) checkConsistency() {
    // 抽样检查 10% 的记录
    rows, _ := server.db.Query(`SELECT id, path, name FROM files ORDER BY random() LIMIT 1000`)
    
    for rows.Next() {
        var id int
        var path, name string
        rows.Scan(&id, &path, &name)
        
        if _, err := os.Stat(filepath.Join(path, name)); os.IsNotExist(err) {
            server.db.Exec(`DELETE FROM files WHERE id = ?`, id)
            log.Printf("Cleaned stale file: %s", filepath.Join(path, name))
        }
    }
}
```

---

## 决策树

```
是否需要搜索功能？
│
├─ 否 ──► 方案 A: 不使用数据库（简单场景）
│
└─ 是
   │
   文件数量？
   │
   ├─ < 10,000 ──► 方案 A: 不使用数据库
   │
   └─ > 10,000
      │
      是否接受短暂不一致？
      │
      ├─ 否 ──► 方案 A: 每次实时查询（性能差）
      │
      └─ 是 ──► 方案 B: 数据库 + 校验（推荐）
                 │
                 需要更好体验？
                 │
                 └─ 是 ──► 方案 C: 数据库 + 文件监控
```

---

## 针对你当前代码的建议

### 最小改动方案

```go
// 1. 查询时校验文件存在
func (server *FileServer) ListFiles(path string, offset, size int) (files []File, err error) {
    rows, err := server.db.Query(`
        SELECT name, size, path, is_dir, icon, title, line1, line2, line3
        FROM files WHERE path = ? LIMIT ? OFFSET ?
    `, path, size, offset)
    if err != nil {
        return
    }
    defer rows.Close()
    
    for rows.Next() {
        var file File
        if err = rows.Scan(&file.Name, &file.Size, &file.Path, &file.IsDir, 
                          &file.Icon, &file.Title, &file.Line1, &file.Line2, &file.Line3); err != nil {
            return
        }
        
        // ✅ 校验文件是否存在
        if _, err := os.Stat(file.filename()); err == nil {
            files = append(files, file)
        }
        // 静默跳过已删除的文件
    }
    return
}

// 2. 添加手动刷新接口
func (server *FileServer) RefreshHandler(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Query().Get("path")
    go server.ScanDirectory(path) // 后台重新扫描
    w.Write([]byte("Refreshing..."))
}
```

### 配置建议

```yaml
# config.yaml
database:
  enabled: true           # 是否启用数据库缓存
  consistency_check: daily  # never | hourly | daily
  validation_mode: lazy     # none | lazy | strict
```

---

## 总结

| 问题 | 答案 |
|------|------|
| 使用数据库是否合理？ | **合理**，大量文件时必须用数据库做缓存 |
| 数据不一致怎么办？ | **接受最终一致性** + **查询时校验** |
| 最佳实践？ | 文件系统是真相源，数据库是加速层 |
| 当前最需要的改进？ | 添加查询时文件存在性校验 |

**核心思想**：不要试图保持数据库和文件系统**完全一致**，而是：
1. 承认不一致会发生
2. 在查询时发现并修复
3. 定期后台清理脏数据
