const assert=require('node:assert/strict');

async function assertDarkGlass(dialog) {
  const {pixel,filter}=await dialog.evaluate(el=>{
    const style=getComputedStyle(el),ctx=document.createElement('canvas').getContext('2d');
    ctx.fillStyle=style.backgroundColor;ctx.fillRect(0,0,1,1);
    return {pixel:Array.from(ctx.getImageData(0,0,1,1).data),filter:style.backdropFilter};
  });
  assert.ok(pixel.slice(0,3).every(c=>c<96)&&pixel[3]>0&&pixel[3]<255,'dialog needs a translucent dark fill');
  assert.match(filter,/blur\((?!0px)/,'dialog must blur the content behind it');
}
module.exports={assertDarkGlass};
