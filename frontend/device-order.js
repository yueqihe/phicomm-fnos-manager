import {useEffect, useState} from 'react';

export const deviceSortOptions=[['room','按空间'],['added','按添加顺序'],['name','按设备名称'],['model','按设备型号'],['online','在线设备优先']];
const key='pm-device-sort';
const normalize=value => deviceSortOptions.some(([id]) => id === value) ? value : 'room';
const collator=new Intl.Collator('zh-CN',{numeric:true,sensitivity:'base'});
const roomOf=device => device.room?.trim() || '未分组';
const compareRooms=(a,b) => a === b ? 0 : a === '未分组' ? 1 : b === '未分组' ? -1 : collator.compare(a,b);

function readSort() {
  try {return normalize(localStorage.getItem(key));} catch {return 'room';}
}

export function useDeviceSort() {
  const [order,setOrder]=useState(readSort);
  useEffect(() => {
    const storage=e => {if(e.key === key || e.key === null) setOrder(readSort());};
    window.addEventListener('storage',storage);
    return () => window.removeEventListener('storage',storage);
  },[]);
  const choose=value => {
    const next=normalize(value);
    try {localStorage.setItem(key,next);} catch {}
    setOrder(next);
  };
  return [order,choose];
}

// Stable ties retain registration order. Sorting must never mutate API data.
export function sortDevices(devices,order) {
  return [...devices].sort((a,b) => {
    switch(normalize(order)) {
      case 'room': return compareRooms(roomOf(a),roomOf(b));
      case 'name': return collator.compare(a.name || '',b.name || '');
      case 'model': return collator.compare(a.model || '',b.model || '');
      case 'online': return Number(!!b.online)-Number(!!a.online);
      default: return 0;
    }
  });
}

export function groupDevices(devices,order) {
  const groups=new Map();
  for(const device of devices) {
    const room=roomOf(device);
    if(!groups.has(room)) groups.set(room,[]);
    groups.get(room).push(device);
  }
  const entries=[...groups];
  if(normalize(order) === 'room') entries.sort(([a],[b]) => compareRooms(a,b));
  return entries.map(([room,devices]) => ({room,devices:sortDevices(devices,order)}));
}
