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
 assert.equal(await tile.locator('.pm-tile-outlet').count(),1);
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
 assert.equal(await control.locator('.pm-outlet').count(),1);
 assert.equal(await control.getByRole('slider').count(),0);
 await control.getByRole('switch',{name:`${name}插口 1`,exact:true}).click();
 await until(async()=>await control.getByRole('switch',{name:`${name}插口 1`,exact:true}).getAttribute('aria-checked')==='false');
 for(const label of ['指示灯','童锁']) {
  const toggle=control.getByRole('switch',{name:`${name}${label}`,exact:true});
  const old=await toggle.getAttribute('aria-checked');await toggle.click();
  await until(async()=>await toggle.getAttribute('aria-checked')!==old);
 }
 await control.getByRole('button',{name:'修改插口 1 名称',exact:true}).click();
 await control.getByLabel('插口 01 名称',{exact:true}).fill('书桌电源');
 await control.getByRole('button',{name:'保存名称',exact:true}).click();
 await control.getByRole('button',{name:'修改插口 1 名称',exact:true}).filter({hasText:'书桌电源'}).waitFor();
 assert.equal(device.state.socket_names[0],'书桌电源');
 for(const width of [320,390,768,1440]) {
  await page.setViewportSize({width,height:900});
  assert.equal(await control.evaluate(el=>el.scrollWidth>el.clientWidth),false);
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
 await page.locator('.pm-detail').getByRole('button',{name:'修改插口 1 名称',exact:true}).filter({hasText:'书桌电源'}).waitFor();
 assert.ok((await page.locator('.pm-detail').textContent()).includes('电压230.5V'));
 console.log('S30 UI passed: one outlet, metering, control, name sync, five chart choices, persistence and 320–1440px.');
};
