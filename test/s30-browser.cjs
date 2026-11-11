const assert=require('node:assert/strict');
const path=require('node:path');
const {chooseOption,optionCount}=require('./select-helpers.cjs');

// UI uses an isolated JSON fixture; Go integration tests cover S30's binary wire.
module.exports=async function testS30(page,fixtures,output) {
 const {httpDevice,until}=await import('./fixtures.js');
 const device=await httpDevice('S30-V1B','aabbcc000710');fixtures.push(device);
 const id='s30-v1b-aabbcc000710',name='书房单路插座';
 await page.setViewportSize({width:1440,height:1000});
 await page.getByRole('button',{name:'空间',exact:true}).click();
 await page.getByRole('button',{name:'添加设备',exact:true}).click();
 const add=page.getByRole('dialog',{name:'添加设备',exact:true});
 await chooseOption(add.getByLabel('设备型号',{exact:true}),'S30-V1B');
 await add.getByLabel('设备名称',{exact:true}).fill(name);
 await add.getByLabel('所属空间',{exact:true}).fill('书房');
 await add.getByLabel('设备内网地址',{exact:true}).fill(device.address);
 await page.locator('[form="add-form"]').click();await add.waitFor({state:'hidden'});
 await page.getByRole('button',{name:'S30-V1B',exact:true}).click();
 const tile=page.locator('.pm-tile').filter({has:page.locator(`[data-open-control="${id}"]`)});
 await tile.locator('[data-power-sparkline]').waitFor();
 assert.equal(await tile.locator('.pm-tile-outlet').count(),0,'single-outlet state is already represented by the master switch');
 assert.ok((await tile.locator('.pm-tile-watts').textContent()).startsWith('23.4W'));
 assert.equal(await tile.locator('.pm-tile-energy-reading').textContent(),'1.23度');
 assert.equal(await tile.getByLabel('实时电气指标').textContent(),'电压230.5V电流0.125A');
 await tile.getByRole('switch',{name:`${name}总开关`,exact:true}).click();
 await until(async()=>await tile.getByRole('switch').getAttribute('aria-checked')==='true');
 assert.equal(device.state.socket_states[0],1);
 for(const width of [320,390,768,1440]) {
  await page.setViewportSize({width,height:1000});
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
  const fit=await tile.evaluate(el=>el.scrollWidth<=el.clientWidth);
  assert.equal(fit,true,`S30 card overflow at ${width}`);
  if(width===390||width===1440) await page.screenshot({path:path.join(output,`s30-card-${width}.png`),fullPage:true});
 }
 await tile.locator(`[data-open-control="${id}"]`).click();
 const control=page.getByRole('dialog',{name:'设备控制',exact:true});
 assert.equal(await control.locator('.pm-outlets,.pm-outlet').count(),0,'single-outlet control must omit the outlet name section');
 assert.equal(await control.getByRole('slider').count(),0);
 assert.equal(await control.locator('[data-action="outlet.set"]').count(),1,'single-outlet control must expose one power switch');
 await control.getByRole('switch',{name:`${name}总开关`,exact:true}).click();
 await until(async()=>await control.getByRole('switch',{name:`${name}总开关`,exact:true}).getAttribute('aria-checked')==='false');
 for(const label of ['指示灯','童锁']) {
  const toggle=control.getByRole('switch',{name:`${name}${label}`,exact:true});
  const old=await toggle.getAttribute('aria-checked');await toggle.click();
  await until(async()=>await toggle.getAttribute('aria-checked')!==old);
 }
 const policy=control.getByRole('combobox',{name:'上电输出',exact:true}),policyArea=control.locator('.pm-power-on'),savePolicy=policyArea.getByRole('button',{name:'保存',exact:true});
 await policy.waitFor();assert.equal(await policy.getAttribute('data-value'),'off');
 assert.equal(await optionCount(policy),3);
 const currentOutput=device.state.socket_states[0];
 for(const mode of ['restore','on','off']) {
  const before=device.calls.filter(c=>c.action==='config.set'&&c.params.section==='device').length;
  await chooseOption(policy,mode);
  assert.equal(device.calls.filter(c=>c.action==='config.set'&&c.params.section==='device').length,before,'choosing a policy must wait for Save');
  await savePolicy.click();
  await until(async()=>device.state.power_on_mode===mode && await savePolicy.isDisabled());
  assert.equal(device.state.socket_states[0],currentOutput,'policy save must not toggle the current outlet');
 }
 // Preserve a chosen value when a write fails, while polling still reports the old one.
 const writeURL=`**/api/devices/${id}/control`;
 await page.route(writeURL,r=>r.fulfill({status:502,json:{error:'测试：上电策略保存失败'}}));
 await chooseOption(policy,'restore');await savePolicy.click();
 await until(async()=>await savePolicy.isEnabled());
 assert.equal(await policy.getAttribute('data-value'),'restore');
 assert.equal(device.state.power_on_mode,'off');
 await page.unroute(writeURL);await savePolicy.click();
 await until(async()=>device.state.power_on_mode==='restore' && await savePolicy.isDisabled());
 device.state.power_on_mode='on';device.state.power_on=true;
 const refreshDevice=()=>page.evaluate(async id=>{
  const session=await (await fetch('./api/session')).json();
  const response=await fetch(`./api/devices/${id}/refresh`,{method:'POST',headers:{'X-Manager-CSRF':session.csrfToken,'Content-Type':'application/json'},body:'{}'});
  if(!response.ok) throw new Error(`fixture refresh failed: ${await response.text()}`);
 },id);
 await refreshDevice();
 await until(async()=>await policy.getAttribute('data-value')==='on');
 // Older firmware exposes only a boolean and must not offer the restore mode.
 delete device.state.power_on_mode;delete device.capabilities.power_on_modes;
 await refreshDevice();
 await until(async()=>await optionCount(policy)===2);
 await chooseOption(policy,'off');await savePolicy.click();
 await until(async()=>device.state.power_on===false && await savePolicy.isDisabled());
 assert.equal(device.calls.filter(c=>c.action==='config.set'&&c.params.section==='device').at(-1).params.values.power_on,false);
 assert.equal(await control.getByRole('button',{name:'修改插口 1 名称',exact:true}).count(),0);
 for(const width of [320,390,768,1440]) {
  await page.setViewportSize({width,height:900});
  assert.equal(await control.evaluate(el=>el.scrollWidth>el.clientWidth),false);
  const bounds=await policyArea.boundingBox();
  assert.ok(bounds.x>=0 && bounds.x+bounds.width<=width,'power-on controls fit the viewport');
  if(width===390) await page.screenshot({path:path.join(output,'s30-power-on-mobile.png'),fullPage:true});
 }
 await control.getByRole('button',{name:`${name}最近24小时功率历史`,exact:true}).click();
 const history=page.getByRole('dialog',{name:'历史记录',exact:true});
 await history.getByRole('group',{name:'功率趋势图',exact:true}).waitFor();
 assert.equal(await optionCount(history.getByLabel('数据指标',{exact:true})),5);
 await chooseOption(history.getByLabel('数据指标',{exact:true}),'voltage');
 await history.getByRole('group',{name:'电压趋势图',exact:true}).waitFor();
 await chooseOption(history.getByLabel('卡片显示曲线',{exact:true}),'current');
 await until(async()=>await history.getByLabel('卡片显示曲线',{exact:true}).getAttribute('aria-busy')==='false');
 await history.getByRole('button',{name:'关闭历史记录',exact:true}).click();
 await tile.getByRole('button',{name:`${name}最近24小时电流历史`,exact:true}).waitFor();
 await page.reload();await page.getByRole('button',{name:'S30-V1B',exact:true}).click();
 await tile.getByRole('button',{name:`${name}最近24小时电流历史`,exact:true}).waitFor();
 await page.getByRole('button',{name:'列表',exact:true}).click();
 assert.equal(await page.locator('.pm-detail [data-action="outlet.set"]').count(),1,'list view must share the single power switch');
 assert.equal(await page.locator('.pm-detail .pm-outlets,.pm-detail .pm-outlet').count(),0);
 assert.ok((await page.locator('.pm-detail').textContent()).includes('电压230.5V'));
 console.log('S30 UI passed: one power switch, metering, power-on policies, failed drafts, device-side sync, legacy firmware, five chart choices, persistence and 320–1440px.');
};
