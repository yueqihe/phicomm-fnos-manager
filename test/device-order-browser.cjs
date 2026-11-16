const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');
const {chooseOption}=require('./select-helpers.cjs');

(async()=>{
 const {httpDevice,freePort,until}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-device-order-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 if(!process.env.MANAGER_GO_BINARY) {
  const result=spawnSync(process.env.GO_BIN || 'go',['build','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(result.status,0);
 }
 const url=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(url).host,'--data',directory],{windowsHide:true,stdio:'ignore'});
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],errors=[];
 try {
  await until(async()=>{try{return (await fetch(url)).ok;}catch{return false;}});
  const session=await (await fetch(url+'api/session')).json();
  for(const [i,[model,name,room]] of [['DAIKIN','10 · 工位检测','工位'],['DAIKIN','04 · 机房检测','机房'],['S30-V1B','02 · 工位插座','工位'],['S30-V1B','01 · 工位插座','工位']].entries()) {
   const f=await httpDevice(model,`aabbcc00130${i}`);fixtures.push(f);
   const response=await fetch(url+'api/devices',{method:'POST',headers:{'Content-Type':'application/json',Origin:new URL(url).origin,'X-Manager-CSRF':session.csrfToken},body:JSON.stringify({model,name,room,method:'lan',address:f.address})});
   assert.ok(response.ok,await response.clone().text());
  }
  const snapshot=await (await fetch(url+'api/devices')).json(),ids=snapshot.devices.map(d=>d.id);
  let devices=snapshot.devices.map((d,i)=>({...d,online:i!==2,lan:i!==2}));
  const context=await browser.newContext({viewport:{width:1100,height:900},colorScheme:'light'}),page=await context.newPage();
  page.on('pageerror',e=>errors.push(e.message));
  await context.route('**/api/discovery/automatic?*',r=>r.fulfill({json:{state:'complete',devices:[]}}));
  await context.route('**/api/updates',r=>r.fulfill({json:{status:'current',message:'当前已是最新正式版'}}));
  await context.route('**/api/devices',r=>r.request().method()==='GET'?r.fulfill({json:{...snapshot,devices}}):r.continue());
  await page.clock.install();await page.goto(url);await page.locator('.pm-room-group').first().waitFor();
  const groups=()=>page.locator('.pm-room-group').evaluateAll(els=>els.map(el=>({heading:el.querySelector('h2').textContent,ids:[...el.querySelectorAll('[data-open-control]')].map(x=>x.dataset.openControl)})));
  assert.deepEqual(await groups(),[{heading:'工位3 台设备',ids:[ids[0],ids[2],ids[3]]},{heading:'机房1 台设备',ids:[ids[1]]}]);
  assert.equal(await page.getByRole('heading').filter({hasText:'工位'}).count(),1);
  const boxes=await page.locator('.pm-room-group').first().locator('.pm-tile').evaluateAll(els=>els.map(el=>{const b=el.getBoundingClientRect();return {top:b.top,bottom:b.bottom,height:b.height};}));
  assert.ok(boxes.every(b=>Math.abs(b.top-boxes[0].top)<1&&Math.abs(b.height-boxes[0].height)<1),'a room must occupy its own aligned row');
  assert.ok((await page.locator('.pm-room-group').nth(1).boundingBox()).y>Math.max(...boxes.map(b=>b.bottom)));
  const settings=page.getByRole('dialog',{name:'设置',exact:true});
  const open=async()=>{await page.getByRole('button',{name:'设置',exact:true}).click();await settings.getByRole('button',{name:'外观',exact:true}).click();};
  const close=()=>settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await open();const sort=settings.getByRole('combobox',{name:'设备排序',exact:true});
  assert.equal(await sort.getAttribute('data-value'),'room');
  for(const [value,expected] of [['name',[ids[3],ids[2],ids[0]]],['model',[ids[0],ids[2],ids[3]]],['online',[ids[0],ids[3],ids[2]]],['added',[ids[0],ids[2],ids[3]]]]) {
   await chooseOption(sort,value);assert.deepEqual((await groups())[0].ids,expected,value);
   assert.equal(await page.evaluate(()=>localStorage.getItem('pm-device-sort')),value);
  }
  await chooseOption(sort,'name');await close();await page.reload();await page.locator('.pm-room-group').first().waitFor();
  assert.deepEqual((await groups())[0].ids,[ids[3],ids[2],ids[0]],'reload must retain sorting');
  await page.getByRole('button',{name:'列表',exact:true}).click();
  const list=()=>page.locator('.pm-device-list [data-device]').evaluateAll(els=>els.map(el=>el.dataset.device));
  assert.deepEqual(await list(),[ids[3],ids[2],ids[1],ids[0]],'list uses the same natural name order');
  await page.locator(`[data-device="${ids[2]}"]`).click();await open();await chooseOption(sort,'online');await close();
  assert.deepEqual(await list(),[ids[0],ids[1],ids[3],ids[2]]);
  assert.equal(await page.locator(`[data-device="${ids[2]}"]`).getAttribute('aria-pressed'),'true','sorting must preserve selection');
  await page.setViewportSize({width:390,height:850});
  assert.equal(await page.getByRole('combobox',{name:'选择设备',exact:true}).getAttribute('data-value'),ids[2]);
  await page.getByRole('button',{name:'空间',exact:true}).click();
  await page.getByRole('button',{name:'S30-V1B',exact:true}).click();
  assert.deepEqual(await groups(),[{heading:'工位2 台设备',ids:[ids[3],ids[2]]}]);
  await page.getByRole('button',{name:'大金',exact:true}).click();
  assert.deepEqual(await groups(),[{heading:'工位1 台设备',ids:[ids[0]]},{heading:'机房1 台设备',ids:[ids[1]]}]);
  await page.getByRole('button',{name:'全部设备',exact:true}).click();
  // A new online state only reorders its own room, without repeating headers.
  devices=devices.map(d=>({...d,online:d.id!==ids[0]}));await page.clock.fastForward(5000);
  await until(async()=>JSON.stringify((await groups())[0].ids)===JSON.stringify([ids[2],ids[3],ids[0]]));
  const output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
  for(const theme of ['light','dark']) {
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   for(const width of [320,390,768,1100,1440]) {
    await page.setViewportSize({width,height:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${theme}/${width} page overflow`);
    assert.equal(await page.locator('.pm-room-header').count(),2);
    const previous=await page.locator('.pm-room-group').first().boundingBox(),next=await page.locator('.pm-room-group').nth(1).boundingBox();
    assert.ok(next.y>=previous.y+previous.height,'rooms must never interleave');
    if(width===390||width===1100) await page.screenshot({path:path.join(output,`device-rooms-${theme}-${width}.png`),fullPage:true});
   }
  }
  await page.setViewportSize({width:390,height:850});await open();await sort.click();
  assert.equal(await page.locator('select,datalist').count(),0,'sorting must use the existing custom menu');
  const menu=page.getByRole('listbox').filter({visible:true});const menuBox=await menu.boundingBox();
  assert.ok(menuBox.x>=0&&menuBox.x+menuBox.width<=390);
  await page.screenshot({path:path.join(output,'device-sort-mobile.png')});await sort.press('Escape');await close();
  const second=await context.newPage();await second.goto(url);await second.locator('.pm-room-group').first().waitFor();
  await open();await chooseOption(sort,'model');await close();
  await until(async()=>JSON.stringify(await second.locator('.pm-room-group').first().locator('[data-open-control]').evaluateAll(els=>els.map(el=>el.dataset.openControl)))===JSON.stringify([ids[0],ids[2],ids[3]]));
  await second.close();
  await page.evaluate(()=>localStorage.setItem('pm-device-sort','invalid'));await page.reload();await page.locator('.pm-room-group').first().waitFor();
  await open();assert.equal(await sort.getAttribute('data-value'),'room');await close();
  devices=devices.map((d,i)=>({...d,room:i===0?'':i===1?'__proto__':'同一空间'}));await page.clock.fastForward(5000);
  await until(async()=>await page.locator('.pm-room-group').count()===3);
  assert.ok((await groups()).some(g=>g.heading==='同一空间2 台设备'&&g.ids.length===2));
  assert.equal((await groups()).at(-1).heading,'未分组1 台设备');
  assert.deepEqual(errors,[]);
  console.log('Room grouping and sorting passed: one header per space, natural/stable sorting, selection, filters, live changes, persistence, cross-tab sync, custom menus and 320–1440px in both themes.');
 } finally {
  await browser.close();for(const f of fixtures)await f.close();child.kill();
  await new Promise(resolve=>child.exitCode!==null?resolve():child.once('exit',resolve));
  assert.ok(directory.startsWith(path.join(tmpdir(),'pm-device-order-')));
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
