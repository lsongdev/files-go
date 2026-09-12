package processor

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lsongdev/files-go/catalog"
	"github.com/lsongdev/files-go/database"
	"github.com/lsongdev/files-go/model"
	"github.com/lsongdev/files-go/storage"
)

func mediaFixture(t *testing.T, name string, data []byte) (*catalog.Catalog, *storage.Registry, model.Entry) {
	t.Helper()
	ctx := context.Background()
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, name), data, 0644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cat := catalog.New(db)
	if err := cat.RegisterStorage(ctx, "disk", "Disk", "local"); err != nil {
		t.Fatal(err)
	}
	generation, err := cat.BeginScan(ctx, "disk")
	if err != nil {
		t.Fatal(err)
	}
	root, err := cat.EnsureRoot(ctx, "disk", generation)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(rootPath, name))
	if err != nil {
		t.Fatal(err)
	}
	parentID := root.ID
	entries, err := cat.UpsertEntries(ctx, []model.Entry{{
		StorageID: "disk", ParentID: &parentID, Name: name, Path: name, Type: model.EntryFile,
		Size: info.Size(), ModifiedAt: info.ModTime(), Extension: filepath.Ext(name)[1:],
	}}, generation)
	if err != nil {
		t.Fatal(err)
	}
	local, err := storage.NewLocal(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	registry := storage.NewRegistry()
	if err := registry.Add("disk", local); err != nil {
		t.Fatal(err)
	}
	return cat, registry, entries[0]
}

