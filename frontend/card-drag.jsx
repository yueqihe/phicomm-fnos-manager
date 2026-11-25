import React, {useEffect, useRef, useState} from 'react';
import {Icon} from './ui.jsx';

export function SortableRoom({room,devices,onMove,renderCard}) {
  const grid=useRef(null), gesture=useRef(null), frame=useRef(0), skipClick=useRef(false);
  const [drag,setDrag]=useState(null), [picked,setPicked]=useState(''), [announcement,announce]=useState('');
  const pickedId=devices.some(d => d.id === picked) ? picked : '';
  useEffect(() => () => cancelAnimationFrame(frame.current),[]);

  function targetAt(x,y,source) {
    const card=document.elementFromPoint(x,y)?.closest('[data-card-id]');
    return card && grid.current?.contains(card) && card.dataset.cardId !== source ? card.dataset.cardId : '';
  }
  function scrollWhileDragging() {
    frame.current=0;
    const g=gesture.current;
    if(!g?.active) return;
    const edge=72, speed=g.y < edge ? -Math.ceil((edge-g.y)/5) : g.y > innerHeight-edge ? Math.ceil((g.y-innerHeight+edge)/5) : 0;
    if(!speed) return;
    const before=window.scrollY;
    window.scrollBy({top:speed,behavior:'instant'});
    if(window.scrollY === before) return;
    const target=targetAt(g.x,g.y,g.id);
    setDrag(current => current && current.target !== target ? {...current,target} : current);
    frame.current=requestAnimationFrame(scrollWhileDragging);
  }
  function move(source,target) {
    const from=devices.find(d => d.id === source), index=devices.findIndex(d => d.id === target);
    if(!from || index < 0 || source === target) return;
    onMove(source,target);
    announce(`${from.name}已移到第 ${index+1} 位`);
  }
  function start(event,id) {
    if(!event.isPrimary || event.button !== 0 || gesture.current) return;
    skipClick.current=false;
    const {pointerId,clientX:x,clientY:y,currentTarget:button}=event;
    gesture.current={id,pointerId,x,y,startX:x,startY:y,button,active:false};
    button.setPointerCapture(pointerId);
  }
  function pointerMove(event) {
    const g=gesture.current;
    if(g?.pointerId !== event.pointerId) return;
    const {clientX:x,clientY:y}=event;
    if(!g.active && Math.hypot(x-g.startX,y-g.startY) < 6) return;
    if(!g.active) {g.active=true;setPicked('');}
    g.x=x;g.y=y;
    setDrag({id:g.id,x,y,target:targetAt(x,y,g.id)});
    if(!frame.current) frame.current=requestAnimationFrame(scrollWhileDragging);
  }
  function stop(event,drop=false) {
    const g=gesture.current;
    if(!g || event.pointerId != null && g.pointerId !== event.pointerId) return;
    gesture.current=null;
    cancelAnimationFrame(frame.current);frame.current=0;
    if(g.button.hasPointerCapture(g.pointerId)) g.button.releasePointerCapture(g.pointerId);
    if(g.active) {
      skipClick.current=true;
      setDrag(null);
      if(drop) move(g.id,targetAt(event.clientX,event.clientY,g.id));
    }
  }
  function click(event,id,name) {
    if(event.detail && skipClick.current) {skipClick.current=false;return;}
    if(pickedId) {
      setPicked('');
      if(pickedId !== id) move(pickedId,id);else announce('已取消排序');
      return;
    }
    setPicked(id);announce(`已选中${name}，请选择目标卡片`);
  }
  function keyDown(event) {
    if(event.key === 'Escape' && (gesture.current || pickedId)) {
      event.preventDefault();stop(event);setPicked('');announce('已取消排序');return;
    }
    const grip=event.target.closest('[data-card-grip]');
    const step={ArrowUp:-1,ArrowLeft:-1,ArrowDown:1,ArrowRight:1}[event.key];
    if(!grip || !step || gesture.current) return;
    event.preventDefault();
    const id=grip.closest('[data-card-id]').dataset.cardId, index=devices.findIndex(d => d.id === id);
    const target=devices[index+step];
    if(index < 0 || !target) return;
    setPicked('');move(id,target.id);
  }

  const carried=devices.find(d => d.id === drag?.id);
  return <section className="pm-room-group" aria-label={`${room}设备`} onKeyDown={keyDown}>
    <h2 className="pm-room-header">{room}<span>{devices.length} 台设备</span></h2>
    <div ref={grid} className="pm-room-grid" onPointerMove={pointerMove} onPointerUp={event => stop(event,true)} onPointerCancel={stop} onLostPointerCapture={stop}>
      {devices.map(device => renderCard(device,devices.length > 1 && <button type="button" className="pm-card-grip" data-card-grip aria-label={`排序：${device.name}`} aria-description="拖动，点击后选择目标，或使用方向键移动" aria-pressed={pickedId === device.id} title="拖动排序" onPointerDown={event => start(event,device.id)} onClick={event => click(event,device.id,device.name)}><Icon name="grip"/></button>,drag?.id === device.id || pickedId === device.id ? 'source' : drag?.target === device.id ? 'target' : undefined))}
    </div>
    <span className="pm-sr-only" role="status">{announcement}</span>
    {carried && <div className="pm-card-drag-preview" aria-hidden="true" style={{left:Math.max(8,Math.min(drag.x+12,innerWidth-184)),top:Math.min(innerHeight-44,Math.max(8,drag.y-42))}}><Icon name="grip"/><span>{carried.name}</span></div>}
  </section>;
}
