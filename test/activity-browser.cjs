const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');

(async()=>{
 const {httpDevice,freePort,until}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-activity-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 if(!process.env.MANAGER_GO_BINARY) {
  const result=spawnSync(process.env.GO_BIN || 'go',['build','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(result.status,0);
 }
 const url=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(url).host,'--data',directory],{windowsHide:true,stdio:'ignore'});
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixture=await httpDevice('TC1','aabbcc001201'),errors=[];
 try {
  await until(async()=>{try{return (await fetch(url)).ok;}catch{return false;}});
  const session=await (await fetch(url+'api/session')).json();
  const request=async(route,body)=>{
   const response=await fetch(url+'api/'+route,{method:'POST',headers:{'Content-Type':'application/json',Origin:new URL(url).origin,'X-Manager-CSRF':session.csrfToken},body:JSON.stringify(body)});
   assert.ok(response.ok,await response.clone().text());return response.json();
  };
  Object.assign(fixture.state,{ip:'192.168.1.20',ssid:'Original Wi-Fi',gateway:'192.168.1.1'});
  const {device}=await request('devices',{model:'TC1',method:'lan',name:'测试排插',address:fixture.address});
  await request(`devices/${device.id}/control`,{action:'outlet.set',params:{index:2,on:true}});
  Object.assign(fixture.state,{ip:'192.168.9.20',ssid:'New Wi-Fi',gateway:'192.168.9.1',child_lock:true});
  fixture.state.socket_states[4]=1;
  await request(`devices/${device.id}/refresh`,{});
  const context=await browser.newContext({viewport:{width:1280,height:850}}),page=await context.newPage();
  page.on('pageerror',e=>errors.push(e.message));
  await context.route('**/api/discovery/automatic?*',r=>r.fulfill({json:{state:'complete',devices:[]}}));
  await context.route('**/api/updates',r=>r.fulfill({json:{status:'current',message:'当前已是最新正式版'}}));
  await page.clock.install();
  await page.goto(url);await page.locator('.pm-history-sparkline').first().click();
  const history=page.getByRole('dialog',{name:'历史记录',exact:true});
  await history.getByRole('tab',{name:'活动记录',exact:true}).click();await history.locator('.pm-event').first().waitFor();
  const texts=()=>history.locator('.pm-event').allTextContents();
  let rows=await texts();
  for(const label of ['设备上线','IP 变更','Wi-Fi 变更','网关变更','开关操作','开关变化','童锁变化']) assert.ok(rows.some(row=>row.includes(label)),label);
  assert.ok(rows.some(row=>row.includes('192.168.1.20 → 192.168.9.20')));
  assert.ok(rows.some(row=>row.includes('插口 3（三）：开启')&&row.includes('已确认')));
  assert.ok(rows.some(row=>row.includes('插口 5（五）：关闭 → 开启')&&row.includes('已记录')));
  assert.equal(rows.filter(row=>row.includes('outlet.set')).length,0);
  assert.equal(rows.filter(row=>row.includes('插口 3')).length,1);
  fixture.state.ssid='Live Wi-Fi <test>';
  await request(`devices/${device.id}/refresh`,{});
  await page.clock.fastForward(10000);
  await until(async()=>(await texts()).some(row=>row.includes('New Wi-Fi → Live Wi-Fi <test>')));
  const output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
  for(const theme of ['light','dark']) {
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:850});
    assert.equal(await history.evaluate(el=>el.scrollWidth>el.clientWidth),false,`${theme}/${width} dialog overflow`);
    assert.equal(await history.locator('.pm-dialog-body').evaluate(el=>el.scrollWidth>el.clientWidth),false,`${theme}/${width} body overflow`);
    if(width===390) await page.screenshot({path:path.join(output,`activity-${theme}-mobile.png`)});
   }
  }
  // Polling must preserve older pages and avoid repeating already loaded rows.
  let newest=205;
  await context.route('**/api/history*',r=>{
   const before=Number(new URL(r.request().url()).searchParams.get('before') || newest+1);
   const records=Array.from({length:newest},(_,i)=>({id:newest-i,time:new Date().toISOString(),device_id:device.id,name:'测试设备',action:'device.offline',outcome:'observed',message:`离线记录 ${newest-i}`})).filter(row=>row.id<before).slice(0,100);
   return r.fulfill({json:{records,next:records.length===100?records.at(-1).id:null}});
  });
  await history.getByRole('tab',{name:'历史数据',exact:true}).click();await history.getByRole('tab',{name:'活动记录',exact:true}).click();
  await until(async()=>await history.locator('.pm-event').count()===100);
  await history.getByRole('button',{name:'加载更早记录',exact:true}).click();
  await until(async()=>await history.locator('.pm-event').count()===200);
  newest=206;await page.clock.fastForward(10000);
  await until(async()=>await history.locator('.pm-event').count()===201);
  rows=await texts();assert.equal(new Set(rows).size,201);assert.ok(rows[0].includes('离线记录 206'));
  await history.getByRole('button',{name:'加载更早记录',exact:true}).click();
  await until(async()=>await history.locator('.pm-event').count()===206);
  assert.equal(await history.getByRole('button',{name:'加载更早记录',exact:true}).count(),0);
  assert.deepEqual(errors,[]);
  console.log('Activity UI passed: real LAN events and commands, readable changes, live refresh, deduplication, pagination, light/dark and 320–1440px.');
 } finally {
  await browser.close();await fixture.close();child.kill();
  await new Promise(resolve=>child.exitCode!==null?resolve():child.once('exit',resolve));
  assert.ok(directory.startsWith(path.join(tmpdir(),'pm-activity-')));
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
