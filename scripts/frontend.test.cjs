const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

class Element {
  constructor() { this.value = ''; this.textContent = ''; this.hidden = false; this.disabled = false; this.dataset = {}; this.children = []; this.events = {}; this.scrollTop = 0; this.scrollHeight = 0; this.clientHeight = 0; }
  addEventListener(name, handler) { (this.events[name] ||= []).push(handler); }
  emit(name, event = {}) { for (const handler of this.events[name] || []) handler(event); }
  append(...children) { this.children.push(...children); }
  appendChild(child) { this.append(child); }
  replaceChildren(...children) { this.children = children; }
  add(option) { this.append(option); if (!this.value) this.value = option.value; }
  setAttribute() {}
  remove() {}
  click() { this.emit('click'); }
}

const scan = (id) => ({ id, status: 'completed', total_bytes: 30, finished_at: '2026-11-16T00:00:00Z' });
async function settle() { for (let i = 0; i < 8; i++) await new Promise(setImmediate); }

async function mount({ active = false, handler } = {}) {
  const elements = new Map();
  const requests = [];
  const timers = [];
  const getElement = (id) => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  const window = {
    location: { search: '', origin: 'http://localhost', pathname: '/' },
    history: { replaceState() {} }, addEventListener() {},
    requestAnimationFrame(callback) { callback(); },
    setTimeout(callback) { timers.push(callback); return timers.length; },
  };
  const fetch = async (input, options = {}) => {
    const url = new URL(input, 'http://localhost'); requests.push({ url, options });
    let data;
    if (handler) data = await handler(url, options);
    if (data === undefined) {
      if (url.pathname.endsWith('/config')) data = { analyze_root: '/root', max_children_per_query: 2, current_scan: active ? { id: 3, status: 'running' } : scan(2), latest_completed_scan: scan(2) };
      else if (url.pathname === '/api/v1/scans') data = { scans: [scan(2), scan(1)] };
      else if (url.pathname.endsWith('/diagnostics')) data = { warnings: [{ path: '/root/private', operation: 'readdir', message: 'permission denied' }], exclude_patterns: ['cache'], excluded_count: 1 };
      else if (url.pathname.endsWith('/explore')) {
        const offset = Number(url.searchParams.get('offset') || 0);
        data = { path: '/root', summary: { total_bytes: 30, allocated_bytes: 4096, returned_item_count: 2, matching_item_count: 4, is_result_truncated: true }, items: [], treemap: { children: [] }, has_more: offset === 0, next_offset: offset + 2 };
      } else if (url.pathname.endsWith('/largest')) data = { items: [], has_more: false };
      else if (url.pathname.endsWith('/diff')) data = { items: [{ path: '/root/test', change: 'added', before_bytes: 0, after_bytes: 20, delta_bytes: 20 }], has_more: false };
      else if (url.pathname.endsWith('/cancel')) data = {};
      else data = active && url.pathname.endsWith('/3') ? { id: 3, status: 'running' } : scan(Number(url.pathname.split('/').pop()));
    }
    return { ok: true, status: 200, json: async () => data };
  };
  const context = vm.createContext({ window, document: { getElementById: getElement, createElement: () => new Element(), body: new Element() },
    console: { log() {}, warn() {} }, URL, URLSearchParams, AbortController, fetch, setTimeout: window.setTimeout, clearTimeout() {},
    Option: function(text, value) { this.textContent = text; this.value = value; } });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/app.js'), 'utf8'), context);
  for (const renderer of ['renderBreadcrumb', 'renderItemList', 'renderTreemap']) window.DiskTreemapApp[renderer] = () => {};
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../web/explore.js'), 'utf8'), context);
  await settle();
  return { el: getElement, requests, timers };
}

test('folder pagination sends offsets and filters reset the page', async () => {
  const { el, requests } = await mount();
  assert.equal(el('nextPageButton').disabled, false);
  assert.equal(el('summaryAllocated').textContent, '4.0 KB');
  el('nextPageButton').click(); await settle();
  assert.equal(requests.filter(r => r.url.pathname.endsWith('/explore')).at(-1).url.searchParams.get('offset'), '2');
  assert.equal(el('nextPageButton').disabled, true);
  assert.equal(el('previousPageButton').disabled, false);
  el('extensionInput').value = '.zip';
  el('modifiedBeforeInput').value = '2025-01-01';
  el('extensionInput').emit('change'); await settle();
  const query = requests.filter(r => r.url.pathname.endsWith('/explore')).at(-1).url.searchParams;
  assert.equal(query.get('offset'), '0'); assert.equal(query.get('extension'), '.zip');
  assert.equal(Number(query.get('modified_before')), new Date('2025-01-01T00:00:00').getTime() / 1000);
});

test('snapshot selection updates coverage and comparison uses explicit base', async () => {
  const { el, requests } = await mount();
  assert.equal(el('scanDiagnostics').hidden, false);
  assert.match(el('diagnosticsTitle').textContent, /1 warnings/);
  el('snapshotSelect').value = '1'; el('openSnapshotButton').click(); await settle();
  assert.ok(requests.some(r => r.url.pathname === '/api/v1/scans/1/explore'));
  el('compareSelect').value = '2'; el('compareButton').click(); await settle();
  assert.ok(requests.some(r => r.url.pathname === '/api/v1/scans/1/diff' && r.url.searchParams.get('base') === '2'));
  assert.match(el('analysisStatus').textContent, /Changes from #2 to #1/);
});

test('global file search forces root scope and excludes directories', async () => {
  const { el, requests } = await mount();
  el('typeFilter').value = 'dir'; el('typeFilter').emit('change'); await settle();
  el('globalSearchButton').click(); await settle();
  const query = requests.find(r => r.url.pathname.endsWith('/largest')).url.searchParams;
  assert.equal(query.get('path'), '/root'); assert.equal(query.get('type'), 'file');
});

test('active scan exposes cancellation and uses POST', async () => {
  const { el, requests } = await mount({ active: true });
  assert.equal(el('cancelScanButton').hidden, false);
  el('cancelScanButton').click(); await settle();
  assert.ok(requests.some(r => r.url.pathname === '/api/v1/scans/3/cancel' && r.options.method === 'POST'));
  assert.match(el('appAlertText').textContent, /Cancellation requested/);
});
