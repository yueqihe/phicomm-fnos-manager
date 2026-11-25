import React, {useCallback, useEffect, useRef, useState} from 'react';
import {createRoot} from 'react-dom/client';
import {api, json, Icon, Message, Modal, UpdateDot, DeviceSettingsButton} from './ui.jsx';
import {DeviceToolbar, DeviceWorkspace, Detail} from './devices.jsx';
import {AddDialog, SettingsDialog, HistoryDialog} from './dialogs.jsx';
import {useTheme} from './theme.js';
import {useDefaultLayout} from './layout.js';
import {moveDeviceCard, useDeviceSort} from './device-order.js';
import {NewDevicesNotice, useAutomaticDiscovery} from './automatic-discovery.jsx';
import {useUpdateCheck} from './updates.js';
import {AppFooter} from './about.jsx';
import {AppIcon, useAppFavicon} from './app-icon.jsx';
import {useWallpaper} from './wallpaper.jsx';

function App() {
  const [theme,setTheme]=useTheme();
  const wallpaper=useWallpaper();
  const [defaultLayout,setDefaultLayout]=useDefaultLayout();
  const [deviceSort,setDeviceSort,manualOrder,saveManualOrder]=useDeviceSort();
  const [session,setSession]=useState('loading'), [devices,setDevices]=useState([]), [settings,setSettings]=useState(null), [selected,setSelected]=useState(''), [layout,setLayout]=useState(defaultLayout), [filter,setFilter]=useState('all'), [loading,setLoading]=useState(true), [connected,setConnected]=useState(true), [serviceError,setServiceError]=useState(''), [modal,setModal]=useState(null), [message,setMessage]=useState(null), [busy,setBusy]=useState(new Set());
  const [cardToReveal,setCardToReveal]=useState(''), workspace=useRef(null), deviceRevision=useRef(0);
  const updateCheck=useUpdateCheck(session === 'ready'), updateAvailable=updateCheck.update?.status === 'update';
  useAppFavicon(session === 'ready' && updateAvailable);
  useEffect(() => {setLayout(defaultLayout);},[defaultLayout]);
  const chooseDefaultLayout=value => {setDefaultLayout(value);setLayout(value);};
  const alive=useRef(true), busyRef=useRef(new Set()), toastTimer=useRef(), fetching=useRef(false);
  const discovery=useAutomaticDiscovery(session === 'ready',devices);
  const notify=useCallback((text,error=false) => {clearTimeout(toastTimer.current);setMessage({text,error});toastTimer.current=setTimeout(() => setMessage(null),6000);},[]);
  const sync=useCallback(async (configuration=false) => {
    const revision=deviceRevision.current;
    try {
      const [data,config]=await Promise.all([api('devices'),configuration ? api('settings') : Promise.resolve(null)]);
      if(!alive.current) return;
      if(revision === deviceRevision.current) setDevices(data.devices);
      setSettings(old => ({...(config || old),...(data.refresh ? {refresh:data.refresh} : {}),mqttStatus:data.service}));setLoading(false);setConnected(true);setServiceError('');
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
    if(!cardToReveal || layout !== 'rooms' || modal) return;
    workspace.current?.querySelector(`[data-card-id="${CSS.escape(cardToReveal)}"]`)?.scrollIntoView({block:'nearest',inline:'nearest'});
    setCardToReveal('');
  },[cardToReveal,layout,modal]);
  useEffect(() => {
    alive.current=true;
    (async () => {try {const data=await api('session');if(!alive.current) return;if(!data.authenticated) {setSession('denied');return;}await sync(true);if(alive.current) setSession('ready');} catch(e) {if(alive.current) {setServiceError(e.message);setSession(e.status === 401 ? 'denied' : 'error');}}})();
    return () => {alive.current=false;clearTimeout(toastTimer.current);};
  },[sync]);
  useEffect(() => {
    if(session !== 'ready') return;
    const synchronize=async () => {if(fetching.current || document.hidden) return;fetching.current=true;try {await sync();} catch {} finally {fetching.current=false;}};
    const interval=setInterval(synchronize,(settings?.refresh?.pageSeconds || 5)*1000);
    document.addEventListener('visibilitychange',synchronize);
    window.addEventListener('online',synchronize);
    return () => {clearInterval(interval);document.removeEventListener('visibilitychange',synchronize);window.removeEventListener('online',synchronize);};
  },[session,sync,settings?.refresh?.pageSeconds]);
  const close=() => {setModal(null);setMessage(null);};
  const open=(type,options={}) => {setMessage(null);setModal({type,...options});};
  const selectLayout=value => {
    if(layout !== value) setCardToReveal(value === 'rooms' ? selected : '');
    setLayout(value);
  };
  const deviceAdded=device => {
    // Ignore list responses started before this registration succeeded.
    deviceRevision.current++;
    setDevices(old => old.some(d => d.id === device.id) ? old.map(d => d.id === device.id ? device : d) : [...old,device]);
    discovery.dismiss(device.id);
    setFilter(old => old === device.model ? old : 'all');
    setSelected(device.id);
    setCardToReveal(layout === 'rooms' ? device.id : '');
    close();
    notify(device.online ? '设备已接入' : '已登记，等待设备上线');
    void sync().catch(() => {});
  };
  const openSettings=async (id=selected,tab='devices') => {try {const config=await api('settings');setSettings(config);open('settings',{id,tab});} catch(e) {notify(e.message,true);}};
  const changeBusy=(id,value) => {if(value) busyRef.current.add(id);else busyRef.current.delete(id);setBusy(new Set(busyRef.current));};
  const replace=device => setDevices(old => old.map(d => d.id === device.id ? device : d));
  const saveChartMetric=async (id,chartMetric) => {
    const data=await api(`devices/${id}`,json('PUT',{chartMetric}));
    replace(data.device);
  };
  const control=async (d,action,params) => {
    if(busyRef.current.has(d.id)) return;changeBusy(d.id,true);
    try {const data=await api(`devices/${d.id}/control`,json('POST',{action,params}));replace(data.device);notify(data.message);return data;}
    catch(e) {notify(e.message,true);}
    finally {changeBusy(d.id,false);await sync().catch(() => {});}
  };
  const controls={control,busy,settings:openSettings,history:(id,metric) => open('history',{id,metric}),can:(d,action) => d.online && connected && !busy.has(d.id) && d.actions.includes(action)};
  if(session !== 'ready') return <div className="pm-app pm-login"><div className="pm-panel pm-login-card"><AppIcon/><h1>IoT 设备管理</h1>{session === 'loading' ? <p className="pm-help" role="status">正在连接管理服务…</p> : <><p className="pm-help" role="alert">{session === 'denied' ? '请使用飞牛 NAS 管理员账号登录，并从桌面重新打开应用。' : serviceError}</p><button className="pm-btn pm-btn-primary" onClick={() => location.reload()}>重新检查连接</button></>}</div></div>;
  const active=devices.find(d => d.id === modal?.id);
  const openAbout=() => openSettings(selected,'about');
  const moveCard=(source,target) => {
    const next=moveDeviceCard(devices,deviceSort,manualOrder,source,target);
    if(next) saveManualOrder(next);
  };
  return <div className="pm-app" data-layout={layout}>
    <header className="pm-top">
      <h1 className="pm-sr-only">IoT 设备管理</h1>
      <div className="pm-top-inner"><DeviceToolbar {...{devices,layout,filter,setFilter}} setLayout={selectLayout} add={() => open('add')}/><nav className="pm-top-actions" aria-label="应用设置"><button className="pm-nav pm-utility" aria-label="设置" aria-description={updateAvailable ? '有新版本可更新' : undefined} title={updateAvailable ? '设置（有新版本）' : '设置'} aria-haspopup="dialog" onClick={() => openSettings()}><Icon name="settings"/>{updateAvailable && <UpdateDot/>}</button></nav></div>
    </header>
    <div className="pm-service-error" role="status" hidden={!serviceError}>{serviceError}</div>
    <main className="pm-content" ref={workspace}><NewDevicesNotice devices={discovery.candidates} dismiss={discovery.dismiss} add={device => open('add',{device})}/><DeviceWorkspace {...{devices,selected,layout,filter,setFilter,loading,deviceSort,manualOrder,moveCard}} select={setSelected} openControl={id => {setSelected(id);open('control',{id});}} {...controls}/></main>
    <AppFooter updateCheck={updateCheck} onAbout={openAbout}/><Message message={modal ? null : message}/>
    {modal?.type === 'add' && <AddDialog {...{devices,message}} initialDevice={modal.device} onClose={close} added={deviceAdded}/>}
    {modal?.type === 'settings' && <SettingsDialog {...{devices,settings,sync,message,notify,busy,theme,setTheme,wallpaper,defaultLayout,deviceSort,setDeviceSort,updateCheck}} setDefaultLayout={chooseDefaultLayout} initialId={modal.id} initialTab={modal.tab} onClose={close}/>}
    {modal?.type === 'history' && <HistoryDialog saveChartMetric={saveChartMetric} devices={devices} initialId={modal.id || selected} initialMetric={modal.metric} message={message} onClose={close}/>}
    {modal?.type === 'control' && <Modal name="control" title="设备控制" message={message} onClose={close} headerActions={<DeviceSettingsButton device={active} onOpen={openSettings}/>}><div className="pm-dialog-body pm-control-body">{active ? <Detail device={active} {...controls}/> : <div className="pm-empty">设备已移除</div>}</div></Modal>}
  </div>;
}
createRoot(document.getElementById('manager')).render(<App/>);
