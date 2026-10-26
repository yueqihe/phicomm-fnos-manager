const assert=require('node:assert/strict');

async function menuFor(control) {
  await control.click();
  const id=await control.getAttribute('aria-controls');
  assert.ok(id,'combobox must expose its open listbox');
  const menu=control.page().locator(`[id=${JSON.stringify(id)}]`);
  await menu.waitFor({state:'visible'});
  return menu;
}
async function chooseOption(control,value) {
  const menu=await menuFor(control);
  await menu.locator(`[role="option"][data-value=${JSON.stringify(value)}]`).click();
  await menu.waitFor({state:'hidden'});
}
async function optionCount(control) {
  const menu=await menuFor(control), count=await menu.getByRole('option').count();
  await control.press('Escape');
  await menu.waitFor({state:'hidden'});
  return count;
}
module.exports={chooseOption,optionCount};
