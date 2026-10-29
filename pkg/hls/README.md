# hls

A context-aware, on-demand HLS VOD backend using `ffprobe` and `ffmpeg`.
It depends only on the Go standard library and has no knowledge of files-go's
index, metadata models, HTTP routes, authentication, or hardware/codec policy.

`Open` probes the source and creates a complete, immutable VOD timeline. It
does **not** encode media. `Playlist` lists every fragment's duration and URL,
with `EXT-X-PLAYLIST-TYPE:VOD` and `EXT-X-ENDLIST`, so a media element has the
full duration and seekable timeline from the beginning.

`Segment` generates only the requested fragment, or returns its cached result.
An initialization request also generates that fragment: ffmpeg writes the init
section and media data together. Normal player buffering may request nearby
fragments; the backend does not prefetch or encode ahead by itself.

## Example

```go
manager, err := hls.New(ctx, hls.Config{
    CacheDir: "/var/cache/my-app/hls",
    MaxBytes: 512 << 20,
    MaxConcurrent: 2,
})
if err != nil {
    return err
}
defer manager.Close()

playback, err := manager.Open(requestContext, hls.Source{
    Path: "/media/movie.mkv",
    CopyVideo: true,
    OutputArgs: []string{
        "-map", "0:v:0", "-map", "0:a:0?",
        "-c:v", "copy", "-c:a", "aac",
    },
})
if err != nil {
    return err
}
// Route a capability URL to this playback.Key. Do not expose the source path.
playlist, err := manager.Playlist(requestContext, playback.Key)
// Serve playlist as application/vnd.apple.mpegurl.
// Relative URLs are init-00000.mp4, seg-00000.m4s, etc.
fragment, err := manager.Segment(requestContext, playback.Key, 0, false)
if err != nil {
    return err
}
defer fragment.Close()
// Serve fragment as video/mp4; it is seekable and supports HTTP Range serving.
```

For re-encoding, set `CopyVideo: false` and supply suitable encoders in
`OutputArgs`. For audio-only output, supply `-vn` and an audio mapping/encoder.
`InputArgs` can select a hardware device/decoder. These arguments are trusted
application configuration, not user-supplied command-line text.

## Time and fragment boundaries

Copied video uses a packet-header pass to locate keyframe boundaries and count
video packets. This avoids decoding the source and prevents B-frame packets
from leaking into the next GOP. Opening a large copied file still requires
reading its packet headers; this is not a background transcode. Encoded video
and audio use the configured target duration (six seconds by default).

Every fragment is independently produced, with its own fMP4 initialization
section and an HLS discontinuity between fragments. This permits out-of-order
requests, independent codec initialization and local decode timestamps. Full
VOD time belongs to the playlist; applications must not restart playback with
a new zero-based playlist when the user seeks. Subtitle cue times stay on the
original full timeline. Frame/sample rounding can cause small timing differences
in the media element's reported duration.

## Lifecycle and limits

- Independent random capability tokens share a rendition cache. Releasing one
  playback does not invalidate another viewer's token or discard cached media.
- Cache identity includes the absolute source path, file size, modification
  time and encoder options. Changed sources cannot generate new fragments
  under an old timeline.
- Concurrent requests for one fragment, including its init section, share one
  job. A job is canceled when its last waiting request goes away.
- By default, at most two probe/ffmpeg jobs run at once, and jobs time out after
  two minutes (including queue time). Failed or partial outputs are removed.
- Completed fragments have a 512 MiB LRU budget. In-progress temporary outputs
  are additional to this budget. A single fragment over budget is rejected.
- Playback tokens and fragments expire after 30 minutes without access. At
  most 128 tokens are retained; opening another evicts the least recently used.
- A private temporary cache directory is created inside `CacheDir`. `Close` or
  canceling the manager's context stops jobs, waits for them and removes it.
  Cache contents are not reused across process restarts.

The HTTP adapter owns routing and authorization, serves playlists and open
fragment files, and maps `ErrNotFound`, `ErrSourceChanged`, context errors and
encoder failures to suitable responses. Validate resource names and indices
before calling `Segment`. Native HLS players can use these playlists directly;
MSE browsers can use hls.js with the media element's native `controls` enabled.
