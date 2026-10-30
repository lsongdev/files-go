// Navigation behavior tests with mocked hooks, history, DOM geometry and API.
// Run with: node web/app_test.cjs
const vm = require('node:vm');
const fs = require('node:fs');
const assert = require('node:assert/strict');
const slots = []; let cursor = 0, dirty = false, layouts = [];
let entries = [{url: '/', state: null}], index = 0, y = 0, anchor = null;
let fetches = [], deferred = null, nextError = null;
const ctx = {
 console, URL, URLSearchParams, Date, Math,
 location: {origin:'http://test', pathname:'/', search:''},
 localStorage:{getItem:()=>null}, matchMedia:()=>({matches:false}),
 document:{querySelector:(sel)=>sel === '.top' ? {getBoundingClientRect:()=>({bottom:60})} : anchor && sel.includes(`"${anchor.id}"`) ? {getBoundingClientRect:()=>({top:anchor.top})} : null, querySelectorAll:()=>anchor ? [{dataset:{entryId:anchor.id}, getBoundingClientRect:()=>({top:anchor.top,bottom:anchor.top+100})}] : [], getElementById:()=>({})},
 window:{get scrollY(){return y},scrollTo:({top})=>{y=top}},
 history:{get state(){return entries[index].state}, scrollRestoration:'auto', replaceState:(state,_,url)=>{entries[index]={state,url:url||entries[index].url}},pushState:(state,_,url)=>{entries.splice(index+1); entries.push({state,url}); index++}},
 useState:(init)=>{const i=cursor++;if(!(i in slots))slots[i]=typeof init==='function'?init():init;return [slots[i],v=>{slots[i]=typeof v==='function'?v(slots[i]):v;dirty=true}]},
 useRef:(init)=>{const i=cursor++;return slots[i]|| (slots[i]={current:init})},
 useCallback:fn=>fn,useEffect:()=>{},useLayoutEffect:fn=>layouts.push(fn), html:()=>null,render:()=>{},
 fetch:async url=>{
   fetches.push(url.pathname+url.search);
   if(deferred){const wait=deferred;deferred=null;await wait;}
   if(nextError){const error=nextError;nextError=null;throw error;}
   let data;
   if(url.pathname.endsWith('/children')){
     const offset=+(url.searchParams.get('offset')||0), limit=+url.searchParams.get('limit');
     const total=url.pathname.includes('/1/')?1450:10;
     data={items:Array.from({length:Math.max(0,Math.min(limit,total-offset))},(_,i)=>({id:offset+i+100,dir:true})),more:offset+limit<total};
   } else if(url.pathname.startsWith('/api/files/')){data={file:{id:+url.pathname.split('/').pop(),dir:true},ancestors:[]};}
   else data={items:[{id:777}]};
   return {ok:true,json:async()=>data};
 }
};
let src = fs.readFileSync(require('node:path').join(__dirname, 'app.js'),'utf8').replace(/^import .*\n/, '').replaceAll('import.meta.url', "'http://test/app.js'");
src=src.replace('  const activeRoot =', '  globalThis.controls = {load, search, issues, loadMore, navigate, current, items, loading, opening, error};\n  const activeRoot =');
vm.createContext(ctx); vm.runInContext(src,ctx);
function draw(){cursor=0;layouts=[];dirty=false;vm.runInContext('App()',ctx);layouts.forEach(fn=>fn());}
async function action(fn){const promise=fn();draw();await promise;draw();}
function traverse(delta){index+=delta;const u=new URL(entries[index].url,'http://test');ctx.location.pathname=u.pathname;ctx.location.search=u.search;}
async function flush(){for(let i=0;i<20;i++)await new Promise(r=>setImmediate(r));draw();}
(async()=>{
 draw(); slots[0]=[{fileId:1}];draw();
 await action(()=>ctx.controls.load(1,false));assert.equal(ctx.controls.items.length,200);assert.equal(y,0);
 for(let i=0;i<6;i++)await action(()=>ctx.controls.loadMore());
 assert.equal(ctx.controls.items.length,1400);
 y=4200;
 await action(()=>ctx.controls.load(2));assert.equal(y,0);
 y=500;
 traverse(-1);ctx.controls.navigate(false);draw();assert.equal(y,4200,'cached back restores before network');await flush();assert.equal(ctx.controls.items.length,1400);assert.equal(y,4200);
 traverse(1);ctx.controls.navigate(false);draw();await flush();assert.equal(y,500,'forward restores separate entry');
 await action(()=>ctx.controls.load(1));assert.equal(y,4200,'ancestor visit restores latest position');
 y=3900;anchor={id:'300',top:120};await action(()=>ctx.controls.load(3));anchor={id:'300',top:320};
 traverse(-1);ctx.controls.navigate(false);draw();assert.equal(y,200,'anchor delta restores relative offset');anchor=null;await flush();
 await action(()=>ctx.controls.search('test'));y=650;await action(()=>ctx.controls.load(4));traverse(-1);ctx.controls.navigate(false);draw();await flush();assert.equal(y,650,'search return');
 await action(()=>ctx.controls.issues('error'));y=220;await action(()=>ctx.controls.load(4));traverse(-1);ctx.controls.navigate(false);draw();await flush();assert.equal(y,220,'issues return');
 // Populate enough entries to evict the long list, then use the history metadata to reload it.
 await action(()=>ctx.controls.load(1));const longIndex=index;y=4100;
 for(let i=10;i<33;i++)await action(()=>ctx.controls.load(i));
 traverse(longIndex-index);fetches=[];ctx.controls.navigate(false);draw();await flush();
 assert.equal(ctx.controls.items.length,1400,'evicted long list reloaded');assert.equal(y,4100);
 assert(fetches.some(url=>url.includes('offset=1000')),'server limit handled in batches');
 // A load-more response from the previous folder must not append into a new page.
 let release;deferred=new Promise(r=>release=r);const old=ctx.controls.loadMore();draw();await action(()=>ctx.controls.load(2));release();await old;draw();assert.equal(ctx.controls.current.id,2);assert.equal(ctx.controls.items.length,10);
 // Opening a slow, uncached page replaces the previous list immediately.
 let finish; deferred=new Promise(r=>finish=r);
 const opening=ctx.controls.load(90);draw();
 assert.equal(ctx.controls.opening.blank,true);
 assert.equal(ctx.controls.items.length,0,'old list is removed before the response');
 assert.equal(ctx.controls.current,null,'old folder is not left on screen');
 assert.equal(ctx.controls.loading,true);assert.equal(y,0,'loading feedback starts in view');
 finish();await opening;draw();assert.equal(ctx.controls.current.id,90);assert.equal(ctx.controls.opening,null);
 // Failed opens retain their target for retry, without caching an incomplete page.
 nextError=new Error('offline');await action(()=>ctx.controls.load(91));
 assert.equal(ctx.controls.error,'offline');assert.equal(ctx.controls.opening.target.id,91);assert.equal(ctx.controls.loading,false);
 await action(()=>ctx.controls.load(91,false));assert.equal(ctx.controls.current.id,91);assert.equal(ctx.controls.opening,null);
 // An obsolete detail response must not fetch children or replace a newer page.
 let unblock;deferred=new Promise(r=>unblock=r);const obsolete=ctx.controls.load(92);draw();
 await action(()=>ctx.controls.load(93));fetches=[];unblock();await obsolete;draw();
 assert.equal(ctx.controls.current.id,93);assert(!fetches.some(url=>url.includes('/92/children')));
 console.log('PASS: back/forward, ancestor, anchor, search/issues, 1400-item cache eviction, stale pagination, instant loading, retry, stale detail');
})().catch(e=>{console.error(e);process.exitCode=1});
