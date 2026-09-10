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
		})
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

func TestPreviewAssetsIncludeNativeAndTextPreviews(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/text`", "aria-modal", "<video", "<audio", "<iframe", "关闭预览"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}

func TestFileManagementAssetsIncludeCreateRenameAndDelete(t *testing.T) {
	response := httptest.NewRecorder()
	Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	body := response.Body.String()
	for _, expected := range []string{"/directories", "/files`", "method: 'PATCH'", "method: 'DELETE'", "新建文件夹", "管理条目", "正在上传", "移动到这里"} {
		if response.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("app.js does not contain %q", expected)
		}
	}
}
