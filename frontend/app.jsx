import React, {useCallback, useEffect, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {api, json, Icon, Dot, Message, Modal} from './ui.jsx';
import {DeviceWorkspace, Detail} from './devices.jsx';
import {AddDialog, SettingsDialog, HistoryDialog} from './dialogs.jsx';

function App() {
  const [session,setSession]=useState('loading'), [devices,setDevices]=useState([]), [settings,setSettings]=useState(null), [selected,setSelected]=useState(''), [layout,setLayoutState]=useState(() => localStorage.getItem('pm-view') === 'rooms' ? 'rooms' : 'list'), [filter,setFilter]=useState('all'), [loading,setLoading]=useState(true), [connected,setConnected]=useState(true), [serviceError,setServiceError]=useState(''), [modal,setModal]=useState(null), [message,setMessage]=useState(null), [busy,setBusy]=useState(new Set());
  const alive=useRef(true), busyRef=useRef(new Set()), toastTimer=useRef(), fetching=useRef(false);
  const notify=useCallback((text,error=false) => {clearTimeout(toastTimer.current);setMessage({text,error});toastTimer.current=setTimeout(() => setMessage(null),6000);},[]);
  const sync=useCallback(async (configuration=false) => {
    try {
      const [data,config]=await Promise.all([api('devices'),configuration ? api('settings') : Promise.resolve(null)]);
      if(!alive.current) return;
      setDevices(data.devices);setSettings(old => ({...(config || old),mqttStatus:data.service}));setLoading(false);setConnected(true);setServiceError('');
      return data;
    } catch(e) {
      if(alive.current) {setConnected(false);setServiceError(e.message);if(e.status === 401) {setSession('denied');setModal(null);}}
      throw e;
    }
  },[]);
  useEffect(() => {
    const visible=devices.filter(d => filter === 'all' || d.model === filter);
    setSelected(old => visible.some(d => d.id === old) ? old : visible[0]?.id || '');
  },[devices,filter]);
  useEffect(() => {
    alive.current=true;
    (async () => {try {const data=await api('session');if(!alive.current) return;if(!data.authenticated) {setSession('denied');return;}await sync(true);if(alive.current) setSession('ready');} catch(e) {if(alive.current) {setServiceError(e.message);setSession(e.status === 401 ? 'denied' : 'error');}}})();
    return () => {alive.current=false;clearTimeout(toastTimer.current);};
  },[sync]);
  useEffect(() => {
    if(session !== 'ready') return;
    const interval=setInterval(async () => {if(fetching.current || document.hidden) return;fetching.current=true;try {await sync();} catch {} finally {fetching.current=false;}},5000);
    return () => clearInterval(interval);
  },[session,sync]);
  const close=() => {setModal(null);setMessage(null);};
  const open=(type,options={}) => {setMessage(null);setModal({type,...options});};
  const openSettings=async (id=selected,tab='devices') => {try {const config=await api('settings');setSettings(config);open('settings',{id,tab});} catch(e) {notify(e.message,true);}};
  const setLayout=value => {localStorage.setItem('pm-view',value);setLayoutState(value);};
  const changeBusy=(id,value) => {if(value) busyRef.current.add(id);else busyRef.current.delete(id);setBusy(new Set(busyRef.current));};
  const replace=device => setDevices(old => old.map(d => d.id === device.id ? device : d));
  const control=async (d,action,params) => {
    if(busyRef.current.has(d.id)) return;changeBusy(d.id,true);
    try {const data=await api(`devices/${d.id}/control`,json('POST',{action,params}));replace(data.device);notify(data.message);}
    catch(e) {notify(e.message,true);}
    finally {changeBusy(d.id,false);await sync().catch(() => {});}
  };
  const refresh=async d => {
    if(busyRef.current.has(d.id)) return;changeBusy(d.id,true);
    try {const data=await api(`devices/${d.id}/refresh`,json('POST',{}));replace(data.device);notify(data.device.online ? '状态已更新' : data.device.error,!data.device.online);}
    catch(e) {notify(e.message,true);}
    finally {changeBusy(d.id,false);}
  };
  const controls={control,refresh,busy,settings:openSettings,can:(d,action) => d.online && connected && !busy.has(d.id) && d.actions.includes(action)};
  if(session !== 'ready') return <div className="pm-app pm-login"><div className="pm-panel pm-login-card"><span className="pm-brandmark"><Icon name="layers"/></span><h1>斐讯设备管理</h1>{session === 'loading' ? <p className="pm-help" role="status">正在连接管理服务…</p> : <><p className="pm-help" role="alert">{session === 'denied' ? '请使用飞牛 NAS 管理员账号登录，并从桌面重新打开应用。' : serviceError}</p><button className="pm-btn pm-btn-primary" onClick={() => location.reload()}>重新检查连接</button></>}</div></div>;
  const values=[devices.length,devices.filter(d => d.online).length,devices.filter(d => !d.online).length], active=devices.find(d => d.id === modal?.id);
  return <div className="pm-app" data-layout={layout}><header className="pm-top"><h1 className="pm-brand"><span className="pm-brandmark"><Icon name="layers"/></span>斐讯设备管理</h1><div className="pm-header-summary" role="group" aria-label="设备概况"><div className="pm-stats">{['全部设备','当前在线','暂时离线'].map((name,i) => <div className="pm-stat" key={name}><span className="pm-stat-label">{name}</span><span className="pm-stat-value">{loading ? '—' : String(values[i]).padStart(2,'0')}<small>台</small></span></div>)}</div></div><div className="pm-header-services" role="group" aria-label="连接状态"><button className="pm-connection-status" onClick={() => openSettings()}><Dot online={devices.some(d => d.lan)}/>局域网 <span className="pm-connection-count">{devices.filter(d => d.lan).length} 台</span></button><button className="pm-connection-status" title={settings.mqttStatus.message || '未启用'} onClick={() => openSettings(selected,'service')}><Dot online={settings.mqttStatus.state === 'connected'}/>MQTT <span className="pm-connection-count">{devices.filter(d => d.mqtt).length} 台</span></button></div><nav className="pm-top-actions" aria-label="设置与记录"><button className="pm-nav pm-utility" aria-label="设置" title="设置" aria-haspopup="dialog" onClick={() => openSettings()}><Icon name="settings"/></button><button className="pm-nav pm-utility" aria-label="历史记录" title="历史记录" aria-haspopup="dialog" onClick={() => open('history')}><Icon name="history"/></button></nav></header><div className="pm-service-error" role="status" hidden={!serviceError}>{serviceError}</div><main className="pm-content"><DeviceWorkspace {...{devices,selected,layout,filter,setFilter,setLayout,loading}} select={setSelected} add={() => open('add')} openControl={id => {setSelected(id);open('control',{id});}} {...controls}/></main><Message message={modal ? null : message}/>
    {modal?.type === 'add' && <AddDialog {...{devices,message}} onClose={close} added={async device => {setSelected(device.id);await sync();close();notify(device.online ? '设备已接入' : '已登记，等待设备上线');}}/>}
    {modal?.type === 'settings' && <SettingsDialog {...{devices,settings,sync,message,notify,busy}} initialId={modal.id} initialTab={modal.tab} onClose={close}/>}
    {modal?.type === 'history' && <HistoryDialog message={message} onClose={close}/>}
    {modal?.type === 'control' && <Modal name="control" title="设备控制" message={message} onClose={close}><div className="pm-dialog-body pm-control-body">{active ? <Detail device={active} {...controls}/> : <div className="pm-empty">设备已移除</div>}</div></Modal>}
  </div>;
}
createRoot(document.getElementById('manager')).render(<App/>);
