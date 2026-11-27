(() => {
  const entriesEl = document.getElementById('index-entries');
  if (!entriesEl) return;

  const state = window.__MD_LIVE__ || {};
  const basePath = String(state.basePath || '').replace(/\/+$/, '');
  const apiUrl = (path) => basePath + path;

  const sourcesWrap = document.getElementById('index-sources-wrap');
  const countPill = document.getElementById('index-count-pill');
  const filterInput = document.getElementById('index-filter');
  const filterMeta = document.getElementById('index-filter-meta');
  const refreshBtn = document.getElementById('index-refresh-btn');
  const toastEl = document.getElementById('toast');

  let snapshot = state.snapshot || { sources: [], entries: [], count: 0 };
  let toastTimer = null;

  function showToast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.classList.add('is-open');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => toastEl.classList.remove('is-open'), 2600);
  }

  function esc(s) {
    return String(s || '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function formatDate(iso) {
    if (!iso) return '—';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '—';
    const pad = (n) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  function renderSources(sources) {
    if (!sourcesWrap) return;
    if (!Array.isArray(sources) || sources.length === 0) {
      sourcesWrap.innerHTML = '';
      return;
    }
    const chips = sources.map((src) => {
      const label = src.kind === 'scan' && src.recursive ? 'scan · recursive' : String(src.kind || '');
      return `<div class="source-chip" data-source-id="${esc(src.id)}">` +
        `<span class="source-kind">${esc(label)}</span>` +
        `<span class="source-path" title="${esc(src.path)}">${esc(src.path)}</span>` +
        `<button type="button" class="mini-btn source-remove-btn" data-remove-source="${esc(src.id)}" title="Remove source">Remove</button>` +
        `</div>`;
    }).join('');
    sourcesWrap.innerHTML = `<h2 class="index-section-title">Sources</h2><div class="index-sources">${chips}</div>`;
  }

  function renderEntries(entries) {
    const query = filterInput ? filterInput.value.trim().toLowerCase() : '';
    const terms = query ? query.split(/\s+/).filter(Boolean) : [];
    const list = Array.isArray(entries) ? entries : [];
    const filtered = list.filter((ent) => {
      if (terms.length === 0) return true;
      const isPaired = Boolean(ent.paired || (ent.markdownPath && ent.htmlPath));
      const kindTok = isPaired ? 'paired md html' : (ent.htmlPath ? 'html' : 'md markdown');
      const hay = [ent.title, ent.dir, ent.markdownPath, ent.htmlPath, kindTok].join(' ').toLowerCase();
      return terms.every((t) => hay.includes(t));
    });

    if (countPill) countPill.textContent = `${list.length} indexed`;
    if (filterMeta) {
      filterMeta.textContent = terms.length > 0 ? `${filtered.length} of ${list.length} shown · Newest first` : 'Newest first';
    }

    if (filtered.length === 0) {
      entriesEl.innerHTML = list.length === 0
        ? '<p class="index-empty">No indexed documents yet. Add a single file, add a directory, or scan a base directory above.</p>'
        : '<p class="index-empty">No indexed documents match the current filter.</p>';
      return;
    }

    const rows = filtered.map((ent) => {
      const isPaired = Boolean(ent.paired || (ent.markdownPath && ent.htmlPath));
      const badgeCls = isPaired ? 'badge-paired' : (ent.htmlPath ? 'badge-html' : 'badge-md');
      const badgeText = isPaired ? 'MD + HTML' : (ent.htmlPath ? 'HTML' : 'Markdown');
      const dateStr = formatDate(ent.sortTime);
      const dateTitle = `Updated: ${formatDate(ent.updatedAt)} | Created: ${formatDate(ent.createdAt)}`;
      let links = '';
      if (ent.markdownPath) {
        links += `<a class="entry-link entry-link-md" href="${esc(basePath)}/?file=${encodeURIComponent(ent.markdownPath)}">Markdown</a>`;
      }
      if (ent.htmlPath) {
        links += `<a class="entry-link entry-link-html" href="${esc(basePath)}/html?file=${encodeURIComponent(ent.htmlPath)}">HTML</a>`;
      }
      return `<li class="index-entry-row">` +
        `<div class="entry-main">` +
          `<div class="entry-title-row"><span class="entry-title">${esc(ent.title)}</span><span class="entry-badge ${badgeCls}">${badgeText}</span></div>` +
          `<div class="entry-meta"><span class="entry-date" title="${esc(dateTitle)}">${esc(dateStr)}</span><span class="entry-dir" title="${esc(ent.dir)}">${esc(ent.dir)}</span></div>` +
        `</div>` +
        `<div class="entry-actions">${links}</div>` +
        `</li>`;
    }).join('');
    entriesEl.innerHTML = `<ul class="index-entry-list">${rows}</ul>`;
  }

  function applySnapshot(next) {
    snapshot = next || { sources: [], entries: [], count: 0 };
    renderSources(snapshot.sources);
    renderEntries(snapshot.entries);
  }

  async function postJSON(endpoint, payload, successMsg) {
    try {
      const resp = await fetch(apiUrl(endpoint), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload || {}),
      });
      const data = await resp.json();
      if (!resp.ok) {
        showToast(data.error || `Request failed (${resp.status})`);
        return false;
      }
      applySnapshot(data);
      if (successMsg) showToast(successMsg);
      return true;
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Request failed');
      return false;
    }
  }

  const addFileForm = document.getElementById('form-add-file');
  if (addFileForm) {
    addFileForm.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      const input = document.getElementById('input-add-file');
      const path = input ? input.value.trim() : '';
      if (!path) return;
      if (await postJSON('/api/index/add', { path, mode: 'file' }, 'Added file to index')) {
        if (input) input.value = '';
      }
    });
  }

  const addDirForm = document.getElementById('form-add-dir');
  if (addDirForm) {
    addDirForm.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      const input = document.getElementById('input-add-dir');
      const path = input ? input.value.trim() : '';
      if (!path) return;
      if (await postJSON('/api/index/add', { path, mode: 'dir' }, 'Added directory to index')) {
        if (input) input.value = '';
      }
    });
  }

  const scanForm = document.getElementById('form-scan-dir');
  if (scanForm) {
    scanForm.addEventListener('submit', async (ev) => {
      ev.preventDefault();
      const input = document.getElementById('input-scan-dir');
      const recBox = document.getElementById('input-scan-recursive');
      const dir = input ? input.value.trim() : '';
      if (!dir) return;
      const recursive = recBox ? recBox.checked : true;
      if (await postJSON('/api/index/scan', { dir, recursive }, 'Scanned base directory')) {
        if (input) input.value = '';
      }
    });
  }

  if (sourcesWrap) {
    sourcesWrap.addEventListener('click', (ev) => {
      const btn = ev.target && ev.target.closest ? ev.target.closest('[data-remove-source]') : null;
      if (!btn) return;
      const id = btn.getAttribute('data-remove-source');
      if (id) postJSON('/api/index/remove', { id }, 'Removed source from index');
    });
  }

  if (refreshBtn) {
    refreshBtn.addEventListener('click', () => {
      postJSON('/api/index/refresh', {}, 'Refreshed index from disk');
    });
  }

  if (filterInput) {
    filterInput.addEventListener('input', () => {
      renderEntries(snapshot.entries);
    });
  }
})();
