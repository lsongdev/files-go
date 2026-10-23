// Video and audio player: ready-made Media Chrome themes from player.style
// (Notflix for video, Sutro for audio) around <files-video>, a media element
// that presents a file's full timeline even when it streams from an ffmpeg
// session that started somewhere in the middle. The themes bring their own
// Media Chrome build.
import 'https://cdn.jsdelivr.net/npm/player.style@0.3.5/notflix/+esm';
import 'https://cdn.jsdelivr.net/npm/player.style@0.3.5/sutro-audio/+esm';
import { CustomVideoElement } from 'https://cdn.jsdelivr.net/npm/custom-media-element@1.4.7/+esm';

const HLS_JS = 'https://unpkg.com/hls.js@1.6.13/dist/hls.mjs';

// Playback falls back step by step when the browser cannot decode a mode:
// the original file, then an HLS remux, then an HLS transcode. Codecs the
// browser says it cannot play skip ahead.
const MODES = ['direct', 'stream', 'transcode'];
const CODEC_TYPES = { h264: 'avc1.640028', hevc: 'hvc1.1.6.L120.90', vp9: 'vp09.00.40.08', av1: 'av01.0.08M.08' };
const CONTAINER_TYPES = { mp4: 'video/mp4', m4v: 'video/mp4', mov: 'video/quicktime', webm: 'video/webm' };
const probe = document.createElement('video');
const canPlay = (type) => probe.canPlayType(type) !== '';
// Apple's own HLS player is the most capable on Safari and iOS; elsewhere
// (Chrome's built-in HLS stalls on growing playlists) use hls.js.
const nativeHLS = /Apple/.test(navigator.vendor) && canPlay('application/vnd.apple.mpegurl');

export const transcodes = (item) => item.kind === 'video' && !CODEC_TYPES[item.meta?.video];

function initialMode(item) {
  if (item.kind !== 'video') return item.play;
  const codec = CODEC_TYPES[item.meta?.video];
  if (codec && !canPlay(`video/mp4; codecs="${codec}"`)) return 'transcode';
  const container = CONTAINER_TYPES[item.name.split('.').pop().toLowerCase()];
  if (item.play === 'direct' && container && !canPlay(container)) return 'stream';
  return item.play;
}

const getJSON = async (path) => {
  const res = await fetch(new URL(path, location.origin));
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
};

// Subtitle cue times are absolute; a stream that starts at offset needs
// them moved back by the same amount.
function shiftVTT(text, offset) {
  if (!offset) return text;
  return text.replace(/(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})/g, (_, h, m, s, ms) => {
    const t = Math.max(0, (+(h || 0)) * 3600 + (+m) * 60 + (+s) + (+ms) / 1000 - offset);
    const pad = (n, w = 2) => String(n).padStart(w, '0');
    return `${pad(Math.floor(t / 3600))}:${pad(Math.floor(t / 60) % 60)}:${pad(Math.floor(t % 60))}.${pad(Math.round((t % 1) * 1000), 3)}`;
  });
}

class FilesVideo extends CustomVideoElement {
  #item;
  #mode;
  #offset = 0;
  #session = 0;
  #hls;
  #tracks = [];
  #vtt = new Map();
  #starting = false; // a session is being prepared: playing, but loading

  // load starts playing a file; tracks are its subtitles.
  load(item, tracks = []) {
    this.#item = item;
    this.#tracks = tracks;
    this.#mode = initialMode(item);
    this.#start(0);
  }

