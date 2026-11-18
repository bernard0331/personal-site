// The team optimizer's front end: landing page, the account editor (characters,
// weapons, artifacts), options, and runs. Plain JavaScript, no build step.
//
// The visitor's account is a draft kept in this browser (localStorage) until
// they press Optimize, which sends a locked snapshot to the server.

const EL = { pyro: 'var(--pyro)', hydro: 'var(--hydro)', electro: 'var(--electro)', cryo: 'var(--cryo)', anemo: 'var(--anemo)', geo: 'var(--geo)', dendro: 'var(--dendro)' };
const ELEMENTS = Object.keys(EL);
const WEAPON_TYPES = ['sword', 'claymore', 'polearm', 'bow', 'catalyst'];
const SLOTS = ['flower', 'plume', 'sands', 'goblet', 'circlet'];
// Constellation and refinement badges, grey (low) to gold (high).
const CSCALE = ['#6f6e69', '#4f86b8', '#4f9e9a', '#6f9b45', '#b08b2e', '#c9702f', '#d9a12b'];
const RSCALE = ['#6f6e69', '#4f86b8', '#6f9b45', '#c9702f', '#d9a12b'];
const STAT = { hp: 'HP', hp_: 'HP%', atk: 'ATK', atk_: 'ATK%', def: 'DEF', def_: 'DEF%', eleMas: 'EM', enerRech_: 'ER%', critRate_: 'CRIT Rate%', critDMG_: 'CRIT DMG%', heal_: 'Healing%', physical_dmg_: 'Physical DMG%', pyro_dmg_: 'Pyro DMG%', hydro_dmg_: 'Hydro DMG%', electro_dmg_: 'Electro DMG%', cryo_dmg_: 'Cryo DMG%', anemo_dmg_: 'Anemo DMG%', geo_dmg_: 'Geo DMG%', dendro_dmg_: 'Dendro DMG%' };

const $ = id => document.getElementById(id);
const cap = s => s[0].toUpperCase() + s.slice(1);
const imgURL = icon => `/img/${icon}.png`;

// ---------- state ----------

let catalog;               // from /api/catalog
const byKey = { char: {}, weapon: {}, set: {} };
const STORE = 'team-optimizer-draft';
let draft = { chars: {}, weapons: {}, unlimited3: false, artifacts: [], options: { energy: 2, pins: [], excluded: [] } };

function load() {
  try {
    const saved = JSON.parse(localStorage.getItem(STORE));
    if (saved && saved.chars) draft = { ...draft, ...saved };
  } catch { /* private window or blocked storage: start empty */ }
}
function save() {
  try { localStorage.setItem(STORE, JSON.stringify(draft)); } catch { /* not fatal */ }
}
const owns = key => draft.chars[key] !== undefined;

function toast(msg, ms = 4000) {
  const t = document.createElement('div'); t.className = 'toast'; t.textContent = msg;
  document.body.appendChild(t); setTimeout(() => t.remove(), ms);
}

function art(icon, label) {
  return icon ? `<img src="${imgURL(icon)}" alt="" loading="lazy">` : `<div class="placeholder-art" style="width:100%;height:100%">${label.slice(0, 2)}</div>`;
}

// ---------- navigation ----------

const STEPS = ['Characters', 'Weapons', 'Artifacts', 'Optimize'];
function go(i) {
  document.querySelectorAll('.page').forEach(p => p.classList.toggle('on', +p.dataset.step === i));
  $('steps').style.display = i >= 0 && i < 4 ? 'flex' : 'none';
  const bar = $('steps'); bar.innerHTML = '';
  STEPS.forEach((t, j) => {
    const d = document.createElement('div');
    d.className = 'step' + (j === i ? ' on' : j < i ? ' done' : '');
    d.innerHTML = `<span class="n">${j < i ? '✓' : j + 1}</span>${t}`;
    d.onclick = () => go(j);
    bar.appendChild(d);
  });
  if (i === 2) drawArtifacts();
  if (i === 3) drawPins();
  window.scrollTo(0, 0);
}

// ---------- multi-select dropdown ----------

