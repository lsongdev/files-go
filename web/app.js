import { html, render, useCallback, useEffect, useMemo, useRef, useState } from 'https://unpkg.com/htm@3.1.1/preact/standalone.module.js';
import Hls from 'https://unpkg.com/hls.js@1.6.13/dist/hls.mjs';

const API = '/api/v1';

async function request(url, options) {
  const response = await fetch(url, options);
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(body.error?.message || `请求失败 (${response.status})`);
  }
  return body;
}

function Icon({ name, size = 20 }) {
  const paths = {
    brand: html`<path d="M4 6.5 12 2l8 4.5v11L12 22l-8-4.5z"/><path d="m4 6.5 8 4.5 8-4.5M12 11v11"/>`,
    movies: html`<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m7 5 2-3m4 3 2-3m2 7h.01M7 9h6"/>`,
    tv: html`<rect x="3" y="6" width="18" height="13" rx="2"/><path d="m8 2 4 4 4-4M7 15h6"/>`,
    music: html`<path d="M9 18V5l10-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="16" cy="16" r="3"/>`,
    photos: html`<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-5-5L5 21"/>`,
    files: html`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h6"/>`,
    folder: html`<path d="M3 6a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v9a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3z"/>`,
    file: html`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/>`,
    grid: html`<rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>`,
    list: html`<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>`,
    refresh: html`<path d="M20 6v5h-5M4 18v-5h5"/><path d="M18.5 9A7 7 0 0 0 6 5.5L4 8m2 7a7 7 0 0 0 12 3l2-2"/>`,
    chevron: html`<path d="m9 18 6-6-6-6"/>`,
    menu: html`<path d="M4 6h16M4 12h16M4 18h16"/>`,
    close: html`<path d="m6 6 12 12M18 6 6 18"/>`,
    drive: html`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 15h.01M11 15h6M7 9h10"/>`,
    more: html`<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`,
    sun: html`<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.66 6.34l1.41-1.41"/>`,
    moon: html`<path d="M21 12.8A8.5 8.5 0 1 1 11.2 3 6.5 6.5 0 0 0 21 12.8z"/>`,
    search: html`<circle cx="11" cy="11" r="7"/><path d="m20 20-4-4"/>`,
    x: html`<path d="m6 6 12 12M18 6 6 18"/>`,
    download: html`<path d="M12 3v12m0 0 4-4m-4 4-4-4M5 21h14"/>`,
    plus: html`<path d="M12 5v14M5 12h14"/>`,
    trash: html`<path d="M4 7h16M9 7V4h6v3m3 0-1 14H7L6 7m4 4v6m4-6v6"/>`,
    upload: html`<path d="M12 16V4m0 0L8 8m4-4 4 4M5 20h14"/>`,
    move: html`<path d="M5 9V5h4m10 10v4h-4M5 5l6 6m8 8-6-6M15 5h4v4m0-4-6 6M9 19H5v-4m0 4 6-6"/>`,
    copy: html`<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>`,
  };
  return html`<svg class="icon" width=${size} height=${size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || paths.file}</svg>`;
}

const iconForLibrary = (type) => ({ movies: 'movies', tv: 'tv', music: 'music', photos: 'photos', books: 'files', files: 'files' }[type] || 'files');

function formatSize(bytes) {
  if (!bytes) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / (1024 ** index)).toFixed(index ? 1 : 0)} ${units[index]}`;
}

function formatDate(value) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}

function formatCount(value) {
  return new Intl.NumberFormat('zh-CN').format(value || 0);
}

function ActivityPanel({ storages, processing, onFailures }) {
  const scanning = storages.find((item) => item.state === 'scanning');
  const interrupted = storages.find((item) => item.state === 'interrupted');
  const activeJobs = (processing.pending || 0) + (processing.running || 0);
  const exactScanPercent = scanning?.scanEstimate ? (scanning.scanEntries || 0) * 100 / scanning.scanEstimate : null;
  const scanPercent = exactScanPercent == null ? null : Math.min(99, Math.floor(exactScanPercent));
  const scanPercentLabel = scanPercent === 0 && scanning.scanEntries > 0 ? '<1%' : `${scanPercent}%`;
  const scanProgressWidth = scanPercent === 0 && scanning?.scanEntries > 0 ? 0.5 : scanPercent;
  if (!scanning && !interrupted && !activeJobs && !(processing.failed || 0)) return null;
  return html`<section class="activity-panel" aria-live="polite">
    <strong>后台活动</strong>
    ${scanning && html`<div class="activity-row"><span class="activity-pulse"></span><p><b>正在扫描文件${scanPercent == null ? '' : ` · ${scanPercentLabel}`}</b><small>${formatCount(scanning.scanEntries)} 个条目 · ${formatCount(scanning.scanDirectories)} 个文件夹 · ${formatCount(scanning.scanFiles)} 个文件</small><span class=${`activity-progress ${scanPercent == null ? 'indeterminate' : ''}`} role="progressbar" aria-label="整体扫描进度" aria-valuemin="0" aria-valuemax="100" aria-valuenow=${scanPercent == null ? undefined : scanPercent}><i style=${scanPercent == null ? undefined : { width: `${scanProgressWidth}%` }}></i></span></p></div>`}
    ${!scanning && interrupted && html`<div class="activity-row interrupted"><span>!</span><p><b>上次扫描已中断</b><small>已保留 ${formatCount(interrupted.scanEntries)} 个条目，服务恢复后会从检查点继续</small></p></div>`}
    ${(activeJobs > 0 || processing.failed > 0) && html`<div class="activity-row"><span class=${processing.running ? 'activity-pulse' : ''}></span><p><b>媒体增强</b><small>${formatCount(processing.running)} 个处理中 · ${formatCount(processing.pending)} 个等待${processing.failed ? html` · <button class="failure-link" onClick=${onFailures}>${formatCount(processing.failed)} 个失败</button>` : ''}</small></p></div>`}
  </section>`;
}

function storageSummaryText(storages) {
  const scanning = storages.filter((item) => item.state === 'scanning').length;
  if (scanning) return `${scanning} 个正在扫描 · 可浏览`;
  const available = storages.filter((item) => item.state === 'online').length;
  const issues = storages.filter((item) => ['offline', 'error', 'interrupted'].includes(item.state)).length;
  return issues ? `${available} 个可用 · ${issues} 个异常` : `${available} 个在线`;
}

const textExtensions = new Set(['txt', 'md', 'nfo', 'srt', 'vtt', 'json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'log', 'csv', 'xml', 'html', 'css', 'js', 'ts', 'jsx', 'tsx', 'go', 'py', 'sh']);

function previewKind(item) {
  const mime = item.mime || '';
  const extension = (item.extension || '').toLowerCase();
  if (extension === 'ts' && (item.name.toLowerCase().endsWith('.d.ts') || item.size < 1024 * 1024)) return 'text';
  if (mime.startsWith('image/')) return 'image';
  if (mime.startsWith('audio/')) return 'audio';
  if (mime.startsWith('video/')) return 'video';
  if (mime === 'application/pdf' || extension === 'pdf') return 'pdf';
  if (mime.startsWith('text/') || ['application/json', 'application/xml', 'application/x-subrip'].includes(mime) || textExtensions.has(extension)) return 'text';
  return 'unknown';
}

function copyNameFor(item) {
  if (item.type === 'directory') return `${item.name} copy`;
  const dot = item.name.lastIndexOf('.');
  return dot > 0 ? `${item.name.slice(0, dot)} copy${item.name.slice(dot)}` : `${item.name} copy`;
}

function VideoPlayer({ url, startPositionMS = 0, onProgress }) {
  const videoRef = useRef(null);
  const [playerStatus, setPlayerStatus] = useState('正在缓冲视频…');
  useEffect(() => {
    const video = videoRef.current;
    if (!video || !url) return undefined;
    let hls;
    if (url.includes('.m3u8') && Hls.isSupported()) {
      hls = new Hls({ enableWorker: false, manifestLoadingMaxRetry: 12, manifestLoadingRetryDelay: 750, manifestLoadingMaxRetryTimeout: 3000 });
      hls.loadSource(url);
      hls.attachMedia(video);
      hls.on(Hls.Events.MANIFEST_PARSED, () => { setPlayerStatus(''); video.play().catch(() => {}); });
      hls.on(Hls.Events.ERROR, (_, data) => { if (data.fatal) setPlayerStatus(`暂时无法播放：${data.details}`); });
    } else {
      video.src = url;
      setPlayerStatus('');
    }
    const resume = () => { if (startPositionMS > 0 && Number.isFinite(video.duration)) video.currentTime = Math.min(startPositionMS / 1000, Math.max(0, video.duration - 2)); };
    video.addEventListener('loadedmetadata', resume, { once: true });
    return () => { hls?.destroy(); video.removeAttribute('src'); video.load(); };
  }, [url, startPositionMS]);
  return html`<div class="video-player"><video ref=${videoRef} controls autoplay preload="metadata" onTimeUpdate=${(event) => onProgress?.(event.currentTarget, false)} onEnded=${(event) => onProgress?.(event.currentTarget, true)}></video>${playerStatus && html`<span class="video-status">${playerStatus}</span>`}</div>`;
}

function mediaMetadata(media) {
  if (!media?.metadata) return {};
  if (typeof media.metadata === 'object') return media.metadata;
  try { return JSON.parse(media.metadata); } catch (_) { return {}; }
}

function mediaTypeLabel(type) {
  return ({ movie: '电影', series: '电视剧', season: '季', episode: '剧集', artist: '艺人', album: '专辑', track: '歌曲', photo: '照片', book: '图书' }[type] || '媒体');
}

function durationLabel(durationMS) {
  if (!durationMS) return '';
  const minutes = Math.round(durationMS / 60000);
  return minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分钟` : `${minutes} 分钟`;
}

