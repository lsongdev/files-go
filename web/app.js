import { html, render, useCallback, useEffect, useLayoutEffect, useRef, useState } from './node_modules/@lsongdev/scripts/react/react.js';

// Absolute URLs keep fetch working when the page was opened as http://user:pass@host/.
const request = (url, options) => fetch(new URL(url, location.origin), options);
const api = async (url, options) => {
  const response = await request(url, options);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `请求失败 (${response.status})`);
  return body;
};

const ICONS = {
  movies: 'M3 5h18v14H3zM7 5l2-3m4 3 2-3M7 9h6', tv: 'M3 6h18v13H3zM8 2l4 4 4-4M7 15h6', music: 'M9 18V5l10-2v13M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0zm10-2a3 3 0 1 1-6 0 3 3 0 0 1 6 0z',
  photos: 'M3 3h18v18H3zM21 15l-5-5L5 21M9 9h.01', books: 'M4 19.5A2.5 2.5 0 0 1 6.5 17H20V3H6.5A2.5 2.5 0 0 0 4 5.5zM20 17v4H6.5A2.5 2.5 0 0 1 4 18.5',
  folder: 'M3 6a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v9a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3z', file: 'M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6',
  grid: 'M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z', list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01', search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4',
  refresh: 'M20 6v5h-5M4 18v-5h5M18.5 9A7 7 0 0 0 6 5.5L4 8m2 7a7 7 0 0 0 12 3l2-2', download: 'M12 3v12m0 0 4-4m-4 4-4-4M5 21h14', play: 'M6 4l14 8-14 8z',
  close: 'M6 6l12 12M18 6 6 18', menu: 'M4 6h16M4 12h16M4 18h16', sun: 'M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4',
  moon: 'M21 12.8A8.5 8.5 0 1 1 11.2 3 6.5 6.5 0 0 0 21 12.8z', link: 'M10 13a5 5 0 0 0 7.5.5l3-3a5 5 0 0 0-7-7l-1.7 1.7M14 11a5 5 0 0 0-7.5-.5l-3 3a5 5 0 0 0 7 7l1.7-1.7',
  app: 'M4 4h6v6H4zM14 4h6v6h-6zM4 14h6v6H4zM14 14h6v6h-6z', archive: 'M3 3h18v5H3zM5 8v13h14V8M10 12h4', video: 'M3 5h13v14H3zM16 10l5-3v10l-5-3',
};
const Icon = ({ name, size = 18 }) => html`<svg class="icon" width=${size} height=${size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d=${ICONS[name] || ICONS.file}/></svg>`;
const kindIcon = (item) => item.meta?.kind === 'album' ? 'music' : item.dir ? 'folder' : ({ video: 'video', audio: 'music', image: 'photos', book: 'books', app: 'app', archive: 'archive' }[item.kind] || 'file');
const rootIcon = (type) => ({ movies: 'movies', tv: 'tv', music: 'music', photos: 'photos', books: 'books' }[type] || 'folder');

const KIND_LABEL = { movie: '电影', show: '剧集', season: '季', episode: '单集', video: '视频', audio: '音频', album: '专辑', photo: '照片', image: '图片', book: '图书', document: '文档', app: '应用', archive: '压缩包', text: '文本', code: '代码', font: '字体', torrent: '种子', folder: '文件夹' };
const INFO_LABEL = { Release: '版本', Bitrate: '码率', Channels: '声道', 'Sample rate': '采样率', Subtitles: '字幕', Track: '音轨', Disc: '碟', Composer: '作曲', Format: '格式', Taken: '拍摄时间', Camera: '相机', Lens: '镜头', Exposure: '快门', Aperture: '光圈', ISO: 'ISO', 'Focal length': '焦距', Location: '位置', Publisher: '出版社', Language: '语言', Pages: '页数', Slides: '幻灯片', Application: '应用程序', Subject: '主题', Creator: '创建工具', Producer: '生成工具', Package: '包名', Version: '版本', 'Version code': '版本号', 'Min SDK': '最低 SDK', 'Target SDK': '目标 SDK', Architecture: '架构', Type: '类型', Description: '描述', 'File version': '文件版本', Copyright: '版权', Files: '文件数', Uncompressed: '解压后', Runtime: '片长', 'Embedded title': '内嵌标题', Code: '番号', Studio: '片商', Label: '厂牌', Series: '系列', Director: '导演', Performers: '演员', Released: '发行日期' };