let openDD = null;
function closeAll() { openDD = null; document.querySelectorAll('.dd.open').forEach(x => x.classList.remove('open')); }
document.addEventListener('click', closeAll);
function dropdown(label, options, chosen, redraw) {
  const d = document.createElement('div'); d.className = 'dd';
  d.innerHTML = `<button>${label}${chosen.size ? `<span class="count">${chosen.size}</span>` : ''} ▾</button><div class="dd-panel"></div>`;
  const panel = d.querySelector('.dd-panel');
  options.forEach(([v, t, icon]) => {
    const l = document.createElement('label');
    l.innerHTML = `<input type="checkbox" ${chosen.has(v) ? 'checked' : ''}>${icon || ''}${t}`;
    l.querySelector('input').onchange = e => { e.target.checked ? chosen.add(v) : chosen.delete(v); redraw(); };
    panel.appendChild(l);
  });
  const c = document.createElement('div'); c.className = 'clear'; c.textContent = 'Clear';
  c.onclick = () => { chosen.clear(); redraw(); }; panel.appendChild(c);
  d.querySelector('button').onclick = e => { e.stopPropagation(); const was = d.classList.contains('open'); closeAll(); if (!was) { d.classList.add('open'); openDD = label; } };
  panel.onclick = e => e.stopPropagation();
  if (openDD === label) d.classList.add('open');
  return d;
}

// ---------- characters ----------

const fEl = new Set(), fWeap = new Set(), fRar = new Set();
let onlyOwned = false, query = '';
function drawCharFilters() {
  const f = $('filters');
  f.querySelectorAll('.dd, .chip').forEach(c => c.remove());
  const again = () => { drawCharFilters(); drawGrid(); };
  f.appendChild(dropdown('Element', ELEMENTS.map(e => [e, cap(e), `<span class="dot" style="background:${EL[e]}"></span>`]), fEl, again));
  f.appendChild(dropdown('Weapon', WEAPON_TYPES.map(w => [w, cap(w)]), fWeap, again));
  f.appendChild(dropdown('Rarity', [[5, '5★'], [4, '4★']], fRar, again));
  const o = document.createElement('button'); o.className = 'chip' + (onlyOwned ? ' on' : ''); o.textContent = 'Owned only';
  o.onclick = () => { onlyOwned = !onlyOwned; again(); }; f.appendChild(o);
}
function drawGrid() {
  const g = $('grid'); g.innerHTML = '';
  for (const c of catalog.characters) {
    const has = owns(c.key);
    if (onlyOwned && !has) continue;
    if (fEl.size && !fEl.has(c.element)) continue;
    if (fWeap.size && !fWeap.has(c.weapon_type)) continue;
    if (fRar.size && !fRar.has(c.rarity)) continue;
    if (query && !c.name.toLowerCase().includes(query)) continue;
    const t = document.createElement('div');
    t.className = `tile r${c.rarity} ${has ? 'owned' : 'unowned'}`;
    t.style.setProperty('--ring', EL[c.element]);
    const cons = draft.chars[c.key];
    t.innerHTML = `<div class="art">${art(c.icon, c.name)}</div><div class="name" title="${c.name}">${c.name}</div>
      <span class="el" style="background:${EL[c.element]}"></span>${has ? `<span class="cons" style="background:${CSCALE[cons]}">C${cons}</span>
      <div class="cstrip">${[0, 1, 2, 3, 4, 5, 6].map(i => `<span data-c="${i}" style="background:${cons === i ? CSCALE[i] : 'transparent'}">${i}</span>`).join('')}</div>` : ''}`;
    t.onclick = () => { if (has) delete draft.chars[c.key]; else draft.chars[c.key] = 0; save(); drawGrid(); };
    t.querySelectorAll('.cstrip span').forEach(b => b.onclick = e => { e.stopPropagation(); draft.chars[c.key] = +b.dataset.c; save(); drawGrid(); });
    g.appendChild(t);
  }
  $('ccount').textContent = `${Object.keys(draft.chars).length} characters in your account`;
}

// ---------- weapons ----------

