package server

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/lsongdev/files-go/config"
	"github.com/lsongdev/files-go/processors"
	"github.com/lsongdev/files-go/types"
	"gopkg.in/yaml.v3"
)

type H = map[string]interface{}

type FileServer struct {
	config *config.Config
	db     *sql.DB

	processors []processors.FileProcessor

	// 增量扫描相关
	lastScanTime map[string]time.Time // 每个库的最后扫描时间
	scanMu       sync.RWMutex         // 保护 lastScanTime
}

func NewFileServer() (server *FileServer, err error) {
	var config *config.Config
	f, err := os.Open("config.yaml")
	if err != nil {
		return nil, err
	}
	if err := yaml.NewDecoder(f).Decode(&config); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:metadata.db?_journal_mode=WAL&_cache_size=-64000")
	if err != nil {
		return nil, err
	}
	server = &FileServer{
		config:       config,
		db:           db,
		lastScanTime: make(map[string]time.Time),
	}
	server.initDB()
	processors.SetTMDBAPIKey(config.TMDB.APIKey)
	server.processors = processors.GetProcessors()
	return
}

func (server *FileServer) GetProcessor(file *types.File) (processor processors.FileProcessor) {
	for _, p := range server.processors {
		if p.IsSupport(file) {
			return p
		}
	}
	return &processors.DefaultProcessor{}
}

func (server *FileServer) initDB() error {
	_, err := server.db.Exec(`
		PRAGMA journal_mode=WAL;
		
		CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			path TEXT,
			size INTEGER,
			dir TEXT,
			is_dir BOOLEAN,
			icon TEXT,
			title TEXT,
			line1 TEXT,
			line2 TEXT,
			line3 TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(name, path)
		);
		
		CREATE INDEX IF NOT EXISTS idx_files_path ON files(path);
		CREATE INDEX IF NOT EXISTS idx_files_name ON files(name);
		CREATE INDEX IF NOT EXISTS idx_files_updated ON files(updated_at);
	`)
	return err
}

func (server *FileServer) Insert(info *types.File) error {
	_, err := server.db.Exec(`
		INSERT OR REPLACE INTO files
			(name, path, is_dir, size, icon, title, line1, line2, line3, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		info.Name, info.Path, info.IsDir, info.Size,
		info.Icon, info.Title, info.Line1, info.Line2, info.Line3, time.Now())
	return err
}

// BatchInsert 批量插入文件元数据（事务优化）
func (server *FileServer) BatchInsert(files []*types.File) error {
	if len(files) == 0 {
		return nil
	}

	tx, err := server.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO files
			(name, path, is_dir, size, icon, title, line1, line2, line3, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, file := range files {
		if _, err := stmt.Exec(file.Name, file.Path, file.IsDir, file.Size,
			file.Icon, file.Title, file.Line1, file.Line2, file.Line3, time.Now()); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// DeleteStaleFiles 删除不存在的文件记录（增量扫描使用）
func (server *FileServer) DeleteStaleFiles(libraryPath string, currentFiles map[string]bool) error {
	rows, err := server.db.Query(`
		SELECT id, name, path FROM files 
		WHERE path LIKE ? OR path = ?
	`, libraryPath+"%", libraryPath)
	if err != nil {
		return err
	}
	defer rows.Close()

	var staleIDs []int64
	for rows.Next() {
		var id int64
		var name, path string
		if err := rows.Scan(&id, &name, &path); err != nil {
			continue
		}

		fullPath := filepath.Join(path, name)
		if !currentFiles[fullPath] {
			staleIDs = append(staleIDs, id)
		}
	}

	if len(staleIDs) > 0 {
		tx, err := server.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		stmt, err := tx.Prepare(`DELETE FROM files WHERE id = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for _, id := range staleIDs {
			stmt.Exec(id)
		}
		return tx.Commit()
	}

	return nil
}

// Render renders an HTML template with the provided data.
func (s *FileServer) Render(w http.ResponseWriter, name string, data H) {
	if data == nil {
		data = H{}
	}
	data["Config"] = s.config
	funcMap := template.FuncMap{
		"stringsToLower": strings.ToLower,
		"ext":            filepath.Ext,
		"trimPrefix":     strings.TrimPrefix,
	}
	tmpl, err := template.New(name).Funcs(funcMap).ParseFiles("templates/"+name+".html", "templates/layout.html")
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = tmpl.ExecuteTemplate(w, "layout", data)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *FileServer) Error(w http.ResponseWriter, err error) {
	s.Render(w, "error", H{
		"Error": err,
	})
}

func (server *FileServer) ScanDirectory(root string) error {
	return server.ScanDirectoryIncremental(root, time.Time{})
}

