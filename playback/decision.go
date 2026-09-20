package playback

import (
	"strings"

	"github.com/lsongdev/files-go/model"
)

type Mode string

const (
	ModeDirect    Mode = "direct"
	ModeRemux     Mode = "remux"
	ModeTranscode Mode = "transcode"
)

type Capabilities struct {
	Containers  []string `json:"containers"`
	VideoCodecs []string `json:"videoCodecs"`
	AudioCodecs []string `json:"audioCodecs"`
	HLS         bool     `json:"hls"`
}

func Decide(file model.ParsedMedia, capabilities Capabilities) Mode {
	if supportsContainer(capabilities.Containers, file.Container) && supports(capabilities.VideoCodecs, file.VideoCodec) && supports(capabilities.AudioCodecs, file.AudioCodec) {
		return ModeDirect
	}
	if capabilities.HLS && supports(capabilities.VideoCodecs, file.VideoCodec) && supports(capabilities.AudioCodecs, file.AudioCodec) {
		return ModeRemux
	}
	return ModeTranscode
}

func supportsContainer(values []string, formats string) bool {
	for _, format := range strings.Split(formats, ",") {
		format = strings.TrimSpace(format)
		for _, alias := range containerAliases(format) {
			if supports(values, alias) {
				return true
			}
		}
	}
	return formats == ""
}

func containerAliases(value string) []string {
	switch strings.ToLower(value) {
	case "mov", "m4a", "3gp", "3g2", "mj2":
		return []string{value, "mp4"}
	case "matroska":
		return []string{value, "mkv"}
	default:
		return []string{value}
	}
}

func supports(values []string, value string) bool {
	if value == "" {
		return true
	}
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}
