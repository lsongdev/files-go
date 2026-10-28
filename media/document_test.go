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
		if err := officeFile(name, &Meta{}); err == nil {
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
	if err = officeFile(name, &m); err != nil {
		t.Fatal(err)
	}
	if m.Title != "Workbook" || m.Author != "Author" {
		t.Fatalf("metadata: %#v", m)
	}
}

func TestEPUBMetadataCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, opf, title, author string }{
		{"bare ampersand and comment", `<package><metadata><title>A & B</title><!-- invalid -- comment --><creator>Writer</creator><description><![CDATA[keep <!--literal--> text]]></description></metadata></package>`, "A & B", "Writer"},
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
