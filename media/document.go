package media

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/store"
)

// Document reads titles, authors and covers from e-books and office files.
type Document struct {
	Path    Resolver
	PDFInfo string // optional pdfinfo binary; PDFs get no metadata without it
}

func (Document) Match(f *store.File) bool {
	switch Ext(f.Name) {
	case "epub", "mobi", "azw", "azw3", "docx", "xlsx", "pptx", "pdf":
		return true
	}
	return false
}

func (d Document) Enrich(ctx context.Context, f *store.File, m *Meta) error {
	name, err := d.Path(*f)
	if err != nil {
		return err
	}
	switch ext := Ext(f.Name); ext {
	case "epub":
		m.Kind = "book"
		return withZip(name, func(z *zip.Reader) error { return epub(z, m) })
	case "mobi", "azw", "azw3":
		m.Kind = "book"
		return mobi(name, m)
	case "pdf":
		m.Kind = "document"
		return d.pdf(ctx, name, m)
	default:
		m.Kind = "document"
		return withZip(name, func(z *zip.Reader) error { return office(z, m) })
	}
}

func withZip(name string, fn func(*zip.Reader) error) error {
	z, err := zip.OpenReader(name)
	if err != nil {
		return err
	}
	defer z.Close()
	return fn(&z.Reader)
}

func readZip(z *zip.Reader, name string, limit int64) ([]byte, error) {
	for _, f := range z.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(io.LimitReader(r, limit))
		}
	}
	return nil, fmt.Errorf("%s: not found in archive", name)
}

func epub(z *zip.Reader, m *Meta) error {
	data, err := readZip(z, "META-INF/container.xml", 1<<20)
	if err != nil {
		return err
	}
	var container struct {
		Rootfile struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(data, &container); err != nil {
		return fmt.Errorf("epub container: %w", err)
	}
	if data, err = readZip(z, container.Rootfile.Path, 4<<20); err != nil {
		return err
	}
	var opf struct {
		Title       []string `xml:"metadata>title"`
		Creator     []string `xml:"metadata>creator"`
		Publisher   string   `xml:"metadata>publisher"`
		Language    string   `xml:"metadata>language"`
		Date        string   `xml:"metadata>date"`
		Description string   `xml:"metadata>description"`
		Subject     []string `xml:"metadata>subject"`
		Meta        []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"metadata>meta"`
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
	}
	if err := xml.Unmarshal(data, &opf); err != nil {
		return fmt.Errorf("epub package: %w", err)
	}
	if len(opf.Title) > 0 {
		m.Title = strings.TrimSpace(opf.Title[0])
	}
	m.Author = strings.Join(nonEmpty(trimAll(opf.Creator)...), ", ")
	m.Summary = stripTags(opf.Description)
	m.Year = leadingYear(opf.Date)
	m.Genres = nonEmpty(trimAll(opf.Subject)...)
	m.Set("Publisher", opf.Publisher)
	m.Set("Language", opf.Language)
	coverID := ""
	for _, meta := range opf.Meta {
		if meta.Name == "cover" {
			coverID = meta.Content
		}
	}
	for _, item := range opf.Items {
		if item.ID == coverID || strings.Contains(item.Properties, "cover-image") {
			m.Poster = "zip:" + path.Join(path.Dir(container.Rootfile.Path), item.Href)
			break
		}
	}
	return nil
}

func office(z *zip.Reader, m *Meta) error {
	data, err := readZip(z, "docProps/core.xml", 1<<20)
	if err != nil {
		return nil // not every office file has core properties
	}
	var core struct {
		Title    string `xml:"title"`
		Creator  string `xml:"creator"`
		Subject  string `xml:"subject"`
		Modified string `xml:"modified"`
	}
	if err := xml.Unmarshal(data, &core); err != nil {
		return nil
	}
	m.Title, m.Author = strings.TrimSpace(core.Title), strings.TrimSpace(core.Creator)
	m.Set("Subject", core.Subject)
	if data, err := readZip(z, "docProps/app.xml", 1<<20); err == nil {
		var app struct {
			Pages, Slides string
			Application   string
		}
		if xml.Unmarshal(data, &app) == nil {
			m.Set("Pages", app.Pages)
			m.Set("Slides", app.Slides)
			m.Set("Application", app.Application)
		}
	}
	return nil
}

// mobi reads the PalmDB/MOBI header and its EXTH title and author records.
func mobi(name string, m *Meta) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	head := make([]byte, 78+8)
	if _, err := io.ReadFull(file, head); err != nil {
		return err
	}
	if string(head[60:68]) != "BOOKMOBI" {
		return errors.New("not a MOBI file")
	}
	offset := int64(binary.BigEndian.Uint32(head[78:82]))
	record := make([]byte, 64<<10)
	n, _ := file.ReadAt(record, offset)
	record = record[:n]
	if len(record) < 132 || string(record[16:20]) != "MOBI" {
		return errors.New("missing MOBI header")
	}
	headerLength := int(binary.BigEndian.Uint32(record[20:24]))
	nameOffset, nameLength := int(binary.BigEndian.Uint32(record[84:88])), int(binary.BigEndian.Uint32(record[88:92]))
	if nameOffset+nameLength <= len(record) {
		m.Title = strings.TrimSpace(string(record[nameOffset : nameOffset+nameLength]))
	}
	exth := record[min(16+headerLength, len(record)):]
	if binary.BigEndian.Uint32(record[128:132])&0x40 == 0 || len(exth) < 12 || string(exth[:4]) != "EXTH" {
		return nil
	}
	count, pos := int(binary.BigEndian.Uint32(exth[8:12])), 12
	for i := 0; i < count && pos+8 <= len(exth); i++ {
		kind, size := binary.BigEndian.Uint32(exth[pos:]), int(binary.BigEndian.Uint32(exth[pos+4:]))
		if size < 8 || pos+size > len(exth) {
			break
		}
		value := strings.TrimSpace(string(exth[pos+8 : pos+size]))
		switch kind {
		case 100:
			m.Author = value
		case 101:
			m.Set("Publisher", value)
		case 103:
			m.Summary = stripTags(value)
		case 106:
			m.Year = leadingYear(value)
		case 503:
			m.Title = value
		}
		pos += size
	}
	return nil
}

func (d Document) pdf(ctx context.Context, name string, m *Meta) error {
	if d.PDFInfo == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, d.PDFInfo, "-enc", "UTF-8", name).Output()
	if err != nil {
		return fmt.Errorf("pdfinfo: %w", err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		key, value, ok := strings.Cut(string(line), ":")
		if !ok {
			continue
		}
		switch value = strings.TrimSpace(value); key {
		case "Title":
			m.Title = value
		case "Author":
			m.Author = value
		case "Pages":
			if n, err := strconv.Atoi(value); err == nil {
				m.Set("Pages", strconv.Itoa(n))
			}
		case "Creator", "Producer":
			m.Set(key, value)
		}
	}
	m.Poster = "self"
	return nil
}

func trimAll(values []string) []string {
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values
}

// stripTags turns an HTML description into plain text.
func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
