/* Real Go executable + loopback firmware fixtures; no Node backend. */
const {chromium} = require('playwright');
const {spawn,spawnSync} = require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync} = require('node:fs');
const {tmpdir} = require('node:os');
const path = require('node:path');
const assert = require('node:assert/strict');
(async()=>{
 const {httpDevice,freePort,until}=await import('./fixtures.js');
 const source=path.resolve(__dirname,'..');
 const exists=require('node:fs').existsSync;
 const workspace=['TC1','M1','A1'].every(name=>exists(path.resolve(source,'..',name)))?path.resolve(source,'..'):source;
 const directory=mkdtempSync(path.join(tmpdir(),'pm-go-browser-'));
 const port=await freePort(),url=`http://127.0.0.1:${port}/app/phicomm-manager/`;
 const executable=process.env.MANAGER_GO_BINARY || path.join(workspace,`build/tools/phicomm-manager-go-test${process.platform==='win32'?'.exe':''}`);
 if(!process.env.MANAGER_GO_BINARY){
  mkdirSync(path.dirname(executable),{recursive:true});
  const bundled=path.join(workspace,'build/tools/go-native/go/bin/go.exe');
  const go=process.env.GO_BIN || (process.platform==='win32'&&exists(bundled)?bundled:'go');
  const build=spawnSync(go,['build','-trimpath','-o',executable,'.'],{cwd:path.resolve(__dirname,'..'),env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
  assert.equal(build.status,0,'Go executable build failed');
 }
 const child=spawn(executable,['--dev','--listen',`127.0.0.1:${port}`,'--data',directory],{windowsHide:true,stdio:['ignore','pipe','pipe']});
 let logs='';child.stdout.on('data',c=>logs+=c);child.stderr.on('data',c=>logs+=c);
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],errors=[],output=path.resolve(__dirname,'../test-results');mkdirSync(output,{recursive:true});
 try {
  let ready=false;
  for(let i=0;i<80;i++){try{const r=await fetch(url);if(r.ok){ready=true;break}}catch{} await new Promise(r=>setTimeout(r,100));}
  assert.ok(ready,logs);
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light'});
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  // Discovery transport itself is tested in Go against a single loopback host.
  let discovery={interfaces:[],state:'complete',phase:'done',mdns:'complete',mode:'auto',devices:[],protectedAddresses:[],checked:0,total:0};
  const scans=[];
  await context.route('**/api/discovery',route=>{
   if(route.request().method()==='POST'){scans.push(route.request().postDataJSON());}
   if(route.request().method()==='DELETE'){discovery={...discovery,state:'cancelled'};}
   return route.fulfill({json:discovery});
  });
  await page.goto(url);await page.getByRole('button',{name:'添加设备',exact:true}).waitFor();
  assert.equal(await page.getByLabel('管理密码',{exact:true}).count(),0);
  for(const [model,mac,name] of [['TC1','aabbcc000201','客厅排插'],['M1','aabbcc000202','书房检测仪'],['A1','aabbcc000203','卧室净化器']]){
   const f=await httpDevice(model,mac,'',{unsupportedActions:model==='TC1'?['info.get']:[]});fixtures.push(f);
   if(model==='TC1'){delete f.state.model;f.state.version='0.9.6-ed4474'}
   await page.getByRole('button',{name:'添加设备',exact:true}).click();
   const add=page.getByRole('dialog',{name:'添加设备',exact:true});
   await add.getByLabel('设备名称',{exact:true}).fill(name);await add.getByLabel('设备型号',{exact:true}).selectOption(model);
   await add.getByLabel('设备内网地址',{exact:true}).fill(f.address);await add.getByLabel('所属空间',{exact:true}).fill('客厅');
   await page.locator('[form="add-form"]').click();await add.waitFor({state:'hidden'});
  }
  await page.locator('[data-device="tc1-aabbcc000201"]').click();
  await page.getByRole('switch',{name:'客厅排插插口 1',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-action="outlet.set"][data-index="0"]').getAttribute('aria-checked')==='true');
  assert.equal(fixtures[0].state.socket_states[0],1);
  await page.getByRole('button',{name:'电价设置',exact:true}).click();
  const settings=page.getByRole('dialog',{name:'设置',exact:true});
  await settings.getByLabel('计费方式',{exact:true}).selectOption('flat');await settings.getByLabel('当前电价（元/度）',{exact:true}).fill('0.6');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  assert.equal(await page.getByRole('button',{name:'刷新',exact:true}).count(),0);
  await page.getByText('预估消费 ¥0.00',{exact:false}).waitFor({timeout:25000});
  fixtures[0].power.energy_kwh=3.23;fixtures[0].power.power_watts=43.2;fixtures[0].state.socket_states[1]=1;
  // Device changes reach the page through background collection and synchronization alone.
  await page.getByText('预估消费 ¥1.20',{exact:false}).waitFor({timeout:25000});
  assert.equal(await page.locator('.pm-measurements .pm-metric-value').first().textContent(),'43.2W');
  assert.equal(await page.getByRole('switch',{name:'客厅排插插口 2',exact:true}).getAttribute('aria-checked'),'true');
  // Simulate a suspended mobile page while the real backend keeps collecting.
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));});
  fixtures[0].power.power_watts=51.7;
  await until(async()=>{const data=await (await fetch(`${url}api/devices`)).json();return data.devices.find(d=>d.id==='tc1-aabbcc000201').state.power===51.7;},25000);
  assert.equal(await page.locator('.pm-measurements .pm-metric-value').first().textContent(),'43.2W');
  const resumed=page.waitForResponse(r=>r.url()===`${url}api/devices`&&r.request().method()==='GET',{timeout:2000});
  await page.evaluate(()=>{delete document.hidden;document.dispatchEvent(new Event('visibilitychange'));});await resumed;
  await page.waitForFunction(()=>document.querySelector('.pm-measurements .pm-metric-value').textContent==='51.7W');
  const reconnected=page.waitForResponse(r=>r.url()===`${url}api/devices`&&r.request().method()==='GET',{timeout:2000});
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));await reconnected;
  await page.screenshot({path:path.join(output,'go-billing-desktop.png'),fullPage:true});
  await page.getByRole('button',{name:'设置',exact:true}).click();await settings.getByRole('button',{name:'电价设置',exact:true}).click();
  await settings.getByLabel('计费方式',{exact:true}).selectOption('tou');await settings.getByLabel('峰时电价（元/度）',{exact:true}).fill('1.2');await settings.getByLabel('谷时电价（元/度）',{exact:true}).fill('0.3');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();await settings.getByRole('button',{name:'电价设置',exact:true}).click();
  assert.equal(await settings.getByLabel('峰时电价（元/度）',{exact:true}).inputValue(),'1.2');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.getByRole('button',{name:'历史记录',exact:true}).click();const history=page.getByRole('dialog',{name:'历史记录',exact:true});await history.locator('.pm-event').first().waitFor();await history.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.getByRole('button',{name:'空间',exact:true}).click();
  for(const width of [320,390,768,1440]){await page.setViewportSize({width,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
  await page.setViewportSize({width:390,height:844});await page.locator('[data-open-control="tc1-aabbcc000201"]').click();
  const control=page.getByRole('dialog',{name:'设备控制',exact:true});await control.getByText('预估消费 ¥1.20',{exact:false}).waitFor();
  await page.screenshot({path:path.join(output,'go-billing-mobile.png'),fullPage:true});
  await control.getByRole('button',{name:'电价设置',exact:true}).click();await settings.getByLabel('计费方式',{exact:true}).waitFor();
  for(const viewport of [{width:320,height:568},{width:390,height:844},{width:740,height:360}]){await page.setViewportSize(viewport);const box=await settings.evaluate(el=>({top:el.getBoundingClientRect().top,bottom:el.getBoundingClientRect().bottom,footer:el.querySelector('.pm-dialog-footer').getBoundingClientRect().bottom,height:innerHeight,overflow:el.scrollWidth>el.clientWidth}));assert.ok(box.top>=0&&box.bottom<=box.height+1&&box.footer<=box.height,JSON.stringify(box));assert.equal(box.overflow,false);}
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'go-tariff-mobile.png'),fullPage:true});
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.setViewportSize({width:1440,height:1000});
  discovery={interfaces:[{id:'physical',label:'enp4s0 · 10.0.0.9',range:'10.0.0.0/24'},{id:'second',label:'eth1 · 192.168.2.9',range:'192.168.2.0/24'}],interface:'physical',range:'10.0.0.0/24',state:'scanning',phase:'auto',mdns:'complete',mode:'auto',devices:[{id:'tc1-aabbcc009999',model:'TC1',mac:'aabbcc009999',address:'http://10.0.0.223',added:false}],protectedAddresses:['http://10.0.0.224'],checked:12,total:253};
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  const add=page.getByRole('dialog',{name:'添加设备',exact:true});
  await add.getByRole('button',{name:'停止',exact:true}).waitFor();
  assert.equal(scans.at(-1).mode,'auto');assert.equal(scans.at(-1).interface,undefined,'default scan must choose its own interface');
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).count(),1);
  assert.equal(await add.getByRole('button',{name:'重新发现',exact:true}).count(),0);
  assert.equal(await add.getByRole('button',{name:'局域网扫描',exact:true}).count(),0);
  assert.equal(await add.getByLabel('网络接口',{exact:true}).isVisible(),false);
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).isVisible(),false);
  await page.screenshot({path:path.join(output,'react-scan-desktop.png'),fullPage:true});
  await add.getByRole('button',{name:'停止',exact:true}).click();
  await add.getByText('高级选项',{exact:true}).click();
  await add.getByLabel('网络接口',{exact:true}).selectOption('second');
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).inputValue(),'192.168.2.0/24');
  await add.getByLabel('扫描网段',{exact:true}).fill('192.168.2.128/25');
  discovery={...discovery,interface:'second',range:'192.168.2.128/25',state:'complete'};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-scan-status]').textContent==='发现 1 台设备。');
  assert.deepEqual(scans.at(-1),{mode:'auto',interface:'second',cidr:'192.168.2.128/25'});
  await add.locator('[data-discovered-index="0"]').click();
  assert.equal(await add.getByLabel('设备内网地址',{exact:true}).inputValue(),'http://10.0.0.223');
  await add.getByText('需要密码验证的地址（1）',{exact:true}).click();await add.locator('[data-protected-index="0"]').click();
  assert.equal(await add.getByLabel('设备内网地址',{exact:true}).inputValue(),'http://10.0.0.224');
  await page.waitForFunction(()=>document.querySelector('input[name="password"]')===document.activeElement);
  for(const width of [320,390,768]){await page.setViewportSize({width,height:844});assert.equal(await add.evaluate(el=>el.scrollWidth>el.clientWidth),false);}
  await page.setViewportSize({width:390,height:844});
  await add.getByRole('button',{name:'关闭添加设备',exact:true}).focus();
  await add.evaluate(el=>{el.querySelectorAll('details').forEach(details=>details.open=false);el.querySelector('.pm-dialog-body').scrollTop=0;});
  await page.screenshot({path:path.join(output,'react-scan-mobile.png'),fullPage:true});
  await page.keyboard.press('Escape');await add.waitFor({state:'hidden'});
  await page.setViewportSize({width:1440,height:1000});
  await page.getByRole('button',{name:'列表',exact:true}).click();await page.locator('[data-device="m1-aabbcc000202"]').click();
  await page.getByRole('slider',{name:'书房检测仪屏幕亮度',exact:true}).focus();await page.keyboard.press('End');await page.keyboard.press('Tab');
  await page.waitForFunction(()=>document.querySelector('[aria-label="书房检测仪屏幕亮度"]').value==='100');
  await until(()=>fixtures[1].state.brightness_requested===4);
  await page.locator('[data-device="a1-aabbcc000203"]').click();await page.getByRole('button',{name:'自动净化',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('.pm-mode[aria-pressed="true"]').textContent==='自动净化');assert.equal(fixtures[2].state.fan_mode,'auto');
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByLabel('设备',{exact:true}).selectOption('tc1-aabbcc000201');
  await settings.getByLabel('设备名称',{exact:true}).fill('客厅主排插');await settings.getByLabel('所属空间',{exact:true}).fill('书房');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).fill('10.0.0.9');
  await settings.getByRole('button',{name:'设备连接',exact:true}).click();
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'客厅主排插');
  await settings.getByLabel('设备',{exact:true}).selectOption('m1-aabbcc000202');await settings.getByLabel('设备名称',{exact:true}).fill('检测仪草稿');
  await settings.getByLabel('设备',{exact:true}).selectOption('tc1-aabbcc000201');assert.equal(await settings.getByLabel('所属空间',{exact:true}).inputValue(),'书房');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'客厅主排插');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();assert.equal(await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).inputValue(),'10.0.0.9');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();assert.equal(await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).inputValue(),'');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Theme changes must keep configuration drafts and cover native inputs and modal surfaces.
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByLabel('设备',{exact:true}).selectOption('tc1-aabbcc000201');
  await settings.getByLabel('设备名称',{exact:true}).fill('外观切换保留草稿');
  await settings.getByRole('button',{name:'外观',exact:true}).click();
  assert.equal(await settings.getByLabel('主题',{exact:true}).inputValue(),'system');
  await settings.getByLabel('主题',{exact:true}).selectOption('dark');
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='dark');
  assert.equal(await settings.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  assert.equal(await settings.getByLabel('主题',{exact:true}).evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  assert.equal(await settings.getByRole('button',{name:'保存配置',exact:true}).count(),0);
  await settings.getByRole('button',{name:'设备连接',exact:true}).click();
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'外观切换保留草稿');
  await settings.getByRole('button',{name:'外观',exact:true}).click();
  await page.screenshot({path:path.join(output,'theme-dark-settings.png'),fullPage:true});
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.getByRole('button',{name:'列表',exact:true}).click();
  await page.locator('[data-device="tc1-aabbcc000201"]').click();
  await page.screenshot({path:path.join(output,'theme-dark-desktop.png'),fullPage:true});
  for(const width of [320,390,700,768,900,1440]){await page.setViewportSize({width,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:path.join(output,'theme-dark-mobile.png'),fullPage:true});
  await page.getByRole('button',{name:'空间',exact:true}).click();await page.locator('[data-open-control="tc1-aabbcc000201"]').click();
  assert.equal(await control.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await page.screenshot({path:path.join(output,'theme-dark-control.png'),fullPage:true});
  await control.getByRole('button',{name:'关闭设备控制',exact:true}).click();
  await page.getByRole('button',{name:'历史记录',exact:true}).click();
  assert.equal(await history.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await history.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  assert.equal(await add.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await add.getByRole('button',{name:'关闭添加设备',exact:true}).click();
  // Explicit choices survive reloads and override the system; automatic mode reacts live.
  await page.reload();await page.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).colorScheme),'dark');
  await page.getByRole('button',{name:'设置',exact:true}).click();await settings.getByRole('button',{name:'外观',exact:true}).click();
  assert.equal(await settings.getByLabel('主题',{exact:true}).inputValue(),'dark');
  await page.emulateMedia({colorScheme:'dark'});
  await settings.getByLabel('主题',{exact:true}).selectOption('light');
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='light');
  await settings.getByLabel('主题',{exact:true}).selectOption('system');
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='dark');
  await page.emulateMedia({colorScheme:'light'});
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='light');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // A fresh browser with no stored preference must start in its system's theme.
  const fresh=await browser.newContext({viewport:{width:390,height:844},colorScheme:'dark'});
  const first=await fresh.newPage();await first.goto(url);await first.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await first.evaluate(()=>getComputedStyle(document.documentElement).colorScheme),'dark');
  await fresh.close();
  assert.deepEqual(errors,[]);
  console.log('Go + React browser passed: light/dark/system themes, persisted preferences, modal colors and drafts, automatic device/billing updates, foreground/network recovery, three models, keyboard controls, history, combined scan, Escape and 320–1440px.');
 } finally {
  await browser.close();for(const f of fixtures)await f.close();
  if(child.exitCode===null){child.kill();await new Promise(resolve=>child.once('exit',resolve));}
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1});
