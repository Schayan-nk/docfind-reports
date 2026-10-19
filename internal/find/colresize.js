// Columns made wider or narrower by dragging their edge, the way Word's
// tables do (asked for on 1405/07/12). Loaded by nav.js on every page, so
// every table with a header row gets it; a table opts out with data-noresize.
//
// The handle sits on each header cell's inline end -- the left edge in this
// right-to-left interface -- and works with a mouse, a pen or a finger. The
// first drag freezes the table's current widths (table-layout: fixed), so
// moving one column does not reflow all the others. Widths are kept per page
// and table on this device; a double click on a handle gives the table back
// its natural widths.
(function () {
  'use strict';
  if (window.__colResize) return;
  window.__colResize = true;

  const css = document.createElement('style');
  css.textContent =
    '.col-grip{position:absolute;top:0;bottom:0;inset-inline-end:-3px;width:7px;cursor:col-resize;z-index:2;touch-action:none}' +
    '.col-grip:hover,.col-grip.on{background:color-mix(in srgb,var(--accent,#c66) 45%,transparent)}' +
    'body.col-resizing,body.col-resizing *{cursor:col-resize!important;user-select:none!important}';
  document.head.appendChild(css);

  const store = {
    get(k) { try { return JSON.parse(localStorage.getItem(k) || 'null'); } catch (_) { return null; } },
    set(k, v) { try { if (v) localStorage.setItem(k, JSON.stringify(v)); else localStorage.removeItem(k); } catch (_) {} },
  };

  function headCells(table) {
    const row = table.tHead && table.tHead.rows[table.tHead.rows.length - 1];
    return row ? Array.from(row.cells) : [];
  }
  // A table is told apart from the page's others by its id, else its place
  // among them, and by its column headings -- so a report with different
  // columns does not inherit another's widths.
  function keyOf(table, cells) {
    const all = Array.from(document.querySelectorAll('table'));
    const name = table.id || ('t' + all.indexOf(table));
    const heads = cells.map(c => (c.textContent || '').trim().slice(0, 12)).join('|');
    return 'colw:' + location.pathname + ':' + name + ':' + heads;
  }

  function freeze(table, cells, widths) {
    const ws = widths || cells.map(c => c.getBoundingClientRect().width);
    cells.forEach((c, i) => { c.style.width = Math.max(40, Math.round(ws[i])) + 'px'; });
    table.style.tableLayout = 'fixed';
    table.style.width = ws.reduce((a, b) => a + Math.max(40, Math.round(b)), 0) + 'px';
  }

  // Per header cell, not per table: a page that redraws its table's header
  // (a report re-run with other columns) gets handles on the new cells too.
  function attach(table) {
    if (table.dataset.noresize !== undefined) return;
    const cells = headCells(table);
    if (cells.length < 2 || cells.every(c => c.querySelector(':scope > .col-grip'))) return;
    const key = keyOf(table, cells);
    const saved = store.get(key);
    if (Array.isArray(saved) && saved.length === cells.length) freeze(table, cells, saved);

    cells.forEach(cell => {
      if (cell.querySelector(':scope > .col-grip')) return;
      if (getComputedStyle(cell).position === 'static') cell.style.position = 'relative';
      const grip = document.createElement('span');
      grip.className = 'col-grip';
      grip.setAttribute('aria-hidden', 'true');
      grip.title = 'برای تغییر پهنا بکشید؛ دو بار کلیک: اندازهٔ اولیه';
      // Clicks on the handle are not clicks on the heading, which sorts.
      grip.addEventListener('click', e => e.stopPropagation());
      grip.addEventListener('dblclick', e => {
        e.preventDefault(); e.stopPropagation();
        cells.forEach(c => { c.style.width = ''; });
        table.style.tableLayout = ''; table.style.width = '';
        store.set(key, null);
      });
      grip.addEventListener('pointerdown', e => {
        if (e.button !== 0) return;
        e.preventDefault(); e.stopPropagation();
        if (table.style.tableLayout !== 'fixed') freeze(table, cells);
        const rtl = getComputedStyle(table).direction === 'rtl';
        const startX = e.clientX, startW = cell.getBoundingClientRect().width;
        const startT = table.getBoundingClientRect().width;
        grip.setPointerCapture(e.pointerId);
        grip.classList.add('on');
        document.body.classList.add('col-resizing');
        const move = ev => {
          const d = rtl ? startX - ev.clientX : ev.clientX - startX;
          const w = Math.max(40, Math.round(startW + d));
          cell.style.width = w + 'px';
          table.style.width = Math.round(startT + (w - startW)) + 'px';
        };
        const up = () => {
          grip.removeEventListener('pointermove', move);
          grip.removeEventListener('pointerup', up);
          grip.removeEventListener('pointercancel', up);
          grip.classList.remove('on');
          document.body.classList.remove('col-resizing');
          store.set(key, cells.map(c => Math.round(c.getBoundingClientRect().width)));
        };
        grip.addEventListener('pointermove', move);
        grip.addEventListener('pointerup', up);
        grip.addEventListener('pointercancel', up);
      });
      cell.appendChild(grip);
    });
  }

  // Tables are mostly drawn after the page loads -- a report, a search, a
  // list fetched from the server -- so new ones are picked up as they appear.
  let queued = false;
  function scan() {
    queued = false;
    document.querySelectorAll('table').forEach(attach);
  }
  function soon() {
    if (!queued) { queued = true; requestAnimationFrame(scan); }
  }
  scan();
  new MutationObserver(soon).observe(document.body, { childList: true, subtree: true });
})();
