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
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lsongdev/files-go/pkg/source"
	"github.com/lsongdev/files-go/store"
	"github.com/richardlehane/mscfb"
	"golang.org/x/net/html/charset"
)

// Document reads titles, authors and covers from e-books and office files.
type Document struct {
	Open    Opener
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
	input, err := d.Open(ctx, *f)
	if err != nil {
		return err
	}
	defer input.Close()
	switch ext := Ext(f.Name); ext {
	case "epub":
		m.Kind = "book"
		return withZip(input, func(z *zip.Reader) error { return epub(z, m) })
	case "mobi", "azw", "azw3":
		m.Kind = "book"
		return mobi(input, m)
	case "pdf":
		m.Kind = "document"
		return d.pdf(ctx, input, m)
	default:
		m.Kind = "document"
		return officeFile(input, m)
	}
}

// Some libraries contain legacy DOC/XLS data with DOCX/XLSX suffixes.
// Detect the actual container before selecting a metadata reader.
func officeFile(f *os.File, m *Meta) error {
	var header [8]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return err
	}
	if !bytes.Equal(header[:], []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}) {
		return withZip(f, func(z *zip.Reader) error { return office(z, m) })
	}
	doc, err := mscfb.New(f)
	if err != nil {
		return fmt.Errorf("office compound container: %w", err)
	}
	format := ""
	protected := false
	for {
		entry, err := doc.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("office compound directory: %w", err)
		}
		switch entry.Name {
		case "WordDocument":
			format = "Legacy Word (DOC)"
		case "Workbook", "Book":
			format = "Legacy Excel (XLS)"
		case "PowerPoint Document":
			format = "Legacy PowerPoint (PPT)"
		case "EncryptedPackage":
			protected = true
		}
	}
	if protected {
		m.Set("Protection", "Office document is encrypted")
		return nil
	}
	if format == "" {
		return errors.New("office compound container: no recognized document stream")
	}
	m.Set("Format", format)
	return nil
}

func withZip(f *os.File, fn func(*zip.Reader) error) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return err
	}
	return fn(z)
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
	encrypted := epubEncryptedEntries(z)
	if encrypted["META-INF/container.xml"] {
		m.Set("Protection", "EPUB metadata is encrypted")
		return nil
	}
	packagePath, err := epubPackagePath(z)
	if err != nil {
		return err
	}
	if encrypted[packagePath] {
		m.Set("Protection", "EPUB metadata is encrypted")
		return nil
	}
	data, err := readZip(z, packagePath, 4<<20)
	if err != nil {
		return err
	}
	var opf struct {
		XMLName     xml.Name `xml:"package"`
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
	if err := decodeEPUBXML(data, &opf); err != nil {
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
			m.Poster = "zip:" + path.Join(path.Dir(packagePath), item.Href)
			break
		}
	}
	return nil
}