const fWType = new Set(), fWRar = new Set();
let wOwned = false, wquery = '';
function drawWFilters() {
  const f = $('wfilters');
  f.querySelectorAll('.dd, .chip').forEach(c => c.remove());
  const again = () => { drawWFilters(); drawWeapons(); };
  f.appendChild(dropdown('Weapon type', WEAPON_TYPES.map(w => [w, cap(w)]), fWType, again));
  f.appendChild(dropdown('Rarity ', [[5, '5★'], [4, '4★'], [3, '3★']], fWRar, again));
  const o = document.createElement('button'); o.className = 'chip' + (wOwned ? ' on' : ''); o.textContent = 'Owned only';
  o.onclick = () => { wOwned = !wOwned; again(); }; f.appendChild(o);
  $('unl3').classList.toggle('on', draft.unlimited3);
}
function copiesOf(w) {
  if (w.rarity === 3 && draft.unlimited3) return ['∞'];
  return draft.weapons[w.key] || [];
}
function drawWeapons() {
  const g = $('wgrid'); g.innerHTML = '';
  for (const w of catalog.weapons) {
    if (w.rarity < 3) continue;
    const copies = copiesOf(w), has = copies.length > 0, unlimited = copies[0] === '∞';
    if (wOwned && !has) continue;
    if (fWType.size && !fWType.has(w.weapon_type)) continue;
    if (fWRar.size && !fWRar.has(w.rarity)) continue;
    if (wquery && !w.name.toLowerCase().includes(wquery)) continue;
    const d = document.createElement('div');
    d.className = `wtile r${w.rarity}${has ? '' : ' unowned'}`;
    d.innerHTML = `<div class="wart">${art(w.icon, w.name)}</div>
      <div style="min-width:0"><div class="wname">${w.name}</div><div class="sub" style="font-size:11px">${w.rarity}★ ${cap(w.weapon_type)}${has ? '' : ' · click to add'}</div>
      ${has ? `<div class="copies">${copies.map((r, i) => `<span class="copy" data-i="${i}" style="background:${unlimited ? RSCALE[4] : RSCALE[r - 1]};color:#fff">${unlimited ? 'R5 · unlimited' : 'R' + r}</span>`).join('')}
      ${unlimited ? '' : '<button class="mini" data-a="add" aria-label="Add a copy">+</button><button class="mini" data-a="rm" aria-label="Remove a copy">−</button>'}</div>` : ''}</div>`;
    if (!has) d.onclick = () => { draft.weapons[w.key] = [1]; save(); drawWeapons(); };
    d.querySelectorAll('.copy').forEach(s => s.onclick = () => {
      if (unlimited) return;
      const i = +s.dataset.i; draft.weapons[w.key][i] = draft.weapons[w.key][i] % 5 + 1; save(); drawWeapons();
    });
    d.querySelectorAll('[data-a]').forEach(b => b.onclick = e => {
      e.stopPropagation();
      const list = draft.weapons[w.key];
      if (b.dataset.a === 'add') list.push(1); else { list.pop(); if (!list.length) delete draft.weapons[w.key]; }
      save(); drawWeapons();
    });
    g.appendChild(d);
  }
}

// ---------- artifacts ----------

