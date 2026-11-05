/* Real Go + SQLite history; synthetic past readings live only in a temp database. */
const {chromium}=require('playwright');
const {spawn,spawnSync}=require('node:child_process');
const {mkdtempSync,mkdirSync,rmSync,readFileSync}=require('node:fs');
const {tmpdir}=require('node:os');
const path=require('node:path');
const assert=require('node:assert/strict');
const {chooseOption,optionCount}=require('./select-helpers.cjs');
(async()=>{
 const {httpDevice,freePort,until}=await import('./fixtures.js');
 const root=path.resolve(__dirname,'..'), directory=mkdtempSync(path.join(tmpdir(),'pm-history-browser-'));
 const executable=path.join(directory,'manager'+(process.platform==='win32'?'.exe':''));
 const go=process.env.GO_BIN || 'go';
 const build=spawnSync(go,['build','-mod=readonly','-trimpath','-o',executable,'.'],{cwd:root,env:{...process.env,CGO_ENABLED:'0'},stdio:'inherit',windowsHide:true});
 assert.equal(build.status,0,'History test executable failed to build');
 const port=await freePort(), url=`http://127.0.0.1:${port}/app/phicomm-manager/`;
 const child=spawn(executable,['--dev','--listen',`127.0.0.1:${port}`,'--data',directory],{windowsHide:true,stdio:['ignore','pipe','pipe']});
 let logs='';child.stdout.on('data',c=>logs+=c);child.stderr.on('data',c=>logs+=c);
 const browser=await chromium.launch({headless:true,...(process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE?{executablePath:process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE}:{})});
 const fixtures=[],output=path.join(root,'test-results');mkdirSync(output,{recursive:true});
 try {
  let ready=false;
  for(let i=0;i<80;i++){try {if((await fetch(url)).ok) {ready=true;break;}}catch {} await new Promise(r=>setTimeout(r,100));}
  assert.ok(ready,logs);
  for(const [model,mac,name] of [['TC1','aabbcc000301','客厅排插'],['M1','aabbcc000302','书房空气检测仪'],['A1','aabbcc000303','卧室净化器']]) {
   const fixture=await httpDevice(model,mac);fixtures.push(fixture);
   const added=await fetch(url+'api/devices',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model,method:'lan',address:fixture.address,name,room:'客厅'})});
   assert.equal(added.status,201);
   const data=await (await fetch(`${url}api/devices/${model.toLowerCase()}-${mac}/data-history`)).json();assert.equal(data.samples,1);
  }
  const seeded=spawnSync(process.env.PYTHON_BIN || (process.platform==='win32'?'py':'python3'),[...(process.platform==='win32'&&!process.env.PYTHON_BIN?['-3']:[]),path.join(__dirname,'seed-telemetry.py'),directory],{stdio:'inherit',windowsHide:true});
  assert.equal(seeded.status,0);
  await fetch(url+'api/devices',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({model:'TC1',method:'mqtt',mac:'aabbcc000304',name:'待接入排插'})});
  const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light',acceptDownloads:true}),page=await context.newPage(),errors=[];
  await context.route('**/api/discovery/automatic?*',route=>route.fulfill({json:{state:'complete',devices:[]}}));
  page.on('pageerror',e=>errors.push(e.message));await page.goto(url);
  const tile=page.locator('.pm-tile').filter({has:page.locator('[data-open-control="tc1-aabbcc000301"]')});
  await tile.locator('[data-power-sparkline]').waitFor();
  for(const id of ['m1-aabbcc000302','a1-aabbcc000303']) await page.locator('.pm-tile').filter({has:page.locator(`[data-open-control="${id}"]`)}).locator('[data-history-sparkline]').waitFor();
  assert.ok(await tile.locator('.pm-trend-line').count()>=2,'card curve must preserve offline gaps');
  assert.ok((await tile.locator('.pm-tile-power-reading').textContent()).includes('23.4W'));
  assert.equal(await tile.locator('.pm-tile-count').count(),0);
  assert.equal(await tile.locator('.pm-tile-energy-reading').textContent(),'1.23度');
  await page.locator('.pm-tile').filter({has:page.locator('[data-open-control="tc1-aabbcc000304"]')}).getByText('等待功率记录',{exact:true}).waitFor();
  for(const width of [320,390,700,768,900,1440]) {
   await page.setViewportSize({width,height:900});
   const row=await tile.locator('.pm-tile-primary').evaluate(el=>{const name=el.querySelector('.pm-tile-name').getBoundingClientRect(),readingEl=el.querySelector('.pm-tile-power-reading'),reading=readingEl.getBoundingClientRect(),watts=el.querySelector('.pm-tile-watts').getBoundingClientRect(),energy=el.querySelector('.pm-tile-energy-reading').getBoundingClientRect();return {belowName:reading.top>=name.bottom+8,leftAligned:Math.abs(name.left-reading.left)<1,rightAligned:Math.abs(reading.right-el.getBoundingClientRect().right)<1,sameRow:Math.abs((name.top+name.bottom)/2-(reading.top+reading.bottom)/2)<1,fontMatches:getComputedStyle(readingEl).fontSize===getComputedStyle(document.querySelector('.pm-tile:not(.pm-tile-tc1) .pm-tile-value')).fontSize,powerWithEnergy:Math.abs(watts.bottom-energy.bottom)<8,overflow:document.documentElement.scrollWidth>innerWidth};});
   const mobile=width<=700;
   assert.deepEqual(row,{belowName:!mobile,leftAligned:!mobile,rightAligned:mobile,sameRow:mobile,fontMatches:true,powerWithEnergy:true,overflow:false});
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'card-power-24h-mobile.png'),fullPage:true});
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'card-power-24h-desktop.png'),fullPage:true});
  await tile.getByRole('button',{name:'客厅排插最近24小时功率历史',exact:true}).click();
  const dialog=page.getByRole('dialog',{name:'历史记录',exact:true});
  await dialog.locator('[data-telemetry-chart]').waitFor();
  assert.equal(await dialog.getByRole('tab',{name:'历史数据',exact:true}).getAttribute('aria-selected'),'true');
  assert.equal(await dialog.getByLabel('历史设备',{exact:true}).getAttribute('data-value'),'tc1-aabbcc000301');
  assert.equal(await dialog.getByLabel('数据指标',{exact:true}).getAttribute('data-value'),'power');
  assert.ok((await dialog.locator('.pm-history-summary').textContent()).includes('区间用电'));
  const chart=dialog.getByRole('group',{name:'功率趋势图',exact:true});
  await chart.focus();await chart.press('End');await chart.press('ArrowLeft');
  await dialog.locator('.pm-trend-info strong').waitFor();
  assert.ok(await dialog.locator('.pm-trend-line').count()>=2,'offline gap must split the curve');
  await chooseOption(dialog.getByLabel('数据指标',{exact:true}),'energy');
  await dialog.getByRole('group',{name:'用电量趋势图',exact:true}).waitFor();assert.ok(await dialog.locator('.pm-trend-bar').count()>1);
  const downloadEvent=page.waitForEvent('download');await dialog.getByRole('button',{name:'导出 CSV',exact:true}).click();const download=await downloadEvent;
  assert.equal(download.suggestedFilename(),'TC1-aabbcc000301-24h.csv');
  const csv=readFileSync(await download.path(),'utf8');assert.ok(csv.includes('功率（W）')&&csv.includes('用电量（度）')&&csv.includes('预估花费（元）'));assert.ok(csv.split('\r\n').length>100);
  for(const [label,range] of [['最近 7 天','7d'],['最近 30 天','30d']]) {
   const requested=page.waitForResponse(r=>r.url()===`${url}api/devices/tc1-aabbcc000301/data-history?range=${range}`);
   await dialog.getByRole('button',{name:label,exact:true}).click();
   assert.equal((await (await requested).json()).range,range);await dialog.locator('[data-telemetry-chart]').waitFor();
  }
  await dialog.getByRole('button',{name:'最近 24 小时',exact:true}).click();await dialog.locator('[data-telemetry-chart]').waitFor();
  await chooseOption(dialog.getByLabel('历史设备',{exact:true}),'m1-aabbcc000302');
  await dialog.getByRole('group',{name:'PM2.5趋势图',exact:true}).waitFor();
  await chooseOption(dialog.getByLabel('数据指标',{exact:true}),'temp');await dialog.getByRole('group',{name:'温度趋势图',exact:true}).waitFor();
  await page.screenshot({path:path.join(output,'history-air-desktop.png'),fullPage:true});
  await chooseOption(dialog.getByLabel('历史设备',{exact:true}),'a1-aabbcc000303');
  await dialog.getByRole('group',{name:'风机输出趋势图',exact:true}).waitFor();
  await chooseOption(dialog.getByLabel('数据指标',{exact:true}),'filter');await dialog.getByRole('group',{name:'滤芯剩余趋势图',exact:true}).waitFor();
  await chooseOption(dialog.getByLabel('历史设备',{exact:true}),'tc1-aabbcc000304');
  await dialog.getByText('所选时段暂无历史数据，设备上线后会自动记录。',{exact:true}).waitFor();
  assert.equal(await dialog.getByRole('button',{name:'导出 CSV',exact:true}).isDisabled(),true);
  await dialog.getByRole('tab',{name:'历史数据',exact:true}).focus();await page.keyboard.press('ArrowRight');await dialog.locator('.pm-event').first().waitFor();
  await dialog.getByRole('tab',{name:'历史数据',exact:true}).click();await chooseOption(dialog.getByLabel('历史设备',{exact:true}),'tc1-aabbcc000301');await dialog.locator('[data-telemetry-chart]').waitFor();
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:width===320?568:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   const box=await dialog.evaluate(el=>({overflow:el.scrollWidth>el.clientWidth,top:el.getBoundingClientRect().top,bottom:el.getBoundingClientRect().bottom,height:innerHeight,close:el.querySelector('[data-close]').getBoundingClientRect().bottom}));
   assert.equal(box.overflow,false);assert.ok(box.top>=0&&box.bottom<=box.height+1&&box.close<=box.height+1,JSON.stringify(box));
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'history-power-mobile.png'),fullPage:true});
  await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});
  await page.getByRole('button',{name:'列表',exact:true}).click();await page.locator('.pm-detail').getByRole('button',{name:'客厅排插最近24小时功率历史',exact:true}).click();await dialog.getByLabel('历史设备',{exact:true}).waitFor();assert.equal(await dialog.getByLabel('历史设备',{exact:true}).getAttribute('data-value'),'tc1-aabbcc000301');
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();await page.getByRole('button',{name:'空间',exact:true}).click();
  const controlDialog=page.getByRole('dialog',{name:'设备控制',exact:true});
  for(const [id,name,label] of [['tc1-aabbcc000301','客厅排插','功率'],['m1-aabbcc000302','书房空气检测仪','PM2.5'],['a1-aabbcc000303','卧室净化器','风机输出']]) {
   await page.locator('.pm-tile').filter({has:page.locator(`[data-open-control="${id}"]`)}).getByRole('button',{name:`${name}最近24小时${label}历史`,exact:true}).click();
   await dialog.getByRole('group',{name:`${label}趋势图`,exact:true}).waitFor();assert.equal(await dialog.getByLabel('历史设备',{exact:true}).getAttribute('data-value'),id);
   await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
   await page.locator(`[data-open-control="${id}"]`).click();await controlDialog.locator('[data-history-sparkline]').waitFor();
   assert.equal(await controlDialog.getByText(/^(最后更新|上次在线|历史数据|连接设置)$/).count(),0);
   assert.equal(await controlDialog.locator('.pm-detail-connection,.pm-detail-links').count(),0);
   assert.ok(!(await controlDialog.textContent()).includes(fixtures[['TC1','M1','A1'].indexOf(id.split('-')[0].toUpperCase())].address));
   if(id.startsWith('tc1')) {
    for(const width of [320,390,768,1440]) {
     await page.setViewportSize({width,height:width===320?568:900});
     const box=await controlDialog.evaluate(el=>{const d=el.getBoundingClientRect(),chart=el.querySelector('.pm-detail-sparkline').getBoundingClientRect();return {overflow:el.scrollWidth>el.clientWidth,pageOverflow:document.documentElement.scrollWidth>innerWidth,inside:chart.left>=d.left&&chart.right<=d.right,visible:d.top>=0&&d.bottom<=innerHeight+1};});
     assert.deepEqual(box,{overflow:false,pageOverflow:false,inside:true,visible:true});
     if(width===390||width===1440) {await controlDialog.locator('.pm-detail-sparkline').scrollIntoViewIfNeeded();await page.screenshot({path:path.join(output,`control-history-${width===390?'mobile':'desktop'}.png`),fullPage:true});}
    }
   }
   const sparkline=controlDialog.getByRole('button',{name:`${name}最近24小时${label}历史`,exact:true});
   await sparkline.focus();await sparkline.press('Enter');
   await dialog.getByRole('group',{name:`${label}趋势图`,exact:true}).waitFor();assert.equal(await dialog.getByLabel('历史设备',{exact:true}).getAttribute('data-value'),id);
   assert.equal(await controlDialog.count(),0,'history must replace the control dialog');
   await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  }
  // History still works after reload, including dark colors and transient failures.
  await page.evaluate(()=>localStorage.setItem('pm-theme','dark'));await page.reload();await tile.getByRole('button',{name:'客厅排插最近24小时功率历史',exact:true}).click();await dialog.locator('[data-telemetry-chart]').waitFor();
  assert.equal(await dialog.evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(36, 38, 41)');
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'history-dark-desktop.png'),fullPage:true});
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();await tile.locator('[data-power-sparkline]').waitFor();
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'card-power-24h-dark.png'),fullPage:true});
  await tile.getByRole('button',{name:'客厅排插最近24小时功率历史',exact:true}).click();await dialog.locator('[data-telemetry-chart]').waitFor();
  const unavailable=route=>route.fulfill({status:503,json:{error:'测试：暂时无法读取历史'}});
  await context.route('**/api/devices/*/data-history?range=*',unavailable);
  await dialog.getByRole('button',{name:'最近 7 天',exact:true}).click();await dialog.getByRole('alert').filter({hasText:'测试：暂时无法读取历史'}).waitFor();
  await context.unroute('**/api/devices/*/data-history?range=*',unavailable);await dialog.getByRole('button',{name:'重试',exact:true}).click();await dialog.locator('[data-telemetry-chart]').waitFor();
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.getByRole('button',{name:'设置',exact:true}).click();
  const exportSettings=page.getByRole('dialog',{name:'设置',exact:true});
  await exportSettings.getByRole('button',{name:'数据导出',exact:true}).click();
  assert.equal(await exportSettings.getByRole('button',{name:'保存配置',exact:true}).count(),0);
  const allIds=['tc1-aabbcc000301','m1-aabbcc000302','a1-aabbcc000303'];
  const expectedRawCount=(await Promise.all(allIds.map(async id=>(await (await fetch(`${url}api/devices/${id}/data-history?range=30d`)).json()).samples))).reduce((a,b)=>a+b,0);
  const allDownloadEvent=page.waitForEvent('download');await exportSettings.getByRole('link',{name:'导出全部 CSV',exact:true}).click();
  const allDownload=await allDownloadEvent;assert.match(allDownload.suggestedFilename(),/^phicomm-history-\d{8}-\d{6}\.csv$/);
  const allCSV=readFileSync(await allDownload.path(),'utf8');assert.ok(allCSV.startsWith('\ufeff设备ID,'));
  assert.ok(allCSV.includes('累计用电（度）')&&allCSV.includes('累计预估花费（元）')&&allCSV.includes('滤芯剩余（%）'));
  const rawCount=allCSV.trimEnd().split('\r\n').length-1;
  assert.ok(rawCount>=expectedRawCount&&rawCount<=expectedRawCount+3,`all raw minute readings must be exported: expected ${expectedRawCount}, got ${rawCount}`);
  for(const id of allIds) assert.ok(allCSV.includes(id));
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   assert.equal(await exportSettings.evaluate(el=>el.scrollWidth>el.clientWidth),false);
   assert.equal(await exportSettings.locator('.pm-settings-tabs').evaluate(el=>el.scrollWidth>el.clientWidth),false);
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'settings-data-export-mobile.png'),fullPage:true});
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'settings-data-export-desktop.png'),fullPage:true});
  await exportSettings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Match the upstream DAIKIN command error and individually valid sensors.
  const daikin=await httpDevice('DAIKIN','aabbcc000305');fixtures.push(daikin);
  await page.getByRole('button',{name:'添加设备',exact:true}).click();
  const addDialog=page.getByRole('dialog',{name:'添加设备',exact:true});
  await chooseOption(addDialog.getByLabel('设备型号',{exact:true}),'DAIKIN');
  await addDialog.getByLabel('设备名称',{exact:true}).fill('大金检测仪');
  await addDialog.getByLabel('设备内网地址',{exact:true}).fill(daikin.address);
  await addDialog.getByRole('button',{name:'添加设备',exact:true}).click();await addDialog.waitFor({state:'hidden'});
  const daikinCard=page.locator('.pm-tile').filter({has:page.locator('[data-open-control="daikin-aabbcc000305"]')});
  await daikinCard.locator('[data-history-sparkline]').waitFor();
  assert.equal(await daikinCard.getByRole('switch').count(),0,'a monitor has no master power switch');
  await daikinCard.getByRole('button',{name:'大金检测仪设备控制',exact:true}).click();
  for(const label of ['PM2.5','PM10','CO₂','温度','湿度','TVOC']) await controlDialog.getByText(label,{exact:true}).waitFor();
  assert.equal(await controlDialog.getByText('甲醛',{exact:true}).count(),0);
  assert.equal(await controlDialog.getByText('运行模式',{exact:true}).count(),0);
  assert.equal(await controlDialog.getByRole('slider').count(),0);
  assert.ok((await controlDialog.getByText('TVOC',{exact:true}).locator('..').textContent()).includes('0ppb'),'valid zero TVOC must be displayed');
  const indicator=controlDialog.getByRole('switch',{name:'大金检测仪指示灯',exact:true});
  const indicatorResponse=page.waitForResponse(r=>r.url().endsWith('/devices/daikin-aabbcc000305/control')&&r.request().method()==='POST');
  await indicator.click();assert.equal((await (await indicatorResponse).json()).confirmed,true);
  assert.equal(daikin.state.indicator_enabled,false);
  for(const theme of ['light','dark']) {
   await page.evaluate(value=>{localStorage.setItem('pm-theme',value);document.documentElement.dataset.theme=value;},theme);
   for(const width of [320,390,768,1440]) {
    await page.setViewportSize({width,height:width===320?568:900});
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.equal(await controlDialog.evaluate(el=>el.scrollWidth>el.clientWidth),false);
   }
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'daikin-control-mobile.png'),fullPage:true});
  await controlDialog.getByRole('button',{name:'关闭设备控制',exact:true}).click();
  await daikinCard.getByRole('button',{name:'大金检测仪最近24小时PM2.5历史',exact:true}).click();
  assert.equal(await dialog.getByLabel('历史设备',{exact:true}).getAttribute('data-value'),'daikin-aabbcc000305');
  await chooseOption(dialog.getByLabel('数据指标',{exact:true}),'tvoc');
  await dialog.getByRole('group',{name:'TVOC趋势图',exact:true}).waitFor();
  assert.equal(await optionCount(dialog.getByLabel('数据指标',{exact:true})),7);
  const daikinDownloadEvent=page.waitForEvent('download');await dialog.getByRole('button',{name:'导出 CSV',exact:true}).click();
  const daikinCSV=readFileSync(await (await daikinDownloadEvent).path(),'utf8');assert.ok(daikinCSV.includes('PM10（μg/m³）')&&daikinCSV.includes('TVOC（ppb）'));
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  daikin.state.formaldehyde_present=true;daikin.state.co2_warmup_seconds_remaining=120;
  daikin.state.sensors.formaldehyde={valid:false,value:0.12,age_ms:1000};
  daikin.state.sensors.co2={valid:false,value:550,age_ms:1000};
  daikin.state.sensors.pm10={valid:true,value:99,age_ms:25000};
  await fetch(url+'api/devices/daikin-aabbcc000305/refresh',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
  await page.reload();await daikinCard.getByRole('button',{name:'大金检测仪设备控制',exact:true}).click();
  for(const label of ['CO₂','PM10','甲醛']) assert.ok((await controlDialog.getByText(label,{exact:true}).locator('..').textContent()).includes('—'),`${label} must not show invalid cached readings`);
  await controlDialog.getByText('预热中 · 剩余 120 秒',{exact:true}).waitFor();
  assert.ok((await controlDialog.getByText('温度',{exact:true}).locator('..').textContent()).includes('24.5'));
  await controlDialog.getByRole('button',{name:'关闭设备控制',exact:true}).click();
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   await page.getByRole('button',{name:'大金',exact:true}).click();
   assert.equal(await page.locator('.pm-tile').count(),1);
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   await page.getByRole('button',{name:'全部设备',exact:true}).click();
  }
  await page.getByRole('button',{name:'设置',exact:true}).click();
  const curveSettings=page.getByRole('dialog',{name:'设置',exact:true});
  const curveChoices=[['tc1-aabbcc000301','客厅排插','energy','用电量',3],['m1-aabbcc000302','书房空气检测仪','temp','温度',5],['a1-aabbcc000303','卧室净化器','filter','滤芯剩余',2],['daikin-aabbcc000305','大金检测仪','tvoc','TVOC',7]];
  for(const [id,,metric,,count] of curveChoices) {
   await chooseOption(curveSettings.getByLabel('设备',{exact:true}),id);
   assert.equal(await optionCount(curveSettings.getByLabel('卡片显示曲线',{exact:true})),count);
   await chooseOption(curveSettings.getByLabel('卡片显示曲线',{exact:true}),metric);
   const saved=page.waitForResponse(r=>r.url()===`${url}api/devices/${id}`&&r.request().method()==='PUT');
   await curveSettings.getByRole('button',{name:'保存配置',exact:true}).click();
   assert.equal((await (await saved).json()).device.chartMetric,metric);
   await curveSettings.getByText('配置已保存',{exact:true}).waitFor();
  }
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   assert.equal(await curveSettings.evaluate(el=>el.scrollWidth>el.clientWidth),false);
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'curve-metric-settings-mobile.png'),fullPage:true});
  await curveSettings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await page.reload();
  for(const [id,name,metric,label] of curveChoices) {
   const card=page.locator('.pm-tile').filter({has:page.locator(`[data-open-control="${id}"]`)});
   const curve=card.getByRole('button',{name:`${name}最近24小时${label}历史`,exact:true});
   await curve.locator('[data-history-sparkline]').waitFor();
   assert.ok((await curve.locator('.pm-sparkline-heading').textContent()).includes(label));
   if(metric==='energy') assert.ok(await curve.locator('.pm-trend-bar').count()>1,'usage must show interval totals as bars');
   await card.getByRole('button',{name:`${name}设备控制`,exact:true}).click();
   await controlDialog.getByRole('button',{name:`${name}最近24小时${label}历史`,exact:true}).waitFor();
   await controlDialog.getByRole('button',{name:'关闭设备控制',exact:true}).click();
   await curve.click();
   await dialog.getByRole('group',{name:`${label}趋势图`,exact:true}).waitFor();
   assert.equal(await dialog.getByLabel('数据指标',{exact:true}).getAttribute('data-value'),metric);
   await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  }
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await chooseOption(curveSettings.getByLabel('设备',{exact:true}),'daikin-aabbcc000305');
  assert.equal(await curveSettings.getByLabel('卡片显示曲线',{exact:true}).getAttribute('data-value'),'tvoc');
  await chooseOption(curveSettings.getByLabel('卡片显示曲线',{exact:true}),'hcho');
  const emptySaved=page.waitForResponse(r=>r.url()===`${url}api/devices/daikin-aabbcc000305`&&r.request().method()==='PUT');
  await curveSettings.getByRole('button',{name:'保存配置',exact:true}).click();await emptySaved;
  await curveSettings.getByText('配置已保存',{exact:true}).waitFor();
  await curveSettings.getByRole('button',{name:'关闭设置',exact:true}).click();
  await daikinCard.getByRole('button',{name:'大金检测仪最近24小时甲醛历史',exact:true}).getByText('等待甲醛记录',{exact:true}).waitFor();
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'curve-metric-cards-desktop.png'),fullPage:true});
  await daikinCard.getByRole('button',{name:'大金检测仪最近24小时甲醛历史',exact:true}).click();
  await dialog.getByLabel('卡片显示曲线',{exact:true}).waitFor();
  assert.equal(await dialog.getByLabel('卡片显示曲线',{exact:true}).getAttribute('data-value'),'hcho');
  await chooseOption(dialog.getByLabel('数据指标',{exact:true}),'temp');
  await dialog.getByRole('group',{name:'温度趋势图',exact:true}).waitFor();
  const inlineSaved=page.waitForResponse(r=>r.url()===`${url}api/devices/daikin-aabbcc000305`&&r.request().method()==='PUT');
  await chooseOption(dialog.getByLabel('卡片显示曲线',{exact:true}),'co2');
  assert.deepEqual((await inlineSaved).request().postDataJSON(),{chartMetric:'co2'});
  await until(async()=>await dialog.getByLabel('卡片显示曲线',{exact:true}).isEnabled());
  assert.equal(await dialog.getByLabel('卡片显示曲线',{exact:true}).getAttribute('data-value'),'co2');
  assert.equal(await dialog.getByLabel('数据指标',{exact:true}).getAttribute('data-value'),'temp','saving the default must preserve the currently viewed metric');
  for(const width of [320,390,768,1440]) {
   await page.setViewportSize({width,height:900});
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
   assert.equal(await dialog.evaluate(el=>el.scrollWidth>el.clientWidth),false);
   const first=await dialog.getByLabel('数据指标',{exact:true}).boundingBox(),second=await dialog.getByLabel('卡片显示曲线',{exact:true}).boundingBox();
   assert.ok(second.x>=first.x+first.width+8&&Math.abs(first.y-second.y)<1&&second.width>=90,'both selects must share a row without overflow');
  }
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(output,'history-default-metric-mobile.png'),fullPage:true});
  await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:path.join(output,'history-default-metric-desktop.png'),fullPage:true});
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await daikinCard.getByRole('button',{name:'大金检测仪最近24小时CO₂历史',exact:true}).click();
  await dialog.getByRole('group',{name:'CO₂趋势图',exact:true}).waitFor();
  assert.equal(await dialog.getByLabel('数据指标',{exact:true}).getAttribute('data-value'),'co2');
  const rejectDefault=route=>route.request().method()==='PUT'?route.fulfill({status:503,json:{error:'测试：默认曲线保存失败'}}):route.continue();
  await context.route(`${url}api/devices/daikin-aabbcc000305`,rejectDefault);
  await chooseOption(dialog.getByLabel('卡片显示曲线',{exact:true}),'tvoc');
  await dialog.getByRole('alert').filter({hasText:'测试：默认曲线保存失败'}).waitFor();
  assert.equal(await dialog.getByLabel('卡片显示曲线',{exact:true}).getAttribute('data-value'),'co2','failed save must retain the actual saved default');
  await context.unroute(`${url}api/devices/daikin-aabbcc000305`,rejectDefault);
  const retryDefault=page.waitForResponse(r=>r.url()===`${url}api/devices/daikin-aabbcc000305`&&r.request().method()==='PUT');
  await chooseOption(dialog.getByLabel('卡片显示曲线',{exact:true}),'tvoc');assert.equal((await retryDefault).status(),200);
  await until(async()=>await dialog.getByLabel('卡片显示曲线',{exact:true}).isEnabled());
  assert.equal(await dialog.getByRole('alert').count(),0);
  await dialog.getByRole('button',{name:'关闭历史记录',exact:true}).click();
  await page.reload();await daikinCard.getByRole('button',{name:'大金检测仪最近24小时TVOC历史',exact:true}).waitFor();
  await page.getByRole('button',{name:'设置',exact:true}).click();
  await chooseOption(curveSettings.getByLabel('设备',{exact:true}),'daikin-aabbcc000305');
  assert.equal(await curveSettings.getByLabel('卡片显示曲线',{exact:true}).getAttribute('data-value'),'tvoc','history and device settings must share the saved default');
  await curveSettings.getByRole('button',{name:'关闭设置',exact:true}).click();
  // Readings and units must update together as the device reports new values.
  await page.setViewportSize({width:1440,height:1000});
  await daikinCard.getByRole('button',{name:'大金检测仪设备控制',exact:true}).click();
  const sensorFields=['pm25','pm10','co2','hcho','temp','humidity','tvoc'];
  const wireFields=['pm25','pm10','co2','formaldehyde','temperature','humidity','tvoc'];
  const readingColors=[];
  for(const [tone,values] of [['good',[12,18,650,.013,24.5,51,0]],['warning',[50,100,1200,.09,30,65,400]],['high',[100,180,1800,.15,34,80,900]]]) {
   daikin.state.co2_warmup_seconds_remaining=0;daikin.state.formaldehyde_present=true;
   wireFields.forEach((field,i)=>{daikin.state.sensors[field]={valid:true,value:values[i],age_ms:1000};});
   const refreshed=await fetch(url+'api/devices/daikin-aabbcc000305/refresh',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
   assert.equal(refreshed.status,200);
   await until(async()=>(await controlDialog.locator('.pm-air-measurements .pm-live-reading').evaluateAll(els=>els.map(el=>el.getAttribute('data-reading-tone')))).every(value=>value===tone));
   for(const id of sensorFields) {
    const reading=controlDialog.locator(`.pm-air-measurements [data-metric="${id}"]`);
    assert.equal(await reading.getAttribute('data-reading-tone'),tone,`${id} must follow its own threshold`);
    assert.equal(await reading.evaluate(el=>getComputedStyle(el).color===getComputedStyle(el.querySelector('small')).color),true,'the unit must match the reading color');
   }
   readingColors.push(await controlDialog.locator('.pm-air-measurements [data-metric="pm25"]').evaluate(el=>getComputedStyle(el).color));
   await controlDialog.screenshot({path:path.join(output,`reading-colors-${tone}.png`)});
  }
  assert.equal(new Set(readingColors).size,3,'all three tones must be visually distinct');
  daikin.state.sensors.co2={valid:false,value:1800,age_ms:1000};daikin.state.co2_warmup_seconds_remaining=120;
  await fetch(url+'api/devices/daikin-aabbcc000305/refresh',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
  await until(async()=>await controlDialog.locator('.pm-air-measurements [data-metric="co2"]').getAttribute('data-reading-tone')===null);
  assert.ok((await controlDialog.locator('.pm-air-measurements [data-metric="co2"]').textContent()).includes('—'));
  await controlDialog.getByRole('button',{name:'关闭设备控制',exact:true}).click();
  assert.deepEqual(errors,[]);
  console.log('History browser passed: real SQLite collection, four models including DAIKIN, individual sensor validity/warmup, optional HCHO, control, CSV, ranges, gaps, keyboard/touch, themes and 320–1440px.');
 } finally {
  await browser.close();for(const fixture of fixtures) await fixture.close();
  if(child.exitCode===null) {child.kill();await new Promise(resolve=>child.once('exit',resolve));}
  rmSync(directory,{recursive:true,force:true});
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