  get mode() { return this.#mode; }

  // Reporting "not paused" while the server prepares a stream makes the
  // theme show its loading indicator instead of a play button.
  get paused() { return this.#starting ? false : this.nativeEl.paused; }

  get currentTime() { return this.#offset + (this.nativeEl?.currentTime || 0); }

  set currentTime(t) {
    const video = this.nativeEl;
    if (this.#mode === 'direct') {
      video.currentTime = t;
      return;
    }
    // Seek inside what the session has produced, else restart it there.
    const at = t - this.#offset;
    const end = video.seekable.length ? video.seekable.end(video.seekable.length - 1) : 0;
    if (at >= 0 && at <= end) video.currentTime = at;
    else this.#start(t);
  }

  get duration() {
    const full = this.#item?.meta?.duration;
    return this.#mode === 'direct' || !full ? this.nativeEl?.duration : full;
  }

  get seekable() {
    const d = this.duration || 0;
    return { length: d ? 1 : 0, start: () => 0, end: () => d };
  }

  get buffered() {
    const b = this.nativeEl.buffered;
    const o = this.#offset;
    return { length: b.length, start: (i) => b.start(i) + o, end: (i) => b.end(i) + o };
  }

  // A decode error moves on to the next mode instead of surfacing.
  handleEvent(event) {
    if (event.type === 'error' && event.target === this.nativeEl && this.#fallback()) return;
    super.handleEvent(event);
  }

  destroy() {
    this.#session++;
    this.#teardown();
  }

  #status(text) {
    this.dispatchEvent(new CustomEvent('status', { detail: text, bubbles: true }));
  }

  #fallback() {
    const next = MODES[MODES.indexOf(this.#mode) + 1];
    if (!next || !this.#item) return false;
    const at = this.currentTime;
    this.#mode = next;
    this.#start(at);
    return true;
  }

  #teardown() {
    const video = this.nativeEl;
    this.#hls?.destroy();
    this.#hls = null;
    video.querySelectorAll('track').forEach((t) => { URL.revokeObjectURL(t.src); t.remove(); });
    video.removeAttribute('src');
    video.load();
  }

  async #start(at) {
    const session = ++this.#session;
    const video = this.nativeEl;
    const showing = [...video.textTracks].find((t) => t.mode === 'showing')?.label;
    this.#teardown();
    this.#starting = true;
    this.dispatchEvent(new Event('play'));
    this.dispatchEvent(new Event('waiting'));
    try {
      if (this.#mode === 'direct') {
        this.#offset = 0;
        video.src = `/api/files/${this.#item.id}/raw`;
        if (at) video.addEventListener('loadedmetadata', () => { video.currentTime = at; }, { once: true });
      } else {
        this.#offset = Math.floor(at);
        this.#status(this.#mode === 'transcode' || transcodes(this.#item) ? '正在准备转码…' : '正在准备…');
        const started = await getJSON(`/api/files/${this.#item.id}/play?t=${this.#offset}${this.#mode === 'transcode' ? '&transcode=1' : ''}`);
        if (session !== this.#session) return;
        const url = new URL(started.url, location.origin).href;
        if (nativeHLS) {
          video.src = url;
        } else {
          const { default: Hls } = await import(HLS_JS);
          if (session !== this.#session) return;
          this.#hls = new Hls({ enableWorker: false, startPosition: 0 });
          this.#hls.on(Hls.Events.ERROR, (_, data) => { if (data.fatal && session === this.#session && !this.#fallback()) this.#status('这个文件在当前浏览器里无法播放'); });
          this.#hls.loadSource(url);
          this.#hls.attachMedia(video);
        }
      }
      this.#status('');
      this.dispatchEvent(new Event('durationchange'));
      await this.#subtitles(session, showing);
      await video.play().catch(() => {});
    } catch (e) {
      if (session !== this.#session) return;
      if (!this.#fallback()) this.#status(`无法播放：${e.message}`);
    } finally {
      if (session === this.#session) {
        this.#starting = false;
        this.dispatchEvent(new Event(video.paused ? 'pause' : 'playing'));
      }
    }
  }

  async #subtitles(session, showing) {
    const video = this.nativeEl;
    for (const t of this.#tracks) {
      if (!this.#vtt.has(t.id)) {
        const res = await fetch(new URL(`/api/files/${this.#item.id}/subtitles/${t.id}.vtt`, location.origin));
        this.#vtt.set(t.id, res.ok ? await res.text() : '');
      }
      if (session !== this.#session) return;
      const el = document.createElement('track');
      Object.assign(el, { kind: 'subtitles', label: t.label, srclang: t.lang || '' });
      el.src = URL.createObjectURL(new Blob([shiftVTT(this.#vtt.get(t.id), this.#offset)], { type: 'text/vtt' }));
      video.append(el);
      el.track.mode = (showing ? t.label === showing : t.default) ? 'showing' : 'hidden';
    }
    video.textTracks.dispatchEvent?.(new Event('change'));
  }
}

if (!customElements.get('files-video')) customElements.define('files-video', FilesVideo);

// createPlayer builds a player for item inside container; call destroy()
// to stop it and end its server session.
export function createPlayer(container, item, onStatus) {
  const audio = item.kind === 'audio';
  const theme = document.createElement(audio ? 'media-theme-sutro-audio' : 'media-theme-notflix');
  theme.innerHTML = '<files-video slot="media" playsinline autoplay></files-video>';
  container.append(theme);
  const media = theme.querySelector('files-video');
  media.addEventListener('status', (e) => onStatus?.(e.detail));
  const tracks = audio ? Promise.resolve([]) : getJSON(`/api/files/${item.id}/subtitles`).then((d) => d.items || []).catch(() => []);
  tracks.then((list) => media.load(item, list));
  return { destroy: () => { media.destroy(); theme.remove(); } };
}
