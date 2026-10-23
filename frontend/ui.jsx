import React, {useId, useLayoutEffect, useRef} from 'react';
import {Layers2, Settings2, History, X, Plus, List, LayoutGrid, PlugZap, AirVent, Wind, Unplug, ArrowUpRight} from 'lucide-react';

const icons = {layers: Layers2, settings: Settings2, history: History, x: X, plus: Plus, list: List, grid: LayoutGrid, plug: PlugZap, air: AirVent, wind: Wind, unplug: Unplug, arrow: ArrowUpRight};
export function Icon({name}) { const Component = icons[name]; return <Component size={17} strokeWidth={1.6} aria-hidden="true"/>; }
export function DeviceIcon({device}) { return <Icon name={device.model === 'TC1' ? 'plug' : device.model === 'M1' ? 'air' : 'wind'}/>; }
export function Dot({online}) { return <span className={`pm-dot${online ? '' : ' pm-dot-off'}`}/>; }
export function Status({device}) { return <span className="pm-status"><Dot online={device.online}/>{device.online ? '在线' : '离线'}</span>; }
export const metric = (value, digits) => typeof value === 'number' && Number.isFinite(value) ? digits === undefined ? String(value) : value.toFixed(digits) : '—';
export const when = value => value ? new Date(value).toLocaleString('zh-CN', {hour12: false}) : '尚未连接';
export const methods = d => [d.lan ? '局域网' : '', d.mqtt ? 'MQTT' : ''].filter(Boolean).join(' + ') || '等待接入';
export function Field({label, children}) {
  const id = useId();
  return <div className="pm-field"><label htmlFor={id}>{label}</label>{React.Children.map(children, child => React.isValidElement(child) && (['input','select'].includes(child.type) || child.type === Input) ? React.cloneElement(child, {id}) : child)}</div>;
}
export function Input(props) { return <input className="pm-input" {...props}/>; }
export function RoomField({value, onChange, devices}) {
  const id = useId();
  return <Field label="所属空间"><input className="pm-input" name="room" value={value} onChange={onChange} maxLength={32} list={id}/><datalist id={id}>{[...new Set(['未分组',...devices.map(d => d.room)])].map(room => <option key={room} value={room}/>)}</datalist></Field>;
}
export function Message({message, inline = false}) {
  return <div className={`${inline ? 'pm-inline-status' : 'pm-toast'}${message?.error ? ' is-error' : ''}`} role="status" aria-live="polite" hidden={!message}>{message?.text}</div>;
}
export function Modal({name, title, children, footer, onClose, message}) {
  const ref = useRef();
  useLayoutEffect(() => {
    const el = ref.current, previous = document.activeElement;
    el.showModal(); el.querySelector('[data-close]').focus({preventScroll:true});
    return () => { el.close(); if (previous?.isConnected) previous.focus({preventScroll:true}); };
  }, []);
  return <dialog ref={ref} className={`pm-${name}-dialog`} aria-label={title} onCancel={e => {e.preventDefault(); onClose();}} onClick={e => {
    if(e.target !== e.currentTarget) return;
    const box = e.currentTarget.getBoundingClientRect();
    if(e.clientX < box.left || e.clientX > box.right || e.clientY < box.top || e.clientY > box.bottom) onClose();
  }}><div className="pm-dialog-head"><h2>{title}</h2><button className="pm-icon-button" type="button" data-close aria-label={`关闭${title}`} onClick={onClose}><Icon name="x"/></button></div>{children}{footer}<Message inline message={message}/></dialog>;
}

export async function api(path, options = {}) {
  let response;
  try { response = await fetch(`./api/${path}`, {...options, headers: {'Content-Type':'application/json',...options.headers}, signal: options.signal || AbortSignal.timeout(45000)}); }
  catch (error) { if (error.name === 'AbortError') throw error; throw new Error('无法连接管理服务，请检查网络'); }
  const data = await response.json();
  if(!response.ok) { const error = new Error(data.error || '请求失败'); error.status = response.status; throw error; }
  return data;
}
export const json = (method, value) => ({method,body:JSON.stringify(value)});