// Some converted books omit the OCF entry point but retain a single OPF.
// Only use that fallback when container.xml is absent; never mask a damaged
// container or guess between multiple packages.
func epubPackagePath(z *zip.Reader) (string, error) {
	hasContainer := false
	for _, f := range z.File {
		if f.Name == "META-INF/container.xml" {
			hasContainer = true
			break
		}
	}
	if !hasContainer {
		candidate := ""
		for _, f := range z.File {
			if f.FileInfo().IsDir() || !strings.EqualFold(path.Ext(f.Name), ".opf") {
				continue
			}
			if candidate != "" {
				return "", errors.New("epub container: missing container.xml and multiple OPF packages")
			}
			candidate = f.Name
		}
		if candidate == "" {
			return "", errors.New("epub container: missing container.xml and OPF package")
		}
		return candidate, nil
	}
	data, err := readZip(z, "META-INF/container.xml", 1<<20)
	if err != nil {
		return "", err
	}
	var container struct {
		Rootfile struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decodeEPUBXML(data, &container); err != nil {
		return "", fmt.Errorf("epub container: %w", err)
	}
	if container.Rootfile.Path == "" {
		return "", errors.New("epub container: missing rootfile path")
	}
	return container.Rootfile.Path, nil
}

// Only EPUB metadata uses this tolerance. Binary ciphertext and invalid
// encodings remain errors; external entities are never fetched by encoding/xml.
func decodeEPUBXML(data []byte, target any) error {
	clean, err := epubCleanXML(data)
	if err != nil {
		return err
	}
	decoder := xml.NewDecoder(bytes.NewReader(clean))
	decoder.CharsetReader = charset.NewReaderLabel
	decoder.Strict = false
	decoder.Entity = xml.HTMLEntity
	return decoder.Decode(target)
}

// Strip malformed comments and repair attributes, preserving text and CDATA.
func epubCleanXML(data []byte) ([]byte, error) {
	var out bytes.Buffer
	for len(data) > 0 {
		i := bytes.IndexByte(data, '<')
		if i < 0 {
			out.Write(data)
			break
		}
		out.Write(data[:i])
		data = data[i:]
		if bytes.HasPrefix(data, []byte("<!--")) {
			end := bytes.Index(data[4:], []byte("-->"))
			if end < 0 {
				return nil, errors.New("epub XML: unterminated comment")
			}
			data = data[4+end+3:]
			continue
		}
		if bytes.HasPrefix(data, []byte("<![CDATA[")) {
			end := bytes.Index(data[9:], []byte("]]>"))
			if end < 0 {
				return nil, errors.New("epub XML: unterminated CDATA")
			}
			out.Write(data[:9+end+3])
			data = data[9+end+3:]
			continue
		}
		quote := byte(0)
		end := 1
		for ; end < len(data); end++ {
			c := data[end]
			if quote != 0 {
				if c == quote {
					quote = 0
				}
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
			if c == '>' {
				end++
				break
			}
		}
		out.Write(epubRepairAttributes(data[:end]))
		data = data[end:]
	}
	return out.Bytes(), nil
}

// Repair two common conversion defects without interpreting markup in text
// or CDATA: literal < in attributes, and embedded quotes in the optional OPF
// sorting hint. Other malformed attribute syntax remains an error.
func epubRepairAttributes(tag []byte) []byte {
	var out bytes.Buffer
	quote := byte(0)
	fileAs := false
	for i, c := range tag {
		if quote == 0 {
			if c == '\'' || c == '"' {
				quote = c
				prefix := strings.TrimSpace(string(tag[:i]))
				fileAs = strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(prefix, "=")), "opf:file-as")
			}
		} else {
			if c == '<' {
				out.WriteString("&lt;")
				continue
			}
			if c == quote {
				if fileAs && i+1 < len(tag) && !strings.ContainsRune(" \t\r\n/>", rune(tag[i+1])) {
					if quote == '"' {
						out.WriteString("&quot;")
					} else {
						out.WriteString("&apos;")
					}
					continue
				}
				quote = 0
			}
		}
		out.WriteByte(c)
	}
	return out.Bytes()
}

func epubEncryptedEntries(z *zip.Reader) map[string]bool {
	result := map[string]bool{}
	data, err := readZip(z, "META-INF/encryption.xml", 1<<20)
	if err != nil {
		return result
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = false
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		if tag, ok := token.(xml.StartElement); ok && tag.Name.Local == "CipherReference" {
			for _, attr := range tag.Attr {
				if attr.Name.Local == "URI" {
					result[strings.TrimPrefix(path.Clean(attr.Value), "/")] = true
				}
			}
		}
	}
	return result
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
func mobi(file *os.File, m *Meta) error {
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

func (d Document) pdf(ctx context.Context, input *os.File, m *Meta) error {
	if d.PDFInfo == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := source.Output(source.Command(ctx, d.PDFInfo, input, "-enc", "UTF-8", source.Input), 1<<20)
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