const formatSize = (bytes) => {
  if (!bytes) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** i).toFixed(i ? 1 : 0)} ${units[i]}`;
};
const formatDate = (ms) => ms ? new Date(ms).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—';
const formatDuration = (s) => {
  if (!s) return '';
  s = Math.round(s);
  const h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, sec = String(s % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
};
const resolution = (m) => !m.height ? '' : Math.min(m.width, m.height) >= 2000 ? '4K' : Math.min(m.width, m.height) >= 1000 ? '1080p' : Math.min(m.width, m.height) >= 700 ? '720p' : `${m.width}×${m.height}`;
const episodeCode = (m) => `S${String(m.season || 0).padStart(2, '0')}E${String(m.episode || 0).padStart(2, '0')}`;
const join = (...parts) => parts.filter(Boolean).join(' · ');

function title(item) {
  const m = item.meta || {};
  if (m.kind === 'episode') return m.title ? m.title : join(m.show, episodeCode(m));
  if (m.kind === 'season') return `第 ${m.season} 季`;
  return m.title || item.name;
}

function subtitle(item) {
  const m = item.meta || {};
  const info = m.info || {};
  if (item.dir && !m.kind) return `${item.size} 项`;
  switch (m.kind) {
    case 'movie': return join(m.year, (m.genres || []).slice(0, 2).join(' / '), m.rating && `★ ${m.rating.toFixed(1)}`, formatDuration(m.duration));
    case 'show': return join(m.year, (m.genres || []).slice(0, 2).join(' / '), m.rating && `★ ${m.rating.toFixed(1)}`);
    case 'season': return `${item.size} 项`;
    case 'episode': return join(m.title && m.show, episodeCode(m), formatDuration(m.duration));
    case 'video': return join(formatDuration(m.duration), resolution(m), m.video?.toUpperCase());
    case 'audio': return join(m.artist, m.album, formatDuration(m.duration));
    case 'album': return join(m.artist, m.year, m.tracks && `${m.tracks} 首`, formatDuration(m.duration));
    case 'photo': return join(m.width && `${m.width}×${m.height}`, info.Taken?.slice(0, 10), info.Camera);
    case 'book': case 'document': return join(m.author, info.Pages && `${info.Pages} 页`, formatSize(item.size));
    case 'app': return join(info.Version, info.Package || info.Architecture, formatSize(item.size));
    case 'archive': return join(info.Files && `${info.Files} 个文件`, formatSize(item.size));
  }
  return formatSize(item.size);
}

const posterKind = (item) => ['movie', 'show', 'season', 'book'].includes(item.meta?.kind) || /\.pdf$/i.test(item.name);
const route = (id, q) => q ? `/search?q=${encodeURIComponent(q)}` : id ? `/f/${id}` : '/';
const ISSUES = { review: '待确认', error: '识别失败' };
const REVIEW_REASON = {
  ambiguous: '有同名作品，自动匹配不一定正确，请确认。',
  conflict: 'NFO 里的 TMDB 编号与本地文件不符（例如集数对不上），已重新匹配，请确认。',
  unmatched: '没有找到可信的匹配，可以手动匹配。',
};

function Thumb({ item, size = 18 }) {
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [item.id, item.version, item.mtime, item.thumb, item.meta?.poster, item.meta?.artworkVersion]);
  if (item.thumb && !failed) return html`<img src=${`/api/files/${item.id}/thumb?v=${item.version || item.mtime}-${item.meta?.artworkVersion || ''}-${item.meta?.poster || ''}`} loading="lazy" alt="" onError=${() => setFailed(true)}/>`;
  return html`<span class="glyph"><${Icon} name=${kindIcon(item)} size=${size}/></span>`;
}

function Listing({ items, view, onOpen }) {
  if (view === 'grid') {
    return html`<div class="grid">${items.map((item) => html`
      <button class=${`card ${posterKind(item) ? 'poster' : ''} ${item.kind ? `kind-${item.meta?.kind === 'movie' ? 'movie' : item.kind}` : ''}`} key=${item.id} data-entry-id=${item.id} onClick=${() => onOpen(item.id)} title=${item.name}>
        <span class="art"><${Thumb} item=${item} size=${36}/>${item.play && html`<i class="badge"><${Icon} name="play" size=${12}/></i>`}</span>
        <strong>${title(item)}</strong><small>${subtitle(item)}</small>
      </button>`)}</div>`;
  }
  return html`<div class="rows">${items.map((item) => html`
    <button class="row" key=${item.id} data-entry-id=${item.id} onClick=${() => onOpen(item.id)} title=${item.name}>
      <span class="art"><${Thumb} item=${item}/></span>
      <span class="name"><strong>${title(item)}</strong><small>${title(item) !== item.name ? item.name : subtitle(item)}</small></span>
      <span class="meta">${title(item) !== item.name ? subtitle(item) : ''}</span>
      <span class="size">${item.dir ? `${item.size} 项` : formatSize(item.size)}</span>
      <span class="date">${formatDate(item.mtime)}</span>
    </button>`)}</div>`;
}

// The native player module (with optional hls.js) loads only when something plays,
// with the same cache-busting version as this file.
const asset = (name) => new URL(name + new URL(import.meta.url).search, import.meta.url).href;

const loadPlayer = () => import(asset('./player.js'));

function Player({ item }) {
  const ref = useRef(null);
  const [status, setStatus] = useState('');
  const [ready, setReady] = useState(false);
  useEffect(() => {
    let player;
    let live = true;
    loadPlayer()
      .then(({ createPlayer }) => { if (live) { player = createPlayer(ref.current, item, setStatus); setReady(true); } })
      .catch((e) => setStatus(`播放器加载失败：${e.message}`));
    return () => { live = false; player?.destroy(); };
  }, [item.id]);
  return html`<div class=${`player ${item.kind}`}>
    ${!ready && html`<div class="player-placeholder"><span class="spinner"></span></div>`}
    <div class="player-stage" ref=${ref}></div>
    ${status && html`<small class="hint">${status}</small>`}
  </div>`;
}

function TextPreview({ item }) {
  const [text, setText] = useState('加载中…');
  useEffect(() => {
    let live = true;
    request(`/api/files/${item.id}/raw`, { headers: { Range: 'bytes=0-262143' } })
      .then((r) => r.arrayBuffer())
      .then((buffer) => {
        let value;
        try { value = new TextDecoder('utf-8', { fatal: true }).decode(buffer); } catch { value = new TextDecoder('gbk').decode(buffer); }
        if (live) setText(value + (item.size > 262144 ? '\n…' : ''));
      })
      .catch((e) => live && setText(e.message));
    return () => { live = false; };
  }, [item.id]);
  return html`<pre class="text">${text}</pre>`;
}

const TEXT = new Set(['text', 'code']);
function Preview({ item, playing }) {
  const raw = `/api/files/${item.id}/raw`;
  if (item.play) return playing ? html`<${Player} key=${item.id} item=${item}/>` : null;
  if (item.kind === 'image' && !['heic', 'heif', 'psd', 'cr2', 'nef', 'arw', 'dng'].includes(item.name.split('.').pop().toLowerCase())) return html`<img class="full" src=${raw} alt=${item.name}/>`;
  if (item.name.toLowerCase().endsWith('.pdf')) return html`<iframe class="pdf" src=${raw} title=${item.name}></iframe>`;
  if (TEXT.has(item.kind) && item.size < 50 << 20) return html`<${TextPreview} item=${item}/>`;
  return null;
}

// Artwork belongs to the entire detail page, including its navigation.
function PageBackdrop({ item }) {
  const [failed, setFailed] = useState(false);
  const src = `/api/files/${item.id}/backdrop?v=${item.version || item.mtime}-${item.meta?.artworkVersion || ''}`;
  useEffect(() => setFailed(false), [src]);
  return failed ? null : html`<div class="page-backdrop" aria-hidden="true"><img src=${src} alt="" onError=${() => setFailed(true)}/></div>`;
}

function Details({ item, onMatch, onRefresh, onConfirm }) {
  const [playing, setPlaying] = useState(false);
  const m = item.meta || {};
  const rows = [
    ['类型', KIND_LABEL[m.kind] || KIND_LABEL[item.kind] || item.kind || '文件'],
    !item.dir && ['大小', formatSize(item.size)], ['修改时间', formatDate(item.mtime)],
    m.original && ['原名', m.original], m.author && ['作者', m.author], m.artist && ['艺术家', m.artist], m.album && ['专辑', m.album],
    m.tracks && ['曲目数', m.tracks], m.albums && ['专辑数', m.albums],
    m.duration && ['时长', formatDuration(m.duration)], m.width && ['分辨率', `${m.width}×${m.height}`],
    m.video && ['编码', join(m.video, m.audio, m.container)], !m.video && m.audio && ['编码', join(m.audio, m.container)],
    m.genres?.length && ['类型标签', m.genres.join(' / ')],
    m.subtitles?.length && ['内嵌字幕', m.subtitles.map((x) => x.title || x.lang || x.codec).join(' / ')], m.match && [m.match.provider === 'tmdb' ? 'TMDB' : 'ThePornDB', html`<a href=${`${m.match.provider === 'tmdb' ? 'https://www.themoviedb.org' : 'https://theporndb.net'}/${m.match.type}/${m.match.id}`} target="_blank" rel="noreferrer">${m.match.type}/${m.match.id}${m.locked ? '（已锁定）' : ''}</a>`],
    ...Object.entries(m.info || {}).map(([k, v]) => [INFO_LABEL[k] || k, v]),
    item.error && ['增强错误', item.error], item.retryCount && ['重试次数', item.retryCount], item.retryAfter && ['下次重试', formatDate(item.retryAfter)],
  ].filter(Boolean);
  const canMatch = ['video', 'movie', 'episode', 'show', 'season'].includes(m.kind) || item.kind === 'video' || m.locked;
  return html`<section class="details">
    <div class="hero">
      ${(item.thumb || !item.dir) && html`<div class=${`cover ${m.kind === 'album' ? 'album' : posterKind(item) ? 'poster' : ''}`}><${Thumb} item=${item} size=${48}/></div>`}
      <div class="heading">
        <small>${KIND_LABEL[m.kind] || ''}</small>
        <h1>${title(item)}</h1>
        <p class="sub">${subtitle(item)}</p>
        ${m.summary && html`<p class="summary">${m.summary}</p>`}
        ${m.review && html`<p class="review">${REVIEW_REASON[m.review] || '请确认识别结果。'}</p>`}
        <div class="actions">
          ${m.review && m.match && html`<button onClick=${onConfirm}>确认无误</button>`}
          ${!item.dir && item.play && html`<button disabled=${playing} onClick=${() => setPlaying(true)}><${Icon} name="play"/>播放</button>`}
          ${!item.dir && html`<a class="button" href=${`/api/files/${item.id}/raw?download`}><${Icon} name="download"/>下载</a>`}
          ${!item.dir && !item.play && html`<a class="button ghost" href=${`/api/files/${item.id}/raw`} target="_blank"><${Icon} name="link"/>打开原文件</a>`}
          ${canMatch && html`<button class="ghost" onClick=${onMatch}>手动匹配</button>`}
          <button class="ghost" onClick=${onRefresh}><${Icon} name="refresh"/>${item.dir ? '重新扫描' : '重新识别'}</button>
        </div>
      </div>
    </div>
    ${!item.dir && html`<${Preview} item=${item} playing=${playing}/>`}
    <dl class="info">${rows.map(([k, v]) => html`<div><dt>${k}</dt><dd>${v}</dd></div>`)}</dl>
  </section>`;
}

function MatchDialog({ item, onClose, onDone }) {
  const m = item.meta || {};
  // Empty until the server suggests the names identification parsed, not the
  // possibly wrong result; switching provider asks again.
  const [query, setQuery] = useState('');
  const [provider, setProvider] = useState(m.match?.provider || (m.info?.Code ? 'theporndb' : 'tmdb'));
  const [type, setType] = useState(['episode', 'show', 'season'].includes(m.kind) || m.match?.type === 'tv' ? 'tv' : 'movie');
  const [results, setResults] = useState([]);
  const [scope, setScope] = useState(item);
  const searchVersion = useRef(0);
  const edited = useRef(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const search = useCallback(async (event) => {
    event?.preventDefault();
    const version = ++searchVersion.current;
    setBusy(true); setError('');
    try {
      const q = event || edited.current ? query : '';
      const data = await api(`/api/files/${item.id}/match?provider=${provider}&type=${provider === 'theporndb' ? 'jav' : type}&q=${encodeURIComponent(q)}`);
      if (version !== searchVersion.current) return;
      setResults(data.items); setScope(data.scope || item);
      if (!event && data.query) setQuery(data.query);
    } catch (e) { if (version === searchVersion.current) setError(e.message); }
    if (version === searchVersion.current) setBusy(false);
  }, [item.id, query, provider, type]);
  useEffect(() => { search(); }, [provider, type]);
  const choose = async (id) => {
    setBusy(true);
    try { onDone(await api(`/api/files/${item.id}/match`, { method: id ? 'PUT' : 'DELETE', headers: { 'Content-Type': 'application/json' }, body: id ? JSON.stringify({ match: id }) : undefined })); } catch (e) { setError(e.message); setBusy(false); }
  };
  return html`<div class="overlay" onClick=${(e) => e.target === e.currentTarget && onClose()}>
    <div class="dialog">
      <header><h2>手动匹配</h2><button class="icon-button" onClick=${onClose}><${Icon} name="close"/></button></header>
      ${(scope.dir || scope.id !== item.id) && html`<p class="hint">匹配将应用于「${scope.name}」中的同一作品文件。</p>`}
      <form onSubmit=${search}>
        <select value=${provider} onChange=${(e) => setProvider(e.target.value)}><option value="tmdb">TMDB</option><option value="theporndb">ThePornDB</option></select>
        ${provider === 'tmdb' && html`<select value=${type} onChange=${(e) => setType(e.target.value)}><option value="movie">电影</option><option value="tv">剧集</option></select>`}
        <input value=${query} onInput=${(e) => { edited.current = true; setQuery(e.target.value); }} placeholder="名称"/>
        <button disabled=${busy}>搜索</button>
      </form>
      ${error && html`<p class="error">${error}</p>`}
      <div class="candidates">${results.map((r) => html`<button key=${`${r.match.provider}/${r.match.type}/${r.match.id}`} disabled=${busy} onClick=${() => choose(r.match)}>
        ${r.poster ? html`<img src=${r.poster} alt=""/>` : html`<span class="glyph"><${Icon} name="movies"/></span>`}
        <span><strong>${r.title} ${r.year ? `(${r.year})` : ''}</strong><small>${r.original !== r.title ? r.original : ''}</small><small class="clamp">${r.overview}</small></span>
      </button>`)}</div>
      ${m.locked && html`<footer><button class="ghost" disabled=${busy} onClick=${() => choose('')}>解除锁定并自动识别</button></footer>`}
    </div>
  </div>`;
}

function Status({ status, active, onOpen }) {
  if (!status) return null;
  const scanning = status.roots.filter((r) => r.state === 'scanning');
  const offline = status.roots.filter((r) => r.state === 'offline' || r.state === 'error' || r.state === 'mount_changed');
  const { files, dirs, pending, failed } = status.stats;
  return html`<button class=${`status ${active ? 'active' : ''}`} title="实时活动" onClick=${onOpen}>
    <div>${files.toLocaleString()} 个文件 · ${dirs.toLocaleString()} 个文件夹</div>
    ${scanning.map((r) => html`<div class="busy">正在扫描 ${r.name} · ${r.scanned.toLocaleString()}</div>`)}
    ${pending > 0 && html`<div class="busy">正在识别 · 剩余 ${pending.toLocaleString()}</div>`}
    ${failed > 0 && html`<div class="muted">${failed.toLocaleString()} 个文件识别失败</div>`}
    ${offline.map((r) => html`<div class="warn">${r.name}：${r.state === 'offline' ? '离线' : r.state === 'mount_changed' ? '挂载发生变化' : r.error}</div>`)}
  </button>`;
}

const ROOT_STATE = { idle: '空闲', scanning: '正在扫描', offline: '离线', mount_changed: '挂载发生变化', error: '错误' };
const ACTIVITY = { scan: '扫描', identified: '已识别', processed: '已处理', review: '待确认', failed: '失败', retry: '稍后重试' };
const clock = (ms) => new Date(ms).toLocaleTimeString('zh-CN', { hour12: false });

function LibraryRow({ root, onOpen }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [submitted, setSubmitted] = useState(false);
  useEffect(() => { setSubmitted(false); }, [root.state, root.lastScan]);
  const scanning = root.state === 'scanning';
  const date = root.lastScan && new Date(root.lastScan);
  const lastScan = date && date.valueOf() > 0 ? date.toLocaleString('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }) : '尚未扫描';
  const scan = async () => {
    if (busy || scanning) return;
    setBusy(true); setError(''); setSubmitted(false);
    try {
      await api(`/api/roots/${encodeURIComponent(root.id)}/scan`, { method: 'POST' });
      setSubmitted(true);
    } catch (e) { setError(e.message); }
    finally { setBusy(false); }
  };
  return html`<tr>
    <td class="library-name" data-label="媒体库名称"><button disabled=${!root.fileId} onClick=${() => onOpen(root.fileId)}>${root.name}</button></td>
    <td data-label="状态"><span class=${`state ${root.state}`}>${ROOT_STATE[root.state] || root.state}</span>
      ${scanning && html`<small>已扫描 ${root.scanned.toLocaleString()} 项</small>`}
      ${(root.error || root.watchError) && html`<small class="error">${root.error || root.watchError}</small>`}
    </td>
    <td class="library-time" data-label="最后扫描时间">${date && date.valueOf() > 0 ? html`<time datetime=${root.lastScan}>${lastScan}</time>` : lastScan}</td>
    <td class="library-action" data-label="操作"><button class="ghost small" disabled=${busy || scanning} onClick=${scan} aria-label=${`扫描媒体库 ${root.name}`}>
      <${Icon} name="refresh" size=${14}/>${scanning ? '扫描中' : busy ? '提交中…' : '扫描'}
    </button>
      ${(error || submitted) && html`<small class=${`library-feedback ${error ? 'error' : ''}`} role="status">${error || '扫描请求已提交'}</small>`}
    </td>
  </tr>`;
}

// Live view of scanning and identification, fed by server-sent events.
function ActivityPage({ status, feed, roots, onOpen }) {
  if (!status) return html`<p class="empty">加载中…</p>`;
  const rootName = (id) => roots.find((r) => r.id === id)?.name || id;
  const where = (root, path) => [rootName(root), path].filter(Boolean).join(' / ');
  const { files, dirs, pending, review, failed } = status.stats;
  const counters = [['文件', files], ['文件夹', dirs], ['待识别', pending], ['待确认', review], ['失败', failed]];
  return html`<section class="activity">
    <h2>实时活动</h2>
    <div class="counters">${counters.map(([label, n]) => html`<div><strong>${(n || 0).toLocaleString()}</strong><span>${label}</span></div>`)}</div>
    <h3>媒体库</h3>
    <div class="library-list"><table>
      <thead><tr><th scope="col">媒体库名称</th><th scope="col">状态</th><th scope="col">最后扫描时间</th><th scope="col" class="library-action">操作</th></tr></thead>
      <tbody>${status.roots.map((r) => html`<${LibraryRow} key=${r.id} root=${r} onOpen=${onOpen}/>` )}</tbody>
    </table>${!status.roots.length && html`<p class="hint">尚未配置媒体库</p>`}</div>
    <h3>正在处理</h3>
    ${status.running?.length ? html`<ul class="rows">${status.running.map((f) => html`<li><button onClick=${() => onOpen(f.id)}>${where(f.root, f.path)}</button></li>`)}</ul>`
      : html`<p class="hint">${pending > 0 ? '等待中' : '空闲'}</p>`}
    <h3>最近活动</h3>
    ${feed.length ? html`<ul class="rows feed">${feed.map((a) => html`<li key=${a.id}>
      <time>${clock(a.time)}</time><span class=${`tag ${a.type}`}>${ACTIVITY[a.type] || a.type}</span>
      ${a.fileId ? html`<button onClick=${() => onOpen(a.fileId)}>${where(a.root, a.path)}</button>` : html`<span>${where(a.root, a.path)}</span>`}
      <small>${a.text}</small>
    </li>`)}</ul>` : html`<p class="hint">暂无活动</p>`}
  </section>`;
}

function App() {
  const [roots, setRoots] = useState([]);
  const [status, setStatus] = useState(null);
  const [current, setCurrent] = useState(null);
  const [ancestors, setAncestors] = useState([]);
  const [items, setItems] = useState([]);
  const [more, setMore] = useState(false);
  const [query, setQuery] = useState('');
  const [searching, setSearching] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [matching, setMatching] = useState(false);
  const [activity, setActivity] = useState(false); // the live activity page is open
  const [feed, setFeed] = useState([]);
  const [changed, setChanged] = useState(null); // {force}: re-check what is shown
  const [nav, setNav] = useState(false);
  const [view, setView] = useState(() => localStorage.getItem('view') || 'grid');
  const [opening, setOpening] = useState(null);
  const [scrolled, setScrolled] = useState(false);
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') || (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'));
  const sentinel = useRef(null);
  const seq = useRef(0);
  const pages = useRef(new Map());
  const active = useRef(null);
  const nextActive = useRef(null);
  const displayed = useRef(null);
  const restore = useRef(null);
  displayed.current = { current, ancestors, items, more, searching, loading, opening, roots };

  const position = () => {
    const top = document.querySelector('.top')?.getBoundingClientRect().bottom || 0;
    const anchor = [...document.querySelectorAll('[data-entry-id]')]
      .find((el) => el.getBoundingClientRect().bottom > top);
    return { y: window.scrollY, anchor: anchor?.dataset.entryId, offset: anchor?.getBoundingClientRect().top };
  };

  const remember = () => {
    if (!active.current || displayed.current.opening?.blank) return;
    const page = { ...active.current, ...displayed.current, position: position() };
    pages.current.delete(page.key);
    pages.current.set(page.key, page);
    // Bound the retained lists; history keeps enough information to refetch evicted pages.
    while (pages.current.size > 20) pages.current.delete(pages.current.keys().next().value);
    if (history.state?.pageKey === page.key) {
      history.replaceState({ ...history.state, position: page.position, count: page.items.length }, '');
    }
  };

  useLayoutEffect(() => {
    if (nextActive.current) { active.current = nextActive.current; nextActive.current = null; }
    const saved = restore.current;
    if (!saved) return;
    restore.current = null;
    const anchor = saved.anchor && document.querySelector(`[data-entry-id="${saved.anchor}"]`);
    window.scrollTo({ top: anchor ? window.scrollY + anchor.getBoundingClientRect().top - saved.offset : saved.y, behavior: 'instant' });
  });

  useEffect(() => {
    const previous = history.scrollRestoration;
    history.scrollRestoration = 'manual';
    let frame;
    const save = () => {
      frame = null;
      setScrolled(window.scrollY > 8);
      if (!active.current || displayed.current.loading || history.state?.pageKey !== active.current.key) return;
      history.replaceState({ ...history.state, position: position(), count: displayed.current.items.length }, '');
    };
    const onScroll = () => { if (frame == null) frame = requestAnimationFrame(save); };
    addEventListener('scroll', onScroll, { passive: true });
    addEventListener('pagehide', save);
    return () => {
      history.scrollRestoration = previous;
      cancelAnimationFrame(frame);
      removeEventListener('scroll', onScroll);
      removeEventListener('pagehide', save);
    };
  }, []);

  useEffect(() => { document.documentElement.dataset.theme = theme; localStorage.setItem('theme', theme); }, [theme]);
  useEffect(() => { localStorage.setItem('view', view); }, [view]);
  useEffect(() => {
    if (!nav) return undefined;
    const close = (e) => e.key === 'Escape' && setNav(false);
    addEventListener('keydown', close);
    return () => removeEventListener('keydown', close);
  }, [nav]);

  const loadStatus = useCallback(() => api('/api/status').then(setStatus).catch(() => {}), []);
  // Server-sent events push status, changed folders and activity. Events are
  // hints: what is shown is re-read through the API. Polling every 5s and
  // comparing folder versions takes over while the stream is down.
  const live = useRef(false);
  useEffect(() => {
    api('/api/roots').then(setRoots).catch((e) => setError(e.message));
    loadStatus();
    const timer = setInterval(() => {
      if (!live.current) loadStatus().then(() => setChanged({ force: false }));
    }, 5000);
    let source;
    if (typeof EventSource === 'function') {
      source = new EventSource('/api/events');
      source.onopen = () => { live.current = true; setChanged({ force: false }); }; // catch up after a reconnect
      source.onerror = () => { live.current = false; };
      source.addEventListener('status', (e) => setStatus(JSON.parse(e.data)));
      source.addEventListener('changes', (e) => {
        const { dirs = [], files = [], resync } = JSON.parse(e.data);
        const id = displayed.current.current?.id;
        if (resync || (id && (dirs.includes(id) || files.includes(id)))) setChanged({ force: true });
      });
      source.addEventListener('activity', (e) => {
        const { items } = JSON.parse(e.data);
        setFeed((list) => [...items, ...list].slice(0, 300));
      });
    }
    return () => { clearInterval(timer); source?.close(); };
  }, []);

  const openPage = useCallback(async (target, push = true) => {
    remember();
    setActivity(false);
    const url = target.type ? `/issues/${target.type}` : route(target.id, target.q);
    const n = ++seq.current;
    const state = push ? {} : (history.state || {});
    const key = push ? `${Date.now()}-${Math.random().toString(36).slice(2)}` : (state.pageKey || `${Date.now()}-${Math.random().toString(36).slice(2)}`);
    // A new visit to an ancestor also resumes its last browsing position.
    const cached = push
      ? [...pages.current.values()].reverse().find((page) => page.url === url)
      : pages.current.get(key);
    const saved = cached?.position || state.position || { y: 0 };
    const count = Math.max(cached?.items.length || state.count || 0, 200);
    const label = target.q || ISSUES[target.type] || '';
    const install = (page, scroll) => {
      nextActive.current = { key, url };
      restore.current = scroll;
      setCurrent(page.current); setAncestors(page.ancestors); setItems(page.items);
      setMore(page.more); setSearching(label);
    };
    const entry = { ...target, pageKey: key, position: saved, count };
    if (push) history.pushState(entry, '', url);
    else history.replaceState({ ...state, ...entry }, '');
    setLoading(true); setError(''); setNav(false);
    const previous = displayed.current;
    const sameID = (f) => f && String(f.id) === String(target.id);
    const root = previous.roots.find((r) => String(r.fileId) === String(target.id));
    const preview = previous.items.find(sameID) || previous.ancestors.find(sameID)
      || (sameID(previous.current) ? previous.current : null)
      || (root ? { id: root.fileId, root: root.id, name: root.name, dir: true } : null);
    let previewAncestors = [];
    if (preview && previous.items.some(sameID) && previous.current?.dir) {
      previewAncestors = [...previous.ancestors, previous.current];
    } else if (preview && previous.ancestors.some(sameID)) {
      previewAncestors = previous.ancestors.slice(0, previous.ancestors.findIndex(sameID));
    }
    setOpening({ target, blank: !cached, label: preview ? title(preview) : (label || '内容') });
    if (cached) install(cached, saved);
    else install({ current: preview, ancestors: previewAncestors, items: [], more: false }, { y: 0 });
    try {
      let page;
      if (target.id) {
        const data = await api(`/api/files/${target.id}`);
        if (n !== seq.current) return;
        let children = { items: [], more: false };
        if (data.file.dir) {
          // The server caps each request at 1000; restore every previously loaded page.
          do {
            const batch = await api(`/api/files/${target.id}/children?limit=${Math.min(1000, count - children.items.length)}&offset=${children.items.length}`);
            if (n !== seq.current) return;
            children.items.push(...batch.items); children.more = batch.more;
            if (!batch.items.length) break;
          } while (children.more && children.items.length < count);
        }
        page = { current: data.file, ancestors: data.ancestors, ...children };
      } else {
        const endpoint = target.type ? `/api/issues?type=${target.type}&limit=1000` : `/api/search?q=${encodeURIComponent(target.q)}`;
        const data = await api(endpoint);
        page = { current: null, ancestors: [], items: data.items, more: false };
      }
      if (n !== seq.current) return;
      // Preserve any scrolling done while the cached list was being refreshed.
      install(page, cached ? position() : saved);
      setOpening(null);
    } catch (e) { if (n === seq.current) setError(e.message); }
    if (n === seq.current) setLoading(false);
  }, []);

  const load = useCallback((id, push = true) => openPage({ id }, push), [openPage]);
  const search = useCallback((q, push = true) => {
    if (q.trim()) return openPage({ q }, push);
  }, [openPage]);
  const issues = useCallback((type, push = true) => openPage({ type }, push), [openPage]);
  const openActivity = useCallback((push = true) => {
    remember();
    ++seq.current; setOpening(null); setLoading(false);
    if (push) history.pushState({ activity: true }, '', '/activity');
    setActivity(true); setNav(false); setError('');
    api('/api/activity').then((data) => { setFeed(data.items); setStatus(data.status); }).catch((e) => setError(e.message));
  }, []);

  const loadMore = useCallback(async () => {
    if (!current || !more || loading) return;
    const n = seq.current;
    setLoading(true);
    try {
      const data = await api(`/api/files/${current.id}/children?limit=200&offset=${items.length}`);
      if (n !== seq.current) return;
      setItems((list) => [...list, ...data.items]); setMore(data.more);
    } catch (e) { if (n === seq.current) setError(e.message); }
    if (n === seq.current) setLoading(false);
  }, [current, more, loading, items.length]);

  useEffect(() => {
    const el = sentinel.current;
    if (!el) return undefined;
    const observer = new IntersectionObserver((entries) => entries[0].isIntersecting && loadMore(), { rootMargin: '600px' });
    observer.observe(el);
    return () => observer.disconnect();
  }, [loadMore]);

  const navigate = useCallback((push) => {
    const params = new URLSearchParams(location.search);
    const match = location.pathname.match(/^\/f\/(\d+)/);
    const issue = location.pathname.match(/^\/issues\/(review|error)/);
    if (location.pathname === '/activity') openActivity(false);
    else if (location.pathname === '/search' && params.get('q')) { setQuery(params.get('q')); search(params.get('q'), push); }
    else if (issue) issues(issue[1], push);
    else if (match) load(match[1], push);
    else if (roots[0]) load(roots[0].fileId, false);
  }, [roots, load, search, issues, openActivity]);
  useEffect(() => { if (roots.length) navigate(false); }, [roots]);
  useEffect(() => {
    const onPop = () => navigate(false);
    addEventListener('popstate', onPop);
    return () => removeEventListener('popstate', onPop);
  }, [navigate]);

  // Re-read the open folder or file when it changed: pushed changes name it,
  // and without the stream its version is compared on each status poll. The
  // server bumps a folder's version when entries are added, removed or
  // renamed and when a member's identification changes.
  useEffect(() => {
    if (!changed || !current || searching || loading || activity) return;
    const n = seq.current;
    const count = items.length;
    (async () => {
      const detail = await api(`/api/files/${current.id}`);
      if (n !== seq.current || !changed.force && detail.file.version === current.version) return;
      if (!current.dir) { setCurrent(detail.file); return; }
      const children = [];
      let more;
      do {
        const data = await api(`/api/files/${current.id}/children?limit=${Math.min(1000, Math.max(count, 200) - children.length)}&offset=${children.length}`);
        if (n !== seq.current || displayed.current.items.length !== count) return;
        children.push(...data.items); more = data.more;
        if (!data.items.length) break;
      } while (more && children.length < count);
      restore.current = position();
      setCurrent(detail.file); setAncestors(detail.ancestors);
      setItems(children); setMore(more);
    })().catch(() => {});
  }, [changed]);

  const refresh = async () => {
    try {
      const updated = await api(`/api/files/${current.id}/refresh`, { method: 'POST' });
      if (current.dir) load(current.id, false); else setCurrent(updated);
    } catch (e) { setError(e.message); }
  };

  const matched = (file) => {
    // Shared file jobs may finish after the match returns. Always perform a
    // final directory reload after an explicit match.
    setMatching(false); setCurrent(file); loadStatus();
    if (file.dir) setChanged({ force: true });
  };

  const activeRoot = ancestors[0]?.root || current?.root;
  const hasBackdrop = !activity && !searching && !opening?.blank && current?.meta?.backdrop
    && (!current.dir || current.meta.kind || current.meta.locked);
  return html`<div class=${`app ${nav ? 'nav-open' : ''}`}>
    <aside>
      <div class="brand">files</div>
      <nav>${roots.map((r) => html`<button class=${r.id === activeRoot ? 'active' : ''} onClick=${() => load(r.fileId)}><${Icon} name=${rootIcon(r.type)}/>${r.name}${r.state === 'scanning' && html`<i class="dot"></i>`}</button>`)}</nav>
      ${(status?.stats.review > 0 || status?.stats.failed > 0) && html`<nav class="issues">
        ${Object.entries(ISSUES).map(([type, label]) => {
          const count = type === 'review' ? status.stats.review : status.stats.failed;
          return count > 0 && html`<button class=${searching === label ? 'active' : ''} onClick=${() => issues(type)}><${Icon} name=${type === 'review' ? 'search' : 'close'}/>${label}<span class="count">${count.toLocaleString()}</span></button>`;
        })}
      </nav>`}
      <${Status} status=${status} active=${activity} onOpen=${() => openActivity()}/>
    </aside>
    ${nav && html`<div class="scrim" onClick=${() => setNav(false)}></div>`}
    <main class=${hasBackdrop ? 'has-backdrop' : ''}>
      ${hasBackdrop && html`<${PageBackdrop} key=${current.id} item=${current}/>`}
      ${opening && loading && html`<div class="navigation-progress" role="status" aria-label="正在加载页面"></div>`}
      <header class=${`top ${scrolled ? 'scrolled' : ''}`}>
        <button class="icon-button menu" onClick=${() => setNav(!nav)}><${Icon} name="menu"/></button>
        <form class="search" onSubmit=${(e) => { e.preventDefault(); search(query); }}>
          <${Icon} name="search"/><input type="search" placeholder="搜索文件名、片名、书名…" value=${query} onInput=${(e) => setQuery(e.target.value)}/>
        </form>
        <button class="icon-button" title="切换视图" onClick=${() => setView(view === 'grid' ? 'list' : 'grid')}><${Icon} name=${view === 'grid' ? 'list' : 'grid'}/></button>
        <button class="icon-button" title="切换主题" onClick=${() => setTheme(theme === 'dark' ? 'light' : 'dark')}><${Icon} name=${theme === 'dark' ? 'sun' : 'moon'}/></button>
      </header>
      ${activity ? html`${error && html`<p class="error">${error}</p>`}<${ActivityPage} status=${status} feed=${feed} roots=${roots} onOpen=${load}/>` : html`
      <div class="crumbs">${searching ? html`<span>${Object.values(ISSUES).includes(searching) ? searching : `搜索 “${searching}”`} · ${items.length} 个结果</span>` : [...ancestors, current].filter(Boolean).map((f, i, all) => html`
        ${i > 0 && html`<span class="sep">/</span>`}<button disabled=${i === all.length - 1} onClick=${() => load(f.id)}>${f.parent ? f.name : (roots.find((r) => r.id === f.root)?.name || f.name)}</button>`)}</div>
      ${error && !opening?.blank && html`<p class="error">${error}</p>`}
      ${opening?.blank ? html`<section class="page-loading" aria-busy=${loading}>
        ${loading ? html`<span class="loading-spinner" aria-hidden="true"></span><p role="status">正在打开 ${opening.label}…</p>`
          : html`<p class="error" role="alert">无法打开 ${opening.label}：${error}</p><button class="ghost small" onClick=${() => openPage(opening.target, false)}>重试</button>`}
      </section>` : html`
      ${current && !searching && (!current.dir || current.meta?.kind || current.meta?.locked) && html`<${Details} key=${current.id} item=${current} onMatch=${() => setMatching(true)} onRefresh=${refresh}
        onConfirm=${() => api(`/api/files/${current.id}/match`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ match: current.meta.match }) }).then(matched).catch((e) => setError(e.message))}/>`}
      ${current?.dir && !searching && !current.meta?.kind && !current.meta?.locked && html`<div class="toolbar"><span>${current.size} 项</span><button class="ghost small" onClick=${refresh}><${Icon} name="refresh" size=${14}/>重新扫描</button></div>`}
      ${(current?.dir || searching) && html`<${Listing} items=${items} view=${view} onOpen=${load}/>`}
      ${(current?.dir || searching) && !loading && !items.length && html`<p class="empty">${searching ? '没有找到匹配的文件' : '这个文件夹是空的'}</p>`}
      <div ref=${sentinel} class="sentinel">${loading && '加载中…'}</div>`}`}
    </main>
    ${matching && current && html`<${MatchDialog} item=${current} onClose=${() => setMatching(false)} onDone=${matched}/>`}
  </div>`;
}

render(html`<${App}/>`, document.getElementById('app'));
