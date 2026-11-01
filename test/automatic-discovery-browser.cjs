/* Page lifecycle and one-minute timing with a real manager and isolated discovery responses. */
const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');

(async()=>{
 const {httpDevice,freePort,until:waitUntil}=await import('./fixtures.js');
 const until=async(fn,label)=>{try{await waitUntil(fn)}catch(e){throw new Error(label,{cause:e})}};
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-auto-discovery-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,`manager${process.platform==='win32'?'.exe':''}`);
 if(!process.env.MANAGER_GO_BINARY) {
  const build=spawnSync(process.env.GO_BIN || 'go',['build','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(build.status,0);
 }
 const port=await freePort(),url=`http://127.0.0.1:${port}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',`127.0.0.1:${port}`,'--data',directory],{windowsHide:true,stdio:['ignore','pipe','pipe']});
 let logs='';child.stdout.on('data',s=>logs+=s);child.stderr.on('data',s=>logs+=s);
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
 try {
  await until(async()=>{try{return (await fetch(url)).ok}catch{return false}},'manager startup');
  for(const [model,mac] of [['TC1','aabbcc000801'],['M1','aabbcc000802'],['DAIKIN','aabbcc000803']]) fixtures.push(await httpDevice(model,mac));
  const candidate=(i,extra={})=>({id:`${fixtures[i].model.toLowerCase()}-${fixtures[i].mac}`,model:fixtures[i].model,mac:fixtures[i].mac,address:fixtures[i].address,added:false,...extra});
  // Fixture objects expose their protocol state as the source of identity.
  fixtures.forEach(f=>{f.model=f.state.model.toUpperCase();f.mac=f.state.device_id;});
  await fetch(url+'api/devices',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:'TC1',method:'lan',address:fixtures[0].address,name:'已有排插'})});
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light'}),page=await context.newPage(),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  let snapshot={state:'complete',interfaces:[],devices:[candidate(0,{added:true}),candidate(1),candidate(2),{...candidate(2),id:'m1-deleted',model:'M1',ignored:true}],protectedAddresses:[]};
  const requests=[];let fail=false,deny=false;
  await context.route('**/api/discovery/automatic?*',route=>{
   requests.push({method:route.request().method(),client:new URL(route.request().url()).searchParams.get('client')});
   return route.fulfill(fail ? {status:503,json:{error:'暂时无法扫描'}} : {json:snapshot});
  });
  await context.route('**/api/devices',route=>deny && route.request().method()==='GET' ? route.fulfill({status:401,json:{error:'请重新登录'}}) : route.continue());
  await context.route('**/api/discovery',route=>route.fulfill({json:{...snapshot,state:'complete',devices:[]}}));
  await page.clock.install();await page.clock.pauseAt(new Date(Date.now()+1000));
  await page.goto(url);
  const notice=page.getByRole('region',{name:'发现新设备'});
  await notice.getByText('发现 2 台新设备，是否添加？',{exact:true}).waitFor();
  const starts=()=>requests.filter(r=>r.method==='POST').length;
  assert.equal(starts(),1,'opening the application must scan once');
  assert.match(requests[0].client,/^[a-f0-9]{32}$/);
  assert.equal((await (await fetch(url+'api/devices')).json()).devices.length,1,'discovery must not add devices automatically');
  await page.clock.fastForward(59999);assert.equal(starts(),1,'no early scan');
  await page.clock.fastForward(1);await until(()=>starts()===2,'one-minute scan');
  snapshot={...snapshot,state:'scanning'};
  await page.clock.fastForward(60000);await until(()=>starts()===3,'ongoing scan');
  await page.waitForLoadState('networkidle');
  const polls=requests.filter(r=>r.method==='GET').length;
  await page.clock.fastForward(3000);await until(()=>requests.filter(r=>r.method==='GET').length===polls+1,'scan heartbeat');
  await page.waitForLoadState('networkidle');
  assert.equal(starts(),3,'result polling must not start another scan');
  snapshot={...snapshot,state:'complete'};
  await page.clock.fastForward(3000);await until(()=>requests.filter(r=>r.method==='GET').length===polls+2,'completed result');
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   if(width<=390) assert.ok(await notice.getByRole('button').first().evaluate(el=>el.getBoundingClientRect().height>=44));
  }
  await page.screenshot({path:path.join(output,'automatic-discovery-desktop.png'),fullPage:true,animations:'disabled'});
  await page.setViewportSize({width:390,height:844});await page.emulateMedia({colorScheme:'dark'});
  await page.clock.fastForward(200);
  await page.screenshot({path:path.join(output,'automatic-discovery-mobile-dark.png'),fullPage:true,animations:'disabled'});
  await notice.getByRole('button',{name:'添加 M1 aabbcc000802',exact:true}).click();
  const add=page.getByRole('dialog',{name:'添加设备',exact:true});
  assert.equal(await add.getByLabel('设备型号',{exact:true}).getAttribute('data-value'),'M1');
  assert.equal(await add.getByLabel('设备内网地址',{exact:true}).inputValue(),fixtures[1].address);
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).count(),0,'confirmed discovery needs no second scan form');
  await page.keyboard.press('Escape');await add.waitFor({state:'hidden'});
  await notice.getByRole('button',{name:'添加 M1 aabbcc000802',exact:true}).click();
  await page.locator('[form="add-form"]').click();await add.waitFor({state:'hidden'});
  await notice.getByText('发现 1 台新设备，是否添加？',{exact:true}).waitFor();
  assert.equal((await (await fetch(url+'api/devices')).json()).devices.length,2);
  await notice.getByRole('button',{name:'暂不添加 DAIKIN aabbcc000803',exact:true}).click();
  await notice.waitFor({state:'hidden'});
  snapshot={...snapshot,devices:snapshot.devices.map(d=>({...d,address:'http://192.168.9.20'}))};
  let expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'dismissed device rescan');
  assert.equal(await notice.count(),0,'dismissed identity must stay quiet even if its IP changes');
  snapshot={...snapshot,devices:[{...candidate(2),id:'a1-aabbcc000804',model:'A1',mac:'aabbcc000804'}]};
  expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'new identity rescan');
  await notice.getByText('发现 1 台新设备，是否添加？',{exact:true}).waitFor();
  // An open settings form retains focus and its draft when a new result arrives.
  await page.getByRole('button',{name:'设置',exact:true}).click();
  const settings=page.getByRole('dialog',{name:'设置',exact:true});
  await settings.getByLabel('设备名称',{exact:true}).fill('编辑中的草稿');
  expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'scan while editing');
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'编辑中的草稿');
  assert.equal(await settings.isVisible(),true);
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Suspend/close stops the timer; a restored page starts once and resumes minutes.
  await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true})));
  await until(()=>requests.some(r=>r.method==='DELETE'),'page cleanup');
  expected=starts();await page.clock.fastForward(180000);assert.equal(starts(),expected);
  await page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
  await until(()=>starts()===expected+1,'restored page scan');
  expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'restored minute timer');
  fail=true;expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'failed scan');
  assert.equal(await page.getByText('暂时无法扫描',{exact:true}).count(),0,'background failure should not interrupt controls');
  fail=false;expected=starts()+1;await page.clock.fastForward(60000);await until(()=>starts()===expected,'retry next minute');
  deny=true;await page.clock.fastForward(5000);
  await page.getByText('请使用飞牛 NAS 管理员账号登录，并从桌面重新打开应用。',{exact:true}).waitFor();
  expected=starts();await page.clock.fastForward(120000);assert.equal(starts(),expected,'expired login must stop new-device discovery');
  assert.deepEqual(errors,[]);
  console.log('Automatic discovery browser passed: entry scan, 60-second cadence, confirmation, prefilled form, dismiss/dedup, page lifecycle, auth expiry and responsive themes.');
 } catch(e) {console.error(logs);throw e;}
 finally {await browser.close();if(child.exitCode===null){child.kill();await new Promise(r=>child.once('exit',r));}await Promise.all(fixtures.map(f=>f.close()));rmSync(directory,{recursive:true,force:true,maxRetries:5,retryDelay:200});}
})().catch(e=>{console.error(e);process.exitCode=1});
