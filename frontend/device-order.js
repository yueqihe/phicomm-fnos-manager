import {useEffect, useState} from 'react';

export const deviceSortOptions=[['room','按空间'],['added','按添加顺序'],['name','按设备名称'],['model','按设备型号'],['online','在线设备优先'],['manual','手动排序']];
const key='pm-device-sort';
const manualKey='pm-device-manual-order';
const normalize=value => deviceSortOptions.some(([id]) => id === value) ? value : 'room';
const collator=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'});
const roomOf=device => device.room?.trim() || '未分组';
const compareRooms=(a,b) => a === b ? 0 : a === '未分组' ? 1 : b === '未分组' ? -1 : collator.compare(a,b);

function readSort() {
  try {return normalize(localStorage.getItem(key));} catch {return 'room';}
}

function readManual() {
  try {
    const ids=JSON.parse(localStorage.getItem(manualKey));
    return Array.isArray(ids) ? [...new Set(ids.filter(id => typeof id === 'string'))] : [];
  } catch {return [];}
}

export function useDeviceSort() {
  const [order,setOrder]=useState(readSort);
  const [manual,setManual]=useState(readManual);
  useEffect(() => {
    const storage=e => {
      if(e.key === key || e.key === null) setOrder(readSort());
      if(e.key === manualKey || e.key === null) setManual(readManual());
    };
    window.addEventListener('storage',storage);
    return () => window.removeEventListener('storage',storage);
  },[]);
  const choose=value => {
    const next=normalize(value);
    try {localStorage.setItem(key,next);} catch {}
    setOrder(next);
  };
  const saveManual=ids => {
    try {localStorage.setItem(manualKey,JSON.stringify(ids));} catch {}
    setManual(ids);
    choose('manual');
  };
  return [order,choose,manual,saveManual];
}

// Stable ties retain registration order. Sorting must never mutate API data.
export function sortDevices(devices,order,manual=[]) {
  order=normalize(order);
  const ranks=order === 'manual' ? new Map(manual.map((id,index) => [id,index])) : null;
  return [...devices].sort((a,b) => {
    switch(order) {
      case 'room': return compareRooms(roomOf(a),roomOf(b));
      case 'manual': return compareRooms(roomOf(a),roomOf(b)) || (ranks.get(a.id) ?? manual.length)-(ranks.get(b.id) ?? manual.length);
      case 'name': return collator.compare(a.name || '',b.name || '');
      case 'model': return collator.compare(a.model || '',b.model || '');
      case 'online': return Number(!!b.online)-Number(!!a.online);
      default: return 0;
    }
  });
}

export function groupDevices(devices,order,manual=[]) {
  const groups=new Map();
  for(const device of devices) {
    const room=roomOf(device);
    if(!groups.has(room)) groups.set(room,[]);
    groups.get(room).push(device);
  }
  const entries=[...groups];
  if(['room','manual'].includes(normalize(order))) entries.sort(([a],[b]) => compareRooms(a,b));
  return entries.map(([room,devices]) => ({room,devices:sortDevices(devices,order,manual)}));
}

export function moveDeviceCard(devices,order,manual,source,target) {
  if(source === target) return null;
  const groups=groupDevices(devices,order,manual), group=groups.find(({devices}) => devices.some(d => d.id === source));
  if(!group) return null;
  const from=group.devices.findIndex(d => d.id === source), to=group.devices.findIndex(d => d.id === target);
  if(to < 0) return null;
  group.devices.splice(to,0,group.devices.splice(from,1)[0]);
  return groups.flatMap(({devices}) => devices.map(d => d.id));
}