function MediaPoster({ media, thumbnailURL }) {
  const metadata = mediaMetadata(media);
  const posterURL = metadata.posterPath ? `${API}/media/${encodeURIComponent(media.id)}/poster` : thumbnailURL;
  const hasPoster = Boolean(posterURL);
  const [attempt, setAttempt] = useState(0);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const retryTimer = useRef(null);
  useEffect(() => () => window.clearTimeout(retryTimer.current), []);
  const retry = () => {
    if (attempt >= 4) {
      setFailed(true);
      return;
    }
    window.clearTimeout(retryTimer.current);
    retryTimer.current = window.setTimeout(() => setAttempt((value) => value + 1), 2000);
  };
  const missing = !hasPoster || failed;
  return html`<div class=${`media-poster ${missing ? 'missing' : ''} ${hasPoster && !ready && !failed ? 'pending' : ''}`}>
    ${hasPoster && !failed && html`<img src=${`${posterURL}${posterURL.includes('?') ? '&' : '?'}v=${attempt}`} alt="" onLoad=${() => setReady(true)} onError=${retry}/>`}
    <span><${Icon} name=${media.type === 'album' || media.type === 'track' ? 'music' : 'movies'} size=${32}/></span>
  </div>`;
}

function ThumbnailImage({ item }) {
  const [attempt, setAttempt] = useState(0);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const retryTimer = useRef(null);
  useEffect(() => () => window.clearTimeout(retryTimer.current), []);
  const retry = () => {
    if (attempt >= 8) {
      setFailed(true);
      return;
    }
    window.clearTimeout(retryTimer.current);
    retryTimer.current = window.setTimeout(() => setAttempt((value) => value + 1), 2000);
  };
  const separator = item.links.thumbnail.includes('?') ? '&' : '?';
  return html`<span class=${`card-thumbnail ${!ready && !failed ? 'pending' : ''} ${failed ? 'failed' : ''}`}>
    ${!failed && html`<img src=${`${item.links.thumbnail}${separator}v=${attempt}`} alt="" loading="lazy" onLoad=${() => setReady(true)} onError=${retry}/>`}
    <i><${Icon} name="file" size=${30}/></i>
  </span>`;
}

function MediaHeader({ item, media, technical, actions }) {
  if (!media) return null;
  const metadata = mediaMetadata(media);
  const details = [mediaTypeLabel(media.type), media.year, metadata.voteAverage ? `TMDB ${metadata.voteAverage.toFixed(1)}` : '', durationLabel(technical?.durationMs)].filter(Boolean);
  const overview = metadata.overview || metadata.description;
  return html`<section class="media-header">
    <${MediaPoster} key=${`${media.id}:${metadata.posterPath || item?.links?.thumbnail || ''}`} media=${media} thumbnailURL=${item?.links?.thumbnail}/>
    <div class="media-copy"><p>${details.join(' · ')}</p><h1>${media.title || item.name}</h1>${metadata.originalTitle && metadata.originalTitle !== media.title && html`<small>${metadata.originalTitle}</small>`}${overview && html`<div class="media-overview">${overview}</div>`}<div class="media-match">${media.matchSource === 'tmdb' ? 'TMDB 已匹配' : media.matchSource === 'embedded' ? '来自文件标签' : '根据文件名识别'}${media.matchConfidence ? ` · ${Math.round(media.matchConfidence * 100)}%` : ''}</div></div>
    ${actions && html`<div class="media-actions">${actions}</div>`}
  </section>`;
}

