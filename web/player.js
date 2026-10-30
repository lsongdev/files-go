// Native audio/video controls, with lazy HLS support where needed. The server
// supplies the complete VOD timeline, so duration and seeking belong to the
// actual media element rather than a JavaScript timeline wrapper.
const HLS_JS = new URL('./vendor/hls-1.6.13.mjs', import.meta.url).href;
const MODES = ['direct', 'stream', 'transcode'];
const CODEC_TYPES = { h264: 'avc1.640028', hevc: 'hvc1.1.6.L120.90', vp8: 'vp8', vp9: 'vp09.00.40.08', av1: 'av01.0.08M.08' };
const CONTAINER_TYPES = { mp4: 'video/mp4', m4v: 'video/mp4', mov: 'video/quicktime', webm: 'video/webm' };
const probe = document.createElement('video');
const canPlay = (type) => probe.canPlayType(type) !== '';
const nativeHLS = /Apple/.test(navigator.vendor) && canPlay('application/vnd.apple.mpegurl');

function initialMode(item) {
  if (item.kind !== 'video') return item.play || 'direct';
  const codec = CODEC_TYPES[item.meta?.video];
  const container = CONTAINER_TYPES[item.name.split('.').pop().toLowerCase()];
  if (codec && !canPlay(`${container || 'video/mp4'}; codecs="${codec}"`)) return 'transcode';
  if (item.play === 'direct' && container && !canPlay(container)) return 'stream';
  return item.play || 'stream';
}

const getJSON = async (path, signal) => {
  const res = await fetch(new URL(path, location.origin), { signal });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body;
};

export function createPlayer(container, item, onStatus) {
  const media = document.createElement(item.kind === 'audio' ? 'audio' : 'video');
  media.controls = true;
  media.autoplay = true;
  media.preload = 'metadata';
  media.setAttribute('playsinline', '');
  media.setAttribute('aria-label', item.name);
  container.append(media);
  let mode = initialMode(item);
  let hls, playback, request;
  let version = 0;
  let destroyed = false;
  let starting = false;
  let selectedSubtitle;
  const lifetime = new AbortController();
  const tracks = item.kind === 'audio' ? Promise.resolve([])
    : getJSON(`/api/files/${item.id}/subtitles`, lifetime.signal).then((data) => data.items || []).catch(() => []);
  const listen = (event, handler, options = {}) => media.addEventListener(event, handler, { ...options, signal: lifetime.signal });
  const release = (url) => {
    if (url) fetch(new URL(url.replace(/\/index\.m3u8$/, ''), location.origin), { method: 'DELETE', keepalive: true }).catch(() => {});
  };
  const clearSource = () => {
    request?.abort();
    const installed = [...media.querySelectorAll('track')];
    if (installed.length) selectedSubtitle = installed.find((el) => el.track.mode === 'showing')?.dataset.id ?? null;
    for (const el of installed) { el.track.mode = 'disabled'; el.remove(); }
    hls?.destroy();
    hls = null;
    release(playback);
    playback = null;
    media.pause();
    media.removeAttribute('src');
    media.load();
  };
  const fallback = () => {
    const next = MODES[MODES.indexOf(mode) + 1];
    if (!next || destroyed) return false;
    const at = media.currentTime || 0;
    mode = next;
    start(at);
    return true;
  };
  listen('error', () => {
    if (media.error && !fallback()) onStatus?.('这个文件在当前浏览器里无法播放');
  });
  listen('playing', () => { starting = false; onStatus?.(''); });
  // Reset the MSE loader on a distant seek. With independent discontinuities,
  // hls.js can otherwise keep reloading an old fragment after a native seek.
  listen('seeking', () => {
    if (!hls || starting || destroyed) return;
    const at = media.currentTime;
    const buffered = media.buffered;
    for (let i = 0; i < buffered.length; i++) {
      if (at >= buffered.start(i) && at < buffered.end(i) - .1) return;
    }
    start(at);
  });
  // HLS initialization can clear existing cues or reset track modes. Install
  // sidecar tracks after metadata is ready, and keep the user's selection when
  // falling back to another rendition. Cues stay on the full VOD timeline.
  listen('loadedmetadata', async () => {
    const session = version;
    const list = await tracks;
    if (destroyed || session !== version || media.querySelector('track')) return;
    for (const track of list) {
      const el = document.createElement('track');
      const show = selectedSubtitle === undefined ? !!track.default : selectedSubtitle === track.id;
      Object.assign(el, { kind: 'subtitles', label: track.label, srclang: track.lang || '', default: show });
      el.dataset.id = track.id;
      el.src = `/api/files/${item.id}/subtitles/${track.id}.vtt`;
      media.append(el);
      el.track.mode = show ? 'showing' : 'hidden';
    }
  });

  async function start(at = 0) {
    const session = ++version;
    starting = true;
    clearSource();
    request = new AbortController();
    const signal = request.signal;
    try {
      if (mode === 'direct') {
        onStatus?.('');
        media.src = `/api/files/${item.id}/raw`;
      } else {
        onStatus?.(mode === 'transcode' ? '正在准备转码播放…' : '正在准备播放…');
        const started = await getJSON(`/api/files/${item.id}/play${mode === 'transcode' ? '?transcode=1' : ''}`, signal);
        if (destroyed || session !== version) { release(started.url); return; }
        playback = started.url;
        const url = new URL(started.url, location.origin).href;
        if (nativeHLS) {
          media.src = url;
        } else {
          const { default: Hls } = await import(HLS_JS);
          if (destroyed || session !== version) return;
          if (Hls.isSupported()) {
            hls = new Hls({ enableWorker: false, startPosition: at, maxBufferLength: 12, maxMaxBufferLength: 24 });
            hls.on(Hls.Events.ERROR, (_, data) => {
              if (data.fatal && session === version && !destroyed && !fallback()) onStatus?.('这个文件在当前浏览器里无法播放');
            });
            hls.loadSource(url);
            hls.attachMedia(media);
          } else if (canPlay('application/vnd.apple.mpegurl')) {
            media.src = url;
          } else {
            throw new Error('当前浏览器不支持 HLS 播放');
          }
        }
      }
      // A new rendition keeps the same full timeline. Preserve the position
      // through decoder fallbacks; ordinary user seeking requires no JS logic.
      if (at) {
        const restore = () => {
          if (session === version && !destroyed) media.currentTime = at;
        };
        media.addEventListener('loadedmetadata', restore, { once: true, signal });
      }
      media.play().catch((e) => {
        if (!destroyed && session === version && e.name === 'NotAllowedError') onStatus?.('点击播放开始');
      });
    } catch (e) {
      if (destroyed || session !== version || e.name === 'AbortError') return;
      if (!fallback()) onStatus?.(`无法播放：${e.message}`);
    }
  }

  start();
  return {
    destroy() {
      destroyed = true;
      version++;
      lifetime.abort();
      clearSource();
      media.remove();
    },
  };
}
