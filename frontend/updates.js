import {useCallback, useEffect, useRef, useState} from 'react';
import {api, json} from './ui.jsx';

export function useUpdateCheck(enabled) {
  const [update,setUpdate]=useState(null), [error,setError]=useState(''), [checking,setChecking]=useState(false);
  const pending=useRef(), checked=useRef(0);
  const check=useCallback(async (force=false) => {
    if(!enabled || pending.current || !force && Date.now()-checked.current < 15*60*1000) return;
    const controller=new AbortController();pending.current=controller;
    setChecking(true);setError('');
    try {
      const result=await api('updates',{...(force ? json('POST',{}) : {}),signal:controller.signal});
      if(!controller.signal.aborted) {
        setUpdate(result);
        if(result.status !== 'error') checked.current=Date.now();
      }
    } catch(e) {if(!controller.signal.aborted) setError(e.message);}
    finally {
      if(pending.current === controller) pending.current=null;
      if(!controller.signal.aborted) setChecking(false);
    }
  },[enabled]);
  useEffect(() => {
    if(!enabled) return;
    const refresh=() => {if(!document.hidden) check();};
    refresh();
    const interval=setInterval(refresh,15*60*1000);
    document.addEventListener('visibilitychange',refresh);
    return () => {
      clearInterval(interval);
      document.removeEventListener('visibilitychange',refresh);
      pending.current?.abort();pending.current=null;
    };
  },[enabled,check]);
  return {update,error,checking,check};
}