function FileDetail({ item, media, technical, text, loading, error, playbackURL, startPositionMS, onPlay, onPlaybackProgress, onManage, onMatch }) {
  if (!item) return null;
  const kind = previewKind(item);
  const contentURL = item.links.content;
  const metadata = mediaMetadata(media);
  const facts = [
    ['类型', item.extension?.toUpperCase() || item.mime || '文件'], ['大小', formatSize(item.size)], ['修改时间', formatDate(item.modifiedAt)],
    technical?.container && ['封装', technical.container], technical?.width && technical?.height && ['画面', `${technical.width} × ${technical.height}`],
    technical?.videoCodec && ['视频编码', technical.videoCodec.toUpperCase()], technical?.audioCodec && ['音频编码', technical.audioCodec.toUpperCase()],
    durationLabel(technical?.durationMs) && ['时长', durationLabel(technical.durationMs)], metadata.music?.artist && ['艺人', metadata.music.artist], metadata.music?.album && ['专辑', metadata.music.album],
    metadata.authors?.length && ['作者', metadata.authors.join('、')], metadata.author && ['作者', metadata.author], metadata.language && ['语言', metadata.language],
    metadata.publisher && ['出版社', metadata.publisher], Number.isFinite(metadata.pageCount) && ['页数', `${metadata.pageCount} 页`],
  ].filter(Boolean);
  const actions = html`${kind === 'video' && html`<button class="detail-button" onClick=${onMatch}><${Icon} name="refresh" size=${17}/>${media ? '纠正匹配' : '识别媒体'}</button>`}<a class="detail-button primary" href=${contentURL} download=${item.name}><${Icon} name="download" size=${17}/>下载</a><button class="detail-button" onClick=${onManage}><${Icon} name="more" size=${17}/>管理</button>`;
  return html`<article class="file-detail">
    ${media ? html`<${MediaHeader} item=${item} media=${media} technical=${technical} actions=${actions}/>` : html`<header class="plain-detail-head"><div><p>${item.extension?.toUpperCase() || 'FILE'}</p><h1>${item.name}</h1><span>${item.mime || '未知文件类型'}</span></div><div class="media-actions">${actions}</div></header>`}
    <div class="detail-layout">
      <section class=${`detail-preview ${kind}`} aria-label="文件内容">
        ${!item.available ? html`<div class="preview-message"><h2>文件当前不可用</h2><p>重新连接存储并扫描后即可查看。</p></div>` : kind === 'image' ? html`<img src=${contentURL} alt=${item.name}/>` : kind === 'audio' ? html`<audio src=${contentURL} controls preload="metadata"></audio>` : kind === 'video' ? (playbackURL ? html`<${VideoPlayer} url=${playbackURL} startPositionMS=${startPositionMS} onProgress=${(video, played) => onPlaybackProgress?.({...item, mediaID: media?.id}, video, played)}/>` : html`<button class="play-button" onClick=${onPlay}><span>▶</span>${error || '播放视频'}</button>`) : kind === 'pdf' ? html`<iframe src=${contentURL} title=${item.name}></iframe>` : kind === 'text' ? (loading ? html`<div class="preview-message">正在读取文本…</div>` : error ? html`<div class="preview-message"><h2>无法预览文本</h2><p>${error}</p></div>` : html`<pre>${text}</pre>`) : html`<div class="preview-message"><div class="empty-icon"><${Icon} name="file" size=${30}/></div><h2>此格式没有内置预览</h2><p>仍可下载或管理这个文件。</p></div>`}
      </section>
      <aside class="detail-facts"><h2>文件信息</h2>${facts.map(([label, value]) => html`<div key=${label}><span>${label}</span><strong>${value}</strong></div>`)}</aside>
    </div>
  </article>`;
}

function MatchDialog({ item, media, query, setQuery, candidates, loading, saving, error, onSearch, onSelect, onUnmatch, onClose }) {
  return html`<div class="preview-scrim" onClick=${() => !saving && onClose()}><section class="action-dialog match-dialog" role="dialog" aria-modal="true" aria-labelledby="match-title" onClick=${(event) => event.stopPropagation()}>
    <header><div><strong id="match-title">识别媒体</strong><span>${item?.name}</span></div><button type="button" class="icon-button" onClick=${onClose} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header>
    <form class="match-search" onSubmit=${onSearch}><label>电影或电视剧名称<input value=${query} onInput=${(event) => setQuery(event.currentTarget.value)} maxlength="200" required/></label><button class="primary" disabled=${loading || saving}>${loading ? '正在搜索…' : '搜索'}</button></form>
    ${error && html`<p class="action-error" role="alert">${error}</p>`}
    <div class="candidate-list" aria-live="polite">${!loading && candidates.length === 0 ? html`<p>输入名称搜索 TMDB；选择结果后会锁定该匹配。</p>` : candidates.map((candidate) => html`<button key=${`${candidate.type}:${candidate.id}`} disabled=${saving} onClick=${() => onSelect(candidate)}><span><strong>${candidate.title}</strong><small>${[candidate.originalTitle && candidate.originalTitle !== candidate.title ? candidate.originalTitle : '', candidate.year, candidate.type === 'tv' ? '电视剧' : '电影'].filter(Boolean).join(' · ')}</small>${candidate.overview && html`<em>${candidate.overview}</em>`}</span><b>${saving ? '保存中…' : '选择'}</b></button>`)}</div>
    ${media && html`<footer class="match-footer"><button class="danger" disabled=${saving} onClick=${onUnmatch}>移除当前匹配</button><span>之后不会自动重新识别，仍可随时在这里恢复。</span></footer>`}
  </section></div>`;
}

function failureProcessorLabel(value) {
  return ({ epub_metadata: 'EPUB 元数据', image_metadata: '图片元数据', pdf_metadata: 'PDF 元数据', thumbnail: '图片/书籍缩略图', video_thumbnail: '视频缩略图', pdf_thumbnail: 'PDF 首页缩略图', ffprobe: '音视频分析', media_match: 'TMDB 匹配', poster: '海报下载', media_catalog: '媒体整理', other: '其他处理' }[value] || value);
}

function FailureDialog({ groups, loading, error, onClose }) {
  return html`<div class="preview-scrim" onClick=${onClose}><section class="action-dialog failure-dialog" role="dialog" aria-modal="true" aria-labelledby="failure-title" onClick=${(event) => event.stopPropagation()}>
    <header><div><strong id="failure-title">媒体增强失败</strong><span>仅影响派生信息，文件浏览、下载和管理不受影响</span></div><button type="button" class="icon-button" onClick=${onClose} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header>
    ${loading ? html`<p class="failure-empty">正在读取失败分类…</p>` : error ? html`<p class="action-error" role="alert">${error}</p>` : groups.length ? html`<div class="failure-groups">${groups.map((group) => html`<div key=${`${group.processor}:${group.extension}`}><span><strong>${failureProcessorLabel(group.processor)}</strong><small>${group.extension ? `${group.extension.toUpperCase()} 文件` : '未识别格式'}</small></span><b>${formatCount(group.count)}</b></div>`)}</div>` : html`<p class="failure-empty">当前没有失败任务。</p>`}
    <footer><span>通常表示文件损坏、格式伪装或外部工具无法解析；原文件不会被修改。</span><button onClick=${onClose}>知道了</button></footer>
  </section></div>`;
}

function routeEntryID() {
  const match = window.location.pathname.match(/^\/files\/([^/]+)$/);
  return match ? decodeURIComponent(match[1]) : null;
}

function Skeleton() {
  return html`<div class="skeleton-list" aria-label="正在加载">
    ${Array.from({ length: 8 }, (_, index) => html`<div class="skeleton-row" key=${index}><span></span><i></i><b></b></div>`)}
  </div>`;
}

function EmptyState({ searchTerm }) {
  return html`<div class="empty-state"><div class="empty-icon"><${Icon} name=${searchTerm ? 'search' : 'folder'} size=${30}/></div><h2>${searchTerm ? '没有找到匹配文件' : '这个目录是空的'}</h2><p>${searchTerm ? `尝试更换关键词，或在其他资料库中搜索。` : '扫描到的文件会显示在这里。'}</p></div>`;
}