// ScanDirectoryIncremental 增量扫描目录
func (server *FileServer) ScanDirectoryIncremental(root string, lastScan time.Time) error {
	log.Printf("Scanning directory: %s (incremental: %v)", root, !lastScan.IsZero())

	var batch []*types.File
	batchSize := 1000
	currentFiles := make(map[string]bool)

	err := filepath.Walk(root, func(filename string, f os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Walk error on %s: %v", filename, err)
			return nil // 继续扫描
		}

		// 增量扫描：跳过未修改的文件
		if !lastScan.IsZero() && f.ModTime().Before(lastScan) {
			return nil
		}

		file := &types.File{
			Name:  f.Name(),
			Size:  f.Size(),
			IsDir: f.IsDir(),
			Path:  filepath.Dir(filename),
		}

		// 处理文件元数据
		p := server.GetProcessor(file)
		if err := p.Process(file); err != nil {
			log.Printf("Process error on %s: %v", filename, err)
		}
		batch = append(batch, file)
		currentFiles[file.FileName()] = true

		// 批量插入
		if len(batch) >= batchSize {
			if err := server.BatchInsert(batch); err != nil {
				log.Printf("Batch insert error: %v", err)
			}
			batch = batch[:0]
		}

		return nil
	})

	// 插入剩余的文件
	if len(batch) > 0 {
		if err := server.BatchInsert(batch); err != nil {
			log.Printf("Batch insert error: %v", err)
		}
	}

	// 删除不存在的文件记录（只在非增量扫描时执行）
	if lastScan.IsZero() {
		if err := server.DeleteStaleFiles(root, currentFiles); err != nil {
			log.Printf("Delete stale files error: %v", err)
		}
	}

	return err
}

// ScanDirectoryConcurrent 并发扫描目录（适用于大量文件）
func (server *FileServer) ScanDirectoryConcurrent(root string) error {
	log.Printf("Concurrent scanning directory: %s", root)

	fileChan := make(chan string, 1000)
	batchChan := make(chan []*types.File, 100)
	errChan := make(chan error, runtime.NumCPU())
	currentFiles := make(map[string]bool)
	var currentFilesMu sync.Mutex

	// 启动多个处理协程
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var batch []*types.File
			batchSize := 500

			for file := range fileChan {
				info, err := os.Stat(file)
				if err != nil {
					log.Printf("Stat error on %s: %v", file, err)
					continue
				}

				f := &types.File{
					Name:  info.Name(),
					Size:  info.Size(),
					IsDir: info.IsDir(),
					Path:  filepath.Dir(file),
				}
				p := server.GetProcessor(f)
				if err := p.Process(f); err != nil {
					log.Printf("Process error on %s: %v", file, err)
				}
				batch = append(batch, f)
				if len(batch) >= batchSize {
					batchChan <- batch
					batch = batch[:0]
				}
			}

			// 发送剩余的
			if len(batch) > 0 {
				batchChan <- batch
			}
		}(i)
	}

	// 批量写入协程
	var writeWg sync.WaitGroup
	writeWg.Add(1)
	go func() {
		defer writeWg.Done()
		for batch := range batchChan {
			if err := server.BatchInsert(batch); err != nil {
				log.Printf("Batch insert error: %v", err)
				errChan <- err
			}
		}
	}()

	// 遍历目录
	go func() {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				log.Printf("Walk error on %s: %v", path, err)
				return nil
			}
			fileChan <- path
			currentFilesMu.Lock()
			currentFiles[path] = true
			currentFilesMu.Unlock()
			return nil
		})
		close(fileChan)
	}()

	wg.Wait()
	close(batchChan)
	writeWg.Wait()
	close(errChan)

	// 删除不存在的文件
	if err := server.DeleteStaleFiles(root, currentFiles); err != nil {
		log.Printf("Delete stale files error: %v", err)
	}

	// 检查是否有错误
	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

func (server *FileServer) ScanLibraries() {
	log.Println("Starting initial scan of all libraries...")
	start := time.Now()

	for _, library := range server.config.Libraries {
		log.Printf("Scanning library: %s (%s)", library.Name, library.Path)

		// 首次全量扫描，使用并发模式
		if err := server.ScanDirectoryConcurrent(library.Path); err != nil {
			log.Printf("Error scanning %s: %v", library.Name, err)
		}

		// 记录扫描时间
		server.scanMu.Lock()
		server.lastScanTime[library.Path] = time.Now()
		server.scanMu.Unlock()
	}

	log.Printf("Initial scan completed in %v", time.Since(start))

	// 启动定期增量扫描（每小时）
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			log.Println("Starting incremental scan...")
			for _, library := range server.config.Libraries {
				server.scanMu.RLock()
				lastScan := server.lastScanTime[library.Path]
				server.scanMu.RUnlock()

				log.Printf("Incremental scanning %s (since %v)", library.Name, lastScan)
				if err := server.ScanDirectoryIncremental(library.Path, lastScan); err != nil {
					log.Printf("Error incremental scanning %s: %v", library.Name, err)
				}

				server.scanMu.Lock()
				server.lastScanTime[library.Path] = time.Now()
				server.scanMu.Unlock()
			}
		}
	}()
}

