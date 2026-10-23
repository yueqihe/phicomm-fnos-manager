import {useEffect, useState} from 'react';

const modes=['system','light','dark'];
const valid=value => modes.includes(value) ? value : 'system';
function readPreference() {
  try {return valid(localStorage.getItem('pm-theme'));} catch {return 'system';}
}
const systemTheme=() => matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
function apply(mode) {
  document.documentElement.dataset.theme=mode === 'system' ? systemTheme() : mode;
}
apply(readPreference());

export function useTheme() {
  const [theme,setTheme]=useState(readPreference);
  useEffect(() => {
    const media=matchMedia('(prefers-color-scheme: dark)');
    const update=() => apply(theme);
    update();
    media.addEventListener('change',update);
    return () => media.removeEventListener('change',update);
  },[theme]);
  useEffect(() => {
    const storage=e => {if(e.key === 'pm-theme' || e.key === null) {const mode=readPreference();apply(mode);setTheme(mode);}};
    window.addEventListener('storage',storage);
    return () => window.removeEventListener('storage',storage);
  },[]);
  const chooseTheme=value => {
    const mode=valid(value);
    try {localStorage.setItem('pm-theme',mode);} catch {}
    apply(mode);setTheme(mode);
  };
  return [theme,chooseTheme];
}
