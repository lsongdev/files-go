import { html, render, useCallback, useEffect, useMemo, useState } from 'https://unpkg.com/htm@3.1.1/preact/standalone.module.js';

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

const textExtensions = new Set(['txt', 'md', 'nfo', 'srt', 'vtt', 'json', 'yaml', 'yml', 'toml', 'ini', 'conf', 'log', 'csv', 'xml', 'html', 'css', 'js', 'ts', 'jsx', 'tsx', 'go', 'py', 'sh']);

function previewKind(item) {
  const mime = item.mime || '';
  const extension = (item.extension || '').toLowerCase();
  if (mime.startsWith('image/')) return 'image';
  if (mime.startsWith('audio/')) return 'audio';
  if (mime.startsWith('video/')) return 'video';
  if (mime === 'application/pdf' || extension === 'pdf') return 'pdf';
  if (mime.startsWith('text/') || ['application/json', 'application/xml', 'application/x-subrip'].includes(mime) || textExtensions.has(extension)) return 'text';
  return 'unknown';
}

function Preview({ item, text, loading, error, onClose }) {
  if (!item) return null;
  const kind = previewKind(item);
  const contentURL = item.links.content;
  return html`<div class="preview-scrim" onClick=${onClose}>
    <section class="preview-dialog" role="dialog" aria-modal="true" aria-labelledby="preview-title" onClick=${(event) => event.stopPropagation()}>
      <header>
        <div><strong id="preview-title" title=${item.name}>${item.name}</strong><span>${item.extension?.toUpperCase() || item.mime || '文件'} · ${formatSize(item.size)}</span></div>
        <nav aria-label="预览操作">
          <a class="icon-button" href=${contentURL} download=${item.name} aria-label="下载"><${Icon} name="download" size=${18}/></a>
          <button class="icon-button" onClick=${onClose} aria-label="关闭预览" autofocus><${Icon} name="x" size=${18}/></button>
        </nav>
      </header>
      <div class=${`preview-body ${kind}`}>
        ${!item.available ? html`<div class="preview-message"><h2>文件当前不可用</h2><p>重新连接存储并扫描后即可预览。</p></div>` : kind === 'image' ? html`<img src=${contentURL} alt=${item.name}/>` : kind === 'audio' ? html`<audio src=${contentURL} controls preload="metadata"></audio>` : kind === 'video' ? html`<video src=${contentURL} controls preload="metadata"></video>` : kind === 'pdf' ? html`<iframe src=${contentURL} title=${item.name}></iframe>` : kind === 'text' ? (loading ? html`<div class="preview-message">正在读取文本…</div>` : error ? html`<div class="preview-message"><h2>无法预览文本</h2><p>${error}</p></div>` : html`<pre>${text}</pre>`) : html`<div class="preview-message"><div class="empty-icon"><${Icon} name="file" size=${30}/></div><h2>此格式没有内置预览</h2><p>${item.mime || '未知文件类型'}</p><a href=${contentURL} download=${item.name}>下载文件</a></div>`}
      </div>
    </section>
  </div>`;
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
  const [preview, setPreview] = useState(null);
  const [previewText, setPreviewText] = useState('');
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState('');
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const [folderName, setFolderName] = useState('');
  const [manageItem, setManageItem] = useState(null);
  const [renameValue, setRenameValue] = useState('');
  const [actionError, setActionError] = useState('');
  const [actionSaving, setActionSaving] = useState(false);
  const [deleteArmed, setDeleteArmed] = useState(false);

  const activeLibrary = useMemo(() => libraries.find((item) => item.id === activeLibraryID), [libraries, activeLibraryID]);
  const storageByID = useMemo(() => Object.fromEntries(storages.map((item) => [item.id, item])), [storages]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'light' ? '#f5f7fb' : '#0b0d12');
  }, [theme]);

  useEffect(() => {
    if (!preview) return undefined;
    const close = (event) => { if (event.key === 'Escape') setPreview(null); };
    document.body.classList.add('preview-open');
    window.addEventListener('keydown', close);
    return () => {
      document.body.classList.remove('preview-open');
      window.removeEventListener('keydown', close);
    };
  }, [preview]);

  useEffect(() => {
    if (!createFolderOpen && !manageItem) return undefined;
    const frame = window.requestAnimationFrame(() => document.querySelector('.action-dialog input')?.focus());
    const close = (event) => {
      if (event.key !== 'Escape' || actionSaving) return;
      setCreateFolderOpen(false);
      setManageItem(null);
    };
    document.body.classList.add('preview-open');
    window.addEventListener('keydown', close);
    return () => {
      window.cancelAnimationFrame(frame);
      document.body.classList.remove('preview-open');
      window.removeEventListener('keydown', close);
    };
  }, [createFolderOpen, manageItem, actionSaving]);

  const loadNavigation = useCallback(async () => {
    const [libraryData, storageData] = await Promise.all([request(`${API}/libraries`), request(`${API}/storages`)]);
    setLibraries(libraryData.items || []);
    setStorages(storageData.items || []);
    return libraryData.items || [];
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

  const openEntry = useCallback(async (id, { history = true, libraryID = activeLibraryID, libraryList = libraries } = {}) => {
    if (!id) return;
    setLoading(true);
    setError('');
    setSearchQuery('');
    setSearchTerm('');
    setNavOpen(false);
    try {
      const [current, children] = await Promise.all([
        request(`${API}/entries/${encodeURIComponent(id)}`),
        request(`${API}/entries/${encodeURIComponent(id)}/children?limit=100`),
      ]);
      setEntry(current);
      setItems(children.items || []);
      setCursor(children.cursor || null);
      setTrail(await buildTrail(current, libraryList, libraryID));
      if (history) window.history.pushState({ entryID: id, libraryID }, '', `/files/${encodeURIComponent(id)}`);
    } catch (reason) {
      setError(reason.message || '无法打开目录');
    } finally {
      setLoading(false);
    }
  }, [activeLibraryID, buildTrail, libraries]);

  const openLibrary = useCallback((library) => {
    setActiveLibraryID(library.id);
    const source = library.sources?.find((item) => item.entryId);
    if (source) {
      openEntry(source.entryId, { libraryID: library.id });
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
        await openEntry(routeID, { history: false, libraryList });
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
    if (!storages.some((storage) => storage.state === 'scanning')) return undefined;
    const timer = window.setInterval(async () => {
      try {
        const libraryList = await loadNavigation();
        const first = libraryList.find((library) => library.sources?.some((source) => source.entryId));
        if (first && !entry) {
          setActiveLibraryID(first.id);
          await openEntry(first.sources.find((source) => source.entryId).entryId, { history: false, libraryID: first.id, libraryList });
        }
      } catch (_) {
        // The visible connection error and manual retry remain available.
      }
    }, 2500);
    return () => window.clearInterval(timer);
  }, [entry, libraries, loadNavigation, openEntry, storages]);

  const loadMore = async () => {
    if (!cursor || !entry) return;
    setMoreLoading(true);
    try {
      const data = await request(`${API}/entries/${encodeURIComponent(entry.id)}/children?limit=100&after=${encodeURIComponent(cursor)}`);
      setItems((current) => [...current, ...(data.items || [])]);
      setCursor(data.cursor || null);
    } catch (reason) {
      setError(reason.message || '无法加载更多文件');
    } finally {
      setMoreLoading(false);
    }
  };

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

  const openFile = async (item) => {
    setPreview(item);
    setPreviewText('');
    setPreviewError('');
    if (previewKind(item) !== 'text' || !item.available) return;
    setPreviewLoading(true);
    try {
      const response = await fetch(`${API}/entries/${encodeURIComponent(item.id)}/text`);
      if (!response.ok) {
        const body = await response.json().catch(() => ({}));
        throw new Error(body.error?.message || `请求失败 (${response.status})`);
      }
      setPreviewText(await response.text());
    } catch (reason) {
      setPreviewError(reason.message || '无法读取文本');
    } finally {
      setPreviewLoading(false);
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
      if (preview?.id === manageItem.id) setPreview(null);
      setManageItem(null);
    } catch (reason) {
      setActionError(reason.message || '无法删除');
    } finally {
      setActionSaving(false);
    }
  };

  const refresh = async () => {
    setError('');
    try {
      const libraryList = await loadNavigation();
      if (searchTerm) await performSearch(searchTerm);
      else if (entry) await openEntry(entry.id, { history: false, libraryID: activeLibraryID });
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

  return html`<div class="app-shell">
    <div class=${`nav-scrim ${navOpen ? 'visible' : ''}`} onClick=${() => setNavOpen(false)}></div>
    <aside class=${`sidebar ${navOpen ? 'open' : ''}`}>
      <div class="brand"><span class="brand-mark"><${Icon} name="brand" size=${22}/></span><strong>files<span>·go</span></strong><button class="icon-button close-nav" onClick=${() => setNavOpen(false)} aria-label="关闭导航"><${Icon} name="close"/></button></div>
      <div class="side-label">资料库</div>
      <nav class="library-nav" aria-label="资料库">
        ${libraries.map((library) => {
          const source = library.sources?.[0];
          const storageState = source ? storageByID[source.storageId]?.state : 'unknown';
          return html`<button key=${library.id} class=${`library-link ${library.id === activeLibraryID ? 'active' : ''}`} onClick=${() => openLibrary(library)}>
            <span class="library-icon"><${Icon} name=${iconForLibrary(library.type)}/></span>
            <span class="library-copy"><strong>${library.name}</strong><small>${storageState === 'scanning' ? '正在扫描' : storageState === 'offline' ? '存储离线' : '文件资料库'}</small></span>
            <span class=${`status-dot ${storageState}`} title=${storageState}></span>
          </button>`;
        })}
      </nav>
      <div class="storage-summary">
        <span class="summary-icon"><${Icon} name="drive"/></span>
        <div><strong>${storages.length || '—'} 个存储</strong><span>${storages.filter((item) => item.state === 'online').length} 个在线</span></div>
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

      <section class="content-head">
        <div><p>${searchTerm ? `在 ${activeLibrary?.name || '所有文件'} 中搜索` : (activeLibrary?.type || 'files')}</p><h1>${searchTerm ? `“${searchTerm}”` : (entry?.name || activeLibrary?.name || '文件')}</h1></div>
        <div class="head-meta"><span>${items.length}${cursor ? '+' : ''} 个项目</span>${entry && !searchTerm && html`<button class="scan-button" onClick=${() => { setFolderName(''); setActionError(''); setCreateFolderOpen(true); }}><${Icon} name="plus" size=${16}/>新建文件夹</button>`}<button class="scan-button" onClick=${rescan}><${Icon} name="refresh" size=${16}/>重新扫描</button></div>
      </section>

      ${error && html`<div class="error-banner" role="alert"><span>${error}</span><button onClick=${refresh}>重试</button></div>`}

      <section class="browser" aria-live="polite">
        ${loading ? html`<${Skeleton}/>` : items.length === 0 ? html`<${EmptyState} searchTerm=${searchTerm}/>` : view === 'list' ? html`
          <div class="file-table" role="table" aria-label="文件">
            <div class="table-head" role="row"><span>名称</span><span>大小</span><span>修改时间</span><span></span></div>
            ${items.map((item) => html`<div key=${item.id} class="file-row" role="row">
              <button class="file-main" onClick=${() => item.type === 'directory' ? openEntry(item.id) : openFile(item)}>
                <span class="name-cell" role="cell"><i class=${item.type === 'directory' ? 'folder' : 'document'}><${Icon} name=${item.type === 'directory' ? 'folder' : 'file'} size=${20}/></i><span><strong>${item.name}</strong><small>${item.type === 'directory' ? '文件夹' : (item.extension?.toUpperCase() || '文件')}</small></span>${!item.available && html`<em>不可用</em>`}</span>
                <span class="size-cell" role="cell">${item.type === 'directory' ? '—' : formatSize(item.size)}</span>
                <span class="date-cell" role="cell">${formatDate(item.modifiedAt)}</span>
              </button>
              <button class="row-action" onClick=${() => openManage(item)} aria-label=${`管理 ${item.name}`}><${Icon} name="more" size=${18}/></button>
            </div>`)}
          </div>` : html`
          <div class="file-grid">
            ${items.map((item) => html`<article key=${item.id} class="file-card">
              <button class="card-main" onClick=${() => item.type === 'directory' ? openEntry(item.id) : openFile(item)}>
                <span class=${`card-icon ${item.type}`}><${Icon} name=${item.type === 'directory' ? 'folder' : 'file'} size=${30}/></span>
                <strong title=${item.name}>${item.name}</strong><small>${item.type === 'directory' ? '文件夹' : formatSize(item.size)}</small>${!item.available && html`<em>不可用</em>`}
              </button>
              <button class="card-action" onClick=${() => openManage(item)} aria-label=${`管理 ${item.name}`}><${Icon} name="more" size=${18}/></button>
            </article>`)}
          </div>`}
        ${cursor && html`<div class="load-more"><button onClick=${loadMore} disabled=${moreLoading}><${Icon} name="more"/>${moreLoading ? '正在加载…' : '加载更多'}</button></div>`}
      </section>
    </main>
    <${Preview} item=${preview} text=${previewText} loading=${previewLoading} error=${previewError} onClose=${() => setPreview(null)}/>
    ${createFolderOpen && html`<div class="preview-scrim" onClick=${() => !actionSaving && setCreateFolderOpen(false)}><form class="action-dialog" role="dialog" aria-modal="true" aria-labelledby="create-folder-title" onSubmit=${createFolder} onClick=${(event) => event.stopPropagation()}><header><div><strong id="create-folder-title">新建文件夹</strong><span>在 ${entry?.name || activeLibrary?.name || '当前目录'} 中创建</span></div><button type="button" class="icon-button" onClick=${() => setCreateFolderOpen(false)} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header><label>名称<input value=${folderName} onInput=${(event) => setFolderName(event.currentTarget.value)} maxlength="255" required/></label>${actionError && html`<p class="action-error" role="alert">${actionError}</p>`}<footer><button type="button" onClick=${() => setCreateFolderOpen(false)}>取消</button><button class="primary" disabled=${actionSaving}>${actionSaving ? '正在创建…' : '创建'}</button></footer></form></div>`}
    ${manageItem && html`<div class="preview-scrim" onClick=${() => !actionSaving && setManageItem(null)}><form class="action-dialog" role="dialog" aria-modal="true" aria-labelledby="manage-entry-title" onSubmit=${renameEntry} onClick=${(event) => event.stopPropagation()}><header><div><strong id="manage-entry-title">管理条目</strong><span>${manageItem.type === 'directory' ? '文件夹' : formatSize(manageItem.size)}</span></div><button type="button" class="icon-button" onClick=${() => setManageItem(null)} aria-label="关闭"><${Icon} name="x" size=${18}/></button></header><label>名称<input value=${renameValue} onInput=${(event) => { setRenameValue(event.currentTarget.value); setDeleteArmed(false); }} maxlength="255" required/></label>${actionError && html`<p class="action-error" role="alert">${actionError}</p>`}${deleteArmed && html`<p class="delete-warning" role="alert">此操作无法撤销。再次点击删除以确认。</p>`}<footer class="manage-footer"><button type="button" class="danger" onClick=${() => deleteArmed ? deleteManagedEntry() : setDeleteArmed(true)} disabled=${actionSaving}><${Icon} name="trash" size=${16}/>${deleteArmed ? '确认删除' : '删除'}</button><span></span><button type="button" onClick=${() => setManageItem(null)}>取消</button><button class="primary" disabled=${actionSaving || renameValue.trim() === manageItem.name}>${actionSaving ? '正在保存…' : '重命名'}</button></footer></form></div>`}
  </div>`;
}

render(html`<${App}/>`, document.getElementById('app'));