func (server *FileServer) ListFiles(path string, offset, size int) (files []types.File, err error) {
	rows, err := server.db.Query(`
		SELECT id, name, size, path, is_dir, icon, title, line1, line2, line3, updated_at
		FROM files
		WHERE path = ?
		LIMIT ?
		OFFSET ?
	`, path, size, offset)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var file types.File
		if err = rows.Scan(&file.ID, &file.Name, &file.Size, &file.Path, &file.IsDir,
			&file.Icon, &file.Title, &file.Line1, &file.Line2, &file.Line3, &file.UpdatedAt); err != nil {
			return
		}

		// 校验文件是否仍然存在
		if _, err := os.Stat(file.FileName()); err == nil {
			files = append(files, file)
		}
		// 静默跳过已删除的文件
	}
	return
}

// GetFile retrieves a single file by its full path
func (server *FileServer) GetFile(fullpath string) (*types.File, error) {
	dir := filepath.Dir(fullpath)
	name := filepath.Base(fullpath)

	row := server.db.QueryRow(`
		SELECT id, name, size, path, is_dir, icon, title, line1, line2, line3, updated_at
		FROM files
		WHERE path = ? AND name = ?
	`, dir, name)

	var file types.File
	err := row.Scan(&file.ID, &file.Name, &file.Size, &file.Path, &file.IsDir,
		&file.Icon, &file.Title, &file.Line1, &file.Line2, &file.Line3, &file.UpdatedAt)
	if err != nil {
		return nil, err
	}

	// 校验文件是否仍然存在
	if _, err := os.Stat(file.FileName()); err != nil {
		return nil, err
	}

	return &file, nil
}

func (server *FileServer) ListHandler(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if pageSize < 1 {
		pageSize = 100
	}
	path := r.URL.Query().Get("path")

	// 从 URL 查询参数获取 library
	libraryName := r.URL.Query().Get("library")
	index := server.config.FindLibraryIndex(libraryName)
	if index < 0 || index >= len(server.config.Libraries) {
		server.Error(w, fmt.Errorf("invalid library: %s", libraryName))
		return
	}

	prefix := server.config.Libraries[index].Path

	// 安全检查：清理路径并防止路径遍历
	cleanPath := filepath.Clean(path)
	if cleanPath == "." {
		cleanPath = ""
	}
	// 检查是否有路径遍历尝试
	if strings.HasPrefix(cleanPath, "..") {
		server.Error(w, fmt.Errorf("invalid path: %s", path))
		return
	}

	fullpath := filepath.Join(prefix, cleanPath)

	// 再次验证完整路径是否在 library 路径内
	if !strings.HasPrefix(fullpath, prefix) {
		server.Error(w, fmt.Errorf("access denied: %s", path))
		return
	}

	// 检查是否是文件（而不是目录）
	if info, err := os.Stat(fullpath); err == nil && !info.IsDir() {
		// 是文件，重定向到文件视图
		server.FileView(w, r, fullpath, libraryName)
		return
	}

	offset := (page - 1) * pageSize
	files, err := server.ListFiles(fullpath, offset, pageSize)
	if err != nil {
		server.Error(w, err)
		return
	}
	for i, file := range files {
		file.Path = strings.Replace(file.Path, prefix, "", 1)
		files[i] = file
	}
	server.Render(w, "list", H{
		"library": server.config.Libraries[index].Name,
		"path":    cleanPath,
		"files":   files,
		"page":    page,
		"size":    pageSize,
	})
}

func (server *FileServer) IndexView(w http.ResponseWriter, r *http.Request) {
	server.Render(w, "index", H{})
}

// FileView renders a file detail page
func (server *FileServer) FileView(w http.ResponseWriter, r *http.Request, fullpath, libraryName string) {
	file, err := server.GetFile(fullpath)
	if err != nil {
		server.Error(w, fmt.Errorf("file not found: %s", fullpath))
		return
	}

	// 计算相对路径
	library := server.config.Libraries[server.config.FindLibraryIndex(libraryName)]
	relPath := strings.TrimPrefix(file.Path, library.Path)
	if relPath == "" {
		relPath = "/"
	}

	server.Render(w, "file", H{
		"library": libraryName,
		"file":    file,
		"path":    relPath,
	})
}

func (server *FileServer) FileHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	cleanPath := filepath.Clean(path)
	http.ServeFile(w, r, cleanPath)
}