func TestImageMetadataReadsDimensions(t *testing.T) {
	imageValue := image.NewRGBA(image.Rect(0, 0, 800, 600))
	imageValue.Set(1, 1, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageValue); err != nil {
		t.Fatal(err)
	}
	cat, registry, entry := mediaFixture(t, "photo.png", encoded.Bytes())
	processor := NewImageMetadata(cat, registry)
	if !processor.Match(entry) {
		t.Fatal("PNG did not match image processor")
	}
	if err := processor.Process(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	media, err := cat.MediaFile(context.Background(), entry.ID)
	if err != nil || media.Kind != "photo" || media.Width == nil || *media.Width != 800 || media.Height == nil || *media.Height != 600 {
		t.Fatalf("image metadata = %#v, %v", media, err)
	}
	cacheDir := t.TempDir()
	thumbnail := NewThumbnail(cat, registry, cacheDir)
	if err := thumbnail.Process(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	for variant, maximum := range thumbnailVariants {
		artifact, err := cat.ArtifactForEntry(context.Background(), entry.ID, "thumbnail", variant)
		if err != nil {
			t.Fatalf("%s artifact: %v", variant, err)
		}
		filename, err := ThumbnailPath(cacheDir, artifact.Key)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(filename)
		if err != nil {
			t.Fatal(err)
		}
		config, format, decodeErr := image.DecodeConfig(file)
		_ = file.Close()
		if decodeErr != nil || format != "jpeg" || config.Width > maximum || config.Height > maximum {
			t.Fatalf("%s thumbnail = %dx%d %s, %v", variant, config.Width, config.Height, format, decodeErr)
		}
	}
	if _, err := ThumbnailPath(cacheDir, "../escape"); err == nil {
		t.Fatal("invalid thumbnail key was accepted")
	}
}

func TestFFProbeParsesAndProbesAudio(t *testing.T) {
	parsed, err := parseFFProbe("entry", []byte(`{
		"streams":[{"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"codec_type":"audio","codec_name":"aac"}],
		"format":{"format_name":"matroska,webm","duration":"12.345","bit_rate":"8000000"}
	}`))
	if err != nil || parsed.Kind != "video" || parsed.VideoCodec != "h264" || parsed.AudioCodec != "aac" || parsed.DurationMS == nil || *parsed.DurationMS != 12345 {
		t.Fatalf("parsed ffprobe = %#v, %v", parsed, err)
	}

	binaryPath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	cat, registry, entry := mediaFixture(t, "tone.wav", wavSilence(8000, 800))
	processor := NewFFProbe(cat, registry, binaryPath, 5*time.Second)
	if err := processor.Process(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	media, err := cat.MediaFile(context.Background(), entry.ID)
	if err != nil || media.Kind != "audio" || media.AudioCodec != "pcm_s16le" || media.DurationMS == nil || *media.DurationMS != 100 {
		t.Fatalf("probed audio = %#v, %v", media, err)
	}
}

func TestFFProbeSkipsTypeScriptFilesWithTSExtension(t *testing.T) {
	cat, registry, entry := mediaFixture(t, "source.ts", []byte("export const answer = 42;\n"))
	probe := NewFFProbe(cat, registry, "/missing/ffprobe", time.Second)
	if !probe.Match(entry) {
		t.Fatal("plain .ts entry should reach content sniffing")
	}
	if err := probe.Process(context.Background(), entry); err != nil {
		t.Fatalf("TypeScript source should be skipped without invoking ffprobe: %v", err)
	}
	_, _, declaration := mediaFixture(t, "types.d.ts", []byte("export declare const answer: number;\n"))
	if probe.Match(declaration) {
		t.Fatal("TypeScript declaration was classified as media")
	}
}

func TestVideoThumbnailGeneratesCachedVariants(t *testing.T) {
	ctx := context.Background()
	cat, registry, entry := mediaFixture(t, "movie.mp4", []byte("video fixture"))
	duration := int64(100_000)
	if err := cat.UpsertMediaFile(ctx, model.MediaFile{EntryID: entry.ID, Kind: "video", DurationMS: &duration, Metadata: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 320, 180))
	frame.Set(10, 10, color.RGBA{R: 220, G: 80, B: 40, A: 255})
	fixtureDir := t.TempDir()
	framePath := filepath.Join(fixtureDir, "frame.png")
	frameFile, err := os.Create(framePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(frameFile, frame); err != nil {
		t.Fatal(err)
	}
	if err := frameFile.Close(); err != nil {
		t.Fatal(err)
	}
	fakeFFmpeg := filepath.Join(fixtureDir, "ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\ncat '"+framePath+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	thumbnailer := NewThumbnail(cat, registry, t.TempDir())
	processor := NewVideoThumbnail(cat, registry, thumbnailer, fakeFFmpeg, time.Second)
	if !processor.Match(entry) {
		t.Fatal("MP4 did not match video thumbnail processor")
	}
	if err := processor.Process(ctx, entry); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"small", "medium", "large"} {
		if _, err := cat.ArtifactForEntry(ctx, entry.ID, "thumbnail", variant); err != nil {
			t.Fatalf("%s video thumbnail: %v", variant, err)
		}
	}
}

func TestFFProbeNormalizesMusicTags(t *testing.T) {
	parsed, err := parseFFProbe("entry", []byte(`{
		"streams":[{"codec_type":"audio","codec_name":"flac","tags":{"artist":"Portishead"}}, {"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],
		"format":{"format_name":"flac","duration":"120","bit_rate":"900000","tags":{"TITLE":"Roads","ALBUM":"Dummy","track":"5/11"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Music map[string]any `json:"music"`
	}
	if err := json.Unmarshal(parsed.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Music["title"] != "Roads" || metadata.Music["artist"] != "Portishead" || metadata.Music["hasAlbumArt"] != true {
		t.Fatalf("music metadata = %#v", metadata.Music)
	}
}

func TestEPUBMetadataReadsPackage(t *testing.T) {
	var encoded bytes.Buffer
	archive := zip.NewWriter(&encoded)
	writeArchiveFile(t, archive, "META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`)
	writeArchiveFile(t, archive, "OPS/book.opf", `<?xml version="1.0"?><package><metadata><title>The Left Hand of Darkness</title><creator>Ursula K. Le Guin</creator><language>en</language><publisher>Ace</publisher><identifier>book-1</identifier><meta name="cover" content="cover-image"/></metadata><manifest><item id="cover-image" href="images/cover.jpg" media-type="image/jpeg"/></manifest></package>`)
	coverImage := image.NewRGBA(image.Rect(0, 0, 120, 180))
	coverImage.Set(60, 90, color.RGBA{R: 80, G: 120, B: 200, A: 255})
	var cover bytes.Buffer
	if err := png.Encode(&cover, coverImage); err != nil {
		t.Fatal(err)
	}
	writeArchiveBytes(t, archive, "OPS/images/cover.jpg", cover.Bytes())
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	cat, registry, entry := mediaFixture(t, "book.epub", encoded.Bytes())
	if err := NewEPUBMetadata(cat, registry).Process(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	media, err := cat.MediaFile(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Title   string   `json:"title"`
		Authors []string `json:"authors"`
		Cover   struct {
			Path string `json:"path"`
		} `json:"cover"`
	}
	if err := json.Unmarshal(media.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if media.Kind != "book" || metadata.Title != "The Left Hand of Darkness" || len(metadata.Authors) != 1 || metadata.Cover.Path != "OPS/images/cover.jpg" {
		t.Fatalf("EPUB metadata = %#v, media = %#v", metadata, media)
	}
	cacheDir := t.TempDir()
	if err := NewThumbnail(cat, registry, cacheDir).Process(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	artifact, err := cat.ArtifactForEntry(context.Background(), entry.ID, "thumbnail", "medium")
	if err != nil || artifact.MIME != "image/jpeg" {
		t.Fatalf("EPUB cover thumbnail = %#v, %v", artifact, err)
	}
}

func TestResolveEPUBResourceDecodesEscapedPaths(t *testing.T) {
	resolved, err := resolveEPUBResource("OEBPS/book.opf", "Images/%E5%B0%81%E9%9D%A2.jpg#cover")
	if err != nil || resolved != "OEBPS/Images/封面.jpg" {
		t.Fatalf("resolved EPUB cover = %q, %v", resolved, err)
	}
	if _, err := resolveEPUBResource("OEBPS/book.opf", "https://example.com/cover.jpg"); err == nil {
		t.Fatal("external EPUB cover was accepted")
	}
}

func TestPDFInfoMetadata(t *testing.T) {
	metadata := parsePDFInfo("Title: A Book\nAuthor: Ada\nPages: 321\nPDF version: 1.7\n")
	if metadata["title"] != "A Book" || metadata["author"] != "Ada" || metadata["pageCount"] != 321 || metadata["pdfVersion"] != "1.7" {
		t.Fatalf("PDF metadata = %#v", metadata)
	}
}

func TestPDFThumbnailGeneratesCachedVariants(t *testing.T) {
	ctx := context.Background()
	cat, registry, entry := mediaFixture(t, "book.pdf", []byte("PDF fixture"))
	if err := cat.UpsertMediaFile(ctx, model.MediaFile{EntryID: entry.ID, Kind: "book", Container: "pdf", Metadata: json.RawMessage(`{"pageCount":1}`)}); err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 240, 320))
	frame.Set(20, 20, color.RGBA{R: 240, G: 210, B: 80, A: 255})
	fixtureDir := t.TempDir()
	framePath := filepath.Join(fixtureDir, "page.jpg")
	frameFile, err := os.Create(framePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(frameFile, frame); err != nil {
		t.Fatal(err)
	}
	if err := frameFile.Close(); err != nil {
		t.Fatal(err)
	}
	fakePDFToPPM := filepath.Join(fixtureDir, "pdftoppm")
	script := "#!/bin/sh\nfor last; do :; done\ncp '" + framePath + "' \"$last.jpg\"\n"
	if err := os.WriteFile(fakePDFToPPM, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	thumbnailer := NewThumbnail(cat, registry, t.TempDir())
	processor := NewPDFThumbnail(cat, registry, thumbnailer, t.TempDir(), fakePDFToPPM, time.Second)
	if !processor.Match(entry) {
		t.Fatal("PDF did not match PDF thumbnail processor")
	}
	if err := processor.Process(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ArtifactForEntry(ctx, entry.ID, "thumbnail", "medium"); err != nil {
		t.Fatalf("PDF thumbnail: %v", err)
	}
}

func writeArchiveFile(t *testing.T, archive *zip.Writer, name, value string) {
	writeArchiveBytes(t, archive, name, []byte(value))
}

func writeArchiveBytes(t *testing.T, archive *zip.Writer, name string, value []byte) {
	t.Helper()
	file, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(value); err != nil {
		t.Fatal(err)
	}
}

func TestLimitedBufferEnforcesBound(t *testing.T) {
	buffer := &limitedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("abcdef")); written != 4 || err != ErrProcessOutputTooLarge || buffer.String() != "abcd" || !buffer.exceeded {
		t.Fatalf("limited write = %d, %v, %q, %v", written, err, buffer.String(), buffer.exceeded)
	}
}

func wavSilence(sampleRate, samples int) []byte {
	dataSize := samples * 2
	buffer := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+dataSize))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(2))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(16))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(dataSize))
	buffer.Write(make([]byte, dataSize))
	return buffer.Bytes()
}