const fSet = new Set(), fSlot = new Set(), fMain = new Set();
let asort = 'cv';
const SORTS = [['cv', 'Crit value'], ['atk_', 'ATK% rolls'], ['hp_', 'HP% rolls'], ['def_', 'DEF% rolls'], ['enerRech_', 'Energy recharge'], ['eleMas', 'Elemental mastery'], ['set', 'Set'], ['slot', 'Slot'], ['main', 'Main stat']];
const cvOf = a => a.subs.reduce((t, [k, v]) => t + (k === 'critRate_' ? 2 * v : k === 'critDMG_' ? v : 0), 0);
function drawArtifacts() {
  const f = $('afilters'); f.innerHTML = '';
  const again = () => drawArtifacts();
  const used = [...new Set(draft.artifacts.map(a => a.set))].filter(k => byKey.set[k]);
  f.appendChild(dropdown('Set', used.map(k => [k, byKey.set[k].name, byKey.set[k].icons.flower ? `<img src="${imgURL(byKey.set[k].icons.flower)}" alt="">` : '']), fSet, again));
  f.appendChild(dropdown('Slot', SLOTS.map(x => [x, cap(x)]), fSlot, again));
  f.appendChild(dropdown('Main stat', [...new Set(draft.artifacts.map(a => a.main))].map(m => [m, STAT[m] || m]), fMain, again));
  const sel = document.createElement('select'); sel.className = 'fsel';
  sel.innerHTML = SORTS.map(([v, t]) => `<option value="${v}" ${asort === v ? 'selected' : ''}>Sort: ${t}</option>`).join('');
  sel.onchange = () => { asort = sel.value; drawArtifacts(); }; f.appendChild(sel);

  const items = draft.artifacts.filter(a => (!fSet.size || fSet.has(a.set)) && (!fSlot.size || fSlot.has(a.slot)) && (!fMain.size || fMain.has(a.main)));
  const sub = (a, k) => a.subs.reduce((t, [kk, v]) => t + (kk === k ? v : 0), 0);
  const by = {
    cv: (a, b) => cvOf(b) - cvOf(a),
    set: (a, b) => (byKey.set[a.set]?.name || a.set).localeCompare(byKey.set[b.set]?.name || b.set),
    slot: (a, b) => SLOTS.indexOf(a.slot) - SLOTS.indexOf(b.slot),
    main: (a, b) => (STAT[a.main] || a.main).localeCompare(STAT[b.main] || b.main),
  };
  items.sort(by[asort] || ((a, b) => sub(b, asort) - sub(a, asort)));
  $('acount').textContent = `${draft.artifacts.length} artifacts · showing ${Math.min(items.length, 300)}`;
  const g = $('agrid'); g.innerHTML = '';
  if (!draft.artifacts.length) {
    g.innerHTML = '<p class="sub">Import a GOOD file to see your artifacts. Quick runs use KQM-standard artifacts, so you can skip this step.</p>';
    return;
  }
  const fmt = (k, v) => (STAT[k] || k).replace('%', '') + ' +' + (k.endsWith('_') ? (+v).toFixed(1) + '%' : String(Math.round(v)));
  for (const a of items.slice(0, 300)) {
    const set = byKey.set[a.set], icon = set?.icons[a.slot];
    const d = document.createElement('div'); d.className = 'acard' + (a.excluded ? ' excluded' : '');
    d.innerHTML = `<div class="aart r${a.rarity}">${art(icon, set?.name || a.set)}</div>
      <div style="min-width:0;flex:1"><div style="display:flex;justify-content:space-between;gap:6px"><span class="amain">${STAT[a.main] || a.main}</span><span class="cv">CV ${cvOf(a).toFixed(1)}</span></div>
      <div class="asub" style="margin-bottom:2px">${set?.name || a.set} · ${a.slot} · +${a.level}</div>
      ${a.subs.map(([k, v]) => `<div class="asub">${fmt(k, v)}</div>`).join('')}
      <div class="aflags"><button class="${a.excluded ? 'on' : ''}">${a.excluded ? 'Excluded' : 'Exclude'}</button></div></div>`;
    d.querySelector('.aflags button').onclick = () => { a.excluded = !a.excluded; save(); drawArtifacts(); };
    g.appendChild(d);
  }
}

// ---------- options ----------

function pickList(id, list) {
  const box = $(id); box.innerHTML = '';
  for (const k of list) {
    const c = byKey.char[k]; if (!c) continue;
    const p = document.createElement('span'); p.className = 'pill';
    p.innerHTML = `${c.icon ? `<img src="${imgURL(c.icon)}" alt="">` : ''}${c.name} <button class="mini" aria-label="Remove">×</button>`;
    p.querySelector('button').onclick = () => { list.splice(list.indexOf(k), 1); save(); drawPins(); };
    box.appendChild(p);
  }
  const sel = document.createElement('select'); sel.className = 'fsel';
  sel.innerHTML = '<option value="">+ Add</option>' + catalog.characters.filter(c => owns(c.key) && !list.includes(c.key)).map(c => `<option value="${c.key}">${c.name}</option>`).join('');
  sel.onchange = () => { if (sel.value) { list.push(sel.value); save(); drawPins(); } };
  box.appendChild(sel);
}
function drawPins() {
  pickList('pins', draft.options.pins);
  pickList('excl', draft.options.excluded);
  document.querySelectorAll('#energy button').forEach((b, i) => b.classList.toggle('on', (i === 0 ? 1 : 2) === draft.options.energy));
}

// ---------- GOOD import and export ----------

async function importGood(file) {
  const res = await fetch('/api/good', { method: 'POST', body: file });
  const body = await res.json();
  if (!res.ok) { toast(`That file couldn't be read: ${body.error}`); return; }
  draft.chars = {}; draft.weapons = {};
  for (const c of body.characters || []) if (byKey.char[c.key]) draft.chars[c.key] = c.cons;
  for (const w of body.weapons || []) if (byKey.weapon[w.key]) (draft.weapons[w.key] = draft.weapons[w.key] || []).push(w.refinement);
  draft.artifacts = (body.artifacts || []).map(a => ({ ...a, excluded: false }));
  save(); drawGrid(); drawWeapons();
  toast(`Imported ${Object.keys(draft.chars).length} characters, ${(body.weapons || []).length} weapons and ${draft.artifacts.length} artifacts` + (body.dropped ? ` · ${body.dropped} entries skipped` : ''));
}

