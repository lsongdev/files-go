package media

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficeFileContainerDetection(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "document.xlsx")
	for _, data := range [][]byte{nil, {0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := officeFile(openFixture(t, name), &Meta{}); err == nil {
			t.Fatal("empty or truncated container accepted")
		}
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.Create("docProps/core.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte(`<coreProperties><title>Workbook</title><creator>Author</creator></coreProperties>`)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var m Meta
	if err = officeFile(openFixture(t, name), &m); err != nil {
		t.Fatal(err)
	}
	if m.Title != "Workbook" || m.Author != "Author" {
		t.Fatalf("metadata: %#v", m)
	}
}

func TestEPUBMetadataCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, opf, title, author string }{
		{"bare ampersand and comment", `<package><metadata><title>A & B</title><!-- invalid -- comment --><creator>Writer</creator><description><![CDATA[keep <!--literal--> text]]></description></metadata></package>`, "A & B", "Writer"},
		{"quotes in sorting hint", `<package><metadata><title opf:file-as="hechushi"jiangnan":qingchao">何处是“江南”</title><creator>杨念群</creator></metadata></package>`, "何处是“江南”", "杨念群"},
		{"markup in guide attribute", `<package><metadata><title>大数据</title><creator>涂子沛</creator></metadata><guide><reference title="<volum>序言</volum>" href="1.xhtml"/></guide></package>`, "大数据", "涂子沛"},
		{"html entity", `<package><metadata><title>A&nbsp;B</title></metadata></package>`, "A\u00a0B", ""},
		{"latin1", "<?xml version=\"1.0\" encoding=\"iso-8859-1\"?><package><metadata><title>Caf\xe9</title></metadata></package>", "Café", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			z := epubFixture(t, map[string]string{"META-INF/container.xml": `<container><rootfiles><rootfile full-path="book.opf"/></rootfiles></container>`, "book.opf": tc.opf})
			var m Meta
			if err := epub(z, &m); err != nil {
				t.Fatal(err)
			}
			if m.Title != tc.title || m.Author != tc.author {
				t.Fatalf("unexpected metadata: %#v", m)
			}
		})
	}
}

func TestEPUBCommentCleanupPreservesCDATA(t *testing.T) {
	var v struct {
		Value string `xml:"value"`
	}
	err := decodeEPUBXML([]byte(`<root><!-- invalid -- comment --><value><![CDATA[<!--literal-->]]></value></root>`), &v)
	if err != nil || v.Value != "<!--literal-->" {
		t.Fatalf("value %q, error %v", v.Value, err)
	}
}

func TestEPUBEncryptedContainer(t *testing.T) {
	z := epubFixture(t, map[string]string{"META-INF/container.xml": "\x9ewG&\xff", "META-INF/encryption.xml": `<encryption><enc:EncryptedData><enc:CipherData><enc:CipherReference URI="/META-INF/container.xml"/></enc:CipherData></enc:EncryptedData></encryption>`})
	var m Meta
	if err := epub(z, &m); err != nil {
		t.Fatal(err)
	}
	if m.Info["Protection"] == "" || m.Title != "" {
		t.Fatalf("not marked protected: %#v", m)
	}
	z = epubFixture(t, map[string]string{"META-INF/container.xml": "\x9ewG&\xff"})
	if err := epub(z, &Meta{}); err == nil {
		t.Fatal("unexplained binary data must remain an error")
	}
}

func TestEPUBMissingContainer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     map[string]string
		wantError bool
	}{
		{"single package", map[string]string{
			"OPS/content.opf": `<package><metadata><title>Book</title><creator>Author</creator><meta name="cover" content="cover"/></metadata><manifest><item id="cover" href="images/cover.jpg"/></manifest></package>`,
		}, false},
		{"no package", map[string]string{"book.html": "<html>Not EPUB</html>"}, true},
		{"ambiguous packages", map[string]string{"a.opf": `<package/>`, "b.opf": `<package/>`}, true},
		{"unrelated XML", map[string]string{"content.opf": `<html/>`}, true},
		{"damaged container", map[string]string{"META-INF/container.xml": "broken", "content.opf": `<package/>`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m Meta
			err := epub(epubFixture(t, tc.files), &m)
			if (err != nil) != tc.wantError {
				t.Fatalf("error: %v", err)
			}
			if !tc.wantError && (m.Title != "Book" || m.Author != "Author" || m.Poster != "zip:OPS/images/cover.jpg") {
				t.Fatalf("metadata: %#v", m)
			}
		})
	}
}

func TestEPUBAttributeRepairScope(t *testing.T) {
	var v struct {
		Value string `xml:"value"`
	}
	data := []byte(`<root><value><![CDATA[<x title="<raw>" opf:file-as="a"b"/>]]></value></root>`)
	if err := decodeEPUBXML(data, &v); err != nil || v.Value != `<x title="<raw>" opf:file-as="a"b"/>` {
		t.Fatalf("CDATA: %q, error: %v", v.Value, err)
	}
	if err := decodeEPUBXML([]byte(`<root bad="a"b"><value>Text</value></root>`), &v); err == nil {
		t.Fatal("unrelated malformed attributes must remain errors")
	}
}

func epubFixture(t *testing.T, files map[string]string) *zip.Reader {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return z
}
