import React, {useEffect} from 'react';

const source=__PM_APP_ICON__;
const iconURL=svg => `data:image/svg+xml,${encodeURIComponent(svg)}`;
const normal=iconURL(source);
const withUpdate=iconURL(source.replace('</svg>','<circle cx="202" cy="54" r="25" fill="#20242b"/><circle cx="202" cy="54" r="18" fill="#ff5c69"/></svg>'));

export function useAppFavicon(updateAvailable) {
  useEffect(() => {
    const link=document.createElement('link');
    link.rel='icon';
    link.type='image/svg+xml';
    link.href=updateAvailable ? withUpdate : normal;
    document.head.append(link);
    return () => link.remove();
  },[updateAvailable]);
}

export function AppIcon({updateAvailable=false}) {
  return <img className="pm-app-icon pm-icon" data-icon="app" src={updateAvailable ? withUpdate : normal} alt="" width="32" height="32"/>;
}