function App() {
  const [libraries, setLibraries] = useState([]);
  const [storages, setStorages] = useState([]);
  const [processingStatus, setProcessingStatus] = useState({ pending: 0, running: 0, done: 0, failed: 0 });
  const [activeLibraryID, setActiveLibraryID] = useState(null);
  const [entry, setEntry] = useState(null);
  const [trail, setTrail] = useState([]);
  const [items, setItems] = useState([]);
  const [cursor, setCursor] = useState(null);
  const [loading, setLoading] = useState(true);
  const [moreLoading, setMoreLoading] = useState(false);
  const [error, setError] = useState('');
  const [navOpen, setNavOpen] = useState(false);
  const [view, setView] = useState(() => localStorage.getItem('files-go-view') || 'list');
  const [theme, setTheme] = useState(() => localStorage.getItem('files-go-theme') || (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'));
  const [searchQuery, setSearchQuery] = useState('');
  const [searchTerm, setSearchTerm] = useState('');
  const [entryMedia, setEntryMedia] = useState(null);
  const [technicalMedia, setTechnicalMedia] = useState(null);
  const [detailText, setDetailText] = useState('');
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [playbackURL, setPlaybackURL] = useState('');
  const [startPositionMS, setStartPositionMS] = useState(0);
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const [folderName, setFolderName] = useState('');
  const [manageItem, setManageItem] = useState(null);
  const [renameValue, setRenameValue] = useState('');
  const [actionError, setActionError] = useState('');
  const [actionSaving, setActionSaving] = useState(false);
  const [deleteArmed, setDeleteArmed] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [movingItem, setMovingItem] = useState(null);
  const [transferMode, setTransferMode] = useState('move');
  const [copyName, setCopyName] = useState('');
  const [destination, setDestination] = useState(null);
  const [destinationTrail, setDestinationTrail] = useState([]);
  const [destinationFolders, setDestinationFolders] = useState([]);
  const [destinationLoading, setDestinationLoading] = useState(false);
  const [matchOpen, setMatchOpen] = useState(false);
  const [matchQuery, setMatchQuery] = useState('');
  const [matchCandidates, setMatchCandidates] = useState([]);
  const [matchLoading, setMatchLoading] = useState(false);
  const [matchSaving, setMatchSaving] = useState(false);
  const [matchError, setMatchError] = useState('');
  const [failureOpen, setFailureOpen] = useState(false);
  const [failureGroups, setFailureGroups] = useState([]);
  const [failureLoading, setFailureLoading] = useState(false);
  const [failureError, setFailureError] = useState('');
  const loadMoreSentinelRef = useRef(null);
  const loadingMoreRef = useRef(false);
  const playbackSessionRef = useRef(null);
  const entryRequestRef = useRef(0);

  const activeLibrary = useMemo(() => libraries.find((item) => item.id === activeLibraryID), [libraries, activeLibraryID]);
  const storageByID = useMemo(() => Object.fromEntries(storages.map((item) => [item.id, item])), [storages]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'light' ? '#f5f7fb' : '#0b0d12');
  }, [theme]);

  useEffect(() => {
    if (!createFolderOpen && !manageItem && !movingItem && !matchOpen && !failureOpen) return undefined;
    const frame = window.requestAnimationFrame(() => document.querySelector('.action-dialog input, .action-dialog button')?.focus());
    const close = (event) => {
      if (event.key !== 'Escape' || actionSaving || matchSaving) return;
      setCreateFolderOpen(false);
      setManageItem(null);
      setMovingItem(null);
      setMatchOpen(false);
      setFailureOpen(false);
    };
    document.body.classList.add('preview-open');
    window.addEventListener('keydown', close);
    return () => {
      window.cancelAnimationFrame(frame);
      document.body.classList.remove('preview-open');
      window.removeEventListener('keydown', close);
    };
  }, [createFolderOpen, manageItem, movingItem, matchOpen, failureOpen, actionSaving, matchSaving]);

  const loadNavigation = useCallback(async () => {
    const [libraryData, storageData] = await Promise.all([request(`${API}/libraries`), request(`${API}/storages`)]);
    setLibraries(libraryData.items || []);
    setStorages(storageData.items || []);
    request(`${API}/system/status`).then((statusData) => setProcessingStatus(statusData.processing || {})).catch(() => {});
    return libraryData.items || [];
  }, []);

  const loadOperationalStatus = useCallback(async () => {
    const [storageData, statusData] = await Promise.all([request(`${API}/storages`), request(`${API}/system/status`).catch(() => ({ processing: {} }))]);
    setStorages(storageData.items || []);
    setProcessingStatus(statusData.processing || {});
  }, []);

  const buildTrail = useCallback(async (current, libraryList, preferredLibraryID) => {
    const ancestors = [current];
    let node = current;
    for (let depth = 0; node.parentId && depth < 64; depth += 1) {
      node = await request(`${API}/entries/${encodeURIComponent(node.parentId)}`);
      ancestors.unshift(node);
    }
    const sourceMap = new Map();
    libraryList.forEach((library) => library.sources?.forEach((source) => source.entryId && sourceMap.set(source.entryId, library)));
    let library = libraryList.find((item) => item.id === preferredLibraryID);
    let sourceIndex = library ? ancestors.findIndex((item) => library.sources?.some((source) => source.entryId === item.id)) : -1;
    if (sourceIndex < 0) {
      sourceIndex = ancestors.findIndex((item) => sourceMap.has(item.id));
      if (sourceIndex >= 0) library = sourceMap.get(ancestors[sourceIndex].id);
    }
    if (sourceIndex < 0) sourceIndex = 0;
    if (library) setActiveLibraryID(library.id);
    return ancestors.slice(sourceIndex).map((item, index) => ({ ...item, label: index === 0 && library ? library.name : (item.name || '存储根目录') }));
  }, []);

  const stopPlayback = useCallback(() => {
    const session = playbackSessionRef.current;
    playbackSessionRef.current = null;
    setPlaybackURL('');
    setStartPositionMS(0);
    if (session) fetch(`${API}/playback/sessions/${encodeURIComponent(session)}`, { method: 'DELETE', keepalive: true }).catch(() => {});
  }, []);

  useEffect(() => () => {
    const session = playbackSessionRef.current;
    if (session) fetch(`${API}/playback/sessions/${encodeURIComponent(session)}`, { method: 'DELETE', keepalive: true }).catch(() => {});
  }, []);

  const openEntry = useCallback(async (id, { history = true, libraryID = activeLibraryID, libraryList = libraries } = {}) => {
    if (!id) return false;
    const requestID = ++entryRequestRef.current;
    setLoading(true);
    setError('');
    setSearchQuery('');
    setSearchTerm('');
    setNavOpen(false);
    setEntryMedia(null);
    setTechnicalMedia(null);
    setDetailText('');
    setDetailError('');
    setDetailLoading(false);
    stopPlayback();
    try {
      const current = await request(`${API}/entries/${encodeURIComponent(id)}`);
      if (requestID !== entryRequestRef.current) return false;
      setEntry(current);
      if (current.type === 'directory') {
        const children = await request(`${API}/entries/${encodeURIComponent(id)}/children?limit=100`);
        if (requestID !== entryRequestRef.current) return false;
        setItems(children.items || []);
        setCursor(children.cursor || null);
        request(`${API}/entries/${encodeURIComponent(id)}/media-item?optional=1`)
          .then((value) => { if (requestID === entryRequestRef.current) setEntryMedia(value?.id ? value : null); })
          .catch(() => {});
      } else {
        setItems([]);
        setCursor(null);
        const kind = previewKind(current);
        const technicalRequest = ['image', 'audio', 'video', 'pdf'].includes(kind)
          ? request(`${API}/entries/${encodeURIComponent(id)}/media?optional=1`).then((value) => value?.entryId ? value : null).catch(() => null)
          : Promise.resolve(null);
        const [media, technical] = await Promise.all([
          request(`${API}/entries/${encodeURIComponent(id)}/media-item?optional=1`).then((value) => value?.id ? value : null).catch(() => null),
          technicalRequest,
        ]);
        setEntryMedia(media);
        setTechnicalMedia(technical);
        if (current.available && kind === 'text') {
          setDetailLoading(true);
          try {
            const response = await fetch(`${API}/entries/${encodeURIComponent(id)}/text`);
            if (!response.ok) {
              const body = await response.json().catch(() => ({}));
              throw new Error(body.error?.message || `请求失败 (${response.status})`);
            }
            setDetailText(await response.text());
          } catch (reason) {
            setDetailError(reason.message || '无法读取文本');
          } finally {
            setDetailLoading(false);
          }
        }
      }
      setTrail(await buildTrail(current, libraryList, libraryID));
      if (history) window.history.pushState({ entryID: id, libraryID }, '', `/files/${encodeURIComponent(id)}`);
      return true;
    } catch (reason) {
      if (requestID === entryRequestRef.current) setError(reason.message || '无法打开条目');
      return false;
    } finally {
      if (requestID === entryRequestRef.current) setLoading(false);
    }
  }, [activeLibraryID, buildTrail, libraries, stopPlayback]);

  const openLibrary = useCallback(async (library) => {
    setActiveLibraryID(library.id);
    const source = library.sources?.find((item) => item.entryId);
    if (source) {
      await openEntry(source.entryId, { libraryID: library.id });
    } else {
      setEntry(null);
      setItems([]);
      setTrail([{ label: library.name }]);
      setError('资料库尚未完成首次扫描，请稍后刷新。');
      setNavOpen(false);
    }
  }, [openEntry]);

  useEffect(() => {
    let cancelled = false;
    loadNavigation().then(async (libraryList) => {
      if (cancelled) return;
      const routeID = routeEntryID();
      if (routeID) {
        const opened = await openEntry(routeID, { history: false, libraryList });
        if (!opened) {
          const first = libraryList.find((library) => library.sources?.some((source) => source.entryId));
          if (first) {
            setActiveLibraryID(first.id);
            await openEntry(first.sources.find((source) => source.entryId).entryId, { history: true, libraryID: first.id, libraryList });
          }
        }
      } else {
        const first = libraryList.find((library) => library.sources?.some((source) => source.entryId));
        if (first) {
          setActiveLibraryID(first.id);
          await openEntry(first.sources.find((source) => source.entryId).entryId, { history: false, libraryID: first.id, libraryList });
        } else {
          setLoading(false);
          setError('正在建立文件索引，请稍后刷新。');
        }
      }
    }).catch((reason) => {
      setError(reason.message || '无法连接服务');
      setLoading(false);
    });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    const onPopState = () => {
      const id = routeEntryID();
      if (id) openEntry(id, { history: false, libraryID: window.history.state?.libraryID });
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, [openEntry]);

  useEffect(() => {
    const scanActive = storages.some((storage) => storage.state === 'scanning');
    const processingActive = (processingStatus.pending || 0) + (processingStatus.running || 0) > 0;
    if (!scanActive && !processingActive) return undefined;
    const timer = window.setInterval(async () => {
      try {
        if (scanActive) {
          const libraryList = await loadNavigation();
          const first = libraryList.find((library) => library.sources?.some((source) => source.entryId));
          if (first && !entry) {
            setActiveLibraryID(first.id);
            await openEntry(first.sources.find((source) => source.entryId).entryId, { history: false, libraryID: first.id, libraryList });
          }
        } else {
          await loadOperationalStatus();
        }
      } catch (_) {
        // The visible connection error and manual retry remain available.
      }
    }, 2500);
    return () => window.clearInterval(timer);
  }, [entry, loadNavigation, loadOperationalStatus, openEntry, processingStatus.pending, processingStatus.running, storages]);

  const loadMore = useCallback(async () => {
    if (!cursor || !entry || loadingMoreRef.current) return;
    loadingMoreRef.current = true;
    setMoreLoading(true);
    try {
      const data = await request(`${API}/entries/${encodeURIComponent(entry.id)}/children?limit=100&after=${encodeURIComponent(cursor)}`);
      setItems((current) => [...current, ...(data.items || [])]);
      setCursor(data.cursor || null);
    } catch (reason) {
      setError(reason.message || '无法加载更多文件');
    } finally {
      loadingMoreRef.current = false;
      setMoreLoading(false);
    }
  }, [cursor, entry]);

  useEffect(() => {
    const target = loadMoreSentinelRef.current;
    if (!target || !cursor || !entry || typeof IntersectionObserver === 'undefined') return undefined;
    const observer = new IntersectionObserver((records) => {
      if (records.some((record) => record.isIntersecting)) loadMore();
    }, { rootMargin: '320px 0px' });
    observer.observe(target);
    return () => observer.disconnect();
  }, [cursor, entry, loadMore]);

  const performSearch = async (value = searchQuery) => {
    const query = value.trim();
    if (!query) {
      if (entry) await openEntry(entry.id, { history: false, libraryID: activeLibraryID });
      return;
    }
    setLoading(true);
    setError('');
    try {
      const parameters = new URLSearchParams({ q: query, limit: '100' });
      if (activeLibraryID) parameters.set('library', activeLibraryID);
      const data = await request(`${API}/search?${parameters}`);
      setItems(data.items || []);
      setCursor(null);
      setSearchQuery(query);
      setSearchTerm(query);
    } catch (reason) {
      setError(reason.message || '搜索失败');
    } finally {
      setLoading(false);
    }
  };

  const submitSearch = (event) => {
    event.preventDefault();
    performSearch();
  };

  const clearSearch = () => {
    setSearchQuery('');
    setSearchTerm('');
    if (entry) openEntry(entry.id, { history: false, libraryID: activeLibraryID });
  };

  const startDetailPlayback = async () => {
    if (!entry || entry.type !== 'file' || !entry.available) return;
    setDetailError('');
    try {
      const playbackResult = await request(`${API}/playback/${encodeURIComponent(entry.id)}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ containers: ['mp4', 'webm', 'ogg'], videoCodecs: ['h264', 'vp8', 'vp9', 'av1'], audioCodecs: ['aac', 'mp3', 'opus', 'vorbis'], hls: true }) });
      const match = playbackResult.url?.match(/\/playback\/sessions\/([^/]+)\//);
      if (match) playbackSessionRef.current = match[1];
      if (entryMedia) {
        const state = await request(`${API}/media/${encodeURIComponent(entryMedia.id)}/playback-state`).catch(() => null);
        setStartPositionMS(state?.positionMs || 0);
      }
      setPlaybackURL(playbackResult.url);
    } catch (reason) {
      setDetailError(reason.message || '无法开始播放');
    }
  };

  const updatePlaybackProgress = (item, video, played) => {
    if (!item.mediaID || !Number.isFinite(video.currentTime)) return;
    const now = Date.now();
    if (!played && now - Number(video.dataset.savedAt || 0) < 5000) return;
    video.dataset.savedAt = String(now);
    fetch(`${API}/media/${encodeURIComponent(item.mediaID)}/playback-state`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' }, keepalive: true,
      body: JSON.stringify({ positionMs: Math.round(video.currentTime * 1000), played }),
    }).catch(() => {});
  };

  const loadMediaCandidates = async (value = matchQuery) => {
    if (!entry) return;
    setMatchLoading(true);
    setMatchError('');
    try {
      const parameters = new URLSearchParams();
      if (value.trim()) parameters.set('q', value.trim());
      const data = await request(`${API}/entries/${encodeURIComponent(entry.id)}/media-candidates?${parameters}`);
      setMatchCandidates(data.items || []);
      if (!(data.items || []).length) setMatchError('没有找到候选结果，请尝试更简短或更准确的名称。');
    } catch (reason) {
      setMatchCandidates([]);
      setMatchError(reason.message || '无法搜索媒体信息');
    } finally {
      setMatchLoading(false);
    }
  };

  const openMatchDialog = () => {
    setMatchOpen(true);
    setMatchQuery(entryMedia?.title || '');
    setMatchCandidates([]);
    setMatchError('');
    loadMediaCandidates('');
  };

  const searchMediaCandidates = (event) => {
    event.preventDefault();
    loadMediaCandidates(matchQuery);
  };

  const selectMediaCandidate = async (candidate) => {
    if (!entry) return;
    setMatchSaving(true);
    setMatchError('');
    try {
      const media = await request(`${API}/entries/${encodeURIComponent(entry.id)}/media-item`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ candidateId: candidate.id, candidateType: candidate.type }),
      });
      setEntryMedia(media);
      setMatchOpen(false);
    } catch (reason) {
      setMatchError(reason.message || '无法保存媒体匹配');
    } finally {
      setMatchSaving(false);
    }
  };

  const unmatchMedia = async () => {
    if (!entry || !window.confirm('移除这个文件的媒体匹配？之后不会自动重新识别。')) return;
    setMatchSaving(true);
    setMatchError('');
    try {
      await request(`${API}/entries/${encodeURIComponent(entry.id)}/media-item`, { method: 'DELETE' });
      setEntryMedia(null);
      setMatchOpen(false);
    } catch (reason) {
      setMatchError(reason.message || '无法移除媒体匹配');
    } finally {
      setMatchSaving(false);
    }
  };

  const openFailureDetails = async () => {
    setFailureOpen(true);
    setFailureLoading(true);
    setFailureError('');
    try {
      const data = await request(`${API}/system/failures`);
      setFailureGroups(data.items || []);
    } catch (reason) {
      setFailureGroups([]);
      setFailureError(reason.message || '无法读取失败详情');
    } finally {
      setFailureLoading(false);
    }
  };

  const createFolder = async (event) => {
    event.preventDefault();
    if (!entry || !folderName.trim()) return;
    setActionSaving(true);
    setActionError('');
    try {
      await request(`${API}/entries/${encodeURIComponent(entry.id)}/directories`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: folderName.trim() }),
      });
      setCreateFolderOpen(false);
      setFolderName('');
      await openEntry(entry.id, { history: false, libraryID: activeLibraryID });
    } catch (reason) {
      setActionError(reason.message || '无法创建文件夹');
    } finally {
      setActionSaving(false);
    }
  };

  const openManage = (item) => {
    setManageItem(item);
    setRenameValue(item.name);
    setActionError('');
    setDeleteArmed(false);
  };

  const renameEntry = async (event) => {
    event.preventDefault();
    if (!manageItem || !renameValue.trim()) return;
    setActionSaving(true);
    setActionError('');
    try {
      const updated = await request(`${API}/entries/${encodeURIComponent(manageItem.id)}`, {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: renameValue.trim() }),
      });
      setItems((current) => current.map((item) => item.id === updated.id ? updated : item));
      setEntry((current) => current?.id === updated.id ? updated : current);
      setManageItem(null);
    } catch (reason) {
      setActionError(reason.message || '无法重命名');
    } finally {
      setActionSaving(false);
    }
  };

  const deleteManagedEntry = async () => {
    if (!manageItem) return;
    setActionSaving(true);
    setActionError('');
    try {
      await request(`${API}/entries/${encodeURIComponent(manageItem.id)}`, { method: 'DELETE' });
      setItems((current) => current.filter((item) => item.id !== manageItem.id));
      if (entry?.id === manageItem.id) {
        const parentID = entry.parentId;
        setManageItem(null);
        stopPlayback();
        if (parentID) await openEntry(parentID, { history: true, libraryID: activeLibraryID });
        return;
      }
      setManageItem(null);
    } catch (reason) {
      setActionError(reason.message || '无法删除');
    } finally {
      setActionSaving(false);
    }
  };

  const uploadFiles = async (event) => {
    const files = Array.from(event.currentTarget.files || []);
    event.currentTarget.value = '';
    if (!entry || files.length === 0) return;
    setUploading(true);
    setError('');
    try {
      for (const file of files) {
        const form = new FormData();
        form.append('file', file, file.name);
        const created = await request(`${API}/entries/${encodeURIComponent(entry.id)}/files`, { method: 'POST', body: form });
        setItems((current) => [created, ...current]);
      }
    } catch (reason) {
      setError(reason.message || '上传失败');
    } finally {
      setUploading(false);
    }
  };

  const loadDestination = async (id, excludedID = movingItem?.id) => {
    setDestinationLoading(true);
    setActionError('');
    try {
      const [folder, children] = await Promise.all([
        request(`${API}/entries/${encodeURIComponent(id)}`),
        request(`${API}/entries/${encodeURIComponent(id)}/children?limit=500`),
      ]);
      setDestination(folder);
      setDestinationFolders((children.items || []).filter((item) => item.type === 'directory' && item.id !== excludedID));
      setDestinationTrail(await buildTrail(folder, libraries, activeLibraryID));
    } catch (reason) {
      setActionError(reason.message || '无法读取目标目录');
    } finally {
      setDestinationLoading(false);
    }
  };

  const openTransfer = (mode) => {
    if (!manageItem) return;
    const item = manageItem;
    const rootID = activeLibrary?.sources?.find((source) => source.entryId)?.entryId || entry?.id;
    setMovingItem(item);
    setTransferMode(mode);
    setCopyName(mode === 'copy' ? copyNameFor(item) : item.name);
    setManageItem(null);
    setDestination(null);
    setDestinationFolders([]);
    setDestinationTrail([]);
    if (rootID) loadDestination(rootID, item.id);
  };

  const confirmTransfer = async () => {
    if (!movingItem || !destination) return;
    setActionSaving(true);
    setActionError('');
    try {
      const updated = transferMode === 'copy'
        ? await request(`${API}/entries/${encodeURIComponent(movingItem.id)}/copies`, {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ parentId: destination.id, name: copyName.trim() }),
        })
        : await request(`${API}/entries/${encodeURIComponent(movingItem.id)}`, {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ parentId: destination.id }),
        });
      setItems((current) => transferMode === 'copy'
        ? (!searchTerm && entry?.id === updated.parentId ? [updated, ...current] : current)
        : (!searchTerm && entry?.id !== updated.parentId ? current.filter((item) => item.id !== updated.id) : current.map((item) => item.id === updated.id ? updated : item)));
      setMovingItem(null);
    } catch (reason) {
      setActionError(reason.message || (transferMode === 'copy' ? '无法复制条目' : '无法移动条目'));
    } finally {
      setActionSaving(false);
    }
  };

  const refresh = async () => {
    setError('');
    try {
      const libraryList = await loadNavigation();
      if (searchTerm) await performSearch(searchTerm);
      else if (entry) {
        await openEntry(entry.id, { history: false, libraryID: activeLibraryID });
      }
      else {
        const library = libraryList.find((item) => item.id === activeLibraryID) || libraryList[0];
        if (library) openLibrary(library);
      }
    } catch (reason) {
      setError(reason.message || '刷新失败');
    }
  };

  const rescan = async () => {
    const source = activeLibrary?.sources?.[0];
    if (!source) return;
    if (storageByID[source.storageId]?.state === 'scanning') return;
    try {
      await request(`${API}/storages/${encodeURIComponent(source.storageId)}/scan`, { method: 'POST' });
      await loadNavigation();
    } catch (reason) {
      setError(reason.message || '无法开始扫描');
    }
  };

  const setViewMode = (mode) => {
    setView(mode);
    localStorage.setItem('files-go-view', mode);
  };

  const toggleTheme = () => {
    const next = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);
    localStorage.setItem('files-go-theme', next);
  };

  const activeStorage = activeLibrary?.sources?.[0] ? storageByID[activeLibrary.sources[0].storageId] : null;
  const scanActive = activeStorage?.state === 'scanning';
  const directoryActions = entry?.type === 'directory' && !searchTerm && html`<div class="head-meta"><span>${`${items.length}${cursor ? '+' : ''}`} 个项目</span><label class=${`scan-button upload-button ${uploading ? 'disabled' : ''}`}><${Icon} name="upload" size=${16}/>${uploading ? '正在上传…' : '上传'}<input type="file" multiple disabled=${uploading} onChange=${uploadFiles}/></label><button class="scan-button" onClick=${() => { setFolderName(''); setActionError(''); setCreateFolderOpen(true); }}><${Icon} name="plus" size=${16}/>新建文件夹</button><button class="scan-button" disabled=${scanActive} onClick=${rescan}><${Icon} name="refresh" size=${16}/>${scanActive ? '正在扫描…' : '重新扫描'}</button></div>`;

  return html`<div class="app-shell">
    <div class=${`nav-scrim ${navOpen ? 'visible' : ''}`} onClick=${() => setNavOpen(false)}></div>
    <aside class=${`sidebar ${navOpen ? 'open' : ''}`}>
      <div class="brand"><span class="brand-mark"><${Icon} name="brand" size=${22}/></span><strong>files<span>·go</span></strong><button class="icon-button close-nav" onClick=${() => setNavOpen(false)} aria-label="关闭导航"><${Icon} name="close"/></button></div>
      <div class="side-label">资料库</div>
      <nav class="library-nav" aria-label="资料库">
        ${libraries.map((library) => {
          return html`<div key=${library.id} class="library-item"><button class=${`library-link ${library.id === activeLibraryID ? 'active' : ''}`} onClick=${() => openLibrary(library)}>
            <span class="library-icon"><${Icon} name=${iconForLibrary(library.type)}/></span>
            <span class="library-copy"><strong>${library.name}</strong></span>
          </button></div>`;
        })}
      </nav>
      <${ActivityPanel} storages=${storages} processing=${processingStatus} onFailures=${openFailureDetails}/>
      <div class="storage-summary">
        <span class="summary-icon"><${Icon} name="drive"/></span>
        <div><strong>${storages.length || '—'} 个存储</strong><span>${storageSummaryText(storages)}</span></div>
      </div>
    </aside>

    <main class="main-panel">
      <header class="topbar">
        <button class="icon-button menu-button" onClick=${() => setNavOpen(true)} aria-label="打开导航"><${Icon} name="menu"/></button>
        <div class="breadcrumb" aria-label="当前位置">
          ${trail.map((item, index) => html`<span key=${item.id || index}>
            ${index > 0 && html`<${Icon} name="chevron" size=${15}/>`}
            <button onClick=${() => item.id && openEntry(item.id)} aria-current=${index === trail.length - 1 ? 'page' : undefined}>${item.label}</button>
          </span>`)}
        </div>
        <form class="search-form" role="search" onSubmit=${submitSearch}>
          <${Icon} name="search" size=${17}/>
          <input value=${searchQuery} onInput=${(event) => setSearchQuery(event.currentTarget.value)} placeholder="搜索文件" aria-label="搜索当前资料库" maxlength="200"/>
          ${searchTerm && html`<button type="button" onClick=${clearSearch} aria-label="清除搜索"><${Icon} name="x" size=${15}/></button>`}
        </form>
        <div class="top-actions">
          <button class="icon-button" onClick=${toggleTheme} aria-label=${theme === 'dark' ? '切换到亮色模式' : '切换到暗色模式'} title=${theme === 'dark' ? '亮色模式' : '暗色模式'}><${Icon} name=${theme === 'dark' ? 'sun' : 'moon'}/></button>
          <button class="icon-button" onClick=${refresh} aria-label="刷新"><${Icon} name="refresh"/></button>
          <div class="view-switch" role="group" aria-label="显示方式">
            <button class=${view === 'list' ? 'active' : ''} onClick=${() => setViewMode('list')} aria-label="列表"><${Icon} name="list" size=${18}/></button>
            <button class=${view === 'grid' ? 'active' : ''} onClick=${() => setViewMode('grid')} aria-label="网格"><${Icon} name="grid" size=${18}/></button>
          </div>
        </div>
      </header>

      ${entry?.type === 'directory' && entryMedia && !searchTerm ? html`<${MediaHeader} item=${entry} media=${entryMedia} actions=${directoryActions}/>` : entry?.type !== 'file' || searchTerm ? html`<section class="content-head">
        <div><p>${searchTerm ? `在 ${activeLibrary?.name || '所有文件'} 中搜索` : (activeLibrary?.type || 'files')}</p><h1>${searchTerm ? `“${searchTerm}”` : (entry?.name || activeLibrary?.name || '文件')}</h1></div>
        ${searchTerm ? html`<div class="head-meta"><span>${items.length} 个结果</span></div>` : directoryActions}
      </section>` : null}

      ${error && html`<div class="error-banner" role="alert"><span>${error}</span><button onClick=${refresh}>重试</button></div>`}

      <section class="browser" aria-live="polite">
        ${loading ? html`<${Skeleton}/>` : entry?.type === 'file' && !searchTerm ? html`<${FileDetail} item=${entry} media=${entryMedia} technical=${technicalMedia} text=${detailText} loading=${detailLoading} error=${detailError} playbackURL=${playbackURL} startPositionMS=${startPositionMS} onPlay=${startDetailPlayback} onPlaybackProgress=${updatePlaybackProgress} onManage=${() => openManage(entry)} onMatch=${openMatchDialog}/>` : items.length === 0 ? html`<${EmptyState} searchTerm=${searchTerm}/>` : view === 'list' ? html`
          <div class="file-table" role="table" aria-label="文件">
            <div class="table-head" role="row"><span>名称</span><span>大小</span><span>修改时间</span><span></span></div>
            ${items.map((item) => html`<div key=${item.id} class="file-row" role="row">
              <button class="file-main" onClick=${() => openEntry(item.id)}>
                <span class="name-cell" role="cell"><i class=${item.type === 'directory' ? 'folder' : 'document'}><${Icon} name=${item.type === 'directory' ? 'folder' : 'file'} size=${20}/></i><span><strong>${item.name}</strong><small>${item.type === 'directory' ? '文件夹' : item.media ? `${mediaTypeLabel(item.media.type)} · ${item.media.title}${item.media.year ? ` (${item.media.year})` : ''}` : (item.extension?.toUpperCase() || '文件')}</small></span>${!item.available && html`<em>不可用</em>`}</span>
                <span class="size-cell" role="cell">${item.type === 'directory' ? '—' : formatSize(item.size)}</span>
                <span class="date-cell" role="cell">${formatDate(item.modifiedAt)}</span>
              </button>
              <button class="row-action" onClick=${() => openManage(item)} aria-label=${`管理 ${item.name}`}><${Icon} name="more" size=${18}/></button>
            </div>`)}
          </div>` : html`
          <div class="file-grid">
            ${items.map((item) => html`<article key=${item.id} class="file-card">
              <button class="card-main" onClick=${() => openEntry(item.id)}>
                ${item.links.thumbnail ? html`<${ThumbnailImage} key=${item.id} item=${item}/>` : html`<span class=${`card-icon ${item.type}`}><${Icon} name=${item.type === 'directory' ? 'folder' : 'file'} size=${30}/></span>`}
                <strong title=${item.name}>${item.name}</strong><small>${item.type === 'directory' ? '文件夹' : item.media ? `${mediaTypeLabel(item.media.type)} · ${item.media.title}` : formatSize(item.size)}</small>${!item.available && html`<em>不可用</em>`}
              </button>
              <button class="card-action" onClick=${() => openManage(item)} aria-label=${`管理 ${item.name}`}><${Icon} name="more" size=${18}/></button>
            </article>`)}
          </div>`}
        ${cursor && html`<div class="load-more" ref=${loadMoreSentinelRef}><button onClick=${loadMore} disabled=${moreLoading}><${Icon} name="more"/>${moreLoading ? '正在加载…' : '加载更多'}</button></div>`}
      </section>
    </main>
    ${matchOpen && html`<${MatchDialog} item=${entry} media=${entryMedia} query=${matchQuery} setQuery=${setMatchQuery} candidates=${matchCandidates} loading=${matchLoading} saving=${matchSaving} error=${matchError} onSearch=${searchMediaCandidates} onSelect=${selectMediaCandidate} onUnmatch=${unmatchMedia} onClose=${() => setMatchOpen(false)}/>`}
    ${failureOpen && html`<${FailureDialog} groups=${failureGroups} loading=${failureLoading} error=${failureError} onClose=${() => setFailureOpen(false)}/>`}
    ${createFolderOpen && html`<div class="preview-scrim" onClick=${() => !actionSaving && setCreateFolderOpen(false)}><form class="action-dialog" role="dialog" aria-modal="true" aria-labelledby="create-folder-title" onSubmit=${createFolder} onClick=${(event) => event.stopPropagation()}><header><div><strong id="create-folder-title">新建文件夹</strong><span>在 ${entry?.name || activeLibrary?.name || '当前目录'} 中创建</span></div><button type="button" class="icon-button" onClick=${() => setCreateFolderOpen(false)} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header><label>名称<input value=${folderName} onInput=${(event) => setFolderName(event.currentTarget.value)} maxlength="255" required/></label>${actionError && html`<p class="action-error" role="alert">${actionError}</p>`}<footer><button type="button" onClick=${() => setCreateFolderOpen(false)}>取消</button><button class="primary" disabled=${actionSaving}>${actionSaving ? '正在创建…' : '创建'}</button></footer></form></div>`}
    ${manageItem && html`<div class="preview-scrim" onClick=${() => !actionSaving && setManageItem(null)}><form class="action-dialog" role="dialog" aria-modal="true" aria-labelledby="manage-entry-title" onSubmit=${renameEntry} onClick=${(event) => event.stopPropagation()}><header><div><strong id="manage-entry-title">管理条目</strong><span>${manageItem.type === 'directory' ? '文件夹' : formatSize(manageItem.size)}</span></div><button type="button" class="icon-button" onClick=${() => setManageItem(null)} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header><label>名称<input value=${renameValue} onInput=${(event) => { setRenameValue(event.currentTarget.value); setDeleteArmed(false); }} maxlength="255" required/></label>${actionError && html`<p class="action-error" role="alert">${actionError}</p>`}${deleteArmed && html`<p class="delete-warning" role="alert">此操作无法撤销。再次点击删除以确认。</p>`}<footer class="manage-footer"><button type="button" class="danger" onClick=${() => deleteArmed ? deleteManagedEntry() : setDeleteArmed(true)} disabled=${actionSaving}><${Icon} name="trash" size=${16}/>${deleteArmed ? '确认删除' : '删除'}</button><button type="button" onClick=${() => openTransfer('copy')}><${Icon} name="copy" size=${16}/>复制</button><button type="button" onClick=${() => openTransfer('move')}><${Icon} name="move" size=${16}/>移动</button><span></span><button type="button" onClick=${() => setManageItem(null)}>取消</button><button class="primary" disabled=${actionSaving || renameValue.trim() === manageItem.name}>${actionSaving ? '正在保存…' : '重命名'}</button></footer></form></div>`}
    ${movingItem && html`<div class="preview-scrim" onClick=${() => !actionSaving && setMovingItem(null)}><section class=${`action-dialog destination-dialog ${transferMode}`} role="dialog" aria-modal="true" aria-labelledby="transfer-entry-title" onClick=${(event) => event.stopPropagation()}><header><div><strong id="transfer-entry-title">${transferMode === 'copy' ? '复制' : '移动'}“${movingItem.name}”</strong><span>选择${transferMode === 'copy' ? '目标名称和' : ''}目标文件夹</span></div><button type="button" class="icon-button" onClick=${() => setMovingItem(null)} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header>${transferMode === 'copy' && html`<label class="copy-name">副本名称<input value=${copyName} onInput=${(event) => setCopyName(event.currentTarget.value)} maxlength="255" required/></label>`}<nav class="destination-trail" aria-label="目标位置">${destinationTrail.map((item, index) => html`<span key=${item.id || index}>${index > 0 && html`<${Icon} name="chevron" size=${14}/>`}<button onClick=${() => item.id && loadDestination(item.id)}>${item.label}</button></span>`)}</nav><div class="destination-list">${destinationLoading ? html`<p>正在读取文件夹…</p>` : destinationFolders.length ? destinationFolders.map((folder) => html`<button key=${folder.id} onClick=${() => loadDestination(folder.id)}><span class="folder"><${Icon} name="folder" size=${18}/></span>${folder.name}<${Icon} name="chevron" size=${15}/></button>`) : html`<p>这里没有子文件夹</p>`}</div>${actionError && html`<p class="action-error" role="alert">${actionError}</p>`}<footer><button type="button" onClick=${() => setMovingItem(null)}>取消</button><button class="primary" onClick=${confirmTransfer} disabled=${actionSaving || destinationLoading || !destination || !copyName.trim() || (transferMode === 'move' && destination.id === movingItem.parentId) || (transferMode === 'copy' && destination.id === movingItem.parentId && copyName.trim() === movingItem.name)}>${actionSaving ? (transferMode === 'copy' ? '正在复制…' : '正在移动…') : transferMode === 'move' && destination?.id === movingItem.parentId ? '已在此位置' : transferMode === 'copy' ? '复制到这里' : '移动到这里'}</button></footer></section></div>`}
  </div>`;
}

render(html`<${App}/>`, document.getElementById('app'));
