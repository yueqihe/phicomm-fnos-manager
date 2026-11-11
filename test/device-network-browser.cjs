const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');
const {chooseOption}=require('./select-helpers.cjs');

(async()=>{
 const {httpDevice,freePort}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'),directory=mkdtempSync(path.join(tmpdir(),'pm-device-network-'));
 const executable=process.env.MANAGER_GO_BINARY || path.join(directory,process.platform==='win32'?'manager.exe':'manager');
 if(!process.env.MANAGER_GO_BINARY) {
  const build=spawnSync(process.env.GO_BIN || 'go',['build','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(build.status,0);
 }
 const url=`http://127.0.0.1:${await freePort()}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',new URL(url).host,'--data',directory],{windowsHide:true,stdio:'ignore'});
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],errors=[];
 try {
  let ready=false;
  for(let i=0;i<80;i++){try{if((await fetch(url)).ok){ready=true;break;}}catch{}await new Promise(r=>setTimeout(r,100));}
  assert.ok(ready);
  const session=await (await fetch(url+'api/session')).json();
  for(const [i,model] of ['TC1','M1','A1','DAIKIN','S30-V1B'].entries()) {
   const mac=`aabbcc00100${i}`,f=await httpDevice(model,mac);fixtures.push(f);
   Object.assign(f.state,{ssid:`Lab <IoT> ${model}`,rssi:-45-i,ip:`192.168.50.${10+i}`,mask:'255.255.255.0',gateway:'192.168.50.1',wifi_connected:true});
   const response=await fetch(url+'api/devices',{method:'POST',headers:{'Content-Type':'application/json',Origin:new URL(url).origin,'X-Manager-CSRF':session.csrfToken},body:JSON.stringify({model,mac,method:'lan',address:f.address,name:`测试 ${model}`})});
   assert.ok(response.ok,await response.text());
  }
  const context=await browser.newContext({viewport:{width:1280,height:850}}),page=await context.newPage();
  page.on('pageerror',e=>errors.push(e.message));
  await context.route('**/api/discovery/automatic?*',r=>r.fulfill({json:{state:'complete',devices:[]}}));
  await context.route('**/api/updates',r=>r.fulfill({json:{status:'current',message:'当前已是最新正式版'}}));
  const settings=page.getByRole('dialog',{name:'设置',exact:true}),network=settings.getByRole('region',{name:'设备网络信息',exact:true});
  const open=async()=>{await page.getByRole('button',{name:'设置',exact:true}).click();await network.waitFor();};
  const rows=()=>network.evaluate(el=>Object.fromEntries([...el.querySelectorAll('dl>div')].map(row=>[row.querySelector('dt').textContent,row.querySelector('dd').textContent])));
  await page.goto(url);await open();
  for(const [i,model] of ['TC1','M1','A1','DAIKIN','S30-V1B'].entries()) {
   await chooseOption(settings.getByRole('combobox',{name:'设备',exact:true}),`${model.toLowerCase()}-aabbcc00100${i}`);
   const values=await rows();
   assert.equal(values['Wi-Fi'],`Lab <IoT> ${model}`);
   assert.equal(values['信号强度'],`${-45-i} dBm`);
   assert.equal(values['IP 地址'],`192.168.50.${10+i}`);
   assert.equal(values['MAC 地址'],`AA:BB:CC:00:10:0${i}`);
   assert.equal(values['默认网关'],'192.168.50.1');
   const link=network.getByRole('link',{name:'打开设备高级设置（新标签页）',exact:true});
   assert.equal(await link.getAttribute('href'),fixtures[i].address+'/#settings/device');
   assert.equal(await link.getAttribute('target'),'_blank');
   assert.equal(await link.getAttribute('rel'),'noopener noreferrer');
   assert.equal(await settings.locator('.pm-dialog-body .pm-btn-danger').count(),0);
   assert.equal(await settings.locator('.pm-dialog-footer .pm-btn-danger').count(),1);
  }
  // A draft address must not send the user to an unverified destination.
  await settings.getByLabel('设备内网地址',{exact:true}).fill('http://192.168.90.90');
  const link=network.getByRole('link',{name:'打开设备高级设置（新标签页）',exact:true});
  assert.equal(await link.getAttribute('href'),fixtures[4].address+'/#settings/device');
  await context.route(fixtures[4].address+'/**',r=>r.fulfill({contentType:'text/html',body:'<h1>Device settings fixture</h1>'}));
  const popupPromise=page.waitForEvent('popup');await link.click();const popup=await popupPromise;
  await popup.waitForLoadState();assert.equal(popup.url(),fixtures[4].address+'/#settings/device');await popup.close();
  const output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
  for(const theme of ['light','dark']) {
   await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
   for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:850});await network.scrollIntoViewIfNeeded();
    assert.equal(await settings.locator('.pm-dialog-body').evaluate(el=>el.scrollWidth>el.clientWidth),false,`${theme}/${width}`);
    const box=await link.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width);
    if(width===390) await page.screenshot({path:path.join(output,`device-network-${theme}-mobile.png`)});
   }
  }
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Rendering from fresh API data also updates links after an address change.
  const snapshot=await (await fetch(url+'api/devices')).json();
  let first=snapshot.devices[0];
  await context.route('**/api/devices',r=>r.fulfill({json:{...snapshot,devices:[first]}}));
  first={...first,address:'',method:'mqtt',lan:false,mqtt:true,state:{...first.state,network:{ssid:'MQTT Wi-Fi',rssi:-71,ip:'fd00::10'}}};
  await page.reload();await open();
  assert.equal(await network.getByRole('link').getAttribute('href'),'http://[fd00::10]/#settings/device');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  first={...first,address:'http://192.168.60.10',state:{...first.state,network:{ssid:'Changed Wi-Fi',rssi:-38,ip:'192.168.60.10'}}};
  await page.reload();await open();assert.equal((await rows())['Wi-Fi'],'Changed Wi-Fi');
  assert.equal(await network.getByRole('link').getAttribute('href'),'http://192.168.60.10/#settings/device');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  for(const address of ['', 'javascript:alert(1)', 'https://user:secret@192.168.50.10']) {
   first={...first,address,state:{...first.state,network:{}}};
   await page.reload();await open();
   assert.equal((await rows())['信号强度'],'未上报');
   assert.equal(await network.getByRole('link').count(),0);
   assert.equal(await network.getByRole('button',{name:'设备高级设置',exact:true}).isDisabled(),true);
   await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  }
  assert.deepEqual(errors,[]);
  console.log('Device network UI passed: five models, correct metadata, external settings link, saved address, IP changes, MQTT/IPv6 fallback, missing/unsafe addresses, light/dark and 320–1440px.');
 } finally {
  await browser.close();for(const fixture of fixtures)await fixture.close();
  child.kill();await new Promise(resolve=>child.exitCode!==null?resolve():child.once('exit',resolve));
  assert.ok(directory.startsWith(path.join(tmpdir(),'pm-device-network-')));
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
