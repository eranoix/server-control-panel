#!/usr/bin/env node
// Versioned test for the PiP-survives-reconnect logic. Extracts the
// REAL helpers (_preservePipTile / _adoptDetachedPipTile / _dropDetachedPip)
// from the shipped videocall.js (literal bytes, via regex) and drives the full
// preserve→adopt→drop round trip against a tiny DOM mock. Tracks the actual
// implementation, not a re-typed copy. Zero deps.
//
//   run: node scripts/test-pip-reconnect.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'vpsm', 'videocall.js');
const src = readFileSync(SRC, 'utf8');

function extract(name, sig) {
  const re = new RegExp('Call\\.prototype\\.' + name + ' = function \\(' + sig + '\\) \\{([\\s\\S]*?)\\n  \\};');
  const m = src.match(re);
  if (!m) { console.error('FATAL: ' + name + ' not found in', SRC); process.exit(2); }
  // inject `document` via closure; `this` bound at call-time; setTimeout/
  // clearTimeout/console come from Node globals.
  return new Function('document', 'return function(' + sig.replace(/ /g, '') + '){' + m[1] + '\n};');
}

// ---- tiny DOM mock --------------------------------------------------------
function mockEl(attrs) {
  const a = Object.assign({}, attrs);
  return {
    _attrs: a, _removed: false, style: {}, children: [],
    setAttribute(k, v) { a[k] = String(v); },
    getAttribute(k) { return k in a ? a[k] : null; },
    removeAttribute(k) { delete a[k]; },
    hasAttribute(k) { return k in a; },
    querySelector(sel) { const n = sel.replace(/^\[|\]$/g, ''); return this.children.find(c => n in c._attrs) || null; },
    remove() { this._removed = true; },
  };
}
let exitCalls = 0;
const doc = { pictureInPictureElement: null, exitPictureInPicture: () => { exitCalls++; return Promise.resolve(); } };

const preserve = extract('_preservePipTile', 'v, tile, cid')(doc);
const adopt    = extract('_adoptDetachedPipTile', 'cid, newId')(doc);
const drop     = extract('_dropDetachedPip', 'cid')(doc);

let pass = 0, fail = 0;
const ok = (name, cond) => { console.log((cond ? 'PASS ' : 'FAIL ') + name); cond ? pass++ : fail++; };

function freshTile(id) {
  const v = mockEl({ 'data-vc-peer': id });
  const tile = mockEl({ 'data-vc-peer-tile': id });
  tile.children = [
    mockEl({ 'data-vc-peer-avatar': id }),
    mockEl({ 'data-vc-peer-name': id }),
    mockEl({ 'data-vc-peer-sub': id }),
    mockEl({ 'data-vc-peer-caption': id }),
  ];
  return { v, tile };
}

// ---- 1) preserve detaches the tile without removing it --------------------
const call = { _pipDetached: undefined, _pipReattachTimer: null };
const { v, tile } = freshTile('OLD');
preserve.call(call, v, tile, 'CID1');
ok('preserve: the <video> loses data-vc-peer', v.getAttribute('data-vc-peer') === null);
ok('preserve: the <video> is marked data-vc-pip-detached=cid', v.getAttribute('data-vc-pip-detached') === 'CID1');
ok('preserve: the tile loses data-vc-peer-tile (out of prune/reflow reach)', tile.getAttribute('data-vc-peer-tile') === null);
ok('preserve: the tile is hidden (display:none)', tile.style.display === 'none');
ok('preserve: does NOT remove the element from the DOM (the OS window lives on)', v._removed === false && tile._removed === false);
ok('preserve: recorded in the _pipDetached[cid] map', call._pipDetached && call._pipDetached.CID1 && call._pipDetached.CID1.v === v);
ok('preserve: arms the safety timer', !!call._pipReattachTimer);

// ---- 2) adopt re-keys the SAME element to the new peer id ------------------
const got = adopt.call(call, 'CID1', 'NEW');
ok('adopt: returns the very SAME preserved <video>', got === v);
ok('adopt: the <video> is re-keyed to data-vc-peer=newId', v.getAttribute('data-vc-peer') === 'NEW');
ok('adopt: clears the data-vc-pip-detached mark', v.getAttribute('data-vc-pip-detached') === null);
ok('adopt: the tile is re-keyed to data-vc-peer-tile=newId', tile.getAttribute('data-vc-peer-tile') === 'NEW');
ok('adopt: the tile shows up again (display restored)', tile.style.display === '');
ok('adopt: children (avatar/name/sub/caption) re-keyed', tile.children.every(c => Object.values(c._attrs).includes('NEW')));
ok('adopt: clears _pipDetached[cid]', !(call._pipDetached && call._pipDetached.CID1));
ok('adopt: clears the timer', call._pipReattachTimer === null);

// ---- 3) adopt of an unknown cid is a no-op (no create, no throw) ----------
const none = adopt.call(call, 'NOPE', 'X');
ok('adopt(unknown cid): returns null', none === null);

// ---- 4) drop (the peer never came back): leave PiP and remove the tile ----
const call2 = { _pipDetached: undefined, _pipReattachTimer: null };
const t2 = freshTile('OLD2');
preserve.call(call2, t2.v, t2.tile, 'CID2');
doc.pictureInPictureElement = t2.v;     // the OS window is on this element
exitCalls = 0;
drop.call(call2, 'CID2');
ok('drop: leaves Picture-in-Picture (exitPictureInPicture called)', exitCalls === 1);
ok('drop: removes the orphaned tile', t2.tile._removed === true);
ok('drop: clears _pipDetached[cid]', !(call2._pipDetached && call2._pipDetached.CID2));

// cleanup pending safety timers so the process can exit
try { clearTimeout(call._pipReattachTimer); } catch (_) {}
try { clearTimeout(call2._pipReattachTimer); } catch (_) {}

console.log('\n' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
