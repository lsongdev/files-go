package ffmpeg

import (
	"context"
	"github.com/lsongdev/files-go/pkg/source"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestManifestDisguisedAsMediaIsRejected(t *testing.T) {
	if _, e := exec.LookPath("ffprobe"); e != nil {
		t.Skip(e)
	}
	p := filepath.Join(t.TempDir(), "movie.mp4")
	os.WriteFile(p, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nhttp://127.0.0.1:1/private.ts\n#EXT-X-ENDLIST\n"), 0600)
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := source.Output(Command(ctx, "ffprobe", f, "-v", "error", "-show_format", "-of", "json", source.Input), 1<<20); e == nil {
		t.Fatal("manifest accepted as a media file")
	}
}

func TestLocalManifestCannotReadAnUnrelatedMediaFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor-based subprocesses require Unix")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip(e)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.ts")
	if out, e := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=black:s=16x16:r=25:d=1", "-c:v", "mpeg2video", "-f", "mpegts", outside).CombinedOutput(); e != nil {
		t.Fatalf("fixture: %s %v", out, e)
	}
	root := t.TempDir()
	p := filepath.Join(root, "movie.mp4")
	os.WriteFile(p, []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1,\n"+filepath.ToSlash(outside)+"\n#EXT-X-ENDLIST\n"), 0600)
	rooted, e := source.OpenRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer rooted.Close()
	f, e := rooted.Open("movie.mp4")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Force the parser in the control so this exercises format policy rather
	// than relying on automatic format-detection heuristics.
	args := []string{"-f", "hls", "-v", "error", "-show_format", "-of", "json", source.Input}
	if _, e := source.Output(source.Command(ctx, "ffprobe", f, args...), 1<<20); e != nil {
		t.Fatalf("control must show libavformat follows the local manifest: %v", e)
	}
	if _, e := source.Output(Command(ctx, "ffprobe", f, args...), 1<<20); e == nil {
		t.Fatal("manifest read media outside the rooted input")
	}
}
