/* Real Go executable + loopback firmware fixtures; no Node backend. */
const {chromium} = require('playwright');
const {spawn,spawnSync} = require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync} = require('node:fs');
const {tmpdir} = require('node:os');
const path = require('node:path');
const assert = require('node:assert/strict');
const {chooseOption,optionCount}=require('./select-helpers.cjs');
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
  const writeTokens=[];let sessionRequests=0;
  page.on('request',request=>{
   if(request.url()===`${url}api/session`) sessionRequests++;
   if(request.url().startsWith(`${url}api/`)&&!['GET','HEAD'].includes(request.method())) writeTokens.push(request.headers()['x-manager-csrf']);
  });
  // Discovery transport itself is tested in Go against a single loopback host.
  let discovery={interfaces:[],state:'complete',phase:'done',mdns:'complete',mode:'auto',devices:[],protectedAddresses:[],checked:0,total:0};
  const scans=[];let rejectDiscoverySource=false;
  await context.route('**/api/discovery/automatic?*',route=>route.fulfill({json:{state:'complete',devices:[]}}));
  await context.route('**/api/discovery',route=>{
   if(route.request().method()==='POST'){scans.push(route.request().postDataJSON());if(rejectDiscoverySource) return route.fulfill({status:403,json:{error:'请求来源不正确',code:'invalid_origin'}});}
   if(route.request().method()==='DELETE'){discovery={...discovery,state:'cancelled'};}
   return route.fulfill({json:discovery});
  });
  await page.goto(url);await page.getByRole('button',{name:'添加设备',exact:true}).waitFor();
  assert.equal(await page.locator('.pm-app').getAttribute('data-layout'),'rooms');
  assert.deepEqual(await page.locator('.pm-view-option').evaluateAll(els=>els.map(el=>({label:el.getAttribute('aria-label'),text:el.textContent}))),[{label:'空间',text:''},{label:'列表',text:''}]);
  await page.getByRole('button',{name:'列表',exact:true}).click();
  assert.equal(await page.getByLabel('管理密码',{exact:true}).count(),0);
  for(const [model,mac,name] of [['TC1','aabbcc000201','客厅排插'],['M1','aabbcc000202','书房检测仪'],['A1','aabbcc000203','卧室净化器']]){
   const f=await httpDevice(model,mac,'',{unsupportedActions:model==='TC1'?['info.get']:[]});fixtures.push(f);
   if(model==='TC1'){delete f.state.model;f.state.version='0.9.6-ed4474'}
   await page.getByRole('button',{name:'添加设备',exact:true}).click();
   const add=page.getByRole('dialog',{name:'添加设备',exact:true});
   if(model==='TC1') {
    assert.equal(await page.locator('select,datalist').count(),0,'all choice menus must use the custom component');
    const choice=add.getByRole('combobox',{name:'设备型号',exact:true});
    await choice.press('Enter');await choice.press('End');await choice.press('Escape');
    assert.equal(await choice.getAttribute('data-value'),'TC1','Escape must cancel a pending choice');
    assert.equal(await choice.getAttribute('aria-expanded'),'false');assert.equal(await add.isVisible(),true);
    await choice.press('a');await choice.press('Enter');assert.equal(await choice.getAttribute('data-value'),'A1','typeahead must find a matching model');
    await choice.press('Home');await choice.press('Enter');assert.equal(await choice.getAttribute('data-value'),'TC1');
    await choice.click();await add.getByRole('heading',{name:'添加设备',exact:true}).click();
    assert.equal(await choice.getAttribute('aria-expanded'),'false','an outside click must close only the menu');
   }
   await add.getByLabel('设备名称',{exact:true}).fill(name);await chooseOption(add.getByLabel('设备型号',{exact:true}),model);
   await add.getByLabel('设备内网地址',{exact:true}).fill(f.address);await add.getByLabel('所属空间',{exact:true}).fill('客厅');
   await page.locator('[form="add-form"]').click();await add.waitFor({state:'hidden'});
  }
  await page.locator('[data-device="tc1-aabbcc000201"]').click();
  await page.getByRole('switch',{name:'客厅排插插口 1',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-action="outlet.set"][data-index="0"]').getAttribute('aria-checked')==='true');
  assert.equal(fixtures[0].state.socket_states[0],1);
  await page.getByRole('button',{name:'修改插口 1 名称',exact:true}).click();
  await page.getByLabel('插口 01 名称',{exact:true}).fill('电视 & 音箱');
  await page.getByRole('button',{name:'保存名称',exact:true}).click();
  await page.getByRole('button',{name:'修改插口 1 名称',exact:true}).filter({hasText:'电视 & 音箱'}).waitFor();
  assert.equal(fixtures[0].state.socket_names[0],'电视 & 音箱');
  assert.equal(fixtures[0].calls.filter(c=>c.action==='config.set'&&c.params.section==='socket').length,1);
  await page.getByRole('button',{name:'电价设置',exact:true}).click();
  const settings=page.getByRole('dialog',{name:'设置',exact:true});
  await chooseOption(settings.getByLabel('计费方式',{exact:true}),'flat');await settings.getByLabel('当前电价（元/度）',{exact:true}).fill('0.6');
  let tariffAttempts=0;const sessionsBeforeRefresh=sessionRequests;
  const staleRequestToken=route=>++tariffAttempts===1 ? route.fulfill({status:403,json:{error:'请求来源不正确',code:'invalid_origin'}}) : route.continue();
  await context.route('**/api/settings/tariff',staleRequestToken);
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  assert.equal(tariffAttempts,2,'source validation failure must refresh the session and retry once');assert.equal(sessionRequests,sessionsBeforeRefresh+1);
  await context.unroute('**/api/settings/tariff',staleRequestToken);
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  assert.equal(await page.getByRole('button',{name:'刷新',exact:true}).count(),0);
  await page.getByText('预估消费 ¥0.00',{exact:false}).waitFor({timeout:25000});
  fixtures[0].power.energy_kwh=3.23;fixtures[0].power.power_watts=43.2;fixtures[0].state.socket_states[1]=1;fixtures[0].state.socket_names[0]='设备网页电视';
  // Device changes reach the page through background collection and synchronization alone.
  await page.getByText('预估消费 ¥1.20',{exact:false}).waitFor({timeout:25000});
  assert.equal((await page.locator('.pm-measurements .pm-metric-value').first().textContent()).replace(/（[^）]*）/g,''),'43.2W');
  assert.equal(await page.getByRole('switch',{name:'客厅排插插口 2',exact:true}).getAttribute('aria-checked'),'true');
  await page.getByRole('button',{name:'修改插口 1 名称',exact:true}).filter({hasText:'设备网页电视'}).waitFor();
  // Simulate a suspended mobile page while the real backend keeps collecting.
  await page.evaluate(()=>{Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event('visibilitychange'));});
  fixtures[0].power.power_watts=51.7;
  await until(async()=>{const data=await (await fetch(`${url}api/devices`)).json();return data.devices.find(d=>d.id==='tc1-aabbcc000201').state.power===51.7;},25000);
  assert.equal((await page.locator('.pm-measurements .pm-metric-value').first().textContent()).replace(/（[^）]*）/g,''),'43.2W');
  const resumed=page.waitForResponse(r=>r.url()===`${url}api/devices`&&r.request().method()==='GET',{timeout:2000});
  await page.evaluate(()=>{delete document.hidden;document.dispatchEvent(new Event('visibilitychange'));});await resumed;
  await page.waitForFunction(()=>document.querySelector('.pm-measurements .pm-metric-value').textContent.replace(/（[^）]*）/g,'')==='51.7W');
  const reconnected=page.waitForResponse(r=>r.url()===`${url}api/devices`&&r.request().method()==='GET',{timeout:2000});
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));await reconnected;
  await page.screenshot({path:path.join(output,'go-billing-desktop.png'),fullPage:true});
  await page.getByRole('button',{name:'设置',exact:true}).click();await settings.getByRole('button',{name:'电价设置',exact:true}).click();
  await chooseOption(settings.getByLabel('计费方式',{exact:true}),'tou');await settings.getByLabel('峰时电价（元/度）',{exact:true}).fill('1.2');await settings.getByLabel('谷时电价（元/度）',{exact:true}).fill('0.3');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();await settings.getByRole('button',{name:'电价设置',exact:true}).click();
  assert.equal(await settings.getByLabel('峰时电价（元/度）',{exact:true}).inputValue(),'1.2');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.locator('.pm-history-sparkline').first().click();const history=page.getByRole('dialog',{name:'历史记录',exact:true});await history.getByRole('tab',{name:'活动记录',exact:true}).click();await history.locator('.pm-event').first().waitFor();await history.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.getByRole('button',{name:'空间',exact:true}).click();
  const tile=page.locator('.pm-tile').filter({has:page.locator('[data-open-control="tc1-aabbcc000201"]')});
  await tile.locator('.pm-tile-energy-reading').waitFor();
  assert.equal(await tile.locator('.pm-tile-energy-reading').textContent(),'3.23度');
  assert.equal(await tile.locator('[data-energy-cost]').count(),0);
  assert.ok((await tile.locator('.pm-tile-value').textContent()).includes('51.7W'));
  assert.equal(await tile.getByRole('button',{name:'客厅排插电价设置',exact:true}).count(),0);
  for(const width of [320,390,768,1440]){await page.setViewportSize({width,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
  await page.setViewportSize({width:390,height:844});await page.locator('[data-open-control="tc1-aabbcc000201"]').click();
  const control=page.getByRole('dialog',{name:'设备控制',exact:true});await control.getByText('预估消费 ¥1.20',{exact:false}).waitFor();
  await control.getByRole('button',{name:'修改插口 1 名称',exact:true}).filter({hasText:'设备网页电视'}).waitFor();
  await control.getByRole('button',{name:'修改插口 2 名称',exact:true}).click();
  const outletName=control.getByLabel('插口 02 名称',{exact:true});
  await outletName.fill('电脑与显示器');
  // A background update must not replace an in-progress rename.
  fixtures[0].state.socket_names[1]='设备网页电脑';
  await until(async()=>{const data=await (await fetch(`${url}api/devices`)).json();return data.devices.find(d=>d.id==='tc1-aabbcc000201').state.outletNames[1]==='设备网页电脑';},25000);
  await page.evaluate(()=>window.dispatchEvent(new Event('online')));
  assert.equal(await outletName.inputValue(),'电脑与显示器');
  const failedRename=route=>route.fulfill({status:502,json:{error:'测试：设备写入失败'}});
  await context.route('**/api/devices/tc1-aabbcc000201/control',failedRename);
  await control.getByRole('button',{name:'保存名称',exact:true}).click();
  await control.getByText('测试：设备写入失败',{exact:true}).waitFor();
  assert.equal(await outletName.inputValue(),'电脑与显示器');
  assert.equal(fixtures[0].state.socket_names[1],'设备网页电脑');
  await context.unroute('**/api/devices/tc1-aabbcc000201/control',failedRename);
  await control.getByRole('button',{name:'保存名称',exact:true}).click();
  await control.getByRole('button',{name:'修改插口 2 名称',exact:true}).filter({hasText:'电脑与显示器'}).waitFor();
  assert.equal(fixtures[0].state.socket_names[1],'电脑与显示器');
  await control.getByRole('button',{name:'修改插口 2 名称',exact:true}).click();
  await outletName.fill('电'.repeat(22));await control.getByRole('button',{name:'保存名称',exact:true}).click();
  await control.getByRole('alert').filter({hasText:'名称过长'}).waitFor();
  await outletName.fill('   ');await control.getByRole('button',{name:'保存名称',exact:true}).click();
  await control.getByRole('alert').filter({hasText:'请输入插口名称'}).waitFor();
  await outletName.press('Escape');assert.equal(await control.isVisible(),true);
  assert.equal(await outletName.count(),0);assert.equal(fixtures[0].state.socket_names[1],'电脑与显示器');
  await control.getByRole('button',{name:'修改插口 2 名称',exact:true}).click();
  await outletName.fill('电视与音响、电脑显示器及充电器');
  for(const width of [320,390,768,1440]) {await page.setViewportSize({width,height:900});assert.equal(await control.evaluate(el=>el.scrollWidth>el.clientWidth),false);}
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:path.join(output,'room-outlet-rename-mobile.png'),fullPage:true});
  await control.getByRole('button',{name:'取消',exact:true}).click();
  await page.screenshot({path:path.join(output,'go-billing-mobile.png'),fullPage:true});
  await control.getByRole('button',{name:'电价设置',exact:true}).click();await settings.getByLabel('计费方式',{exact:true}).waitFor();
  for(const viewport of [{width:320,height:568},{width:390,height:844},{width:740,height:360}]){await page.setViewportSize(viewport);const box=await settings.evaluate(el=>({top:el.getBoundingClientRect().top,bottom:el.getBoundingClientRect().bottom,footer:el.querySelector('.pm-dialog-footer').getBoundingClientRect().bottom,height:innerHeight,overflow:el.scrollWidth>el.clientWidth}));assert.ok(box.top>=0&&box.bottom<=box.height+1&&box.footer<=box.height,JSON.stringify(box));assert.equal(box.overflow,false);}
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'go-tariff-mobile.png'),fullPage:true});
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.setViewportSize({width:1440,height:1000});
  discovery={interfaces:[{id:'physical',label:'enp4s0 · 10.0.0.9',range:'10.0.0.0/24'},{id:'second',label:'eth1 · 192.168.2.9',range:'192.168.2.0/24'}],interface:'physical',range:'10.0.0.0/24',state:'complete',phase:'auto',mdns:'complete',mode:'auto',devices:[{id:'tc1-aabbcc009999',model:'TC1',mac:'aabbcc009999',address:'http://10.0.0.223',added:false}],protectedAddresses:['http://10.0.0.224'],checked:12,total:253};
  rejectDiscoverySource=true;const scansBeforeRejection=scans.length;
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  const add=page.getByRole('dialog',{name:'添加设备',exact:true});
  await add.getByText('请求来源不正确',{exact:true}).waitFor();
  assert.equal(scans.length,scansBeforeRejection+2,'source error must stop after one session refresh');
  await add.getByText('手动指定网段',{exact:true}).click();
  assert.equal(await optionCount(add.getByLabel('网络接口',{exact:true})),3,'a failed scan must retain the NAS interface list');
  await add.getByText('手动指定网段',{exact:true}).click();
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).isEnabled(),true);
  rejectDiscoverySource=false;discovery={...discovery,state:'scanning'};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await add.getByRole('button',{name:'停止',exact:true}).waitFor();
  assert.equal(scans.at(-1).mode,'auto');assert.equal(scans.at(-1).interface,undefined,'default scan must choose its own interface');
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).count(),1);
  assert.equal(await add.getByRole('button',{name:'重新发现',exact:true}).count(),0);
  assert.equal(await add.getByRole('button',{name:'局域网扫描',exact:true}).count(),0);
  assert.equal(await add.getByLabel('网络接口',{exact:true}).isVisible(),false);
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).isVisible(),false);
  await page.screenshot({path:path.join(output,'react-scan-desktop.png'),fullPage:true});
  await add.getByText('手动指定网段',{exact:true}).click();
  await add.getByLabel('扫描网段',{exact:true}).fill('172.20.3.0/24');
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).isEnabled(),true,'manual range must be available during automatic scanning');
  discovery={...discovery,state:'complete',range:'172.20.3.0/24'};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await add.getByText('发现 1 台设备。',{exact:true}).waitFor();
  assert.deepEqual(scans.at(-1),{mode:'lan',cidr:'172.20.3.0/24'});
  await chooseOption(add.getByLabel('网络接口',{exact:true}),'second');
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).inputValue(),'192.168.2.0/24');
  await add.getByLabel('扫描网段',{exact:true}).fill('192.168.2.128/25');
  discovery={...discovery,interface:'second',range:'192.168.2.128/25',state:'complete'};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('[data-scan-status]').textContent==='发现 1 台设备。');
  assert.deepEqual(scans.at(-1),{mode:'lan',cidr:'192.168.2.128/25'});
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
  // Missing NAS interface information must not disable manual subnet discovery.
  discovery={interfaces:[],state:'idle',phase:'idle',mdns:'idle',mode:'auto',range:'',devices:[],protectedAddresses:[],checked:0,total:0};
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  await add.getByText('未读取到局域网接口，可手动指定网段扫描，或在下方手动添加设备。',{exact:true}).waitFor();
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).inputValue(),'','closing Add must clear the temporary range');
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).isDisabled(),true);
  await add.getByText('手动指定网段',{exact:true}).click();
  assert.equal(await add.getByLabel('网络接口',{exact:true}).getAttribute('data-value'),'');
  await add.getByLabel('扫描网段',{exact:true}).fill('10.0.0.223');
  assert.equal(await add.getByRole('button',{name:'扫描设备',exact:true}).isEnabled(),true);
  discovery={...discovery,state:'scanning',phase:'auto',range:'10.0.0.223/32',mdns:'unavailable',total:1};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();await add.getByRole('button',{name:'停止',exact:true}).waitFor();
  assert.deepEqual(scans.at(-1),{mode:'lan',cidr:'10.0.0.223'});
  discovery={...discovery,state:'complete',checked:1,devices:[{id:'tc1-aabbcc009999',model:'TC1',mac:'aabbcc009999',address:'http://10.0.0.223',added:false}]};
  await add.getByText('发现 1 台设备。',{exact:true}).waitFor();
  assert.equal(await add.getByLabel('扫描网段',{exact:true}).inputValue(),'10.0.0.223','polling must preserve manual subnet input');
  await add.locator('[data-discovered-index="0"]').click();assert.equal(await add.getByLabel('设备内网地址',{exact:true}).inputValue(),'http://10.0.0.223');
  await page.setViewportSize({width:320,height:700});assert.equal(await add.evaluate(el=>el.scrollWidth>el.clientWidth),false);
  await page.screenshot({path:path.join(output,'discovery-manual-no-interface.png'),fullPage:true});
  // A recovered identity stays registered; a pending address update remains selectable.
  discovery={...discovery,devices:[{id:'tc1-aabbcc000201',model:'TC1',mac:'aabbcc000201',address:'http://10.0.0.225',added:true,recovered:true,canUpdate:false}]};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await add.getByText('已恢复连接',{exact:true}).waitFor();
  assert.equal(await add.locator('[data-discovered-index="0"]').isDisabled(),true);
  discovery={...discovery,devices:discovery.devices.map(d=>({...d,recovered:false,canUpdate:true}))};
  await add.getByRole('button',{name:'扫描设备',exact:true}).click();
  await add.getByText('更新地址',{exact:true}).click();
  assert.equal(await add.getByLabel('设备名称',{exact:true}).inputValue(),'客厅排插');
  assert.equal(await add.getByLabel('所属空间',{exact:true}).inputValue(),'客厅');
  assert.equal(await add.getByLabel('设备内网地址',{exact:true}).inputValue(),'http://10.0.0.225');
  await page.keyboard.press('Escape');await add.waitFor({state:'hidden'});
  await page.setViewportSize({width:1440,height:1000});
  await page.getByRole('button',{name:'列表',exact:true}).click();await page.locator('[data-device="m1-aabbcc000202"]').click();
  await page.getByRole('slider',{name:'书房检测仪屏幕亮度',exact:true}).focus();await page.keyboard.press('End');await page.keyboard.press('Tab');
  await page.waitForFunction(()=>document.querySelector('[aria-label="书房检测仪屏幕亮度"]').value==='100');
  await until(()=>fixtures[1].state.brightness_requested===4);
  await page.locator('[data-device="a1-aabbcc000203"]').click();await page.getByRole('button',{name:'自动净化',exact:true}).click();
  await page.waitForFunction(()=>document.querySelector('.pm-mode[aria-pressed="true"]').textContent==='自动净化');assert.equal(fixtures[2].state.fan_mode,'auto');
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await chooseOption(settings.getByLabel('设备',{exact:true}),'tc1-aabbcc000201');
  await settings.getByLabel('设备名称',{exact:true}).fill('客厅主排插');await settings.getByLabel('所属空间',{exact:true}).fill('书房');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).fill('10.0.0.9');
  await settings.getByRole('button',{name:'设备连接',exact:true}).click();
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'客厅主排插');
  await chooseOption(settings.getByLabel('设备',{exact:true}),'m1-aabbcc000202');await settings.getByLabel('设备名称',{exact:true}).fill('检测仪草稿');
  await chooseOption(settings.getByLabel('设备',{exact:true}),'tc1-aabbcc000201');assert.equal(await settings.getByLabel('所属空间',{exact:true}).inputValue(),'书房');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  assert.equal(await settings.getByLabel('设备名称',{exact:true}).inputValue(),'客厅主排插');
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();assert.equal(await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).inputValue(),'10.0.0.9');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByRole('button',{name:'服务设置',exact:true}).click();assert.equal(await settings.getByLabel('Broker 地址 / NAS 内网地址',{exact:true}).inputValue(),'');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Saved refresh times must change the active page timer and survive a reload.
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByRole('button',{name:'刷新设置',exact:true}).click();
  assert.equal(await settings.getByLabel('页面刷新间隔（秒）',{exact:true}).inputValue(),'5');
  assert.equal(await settings.getByLabel('局域网采集间隔（秒）',{exact:true}).inputValue(),'15');
  await settings.getByLabel('页面刷新间隔（秒）',{exact:true}).fill('2');
  await settings.getByLabel('局域网采集间隔（秒）',{exact:true}).fill('10');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();
  await settings.getByText('配置已保存',{exact:true}).waitFor();
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.waitForRequest(r=>r.url()===`${url}api/devices`&&r.method()==='GET',{timeout:3500});
  await page.reload();await page.getByRole('button',{name:'设置',exact:true}).click();
  await settings.getByRole('button',{name:'刷新设置',exact:true}).click();
  assert.equal(await settings.getByLabel('页面刷新间隔（秒）',{exact:true}).inputValue(),'2');
  assert.equal(await settings.getByLabel('局域网采集间隔（秒）',{exact:true}).inputValue(),'10');
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   assert.equal(await settings.evaluate(el=>el.scrollWidth>el.clientWidth),false);
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'refresh-settings-mobile.png'),fullPage:true});
  await settings.getByLabel('页面刷新间隔（秒）',{exact:true}).fill('5');
  await settings.getByLabel('局域网采集间隔（秒）',{exact:true}).fill('15');
  await settings.getByRole('button',{name:'保存配置',exact:true}).click();await settings.getByText('配置已保存',{exact:true}).waitFor();
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.setViewportSize({width:1440,height:1000});
  // Theme changes must keep configuration drafts and cover native inputs and modal surfaces.
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await chooseOption(settings.getByLabel('设备',{exact:true}),'tc1-aabbcc000201');
  await settings.getByLabel('设备名称',{exact:true}).fill('外观切换保留草稿');
  await settings.getByRole('button',{name:'外观',exact:true}).click();
  assert.equal(await settings.getByLabel('主题',{exact:true}).getAttribute('data-value'),'system');
  await chooseOption(settings.getByLabel('主题',{exact:true}),'dark');
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
  await page.getByRole('button',{name:'修改插口 2 名称',exact:true}).filter({hasText:'电脑与显示器'}).waitFor();
  await page.screenshot({path:path.join(output,'theme-dark-desktop.png'),fullPage:true});
  for(const width of [320,390,700,768,900,1440]){await page.setViewportSize({width,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);}
  await page.setViewportSize({width:390,height:844});
  await page.screenshot({path:path.join(output,'theme-dark-mobile.png'),fullPage:true});
  await page.getByRole('button',{name:'空间',exact:true}).click();await page.locator('[data-open-control="tc1-aabbcc000201"]').click();
  assert.equal(await control.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await page.screenshot({path:path.join(output,'theme-dark-control.png'),fullPage:true});
  await control.getByRole('button',{name:'关闭设备控制',exact:true}).click();
  await page.locator('.pm-history-sparkline').first().click();
  assert.equal(await history.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await history.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  assert.equal(await add.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await add.getByRole('button',{name:'关闭添加设备',exact:true}).click();
  // Explicit choices survive reloads and override the system; automatic mode reacts live.
  await page.reload();await page.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await page.evaluate(()=>getComputedStyle(document.documentElement).colorScheme),'dark');
  await page.getByRole('button',{name:'设置',exact:true}).click();await settings.getByRole('button',{name:'外观',exact:true}).click();
  assert.equal(await settings.getByLabel('主题',{exact:true}).getAttribute('data-value'),'dark');
  await page.emulateMedia({colorScheme:'dark'});
  await chooseOption(settings.getByLabel('主题',{exact:true}),'light');
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='light');
  await chooseOption(settings.getByLabel('主题',{exact:true}),'system');
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='dark');
  await page.emulateMedia({colorScheme:'light'});
  await page.waitForFunction(()=>getComputedStyle(document.documentElement).colorScheme==='light');
  await settings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // A fresh browser with no stored preference must start in its system's theme.
  const fresh=await browser.newContext({viewport:{width:390,height:844},deviceScaleFactor:3,colorScheme:'dark',hasTouch:true,isMobile:true});
  await fresh.route('**/api/discovery/automatic?*',route=>route.fulfill({json:{state:'complete',devices:[]}}));
  const first=await fresh.newPage();await first.goto(url);await first.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await first.evaluate(()=>getComputedStyle(document.documentElement).colorScheme),'dark');
  for(const width of [320,360,390,700]) {
   await first.setViewportSize({width,height:844});
   const boxes=await first.locator('.pm-device-toolbar').evaluate(el=>{const a=el.querySelector('.pm-filterbar').getBoundingClientRect(),b=el.querySelector('.pm-view-switch').getBoundingClientRect();return {sameRow:Math.abs((a.top+a.bottom)/2-(b.top+b.bottom)/2)<1,overlap:a.right>b.left,overflow:document.documentElement.scrollWidth>innerWidth};});
   assert.deepEqual(boxes,{sameRow:true,overlap:false,overflow:false});
  }
  await first.setViewportSize({width:390,height:844});await first.screenshot({path:path.join(output,'view-icons-mobile.png'),fullPage:true});
  await first.getByRole('button',{name:'列表',exact:true}).click();
  const picker=first.getByRole('combobox',{name:'选择设备',exact:true});
  for(const width of [320,360,390,430]) {
   await first.setViewportSize({width,height:844});await picker.tap();
   const menu=first.getByRole('listbox'), box=await menu.boundingBox();
   assert.ok(box.x>=7&&box.x+box.width<=width-7&&box.y>=7&&box.y+box.height<=837,'touch menu must stay inside the viewport');
   assert.equal(await picker.evaluate(el=>getComputedStyle(el).fontSize),'13px');
   const targetHeight=(await picker.boundingBox()).height,optionHeight=(await menu.getByRole('option').first().boundingBox()).height;
   assert.ok(targetHeight>=43.9&&optionHeight>=43.9,JSON.stringify({width,targetHeight,optionHeight}));
   await picker.press('Escape');
  }
  await chooseOption(picker,'m1-aabbcc000202');
  assert.equal(await picker.getAttribute('data-value'),'m1-aabbcc000202');
  assert.equal(await picker.getAttribute('aria-expanded'),'false','choosing inside a wrapping label must not reopen the menu');
  await first.setViewportSize({width:390,height:844});await picker.tap();
  await first.screenshot({path:path.join(output,'custom-select-mobile.png'),fullPage:true});await picker.press('Escape');
  await first.evaluate(()=>localStorage.setItem('pm-view','list'));await first.reload();await first.getByRole('button',{name:'设置',exact:true}).waitFor();
  assert.equal(await first.locator('.pm-app').getAttribute('data-layout'),'rooms','opening must default to rooms even with an old saved list view');
  await first.getByRole('button',{name:'设置',exact:true}).click();
  const appearance=first.getByRole('dialog',{name:'设置',exact:true});
  await appearance.getByRole('button',{name:'外观',exact:true}).click();
  assert.equal(await appearance.getByLabel('默认视图',{exact:true}).getAttribute('data-value'),'rooms');
  await chooseOption(appearance.getByLabel('默认视图',{exact:true}),'list');
  assert.equal(await first.locator('.pm-app').getAttribute('data-layout'),'list');
  assert.equal(await first.evaluate(()=>localStorage.getItem('pm-default-layout')),'list');
  for(const width of [320,390,768,1440]) {
   await first.setViewportSize({width,height:900});
   assert.equal(await appearance.evaluate(el=>el.scrollWidth>el.clientWidth),false);
  }
  await first.setViewportSize({width:390,height:844});await first.screenshot({path:path.join(output,'appearance-default-layout-mobile.png'),fullPage:true});
  await appearance.getByRole('button',{name:'关闭设置',exact:true}).click();
  await first.reload();await first.locator('.pm-app[data-layout="list"]').waitFor();
  await first.getByRole('button',{name:'空间',exact:true}).click();
  assert.equal(await first.evaluate(()=>localStorage.getItem('pm-default-layout')),'list','temporary toolbar switch must preserve the chosen default');
  await first.reload();await first.locator('.pm-app[data-layout="list"]').waitFor();
  const other=await fresh.newPage();await other.goto(url);await other.locator('.pm-app[data-layout="list"]').waitFor();
  await first.getByRole('button',{name:'设置',exact:true}).click();
  await appearance.getByRole('button',{name:'外观',exact:true}).click();
  await chooseOption(appearance.getByLabel('默认视图',{exact:true}),'rooms');
  await other.locator('.pm-app[data-layout="rooms"]').waitFor();
  await appearance.getByRole('button',{name:'关闭设置',exact:true}).click();
  await first.reload();await first.locator('.pm-app[data-layout="rooms"]').waitFor();
  await first.evaluate(()=>localStorage.setItem('pm-default-layout','invalid'));await first.reload();
  await first.locator('.pm-app[data-layout="rooms"]').waitFor();
  await fresh.close();
  await require('./s30-browser.cjs')(page,fixtures,output);
  assert.ok(writeTokens.length>10&&writeTokens.every(token=>/^[a-f0-9]{64}$/.test(token)),'all API writes must carry the authenticated page request token');
  assert.deepEqual(errors,[]);
  console.log('Go + React browser passed: compact room power/energy, control-dialog cost and tariff entry, bidirectional outlet names, failed writes and draft preservation, light/dark/system themes, automatic updates, three models, discovery and 320–1440px.');
 } finally {
  await browser.close();for(const f of fixtures)await f.close();
  if(child.exitCode===null){child.kill();await new Promise(resolve=>child.once('exit',resolve));}
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1});
