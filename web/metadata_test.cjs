// Provider selection, shared matching and album presentation without a browser.
const vm = require('node:vm');
const fs = require('node:fs');
const assert = require('node:assert/strict');
let state = [], cursor = 0, requests = [], hold;
const ctx = {
  URL, URLSearchParams, console,
  location: { origin: 'http://test' },
  document: { getElementById: () => ({}) }, render: () => {}, html: () => null,
  useEffect: () => {}, useCallback: (f) => f,
  useState: (init) => {
    const i = cursor++;
    if (!(i in state)) state[i] = typeof init === 'function' ? init() : init;
    return [state[i], (value) => { state[i] = value; }];
  },
  useRef: (init) => { const i = cursor++; return state[i] || (state[i] = { current: init }); },
  fetch: async (url, options = {}) => {
    requests.push({ url, options });
    if (hold) { const wait = hold; hold = null; await wait; }
    const match = { provider: url.searchParams.get('provider'), type: url.searchParams.get('type'), id: url.searchParams.get('q') };
    const data = options.method === 'DELETE' ? { id: 1, meta: {} } : options.method ? { id: 1, meta: { match: JSON.parse(options.body).match } }
      : { items: [{ match, title: 'Result' }], scope: { id: 9, name: 'Shared work', dir: true } };
    return { ok: true, json: async () => data };
  },
};
let source = fs.readFileSync(require('node:path').join(__dirname, 'app.js'), 'utf8')
  .replace(/^import .*\n/, '').replaceAll('import.meta.url', "'http://test/app.js'");
source = source.replace('  return html`<div class="overlay"',
  '  globalThis.matchControls = { search, choose, results, scope };\n  return html`<div class="overlay"');
vm.createContext(ctx); vm.runInContext(source, ctx);
ctx.item = { id: 1, name: 'video.mkv', meta: { kind: 'movie', info: { Code: 'CAWD-549' } } };
const draw = () => { cursor = 0; vm.runInContext('MatchDialog({item, onDone: value => globalThis.chosen = value})', ctx); };
(async () => {
  const album = vm.runInContext("subtitle({dir:true,meta:{kind:'album',artist:'Artist',year:2024,tracks:12,duration:1200}})", ctx);
  assert.equal(album, 'Artist · 2024 · 12 首 · 20:00');
  draw(); await ctx.matchControls.search(); draw();
  assert.equal(requests[0].url.searchParams.get('provider'), 'theporndb');
  assert.equal(requests[0].url.searchParams.get('type'), 'jav');
  assert.equal(ctx.matchControls.scope.id, 9);
  await ctx.matchControls.choose(ctx.matchControls.results[0].match);
  assert.deepEqual(JSON.parse(requests.at(-1).options.body), { match: { provider: 'theporndb', type: 'jav', id: 'CAWD-549' } });
  assert.equal(ctx.chosen.meta.match.provider, 'theporndb');
  await ctx.matchControls.choose('');
  assert.equal(requests.at(-1).options.method, 'DELETE');
  assert.equal(requests.at(-1).options.body, undefined);
  // A slow response from the previous provider must not replace newer results.
  let release; hold = new Promise((resolve) => { release = resolve; });
  const oldSearch = ctx.matchControls.search();
  state[1] = 'tmdb'; state[0] = 'new'; draw();
  await ctx.matchControls.search(); release(); await oldSearch; draw();
  assert.equal(ctx.matchControls.results[0].match.provider, 'tmdb');
  console.log('PASS: album summary, provider candidates, shared scope, match payload, stale search response');
})().catch((error) => { console.error(error); process.exitCode = 1; });
