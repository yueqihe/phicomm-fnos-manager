// Real manager and loopback devices; no physical outlet is switched by this test.
const assert=require('node:assert/strict');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const path=require('node:path');
const {tmpdir}=require('node:os');
const {chromium}=require('playwright');

(async()=>{
 const {httpDevice,freePort,until}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-device-icons-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 if(!process.env.MANAGER_GO_BINARY) {
  const result=spawnSync(process.env.GO_BIN || 'go',['build','-mod=readonly','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(result.status,0);
 }
 const base=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(base).host,'--data',directory],{windowsHide:true,stdio:'ignore'});
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],output=path.join(root,'test-results'),models=['TC1','M1','A1','DAIKIN','S30-V1B'];mkdirSync(output,{recursive:true});
 try {
  await until(async()=>{try{return (await fetch(base)).ok;}catch{return false;}});
  for(const [i,model] of models.entries()) {
   const f=await httpDevice(model,`aabbcc00150${i}`);fixtures.push(f);
   if(f.state.socket_states) f.state.socket_states[0]=1;
   if(model==='A1') Object.assign(f.state,{fan_on:true,fan_state:'running',speed:40,percentage:40,applied_speed:40});
   const response=await fetch(base+'api/devices',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model,method:'lan',address:f.address,name:`动画 ${model}`,room:'动效测试'})});
   assert.equal(response.status,201);
  }
  const context=await browser.newContext({viewport:{width:1100,height:720},colorScheme:'light',reducedMotion:'no-preference'});
  await context.route('**/api/discovery/automatic?*',r=>r.fulfill({json:{state:'complete',devices:[]}}));
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base);await page.mouse.move(0,0);
  const icon=model=>page.locator(`.pm-device-glyph[data-model="${model}"]`);
  for(const model of models) {
   await icon(model).waitFor();
   assert.equal(await icon(model).getAttribute('data-icon-active'),'true',model);
   const start=await icon(model).evaluate(el=>el.getAnimations({subtree:true}).filter(a=>a.effect.getTiming().iterations===Infinity).map(a=>a.currentTime));
   assert.ok(start.length>0,`${model} must keep animating without hover`);
   await until(()=>icon(model).evaluate((el,start)=>el.getAnimations({subtree:true}).filter(a=>a.effect.getTiming().iterations===Infinity).every((a,i)=>a.currentTime>start[i]+80),start));
  }
  const fan=icon('A1'),duration=await fan.evaluate(el=>getComputedStyle(el.querySelector('.pm-glyph-rotor')).animationDuration);
  const snapshot=await (await fetch(base+'api/devices')).json();
  let devices=structuredClone(snapshot.devices);
  await context.route('**/api/devices',r=>r.request().method()==='GET'?r.fulfill({json:{...snapshot,devices}}):r.continue());
  async function paint() {await page.reload();await page.locator('.pm-device-glyph').first().waitFor();}
  devices=devices.map(d=>d.model==='A1'?{...d,state:{...d.state,appliedSpeed:90}}:d);await paint();
  assert.ok(parseFloat(await fan.evaluate(el=>getComputedStyle(el.querySelector('.pm-glyph-rotor')).animationDuration))<parseFloat(duration),'actual fan output must control rotation speed');
  for(const width of [320,390,900]) {
   await page.setViewportSize({width,height:width<700?844:720});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  }
  await page.setViewportSize({width:1100,height:720});
  await page.screenshot({path:path.join(output,'device-icons-cards.png'),fullPage:true});
  await page.getByRole('button',{name:'列表',exact:true}).click();
  for(const model of models) assert.ok(await icon(model).evaluate(el=>el.getAnimations({subtree:true}).some(a=>a.effect.getTiming().iterations===Infinity)),`${model} list icon must also animate`);
  await page.screenshot({path:path.join(output,'device-icons-list.png'),fullPage:true});
  await page.emulateMedia({reducedMotion:'reduce'});
  assert.equal(await page.locator('.pm-device-glyph').evaluateAll(els=>els.flatMap(el=>el.getAnimations({subtree:true})).some(a=>a.effect.getTiming().iterations===Infinity)),false,'reduced motion must stop all device loops');
  await page.emulateMedia({reducedMotion:'no-preference'});
  await page.getByRole('button',{name:'空间',exact:true}).click();
  // Keep positive power/speed values to ensure disabled states take priority.
  devices=devices.map(d=>({...d,state:{...d.state,on:false,outlets:d.state.outlets?.map(()=>false),sensorValid:false,...(d.model==='DAIKIN'?Object.fromEntries(['pm25','pm10','co2','temp','humidity','tvoc','hcho'].map(key=>[key,null])):{})}}));
  await paint();
  for(const model of models) assert.equal(await icon(model).getAttribute('data-icon-active'),'false',`${model}: off/invalid readings must not animate`);
  assert.equal(await page.locator('.pm-device-glyph').evaluateAll(els=>els.flatMap(el=>el.getAnimations({subtree:true})).some(a=>a.effect.getTiming().iterations===Infinity)),false);
  devices=snapshot.devices.map(d=>({...d,online:false}));await paint();
  for(const model of models) assert.equal(await icon(model).getAttribute('data-icon-active'),'false',`${model}: offline overrides its last working state`);
  assert.deepEqual(errors,[]);
  console.log('Device icon motion passed: all five models animate without hover in cards/list, fan speed follows output, off/offline/invalid data and reduced motion stop loops, 320–1100px.');
 } finally {
  await browser.close();for(const f of fixtures)await f.close();
  if(child.exitCode===null){child.kill();await new Promise(resolve=>child.once('exit',resolve));}
  rmSync(directory,{recursive:true,force:true,maxRetries:5,retryDelay:200});
 }
})().catch(error=>{console.error(error);process.exitCode=1;});
