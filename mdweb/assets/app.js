(() => {
  const state = window.__MD_LIVE__ || {};
  const basePath = String(state.basePath || '').replace(/\/+$/, '');
  const apiUrl = (path) => basePath + path;

  const themeToggle = document.getElementById('theme-toggle');
  const savedTheme = localStorage.getItem('md-live-theme');
  const prefersDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  const applyTheme = (t) => {
    document.documentElement.setAttribute('data-theme', t);
    if (themeToggle) themeToggle.textContent = t === 'dark' ? 'Light' : 'Dark';
  };
  applyTheme(savedTheme || (prefersDark ? 'dark' : 'light'));
  if (themeToggle) {
    themeToggle.addEventListener('click', () => {
      const next = document.documentElement.getAttribute('data-theme') === 'dark' ? 'light' : 'dark';
      localStorage.setItem('md-live-theme', next);
      applyTheme(next);
    });
  }

  const docBody = document.getElementById('doc-body');
  if (!docBody) return;

  const syncPill = document.getElementById('sync-pill');
  const syncText = document.getElementById('sync-text');
  const liveSource = document.getElementById('live-source');
  const actionList = document.getElementById('action-list');
  const filePicker = document.getElementById('file-picker');
  const shell = document.getElementById('shell');
  const drawerToggle = document.getElementById('drawer-toggle');
  const toastEl = document.getElementById('toast');

  const MERMAID_LIGHT_THEME = {
    bg: '#ffffff',
    fg: '#0f172a',
    line: '#64748b',
    accent: '#1d4ed8',
    muted: '#475569',
  };
  const MERMAID_DARK_THEME = {
    bg: '#131922',
    fg: '#e5ecf4',
    line: '#64748b',
    accent: '#60a5fa',
    muted: '#94a3b8',
  };
  const MERMAID_CACHE_MAX = 256;
  const mermaidCache = new Map();

  function renderMermaidDual(rawCode) {
    const code = String(rawCode || '').trim();
    if (!code) return { error: 'Mermaid diagram source is empty.' };
    const hit = mermaidCache.get(code);
    if (hit) return hit;

    const bm = window.beautifulMermaid;
    if (!bm || typeof bm.renderMermaidSVG !== 'function') {
      return { error: 'beautifulMermaid renderer not loaded.' };
    }
    let result;
    try {
      const svgLight = bm.renderMermaidSVG(code, MERMAID_LIGHT_THEME);
      const svgDark = bm.renderMermaidSVG(code, MERMAID_DARK_THEME);
      result = { svgLight, svgDark };
    } catch (err) {
      result = { error: err instanceof Error ? err.message : String(err) };
    }
    if (mermaidCache.size >= MERMAID_CACHE_MAX) {
      const oldest = mermaidCache.keys().next().value;
      if (oldest !== undefined) mermaidCache.delete(oldest);
    }
    mermaidCache.set(code, result);
    return result;
  }

  function renderMermaidCard(card) {
    const stage = card.querySelector('.mermaid-stage');
    if (!stage || stage.querySelector('.mermaid-error')) return;
    const code = getCardCode(card);
    const rendered = renderMermaidDual(code);
    if (rendered.error) {
      stage.classList.remove('is-zoomable');
      stage.removeAttribute('title');
      stage.innerHTML =
        '<div class="mermaid-error"><strong>Mermaid render error:</strong> ' +
        escapeClient(rendered.error) +
        '</div>';
      return;
    }
    let lightEl = card.querySelector('.mermaid-light');
    let darkEl = card.querySelector('.mermaid-dark');
    if (!lightEl || !darkEl) {
      stage.innerHTML =
        '<div class="mermaid-svg mermaid-light"></div><div class="mermaid-svg mermaid-dark"></div>';
      lightEl = card.querySelector('.mermaid-light');
      darkEl = card.querySelector('.mermaid-dark');
    }
    lightEl.innerHTML = rendered.svgLight || '';
    darkEl.innerHTML = rendered.svgDark || '';
    stage.classList.add('is-zoomable');
    stage.setAttribute('title', 'Click to open PNG lightbox');
  }

  function renderAllMermaidCards(root) {
    const cards = (root || docBody).querySelectorAll('.mermaid-card');
    cards.forEach((card) => renderMermaidCard(card));
  }

  // Drawer setup
  const savedDrawer = localStorage.getItem('md-live-drawer') === '1';
  if (savedDrawer && shell && drawerToggle) {
    shell.classList.add('with-drawer');
    drawerToggle.classList.add('is-active');
  }
  if (drawerToggle && shell) {
    drawerToggle.addEventListener('click', () => {
      const open = shell.classList.toggle('with-drawer');
      drawerToggle.classList.toggle('is-active', open);
      localStorage.setItem('md-live-drawer', open ? '1' : '0');
    });
  }
  document.querySelectorAll('.drawer-tab').forEach((btn) => {
    btn.addEventListener('click', () => {
      document.querySelectorAll('.drawer-tab').forEach((b) => b.classList.remove('is-active'));
      btn.classList.add('is-active');
      const tab = btn.getAttribute('data-drawer-tab');
      const paneSource = document.getElementById('pane-source');
      const paneActions = document.getElementById('pane-actions');
      if (paneSource) paneSource.hidden = tab !== 'source';
      if (paneActions) paneActions.hidden = tab !== 'actions';
    });
  });

  // File picker navigation
  if (filePicker) {
    filePicker.addEventListener('change', () => {
      const nextFile = filePicker.value;
      window.location.href = apiUrl('/view?file=' + encodeURIComponent(nextFile));
    });
  }

  let toastTimer = null;
  function showToast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.classList.add('is-open');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toastEl.classList.remove('is-open'), 2200);
  }

  function setSyncState(mode, label) {
    if (!syncPill || !syncText) return;
    syncPill.classList.remove('is-saving', 'is-error');
    if (mode === 'saving') syncPill.classList.add('is-saving');
    if (mode === 'error') syncPill.classList.add('is-error');
    syncText.textContent = label;
  }

  function updateChecklistChip(stats) {
    const el = document.getElementById('stat-checklists');
    if (el && stats) {
      el.textContent = stats.checkboxChecked + ' / ' + stats.checkboxTotal + ' checked';
    }
  }

  function escapeClient(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  function prependAction(ev) {
    if (!actionList) return;
    const empty = actionList.querySelector('.action-empty');
    if (empty) empty.remove();
    const card = document.createElement('div');
    card.className = 'action-item';
    const time = new Date(ev.ts).toLocaleTimeString();
    card.innerHTML =
      '<div class="action-item-head"><span>' +
      escapeClient(ev.kind + ':' + ev.name) +
      '</span><span>L' +
      ev.line +
      ' · ' +
      escapeClient(time) +
      '</span></div>' +
      '<div>' +
      escapeClient(JSON.stringify(ev.oldValue)) +
      ' -> ' +
      escapeClient(JSON.stringify(ev.newValue)) +
      '</div>';
    actionList.prepend(card);
    while (actionList.children.length > 50) actionList.lastChild.remove();
  }

  // Serialized mutation queue so rapid clicks never race revision hashes
  let queue = Promise.resolve();
  let inFlightMutations = 0;

  function enqueueMutation(mutation, wrapEl) {
    inFlightMutations++;
    if (wrapEl) {
      wrapEl.classList.remove('is-saved');
      wrapEl.classList.add('is-saving');
    }
    setSyncState('saving', 'Saving...');
    queue = queue
      .then(() => sendMutation(mutation, wrapEl))
      .catch((err) => {
        setSyncState('error', 'Save failed');
        showToast(err.message || 'Save failed');
      })
      .finally(() => {
        inFlightMutations = Math.max(0, inFlightMutations - 1);
      });
    return queue;
  }

  async function sendMutation(mutation, wrapEl) {
    const res = await fetch(apiUrl('/api/mutate'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ file: state.file, revision: state.revision, mutation }),
    });
    const data = await res.json();
    if (res.status === 409) {
      showToast('File changed on disk; refreshing view');
      await refreshFromServer(false);
      return;
    }
    if (!res.ok) {
      throw new Error(data.error || 'Mutation failed');
    }
    state.revision = data.revision;
    if (data.stats) updateChecklistChip(data.stats);
    if (typeof data.source === 'string' && liveSource) liveSource.textContent = data.source;
    if (wrapEl) {
      wrapEl.classList.remove('is-saving');
      wrapEl.classList.add('is-saved');
      setTimeout(() => wrapEl.classList.remove('is-saved'), 1200);
    }
    setSyncState('ok', 'Saved · ' + String(state.revision || '').slice(0, 7));
    if (mutation.kind === 'action') {
      showToast('Action sent: ' + (data.event && data.event.label ? data.event.label : mutation.id));
    }
  }

  // Delegated UI handlers inside docBody
  const debounceTimers = new Map();

  docBody.addEventListener('change', (e) => {
    const target = e.target;
    if (!(target instanceof HTMLElement)) return;
    const kind = target.dataset.widgetKind;
    const id = target.dataset.widgetId;
    if (!kind || !id) return;

    if (kind === 'checkbox' && target instanceof HTMLInputElement) {
      const li = target.closest('.task-item');
      if (li) li.classList.toggle('is-checked', target.checked);
      enqueueMutation({ kind: 'checkbox', id, checked: target.checked }, target.closest('[data-widget-wrap]'));
    } else if (kind === 'select' && target instanceof HTMLSelectElement) {
      enqueueMutation({ kind: 'select', id, value: target.value }, target.closest('[data-widget-wrap]'));
    } else if (
      (kind === 'input' || kind === 'field') &&
      (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)
    ) {
      clearTimeout(debounceTimers.get(id));
      enqueueMutation({ kind, id, value: target.value }, target.closest('[data-widget-wrap]'));
    }
  });

  docBody.addEventListener('input', (e) => {
    const target = e.target;
    if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement)) return;
    const kind = target.dataset.widgetKind;
    const id = target.dataset.widgetId;
    if (kind !== 'input' && kind !== 'field') return;
    const wrap = target.closest('[data-widget-wrap]');
    if (wrap) wrap.classList.add('is-saving');
    clearTimeout(debounceTimers.get(id));
    debounceTimers.set(
      id,
      setTimeout(() => {
        enqueueMutation({ kind, id, value: target.value }, wrap);
      }, 320),
    );
  });

  // Mermaid helpers: rasterize deterministic SVG -> crisp high-DPI PNG, flip, download, lightbox
  function slugFile(name) {
    return (
      String(name || 'diagram')
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/^-+|-+$/g, '') || 'diagram'
    );
  }

  function currentThemeMode() {
    return document.documentElement.getAttribute('data-theme') === 'dark' ? 'dark' : 'light';
  }

  function getCardSvg(card, themeMode) {
    const mode = themeMode || currentThemeMode();
    const wrap = card.querySelector(mode === 'dark' ? '.mermaid-dark' : '.mermaid-light');
    return wrap ? wrap.innerHTML.trim() : '';
  }

  function getCardCode(card) {
    const codeEl = card.querySelector('.mermaid-source code');
    return codeEl ? codeEl.textContent || '' : '';
  }

  function getCardFileBase(card) {
    const title = card.dataset.mermaidTitle || card.dataset.mermaidId || 'diagram';
    const dir = card.dataset.mermaidDir;
    return slugFile(title) + (dir ? '-' + dir.toLowerCase() : '');
  }

  function triggerDownload(blob, filename) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function svgToPngBlob(svgText, themeMode) {
    return new Promise((resolve, reject) => {
      if (!svgText) {
        reject(new Error('No SVG available to rasterize.'));
        return;
      }
      // Strip remote @import url(...) so canvas rasterization is instant, offline, and untainted
      const cleanSvg = svgText.replace(/@import\s+url\([^)]+\)\s*;?/g, '');
      const parser = new DOMParser();
      const doc = parser.parseFromString(cleanSvg, 'image/svg+xml');
      const svgEl = doc.documentElement;
      const vb = (svgEl.getAttribute('viewBox') || '').trim().split(/[\s,]+/).map(Number);
      let w = parseFloat(svgEl.getAttribute('width') || '0') || (vb.length === 4 ? vb[2] : 800);
      let h = parseFloat(svgEl.getAttribute('height') || '0') || (vb.length === 4 ? vb[3] : 500);
      if (!w || !h) {
        w = 800;
        h = 500;
      }
      svgEl.setAttribute('width', String(w));
      svgEl.setAttribute('height', String(h));

      const serialized = new XMLSerializer().serializeToString(svgEl);
      const svgDataUrl = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(serialized);

      const pad = 28;
      const maxDim = Math.max(w + pad * 2, h + pad * 2);
      const targetScale = Math.min(4, Math.max(2.5, Math.min(3200 / maxDim, 4)));
      const outW = Math.round((w + pad * 2) * targetScale);
      const outH = Math.round((h + pad * 2) * targetScale);

      const img = new Image();
      img.onload = () => {
        const canvas = document.createElement('canvas');
        canvas.width = outW;
        canvas.height = outH;
        const ctx = canvas.getContext('2d');
        if (!ctx) {
          reject(new Error('Canvas 2D context unavailable.'));
          return;
        }
        ctx.fillStyle = themeMode === 'dark' ? '#131922' : '#ffffff';
        ctx.fillRect(0, 0, outW, outH);
        ctx.scale(targetScale, targetScale);
        ctx.drawImage(img, pad, pad, w, h);
        canvas.toBlob((blob) => {
          if (!blob) {
            reject(new Error('Failed to encode PNG.'));
            return;
          }
          resolve({ blob, width: outW, height: outH });
        }, 'image/png');
      };
      img.onerror = () => reject(new Error('Failed to load SVG into image for PNG export.'));
      img.src = svgDataUrl;
    });
  }

  async function flipMermaidCard(card) {
    const code = getCardCode(card);
    if (!code) return null;
    if (!card.dataset.originalCode) {
      card.dataset.originalCode = code;
    }
    const res = await fetch(apiUrl('/api/mermaid/flip'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code, action: 'flip' }),
    });
    const data = await res.json();
    if (!res.ok || data.error) {
      showToast(data.error || 'Could not flip diagram');
      return null;
    }
    const rendered = renderMermaidDual(data.code);
    if (rendered.error) {
      showToast(rendered.error);
      return null;
    }
    const codeEl = card.querySelector('.mermaid-source code');
    if (codeEl) codeEl.textContent = data.code;
    const lightEl = card.querySelector('.mermaid-light');
    const darkEl = card.querySelector('.mermaid-dark');
    if (lightEl && rendered.svgLight) lightEl.innerHTML = rendered.svgLight;
    if (darkEl && rendered.svgDark) darkEl.innerHTML = rendered.svgDark;
    card.dataset.mermaidDir = data.direction || '';
    const badge = card.querySelector('[data-mermaid-dir-badge]');
    if (badge && data.direction) badge.textContent = data.direction;
    const isDirty = data.code.trim() !== (card.dataset.originalCode || '').trim();
    const saveBtn = card.querySelector('[data-mermaid-save]');
    if (saveBtn) saveBtn.hidden = !isDirty;
    showToast('Flipped diagram to ' + (data.direction || 'opposite'));
    return data;
  }

  async function saveMermaidCard(card) {
    const id = card.dataset.mermaidId;
    const code = getCardCode(card);
    if (!id || !code) return;
    await enqueueMutation({ kind: 'mermaid', id, code }, null);
    card.dataset.originalCode = code;
    const saveBtn = card.querySelector('[data-mermaid-save]');
    if (saveBtn) saveBtn.hidden = true;
    showToast('Saved diagram orientation to source file');
  }

  async function downloadMermaid(card, format, themeMode) {
    const base = getCardFileBase(card);
    const mode = themeMode || currentThemeMode();
    if (format === 'mmd') {
      const code = getCardCode(card);
      triggerDownload(new Blob([code + '\n'], { type: 'text/plain;charset=utf-8' }), base + '.mmd');
      showToast('Downloaded ' + base + '.mmd');
      return;
    }
    if (format === 'svg') {
      const svg = getCardSvg(card, mode);
      if (!svg) return;
      triggerDownload(new Blob([svg], { type: 'image/svg+xml;charset=utf-8' }), base + '.svg');
      showToast('Downloaded ' + base + '.svg');
      return;
    }
    if (format === 'png') {
      try {
        const { blob } = await svgToPngBlob(getCardSvg(card, mode), mode);
        triggerDownload(blob, base + '.png');
        showToast('Downloaded ' + base + '.png');
      } catch (err) {
        showToast(err.message || 'PNG export failed');
      }
    }
  }

  // Lightbox controller (zoom + pan + PNG + flip + download)
  const lb = {
    el: document.getElementById('mermaid-lightbox'),
    title: document.getElementById('lb-title'),
    dir: document.getElementById('lb-dir'),
    dims: document.getElementById('lb-dims'),
    flipBtn: document.getElementById('lb-flip'),
    saveBtn: document.getElementById('lb-save'),
    themeBtn: document.getElementById('lb-theme'),
    zoomOutBtn: document.getElementById('lb-zoom-out'),
    zoomResetBtn: document.getElementById('lb-zoom-reset'),
    zoomInBtn: document.getElementById('lb-zoom-in'),
    zoomFitBtn: document.getElementById('lb-zoom-fit'),
    copyPngBtn: document.getElementById('lb-copy-png'),
    dlPngBtn: document.getElementById('lb-dl-png'),
    dlSvgBtn: document.getElementById('lb-dl-svg'),
    dlMmdBtn: document.getElementById('lb-dl-mmd'),
    closeBtn: document.getElementById('lb-close'),
    viewport: document.getElementById('lb-viewport'),
    canvas: document.getElementById('lb-canvas'),
    img: document.getElementById('lb-img'),
    card: null,
    theme: 'light',
    blob: null,
    blobUrl: null,
    naturalW: 0,
    naturalH: 0,
    scale: 1,
    fitScale: 1,
    tx: 0,
    ty: 0,
    dragging: false,
    dragX: 0,
    dragY: 0,
  };

  function applyLbTransform() {
    lb.canvas.style.transform = 'translate(' + lb.tx + 'px, ' + lb.ty + 'px) scale(' + lb.scale + ')';
    lb.zoomResetBtn.textContent = Math.round(lb.scale * 100) + '%';
  }

  function fitLightbox() {
    const rect = lb.viewport.getBoundingClientRect();
    if (!lb.naturalW || !lb.naturalH || !rect.width || !rect.height) return;
    const padX = 56;
    const padY = 64;
    const s = Math.min((rect.width - padX) / lb.naturalW, (rect.height - padY) / lb.naturalH, 1);
    lb.fitScale = Math.max(0.15, s);
    lb.scale = lb.fitScale;
    lb.tx = 0;
    lb.ty = 0;
    applyLbTransform();
  }

  function setLbZoom(nextScale, pivotX, pivotY) {
    const clamped = Math.min(6, Math.max(0.15, nextScale));
    if (typeof pivotX === 'number' && typeof pivotY === 'number') {
      const rect = lb.viewport.getBoundingClientRect();
      const cx = pivotX - (rect.left + rect.width / 2);
      const cy = pivotY - (rect.top + rect.height / 2);
      const ratio = clamped / lb.scale;
      lb.tx = cx - (cx - lb.tx) * ratio;
      lb.ty = cy - (cy - lb.ty) * ratio;
    }
    lb.scale = clamped;
    applyLbTransform();
  }

  function syncLbButtons() {
    if (!lb.card) return;
    lb.title.textContent = lb.card.dataset.mermaidTitle || 'Diagram';
    const flippable = lb.card.dataset.mermaidFlippable === '1';
    const dir = lb.card.dataset.mermaidDir || '';
    lb.flipBtn.hidden = !flippable;
    lb.dir.hidden = !flippable || !dir;
    if (dir) lb.dir.textContent = dir;
    const cardSaveBtn = lb.card.querySelector('[data-mermaid-save]');
    lb.saveBtn.hidden = !cardSaveBtn || cardSaveBtn.hidden;
    lb.themeBtn.textContent = lb.theme === 'dark' ? 'Light PNG' : 'Dark PNG';
  }

  async function renderLightboxPng(resetFit) {
    if (!lb.card) return;
    syncLbButtons();
    const svg = getCardSvg(lb.card, lb.theme);
    if (!svg) return;
    try {
      const { blob, width, height } = await svgToPngBlob(svg, lb.theme);
      if (lb.blobUrl) URL.revokeObjectURL(lb.blobUrl);
      lb.blob = blob;
      lb.blobUrl = URL.createObjectURL(blob);
      lb.naturalW = width;
      lb.naturalH = height;
      lb.dims.textContent = width + ' \u00d7 ' + height + ' PNG';
      lb.img.onload = () => {
        if (resetFit) fitLightbox();
      };
      lb.img.src = lb.blobUrl;
    } catch (err) {
      showToast(err.message || 'Could not render PNG preview');
    }
  }

  async function openLightbox(card) {
    if (!lb.el) return;
    lb.card = card;
    lb.theme = currentThemeMode();
    lb.el.hidden = false;
    document.body.style.overflow = 'hidden';
    await renderLightboxPng(true);
  }

  function closeLightbox() {
    if (!lb.el || lb.el.hidden) return;
    lb.el.hidden = true;
    document.body.style.overflow = '';
    if (lb.blobUrl) {
      URL.revokeObjectURL(lb.blobUrl);
      lb.blobUrl = null;
    }
    lb.card = null;
  }

  if (lb.el) {
    lb.closeBtn.addEventListener('click', closeLightbox);
    lb.zoomInBtn.addEventListener('click', () => setLbZoom(lb.scale * 1.25));
    lb.zoomOutBtn.addEventListener('click', () => setLbZoom(lb.scale / 1.25));
    lb.zoomResetBtn.addEventListener('click', () => {
      lb.scale = 1;
      lb.tx = 0;
      lb.ty = 0;
      applyLbTransform();
    });
    lb.zoomFitBtn.addEventListener('click', fitLightbox);
    lb.themeBtn.addEventListener('click', () => {
      lb.theme = lb.theme === 'dark' ? 'light' : 'dark';
      renderLightboxPng(false);
    });
    lb.flipBtn.addEventListener('click', async () => {
      if (!lb.card) return;
      const res = await flipMermaidCard(lb.card);
      if (res) await renderLightboxPng(true);
    });
    lb.saveBtn.addEventListener('click', async () => {
      if (!lb.card) return;
      await saveMermaidCard(lb.card);
      syncLbButtons();
    });
    lb.dlPngBtn.addEventListener('click', () => {
      if (!lb.card) return;
      if (lb.blob) {
        const base = getCardFileBase(lb.card);
        triggerDownload(lb.blob, base + '.png');
        showToast('Downloaded ' + base + '.png');
      } else {
        downloadMermaid(lb.card, 'png', lb.theme);
      }
    });
    lb.dlSvgBtn.addEventListener('click', () => lb.card && downloadMermaid(lb.card, 'svg', lb.theme));
    lb.dlMmdBtn.addEventListener('click', () => lb.card && downloadMermaid(lb.card, 'mmd', lb.theme));
    lb.copyPngBtn.addEventListener('click', async () => {
      if (!lb.blob || !navigator.clipboard || typeof ClipboardItem === 'undefined') {
        showToast('Clipboard PNG copy not supported in this browser; use PNG download');
        return;
      }
      try {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': lb.blob })]);
        showToast('Copied PNG image to clipboard');
      } catch {
        showToast('Clipboard blocked; use PNG download');
      }
    });

    lb.viewport.addEventListener(
      'wheel',
      (e) => {
        e.preventDefault();
        const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15;
        setLbZoom(lb.scale * factor, e.clientX, e.clientY);
      },
      { passive: false },
    );

    lb.viewport.addEventListener('mousedown', (e) => {
      if (e.button !== 0) return;
      lb.dragging = true;
      lb.dragX = e.clientX - lb.tx;
      lb.dragY = e.clientY - lb.ty;
      lb.viewport.classList.add('is-dragging');
    });
    window.addEventListener('mousemove', (e) => {
      if (!lb.dragging) return;
      lb.tx = e.clientX - lb.dragX;
      lb.ty = e.clientY - lb.dragY;
      applyLbTransform();
    });
    window.addEventListener('mouseup', () => {
      if (!lb.dragging) return;
      lb.dragging = false;
      lb.viewport.classList.remove('is-dragging');
    });
    lb.viewport.addEventListener('dblclick', () => {
      if (Math.abs(lb.scale - lb.fitScale) < 0.05) {
        lb.scale = Math.max(1, lb.fitScale * 2);
        lb.tx = 0;
        lb.ty = 0;
        applyLbTransform();
      } else {
        fitLightbox();
      }
    });
    lb.viewport.addEventListener('click', (e) => {
      if (e.target === lb.viewport) closeLightbox();
    });

    window.addEventListener('keydown', (e) => {
      if (lb.el.hidden) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        closeLightbox();
      } else if (e.key === '+' || e.key === '=') {
        e.preventDefault();
        setLbZoom(lb.scale * 1.25);
      } else if (e.key === '-') {
        e.preventDefault();
        setLbZoom(lb.scale / 1.25);
      } else if (e.key === '0') {
        e.preventDefault();
        fitLightbox();
      } else if (e.key === '1') {
        e.preventDefault();
        lb.scale = 1;
        lb.tx = 0;
        lb.ty = 0;
        applyLbTransform();
      } else if (e.key.toLowerCase() === 'f' && !lb.flipBtn.hidden) {
        e.preventDefault();
        lb.flipBtn.click();
      }
    });
  }

  docBody.addEventListener('click', (e) => {
    // Clicking the diagram stage opens PNG lightbox directly
    if (e.target instanceof Element) {
      const stage = e.target.closest('.mermaid-stage.is-zoomable');
      if (stage) {
        const card = stage.closest('.mermaid-card');
        if (card) {
          openLightbox(card);
          return;
        }
      }
    }

    const btn = e.target instanceof Element ? e.target.closest('button') : null;
    if (!btn) return;

    // Radio pill
    if (btn.dataset.widgetKind === 'radio') {
      const id = btn.dataset.widgetId;
      const value = btn.dataset.radioValue;
      const group = btn.closest('.widget-radio-group');
      if (group) {
        group.querySelectorAll('.md-radio-pill').forEach((p) => {
          const on = p === btn;
          p.classList.toggle('is-selected', on);
          p.setAttribute('aria-pressed', on ? 'true' : 'false');
        });
      }
      enqueueMutation({ kind: 'radio', id, value }, group);
      return;
    }

    // Live action button
    if (btn.dataset.widgetKind === 'action') {
      enqueueMutation({ kind: 'action', id: btn.dataset.widgetId }, null);
      return;
    }

    // Mermaid Diagram / Source toggle
    if (btn.dataset.mermaidView) {
      const card = btn.closest('.mermaid-card');
      if (!card) return;
      const mode = btn.dataset.mermaidView;
      card.querySelectorAll('[data-mermaid-view]').forEach((b) => b.classList.toggle('is-active', b === btn));
      const stageEl = card.querySelector('.mermaid-stage');
      const sourceEl = card.querySelector('.mermaid-source');
      if (stageEl) stageEl.hidden = mode !== 'diagram';
      if (sourceEl) sourceEl.hidden = mode !== 'source';
      return;
    }

    // Mermaid Flip orientation
    if ('mermaidFlip' in btn.dataset) {
      const card = btn.closest('.mermaid-card');
      if (card) flipMermaidCard(card);
      return;
    }

    // Mermaid Save flipped orientation to disk
    if ('mermaidSave' in btn.dataset) {
      const card = btn.closest('.mermaid-card');
      if (card) saveMermaidCard(card);
      return;
    }

    // Mermaid Lightbox button
    if ('mermaidLightbox' in btn.dataset) {
      const card = btn.closest('.mermaid-card');
      if (card) openLightbox(card);
      return;
    }

    // Mermaid Download PNG / SVG / MMD
    if (btn.dataset.mermaidDl) {
      const card = btn.closest('.mermaid-card');
      if (card) downloadMermaid(card, btn.dataset.mermaidDl);
      return;
    }

    // Copy code block
    if (btn.classList.contains('copy-code-btn')) {
      const codeEl = btn.closest('.code-card') && btn.closest('.code-card').querySelector('pre code');
      if (codeEl && navigator.clipboard) {
        navigator.clipboard.writeText(codeEl.textContent || '').then(() => {
          btn.textContent = 'Copied';
          setTimeout(() => {
            btn.textContent = 'Copy';
          }, 1200);
        });
      }
    }
  });

  // Initial synchronous Mermaid render
  renderAllMermaidCards(docBody);

  // External file change live reload via SSE
  async function refreshFromServer(preserveFocus) {
    const activeEl = document.activeElement;
    const activeId = activeEl instanceof HTMLElement ? activeEl.dataset.widgetId : null;
    const selStart =
      activeEl instanceof HTMLInputElement || activeEl instanceof HTMLTextAreaElement
        ? activeEl.selectionStart
        : null;
    const selEnd =
      activeEl instanceof HTMLInputElement || activeEl instanceof HTMLTextAreaElement
        ? activeEl.selectionEnd
        : null;

    const res = await fetch(apiUrl('/api/doc?file=' + encodeURIComponent(state.file)));
    if (!res.ok) return;
    const data = await res.json();
    if (data.revision === state.revision) return;

    state.revision = data.revision;
    docBody.innerHTML = data.html;
    renderAllMermaidCards(docBody);
    if (typeof data.toc === 'string' && shell) {
      const existingToc = shell.querySelector('nav.toc');
      if (data.toc) {
        if (existingToc) {
          existingToc.outerHTML = data.toc;
        } else {
          shell.insertAdjacentHTML('afterbegin', data.toc);
        }
        shell.classList.add('has-toc');
      } else {
        if (existingToc) existingToc.remove();
        shell.classList.remove('has-toc');
      }
    }
    if (liveSource && typeof data.source === 'string') liveSource.textContent = data.source;
    updateChecklistChip(data.stats);
    setSyncState('ok', 'Synced · ' + String(state.revision || '').slice(0, 7));

    if (preserveFocus && activeId) {
      const next = docBody.querySelector('[data-widget-id="' + CSS.escape(activeId) + '"]');
      if (next instanceof HTMLElement) {
        next.focus();
        if ((next instanceof HTMLInputElement || next instanceof HTMLTextAreaElement) && selStart !== null) {
          next.setSelectionRange(selStart, selEnd !== null ? selEnd : selStart);
        }
      }
    }
  }

  if (state.file) {
    const fileEvents = new EventSource(apiUrl('/api/events?file=' + encodeURIComponent(state.file)));
    fileEvents.addEventListener('doc:updated', (ev) => {
      try {
        const payload = JSON.parse(ev.data);
        if (payload.revision === state.revision || inFlightMutations > 0) return;
        refreshFromServer(true);
      } catch {}
    });

    fetch(apiUrl('/api/actions?file=' + encodeURIComponent(state.file)))
      .then((r) => r.json())
      .then((list) => {
        if (Array.isArray(list)) list.forEach(prependAction);
      })
      .catch(() => {});

    const actionEvents = new EventSource(apiUrl('/api/actions/stream?file=' + encodeURIComponent(state.file)));
    actionEvents.addEventListener('action', (ev) => {
      try {
        prependAction(JSON.parse(ev.data));
      } catch {}
    });
  }
})();
