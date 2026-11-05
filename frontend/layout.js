import {useEffect, useState} from 'react';

const key='pm-default-layout';
const normalize=value => value === 'list' ? 'list' : 'rooms';
function readDefault() {
  try {return normalize(localStorage.getItem(key));} catch {return 'rooms';}
}

export function useDefaultLayout() {
  const [layout,setLayout]=useState(readDefault);
  useEffect(() => {
    const storage=e => {if(e.key === key || e.key === null) setLayout(readDefault());};
    window.addEventListener('storage',storage);
    return () => window.removeEventListener('storage',storage);
  },[]);
  const choose=value => {
    const next=normalize(value);
    try {localStorage.setItem(key,next);} catch {}
    setLayout(next);
  };
  return [layout,choose];
}
