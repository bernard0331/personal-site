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
// energy 1: KQM standard. run: quick or refined; level: light, standard or
// thorough; adv: a thorough run's settings (the server's defaults).
const freshAdv = () => ({ top: 100, per_element: 20, per_character: 3, iterations: 1000, skip_waiting: false });
const freshOptions = () => ({ energy: 1, pins: [], excluded: [], run: 'quick', level: 'light', artifacts: 'kqm', adv: freshAdv() });
const LEVELS = ['light', 'standard', 'thorough'];
const LEVEL_NOTES = {
  light: 'The quick run\'s picks and the best few teams of each element: about 30–40 teams, roughly 10 minutes on a large account.',
  standard: 'Also each character\'s best team: about 120 teams, roughly 25 minutes.',
  thorough: 'With the settings below, up to about 370 teams on a large account: about an hour. Change them to trade time for coverage.',
};
// all4: weapons "Every 4-star" added, so switching it off removes only those.
let draft = { chars: {}, weapons: {}, unlimited3: true, all4: [], artifacts: [], options: freshOptions() };

function load() {
  try {
    const saved = JSON.parse(localStorage.getItem(STORE));
    if (saved && saved.chars) draft = { ...draft, ...saved, all4: saved.all4 || [], options: { ...freshOptions(), ...saved.options } };
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
  if (location.hash.startsWith('#/runs/')) { clearTimeout(runTimer); history.pushState(null, '', '/'); }
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
    if (c.caveat) t.title = `${c.name} is ${c.caveat}`;
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
  $('all4').classList.toggle('on', draft.all4.length > 0);
}
// 3-star and craftable 4-star weapons are cheap to get and refine: with the
// toggle on (the default) every character can have one at R5.
const unlimitedKind = w => w.rarity === 3 || w.craftable;
function copiesOf(w) {
  if (unlimitedKind(w) && draft.unlimited3) return ['∞'];
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
      <div style="min-width:0"><div class="wname">${w.name}</div><div class="sub" style="font-size:11px">${w.rarity}★ ${cap(w.weapon_type)}${w.craftable ? ' · craftable' : ''}${has ? '' : ' · click to add'}</div>
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
  drawRunChoice();
}

// The run choice: quick or refined, how much to refine, a thorough run's
// settings, and the artifact mode (top 1% only for refined runs).
function drawRunChoice() {
  const o = draft.options, refined = o.run === 'refined';
  if (!refined && o.artifacts === 'top1') o.artifacts = 'kqm';
  document.querySelectorAll('#runkind .runcard').forEach(c => c.classList.toggle('on', c.dataset.kind === o.run));
  $('refined-opts').style.display = refined ? '' : 'none';
  document.querySelectorAll('#level button').forEach((b, i) => b.classList.toggle('on', LEVELS[i] === o.level));
  $('level-note').textContent = LEVEL_NOTES[o.level];
  $('advanced').style.display = refined && o.level === 'thorough' ? '' : 'none';
  $('adv-top').value = o.adv.top; $('adv-el').value = o.adv.per_element; $('adv-char').value = o.adv.per_character;
  $('adv-it').value = String(o.adv.iterations); $('adv-wait').checked = o.adv.skip_waiting;
  const art = document.querySelectorAll('#artmode button');
  art[0].classList.toggle('on', o.artifacts !== 'top1'); art[1].classList.toggle('on', o.artifacts === 'top1');
  art[1].disabled = !refined;
  $('artnote').textContent = refined ? 'Top 1% gives the substat optimiser more rolls to place, as in the best builds players have. Your own artifacts come later.'
    : 'Top 1% needs a refined run. Your own artifacts come later.';
}

function wireRunChoice() {
  const o = () => draft.options;
  document.querySelectorAll('#runkind .runcard').forEach(c => c.onclick = () => { o().run = c.dataset.kind; save(); drawRunChoice(); });
  document.querySelectorAll('#level button').forEach((b, i) => b.onclick = () => { o().level = LEVELS[i]; save(); drawRunChoice(); });
  document.querySelectorAll('#artmode button').forEach((b, i) => b.onclick = () => {
    if (b.disabled || i > 1) return;
    o().artifacts = i === 1 ? 'top1' : 'kqm'; save(); drawRunChoice();
  });
  const num = (id, key, lo, hi) => $(id).onchange = () => {
    const v = Math.max(lo, Math.min(hi, Math.round(+$(id).value || 0)));
    o().adv[key] = v; save(); drawRunChoice();
  };
  num('adv-top', 'top', 0, 500); num('adv-el', 'per_element', 0, 50); num('adv-char', 'per_character', 0, 5);
  $('adv-it').onchange = () => { o().adv.iterations = +$('adv-it').value; save(); };
  $('adv-wait').onchange = () => { o().adv.skip_waiting = $('adv-wait').checked; save(); };
}

// runLabel names a snapshot's run: "Quick run" or e.g. "Light refined run (top 1%)".
function runLabel(snap) {
  if (!snap.mode || snap.mode === 'quick') return 'Quick run';
  return `${cap(snap.mode)} refined run${snap.artifacts === 'top1' ? ' (top 1% artifacts)' : ''}`;
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


// ---------- runs ----------
// Optimize sends a locked snapshot; the run lives at #/runs/<id>, a private
// link. The page polls for progress, then shows the teams.

let runTimer = null, runData = null, view = { name: 'element', ranking: 'min' };

// During a canary release two versions of the site run side by side, and a
// run lives only on the version that started it. Every response names its
// version in X-Track; the page remembers it per run and sends it back when
// asking about that run, and the load balancer routes on it
// (deploy/canary/genshin-web/httproute.yaml). Outside a canary there is no
// X-Track and nothing is stored.
function runTrack(id, track) {
  let all = {};
  try { all = JSON.parse(localStorage.getItem('runTracks') || '{}'); } catch (e) { /* storage blocked */ }
  if (track === undefined) return all[id];
  all[id] = track;
  const ids = Object.keys(all);
  if (ids.length > 50) delete all[ids[0]];
  try { localStorage.setItem('runTracks', JSON.stringify(all)); } catch (e) { /* storage blocked */ }
}

// fetchRun asks for a run, on the version that started it. On the live
// cluster about 3 in 100 requests carrying X-Track: canary still reached
// stable (the load balancer's route was correct), so when the answer comes
// from the wrong version, ask again, a few times.
async function fetchRun(id) {
  const track = runTrack(id);
  let res;
  for (let i = 0; i < 5; i++) {
    res = await fetch(`/api/runs/${id}`, track ? { headers: { 'X-Track': track } } : {});
    const from = res.headers.get('X-Track');
    if (!track || !from || from === track) break;
    await new Promise(r => setTimeout(r, 300));
  }
  return res;
}

async function optimize() {
  const o = draft.options, refined = o.run === 'refined';
  const snapshot = { characters: draft.chars, weapons: draft.weapons, unlimited: draft.unlimited3,
    energy: o.energy, pins: o.pins, excluded: o.excluded,
    mode: refined ? o.level : 'quick', artifacts: refined ? o.artifacts : 'kqm' };
  if (refined && o.level === 'thorough') snapshot.refine = o.adv;
  const res = await fetch('/api/runs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(snapshot) });
  const body = await res.json();
  if (!res.ok) { toast(body.error || 'The run could not be started.'); return; }
  const track = res.headers.get('X-Track');
  if (track) runTrack(body.id, track);
  location.hash = `#/runs/${body.id}`;
}

function routeFromHash() {
  const m = location.hash.match(/^#\/runs\/([a-z2-7]{26})$/);
  if (m) { showRun(m[1]); return true; }
  return false;
}

// shown is what the run page last drew: while the result is unchanged, a
// poll redraws only the progress, so open Details panels stay open.
let shown = '';

async function showRun(id) {
  clearTimeout(runTimer);
  document.querySelectorAll('.page').forEach(p => p.classList.toggle('on', p.id === 'run'));
  $('steps').style.display = 'none';
  const box = $('run');
  const res = await fetchRun(id);
  if (!res.ok) { box.innerHTML = '<h2>Run not found</h2><p class="sub">The link may be mistyped, or the run has been removed.</p>'; shown = ''; return; }
  runData = await res.json();
  const st = runData.status, snap = runData.snapshot || {}, result = runData.result;
  const over = st.state === 'done' || st.state === 'failed';
  const link = `${location.origin}/#/runs/${id}`;
  const status = statusHTML(st, result);
  const sig = [id, result ? `${result.mode}${result.refining ? '+' : ''}` : '-', over, view.name, view.ranking].join('|');
  if (sig === shown && $('run-status')) {
    $('run-status').innerHTML = status;
  } else {
    const weaponCopies = Object.values(snap.weapons || {}).reduce((n, rs) => n + rs.length, 0);
    const pins = (snap.pins || []).map(k => byKey.char[k]?.name || k);
    const head = `<div class="runhead"><div style="display:flex;flex-direction:column;gap:6px">
        <div style="display:flex;gap:10px;align-items:center;flex-wrap:wrap"><span class="urlbar">${link}</span><button class="mini" id="copy-link">Copy link</button></div>
        <span class="lock">${runLabel(snap)} · locked snapshot · ${Object.keys(snap.characters || {}).length} characters · ${weaponCopies} weapon copies${snap.unlimited ? ' + unlimited 3-stars and craftables' : ''} · energy ${snap.energy === 2 ? '×2' : 'KQM standard'}${pins.length ? ' · pinned: ' + pins.join(', ') : ''}</span>
      </div><button id="rerun">Edit and run again</button></div>`;
    box.innerHTML = head + `<div id="run-status">${status}</div>` + (result ? resultsHTML(result) : '');
    if (result) wireResults();
    shown = sig;
    $('copy-link').onclick = () => { navigator.clipboard?.writeText(link).then(() => toast('Link copied'), () => toast(link)); };
    $('rerun').onclick = () => {
      draft.chars = { ...(snap.characters || {}) }; draft.weapons = JSON.parse(JSON.stringify(snap.weapons || {}));
      draft.unlimited3 = !!snap.unlimited;
      const refined = snap.mode && snap.mode !== 'quick';
      draft.options = { ...freshOptions(), energy: snap.energy || 1, pins: [...(snap.pins || [])], excluded: [...(snap.excluded || [])],
        run: refined ? 'refined' : 'quick', level: refined ? snap.mode : 'light', artifacts: snap.artifacts || 'kqm',
        adv: snap.refine ? { ...snap.refine } : freshAdv() };
      save(); history.pushState(null, '', '/'); clearTimeout(runTimer); shown = '';
      drawGrid(); drawWFilters(); drawWeapons(); drawPins(); go(0);
    };
  }
  if (!over) runTimer = setTimeout(() => showRun(id), result ? 5000 : 2000);
}

// statusHTML is the run's progress above the teams (empty once done).
function statusHTML(st, result) {
  const bar = pct => `<div style="height:10px;border-radius:5px;background:var(--border);overflow:hidden;margin:12px 0"><div style="height:100%;width:${pct}%;background:var(--accent);transition:width .4s"></div></div>`;
  const pause = 'You can leave and come back with the link above. If no one has the page open for about 15 minutes, the server pauses the run; opening the link again continues it.';
  if (st.state === 'done') return '';
  if (st.state === 'failed') {
    return result ? `<p class="flag">Refining failed: ${st.error || 'something went wrong'}. These are the quick run's teams.</p>`
      : `<h2>The run failed</h2><p class="sub">${st.error || 'Something went wrong.'}</p>`;
  }
  const pct = st.total ? Math.round(100 * st.done / st.total) : 0;
  if (result && result.refining) {
    const what = st.state === 'solving' ? 'Choosing teams from the refined numbers…'
      : st.state === 'refining' ? `Refining teams: ${st.done.toLocaleString()} of ${st.total.toLocaleString()}${st.cached ? ` (${st.cached.toLocaleString()} already known)` : ''}`
      : 'Starting again…';
    return `<div class="refining"><b>${what}</b>${st.state === 'refining' ? bar(pct) : ''}<div class="sub">Below are the quick run's teams; the refined ones replace them when ready. ${pause}</div></div>`;
  }
  const what = st.state === 'queued' ? (st.queue ? `Waiting: ${st.queue} run${st.queue > 1 ? 's' : ''} ahead` : 'Starting…')
    : st.state === 'solving' ? 'Choosing the best teams…'
    : `Simulating teams: ${st.done.toLocaleString()} of ${st.total.toLocaleString()}${st.cached ? ` (${st.cached.toLocaleString()} already known)` : ''}`;
  return `<h2>Optimizing</h2><p class="sub">${what}</p>${bar(pct)}<p class="sub">${pause}</p>`;
}

function resultNote(r) {
  if (!r.mode || r.mode === 'quick') return 'quick run (rough numbers)' + (r.refining ? ', refined numbers on the way' : '');
  return `${r.mode} refined run: ${r.refined.toLocaleString()} teams simulated again with optimised substats (${r.artifacts === 'top1' ? 'top 1%' : 'KQM standard'})`;
}

function resultsHTML(result) {
  const bd = result.breakdowns.find(b => b.name === view.name && b.ranking === view.ranking);
  bd.teams ||= []; // an empty breakdown comes as null
  const dps = bd.teams.map(t => t.dps);
  const avg = dps.length ? dps.reduce((a, b) => a + b, 0) / dps.length : 0;
  const k = n => Math.round(n / 1000) + 'k';
  const seg = (id, opts, cur) => `<div class="seg" id="${id}">${opts.map(([v, t]) => `<button data-v="${v}" class="${cur === v ? 'on' : ''}">${t}</button>`).join('')}</div>`;
  return `<div class="section-head"><div><h2>Best teams</h2><div class="sub">${result.teams.toLocaleString()} teams your account can field · ${resultNote(result)}</div></div>
      <div style="display:flex;gap:8px;flex-wrap:wrap">${seg('bd-name', [['element', '7 teams, one per element'], ['any', '5 teams, any element']], view.name)}${seg('bd-rank', [['min', 'Best weakest team'], ['sum', 'Highest average']], view.ranking)}</div></div>
    <div class="metrics"><div class="metric"><div class="label">Average</div><div class="value">${k(avg)}</div></div>
      <div class="metric"><div class="label">Weakest team</div><div class="value">${dps.length ? k(Math.min(...dps)) : '–'}</div></div>
      <div class="metric"><div class="label">Teams</div><div class="value">${dps.length}</div></div></div>
    ${bd.error ? `<p class="flag">${bd.error}</p>` : ''}${bd.optimal ? '' : '<p class="sub">The solver hit its time limit: this is the best set found, not proven best.</p>'}
    <div id="teams">${bd.teams.length ? bd.teams.map(teamHTML).join('') : '<p class="sub">No teams in this breakdown.</p>'}</div>`;
}

function teamHTML(t, pos) {
  const total = t.members.reduce((a, m) => a + (m.dps || 0), 0);
  const known = total > 0;
  const members = t.members.map((m, i) => {
    const c = byKey.char[m.key] || { name: m.key, rarity: 4, element: t.element }, w = byKey.weapon[m.weapon] || { name: m.weapon };
    const share = known ? Math.round(100 * m.dps / total) : null;
    return `<div class="member" data-i="${i}"><div class="portrait r${c.rarity}">${art(c.icon, c.name)}${w.icon ? `<span class="w"><img src="${imgURL(w.icon)}" alt=""></span>` : ''}</div>
      <div style="min-width:0"><div class="mname">${c.name} <span class="sub">C${m.cons}</span></div><div class="msub">${w.name} R${m.refinement}</div>
      <div class="msub">${known ? `${Math.round(m.dps / 1000)}k DPS · <span class="share">${share}%</span>` : 'DPS split estimated'}</div></div></div>`;
  }).join('');
  const bar = known ? `<div class="bar">${t.members.map((m, i) => { const c = byKey.char[m.key]; const pct = Math.round(100 * m.dps / total); return `<div style="flex:${Math.max(m.dps, 1)};background:${EL[c?.element] || 'var(--border-strong)'};opacity:${i === 0 ? 1 : .7}" title="${c?.name || m.key} ${Math.round(m.dps / 1000)}k (${pct}%)">${pct >= 8 ? pct + '%' : ''}</div>`; }).join('')}</div>` : '';
  const source = t.source === 'own' ? 'Own rotation' : 'KQM Sim Database';
  return `<div class="team" style="--ring:${EL[t.element] || 'var(--border-strong)'}"><div class="team-head"><div style="display:flex;gap:10px;align-items:center"><span class="rank">#${pos + 1}</span><span class="etag">${cap(t.element || 'physical')}</span><span class="dps">${Math.round(t.dps / 1000)}k</span><span class="sub">DPS</span></div>${t.details ? '<button class="details-toggle">Details</button>' : ''}</div>
    <div class="members">${members}</div>${bar}
    <div class="foot"><span>${known ? t.members.map(m => `${byKey.char[m.key]?.name || m.key} ${Math.round(m.dps / 1000)}k (${Math.round(100 * m.dps / total)}%)`).join(' · ') : ''}</span><span>${source} · ${t.rotation}</span></div>
    ${t.estimated ? '<span class="flag">Weapons changed after simulating: DPS estimated</span>' : ''}${(t.flags || []).filter(f => !f.startsWith('weapons changed')).map(f => `<span class="flag">${f}</span>`).join('')}${caveatFlags(t.members)}${t.details ? detailsHTML(t) : ''}</div>`;
}

// The Details panel: where the rotation comes from and the rotation itself,
// the example builds the quick run used, damage by element, and notes.
function detailsHTML(t) {
  const d = t.details, esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const name = k => byKey.char[k]?.name || k;
  const source = d.source_url ? `<a href="${esc(d.source_url)}" target="_blank" rel="noopener" style="color:var(--accent)">${esc(d.source_name)}</a>` : esc(d.source_name);
  const builds = d.builds.map(b => {
    const sets = Object.entries(b.sets || {}).map(([k, n]) => `${n}pc ${esc(byKey.set[k]?.name || k)}`).join(' + ');
    const subs = d.substats?.[b.key];
    return `<div class="row"><span>${esc(name(b.key))}</span><span>${[sets, esc(b.main || '')].filter(Boolean).join(' · ') || '—'}${subs ? `<br><span class="sub">Optimised rolls: ${esc(subs)}</span>` : ''}</span></div>`;
  }).join('');
  const els = Object.entries(d.element_dps || {}).filter(([, v]) => v >= 500).sort((a, b) => b[1] - a[1]);
  const top = Math.max(1, ...els.map(([, v]) => v));
  const damage = els.map(([el, v]) => `<div class="row" style="border:none;flex-direction:column;gap:0"><div style="display:flex;justify-content:space-between"><span>${cap(el)}</span><span>${Math.round(v / 1000)}k</span></div><div class="srcbar" style="width:${Math.round(100 * v / top)}%;background:${EL[el] || 'var(--accent)'}"></div></div>`).join('');
  const notes = [`Fight ${Math.round(d.duration)} s · ${d.iterations} iterations · ±${(t.sd / 1000).toFixed(1)}k`];
  for (const [why, s] of Object.entries(d.waits || {})) if (s >= 0.5) notes.push(`Waits ${s.toFixed(1)} s in all: ${esc(why.replace(/_/g, ' '))}`);
  if (d.rot_cons) notes.push('Rotation written for ' + t.members.map((m, i) => `${name(m.key)} C${d.rot_cons[i] ?? '?'}`).join(', '));
  notes.push(d.substats ? 'Sets and main stats are the rotation\'s; KQM Sim\'s optimiser chose the substats for your weapons and constellations.'
    : 'Builds are the rotation\'s example builds (KQM-standard substats); a refined run optimises the substats.');
  return `<div class="details">
    <div><h4>Rotation</h4><div class="sub" style="font-size:12px;margin-bottom:6px">${source}</div>${(d.credit || []).map(c => `<div class="sub" style="font-size:12px;margin-bottom:4px">${esc(c)}</div>`).join('')}
      <pre class="script">${esc(d.script.join('\n'))}</pre></div>
    <div><h4>Builds</h4>${builds}</div>
    <div><h4>Damage by element</h4>${damage || '<div class="sub">—</div>'}</div>
    <div><h4>Notes</h4>${notes.map(n => `<div class="row"><span>${n}</span><span></span></div>`).join('')}</div>
  </div>`;
}

// One flag per caveat: "Mitya and Valeriy are new: ...".
function caveatFlags(members) {
  const by = {};
  for (const m of members) { const c = byKey.char[m.key]; if (c?.caveat) (by[c.caveat] ||= []).push(c.name); }
  return Object.entries(by).map(([cav, names]) => `<span class="flag">${names.join(' and ')} ${names.length > 1 ? 'are' : 'is'} ${cav}</span>`).join('');
}

function wireResults() {
  const pick = (id, key) => document.querySelectorAll(`#${id} button`).forEach(b => b.onclick = () => { view[key] = b.dataset.v; $('run').querySelector('.section-head').parentElement; showRun(location.hash.split('/')[2]); });
  pick('bd-name', 'name'); pick('bd-rank', 'ranking');
  document.querySelectorAll('#teams .team').forEach(team => {
    const tog = team.querySelector('.details-toggle');
    if (tog) tog.onclick = () => { team.classList.toggle('open'); tog.textContent = team.classList.contains('open') ? 'Hide details' : 'Details'; };
    const bar = team.querySelector('.bar'); if (!bar) return;
    team.querySelectorAll('.member').forEach(m => {
      m.onmouseenter = () => { bar.classList.add('focus'); bar.children[+m.dataset.i].classList.add('hl'); };
      m.onmouseleave = () => { bar.classList.remove('focus'); bar.children[+m.dataset.i].classList.remove('hl'); };
    });
  });
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
  // "Every 4-star" is a switch: on adds R1 of every 4-star not owned yet;
  // off removes those copies again, unless they were changed since.
  $('all4').onclick = () => {
    if (draft.all4.length) {
      let n = 0;
      for (const k of draft.all4) {
        const list = draft.weapons[k];
        if (list && list.length === 1 && list[0] === 1) { delete draft.weapons[k]; n++; }
      }
      draft.all4 = [];
      save(); drawWFilters(); drawWeapons(); toast(`Removed ${n} 4-star weapons it had added`);
      return;
    }
    for (const w of catalog.weapons) if (w.rarity === 4 && !w.craftable && !draft.weapons[w.key]) { draft.weapons[w.key] = [1]; draft.all4.push(w.key); }
    save(); drawWFilters(); drawWeapons(); toast(`Added R1 of ${draft.all4.length} 4-star weapons`);
  };
  // Clear all, per step. Each asks first: there is no undo.
  const clears = {
    chars: ['Remove every character from your account?', () => { draft.chars = {}; drawGrid(); }],
    weapons: ['Remove every weapon from your account?', () => { draft.weapons = {}; draft.all4 = []; drawWFilters(); drawWeapons(); }],
    artifacts: ['Remove every artifact from your account?', () => { draft.artifacts = []; drawArtifacts(); }],
    options: ['Reset energy drops, pins and left-out characters?', () => { draft.options = freshOptions(); drawPins(); }],
  };
  document.querySelectorAll('[data-clear]').forEach(b => b.onclick = () => {
    const [question, clear] = clears[b.dataset.clear];
    if (confirm(question)) { clear(); save(); }
  });
  $('unl3').onclick = () => { draft.unlimited3 = !draft.unlimited3; save(); drawWFilters(); drawWeapons(); };
  segmented('energy', i => { draft.options.energy = i === 0 ? 1 : 2; save(); });
  wireRunChoice(); drawRunChoice();
  $('optimize').onclick = optimize;
  window.addEventListener('hashchange', () => { if (!routeFromHash()) { clearTimeout(runTimer); go(0); } });
  if (!routeFromHash()) go(Object.keys(draft.chars).length ? 0 : -1);
}

main().catch(err => { document.querySelector('main').innerHTML = `<p>Something went wrong loading the optimizer: ${err.message}</p>`; });
