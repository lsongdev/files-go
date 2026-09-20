package playback

import (
	"github.com/lsongdev/files-go/model"
	"testing"
)

func TestDecidePlaybackMode(t *testing.T) {
	caps := Capabilities{Containers: []string{"mov", "mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLS: true}
	if mode := Decide(model.ParsedMedia{Container: "mov,mp4", VideoCodec: "h264", AudioCodec: "aac"}, caps); mode != ModeDirect {
		t.Fatal(mode)
	}
	if mode := Decide(model.ParsedMedia{Container: "matroska,webm", VideoCodec: "h264", AudioCodec: "aac"}, caps); mode != ModeRemux {
		t.Fatal(mode)
	}
	if mode := Decide(model.ParsedMedia{Container: "matroska", VideoCodec: "hevc", AudioCodec: "dts"}, caps); mode != ModeTranscode {
		t.Fatal(mode)
	}
}