// GOOD keys are the English names in PascalCase without punctuation.
const goodKey = name => name.replace(/['’]/g, '').split(/[^A-Za-z0-9]+/).filter(Boolean).map(w => w[0].toUpperCase() + w.slice(1)).join('');
const GOOD_STAT = Object.fromEntries(Object.keys(STAT).map(k => [k, k]));
function exportGood() {
  const traveler = k => k.startsWith('lumine') ? 'Traveler' : null;
  const good = {
    format: 'GOOD', version: 2, source: 'Team optimizer',
    characters: Object.entries(draft.chars).map(([k, cons]) => ({ key: traveler(k) || goodKey(byKey.char[k].name), level: 90, constellation: cons, ascension: 6, talent: { auto: 10, skill: 10, burst: 10 } })),
    weapons: Object.entries(draft.weapons).flatMap(([k, rs]) => rs.map(r => ({ key: goodKey(byKey.weapon[k].name), level: 90, ascension: 6, refinement: r, location: '', lock: false }))),
    artifacts: draft.artifacts.map(a => ({ setKey: goodKey(byKey.set[a.set]?.name || a.set), slotKey: a.slot, level: a.level, rarity: a.rarity, mainStatKey: GOOD_STAT[a.main] || a.main, location: '', lock: false, substats: a.subs.map(([key, value]) => ({ key, value })) })),
  };
  const link = document.createElement('a');
  link.href = URL.createObjectURL(new Blob([JSON.stringify(good, null, 2)], { type: 'application/json' }));
  link.download = 'good-export.json'; link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

// ---------- start ----------

function heroArt() {
  $('hero-art').innerHTML = ELEMENTS.map(el => {
    const pool = catalog.characters.filter(c => c.element === el && c.icon);
    const c = pool[Math.floor(Math.random() * pool.length)];
    return c ? `<img src="${imgURL(c.icon)}" alt="${c.name}" title="${c.name}" style="background:${c.rarity === 5 ? 'var(--r5-bg)' : 'var(--r4-bg)'};box-shadow:0 0 0 2px ${EL[el]}">` : '';
  }).join('');
}

function segmented(id, onPick) {
  document.querySelectorAll(`#${id} button`).forEach((b, i) => b.onclick = () => {
    if (b.disabled) return;
    document.querySelectorAll(`#${id} button`).forEach(x => x.classList.remove('on')); b.classList.add('on'); onPick(i);
  });
}

async function main() {
  load();
  catalog = await (await fetch('/api/catalog')).json();
  catalog.characters.forEach(c => byKey.char[c.key] = c);
  catalog.weapons.forEach(w => byKey.weapon[w.key] = w);
  catalog.sets.forEach(s => byKey.set[s.key] = s);

  heroArt(); drawCharFilters(); drawGrid(); drawWFilters(); drawWeapons();
  $('search').oninput = e => { query = e.target.value.toLowerCase(); drawGrid(); };
  $('wsearch').oninput = e => { wquery = e.target.value.toLowerCase(); drawWeapons(); };
  document.querySelectorAll('[data-go]').forEach(b => b.onclick = () => go(+b.dataset.go));
  $('home-btn').onclick = () => go(-1);
  $('import-btn').onclick = () => $('import-file').click();
  $('import-file').onchange = e => { if (e.target.files[0]) importGood(e.target.files[0]); e.target.value = ''; };
  $('export-btn').onclick = exportGood;
  $('all4').onclick = () => {
    let n = 0;
    for (const w of catalog.weapons) if (w.rarity === 4 && !draft.weapons[w.key]) { draft.weapons[w.key] = [1]; n++; }
    save(); drawWeapons(); toast(`Added R1 of ${n} 4-star weapons`);
  };
  $('unl3').onclick = () => { draft.unlimited3 = !draft.unlimited3; save(); drawWFilters(); drawWeapons(); };
  segmented('energy', i => { draft.options.energy = i === 0 ? 1 : 2; save(); });
  $('optimize').onclick = () => toast('Runs are the next part of the build.');
  go(Object.keys(draft.chars).length ? 0 : -1);
}

main().catch(err => { document.querySelector('main').innerHTML = `<p>Something went wrong loading the optimizer: ${err.message}</p>`; });
