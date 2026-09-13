package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesAssetsAndHistoryFallback(t *testing.T) {
	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: "type=\"module\""},
		{path: "/files/0199-example", contentType: "text/html", contains: `<div id="app">`},
		{path: "/app.js", contentType: "text/javascript", contains: "standalone.module.js"},
		{path: "/styles.css", contentType: "text/css", contains: "--blue:"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			if contentType := response.Header().Get("Content-Type"); !strings.HasPrefix(contentType, test.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", contentType, test.contentType)
			}
			if !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("response does not contain %q", test.contains)
			}
			if response.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("Content-Security-Policy is missing")
			}
			if cacheControl := response.Header().Get("Cache-Control"); cacheControl == "" {
				t.Fatal("Cache-Control is missing")
			}
		})
	}
}

func TestHTMLShellIsNotCachedAcrossServerUpgrades(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files/example", nil))
	if value := response.Header().Get("Cache-Control"); value != "no-store" {
		t.Fatalf("history fallback Cache-Control = %q, want no-store", value)
	}
	body := response.Body.String()
	if strings.Contains(body, "{{ASSET_VERSION}}") || !strings.Contains(body, "/app.js?v=") || !strings.Contains(body, "/styles.css?v=") {
		t.Fatalf("HTML shell does not contain resolved versioned asset URLs: %q", body)
	}
	response = httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if value := response.Header().Get("Cache-Control"); value != "no-cache, must-revalidate" {
		t.Fatalf("app.js Cache-Control = %q", value)
	}
}

func TestThemeAssetsIncludeLightModeAndPersistentToggle(t *testing.T) {
	checks := map[string]string{
		"/app.js":     "files-go-theme",
		"/styles.css": `:root[data-theme="light"]`,
	}
	for target, expected := range checks {
		response := httptest.NewRecorder()
		Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("%s does not contain %q", target, expected)
		}
	}
}

func TestMobileToolbarKeepsViewSwitcherAndMovesThemeToSidebar(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{`class="sidebar-theme-toggle"`, `aria-label="显示方式"`, `aria-label="列表"`, `aria-label="缩略图"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
	if strings.Contains(body, `onClick=${refresh} aria-label="刷新"`) {
		t.Fatal("top toolbar still contains the ambiguous refresh button")
	}
	response = httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/styles.css", nil))
	if strings.Contains(response.Body.String(), `.view-switch { display: none; }`) {
		t.Fatal("mobile CSS still hides the view switcher")
	}
}

func TestSearchAssetsIncludeSearchAPIAndAccessibleControl(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/search?", `role="search"`, "搜索当前资料库"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}

func TestFileDetailAssetsIncludeNativeAndTextPreviews(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/text`", "FileDetail", "file-detail", "detail-preview", "<video", "<audio", "<iframe"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
	for _, removed := range []string{"function Preview", "preview-dialog", "关闭预览"} {
		if strings.Contains(body, removed) {
			t.Fatalf("app.js still contains modal preview marker %q", removed)
		}
	}
}

func TestMediaDetailAssetsIncludeFileAndDirectoryEnhancement(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"MediaHeader", "media-header", "/media-item", "/media-candidates", "MatchDialog", "纠正匹配", "移除当前匹配", "mediaTypeLabel(item.media.type)", "FailureDialog", "/system/failures", "current.type === 'directory'", "entry?.type === 'file'"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}

func TestPlaybackAssetsUseCSPCompatibleAbsoluteModules(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"https://unpkg.com/hls.js@1.6.13/dist/hls.mjs", "/playback/", "updatePlaybackProgress"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
	if strings.Contains(body, `from 'preact'`) {
		t.Fatal("app.js contains a bare Preact module specifier")
	}
}

func TestScanStatusUsesSingleGlobalActivityAndPhysicalDirectoryBrowsing(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/system/status", "后台活动", "正在扫描文件", "activity-progress", "整体扫描进度", "个正在扫描 · 可浏览", "媒体增强"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
	for _, removed := range []string{"media catalog", "loadMediaLibrary", "browseMode === 'media'", "library-progress", "storageSubtitle"} {
		if strings.Contains(body, removed) {
			t.Fatalf("app.js still contains separate media catalog marker %q", removed)
		}
	}
}

func TestFileListAutomaticallyLoadsMoreWithButtonFallback(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"IntersectionObserver", "loadMoreSentinelRef", "加载更多"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}

func TestFileManagementAssetsIncludeCreateRenameAndDelete(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/directories", "/files`", "/copies", "method: 'PATCH'", "method: 'DELETE'", "新建文件夹", "管理条目", "正在上传", "移动到这里", "复制到这里"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}
