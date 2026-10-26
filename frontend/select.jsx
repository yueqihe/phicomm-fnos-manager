import React, {useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import {Check, ChevronDown} from 'lucide-react';

const textOf=children => React.Children.toArray(children).map(child => React.isValidElement(child) ? textOf(child.props.children) : String(child)).join('');

// Keep the menu in the browser's top layer so scrolling dialogs cannot clip it.
// Focus stays on the combobox; options are reached with the arrow keys.
export function Select({children,value='',onChange,name,id:providedId,disabled=false,editable=false,className='',...props}) {
  const generatedId=useId(), id=providedId || generatedId, menuId=`${id}-options`;
  const root=useRef(), trigger=useRef(), menu=useRef(), typed=useRef({text:'',at:0});
  const [open,setOpen]=useState(false), [active,setActive]=useState(-1), [filtering,setFiltering]=useState(false);
  const options=React.Children.toArray(children).filter(React.isValidElement).map(child => ({value:String(child.props.value ?? textOf(child.props.children)),label:textOf(child.props.children),disabled:!!child.props.disabled}));
  const shown=editable && filtering ? options.filter(option => option.label.toLocaleLowerCase().includes(String(value).toLocaleLowerCase())) : options;
  const optionsKey=JSON.stringify(shown), selected=options.find(option => option.value === String(value));
  const first=shown.findIndex(option => !option.disabled);

  function expand() {
    if(disabled) return;
    const index=options.findIndex(option => option.value === String(value) && !option.disabled);
    setFiltering(false);setActive(index>=0 ? index : options.findIndex(option => !option.disabled));setOpen(true);
  }
  function choose(option) {
    if(!option || option.disabled) return;
    setOpen(false);setFiltering(false);
    if(option.value !== String(value)) onChange?.({target:{name,value:option.value},currentTarget:{name,value:option.value}});
  }
  function move(step) {
    let index=active;
    for(let i=0;i<shown.length;i++) {
      index=(index+step+shown.length)%shown.length;
      if(!shown[index].disabled) {setActive(index);break;}
    }
  }
  function keyDown(e) {
    if(disabled || e.isComposing) return;
    if(e.key === 'Escape' && open) {e.preventDefault();e.stopPropagation();setOpen(false);return;}
    if(e.key === 'Tab') {setOpen(false);return;}
    if(e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();if(!open) expand();else move(e.key === 'ArrowDown' ? 1 : -1);return;
    }
    if(!editable && (e.key === 'Home' || e.key === 'End')) {
      e.preventDefault();if(!open) expand();
      setActive(e.key === 'Home' ? first : shown.findLastIndex(option => !option.disabled));return;
    }
    if(e.key === 'Enter' || !editable && e.key === ' ') {
      if(open) {e.preventDefault();choose(shown[active]);setOpen(false);}
      else if(!editable) {e.preventDefault();expand();}return;
    }
    if(!editable && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      e.preventDefault();const now=Date.now(), previous=now-typed.current.at<700 ? typed.current.text : '';
      const query=previous+e.key.toLocaleLowerCase();typed.current={text:query,at:now};
      const repeated=[...query].every(char => char === query[0]), prefix=repeated ? query[0] : query;
      const start=repeated ? active+1 : active;
      for(let i=0;i<options.length;i++) {
        const index=(Math.max(0,start)+i)%options.length, option=options[index];
        if(!option.disabled && option.label.toLocaleLowerCase().startsWith(prefix)) {setFiltering(false);setOpen(true);setActive(index);break;}
      }
    }
  }

  useEffect(() => {if(disabled) setOpen(false);},[disabled]);
  useEffect(() => {
    if(!open) return;
    const dismiss=e => {if(!root.current?.contains(e.target)) setOpen(false);};
    document.addEventListener('pointerdown',dismiss,true);
    return () => document.removeEventListener('pointerdown',dismiss,true);
  },[open]);
  useLayoutEffect(() => {
    if(!open) return;
    const popup=menu.current, anchor=trigger.current;
    popup.showPopover?.();
    const place=() => {
      const box=anchor.getBoundingClientRect(), viewport=window.visualViewport;
      const leftEdge=(viewport?.offsetLeft || 0)+8, topEdge=(viewport?.offsetTop || 0)+8;
      const rightEdge=leftEdge+(viewport?.width || innerWidth)-16, bottomEdge=topEdge+(viewport?.height || innerHeight)-16;
      if(box.bottom<=topEdge || box.top>=bottomEdge) {setOpen(false);return;}
      const width=Math.min(Math.max(box.width,160),rightEdge-leftEdge);
      popup.style.width=`${width}px`;
      const below=Math.max(0,bottomEdge-box.bottom-5), above=Math.max(0,box.top-topEdge-5);
      const downward=below >= Math.min(popup.scrollHeight,160) || below >= above;
      const height=Math.max(0,Math.min(264,downward ? below : above));
      popup.style.maxHeight=`${height}px`;
      popup.style.left=`${Math.max(leftEdge,Math.min(box.left,rightEdge-width))}px`;
      popup.style.top=`${downward ? box.bottom+5 : box.top-5-Math.min(popup.scrollHeight,height)}px`;
    };
    place();window.addEventListener('resize',place);document.addEventListener('scroll',place,true);
    window.visualViewport?.addEventListener('resize',place);window.visualViewport?.addEventListener('scroll',place);
    const observer=new ResizeObserver(place);observer.observe(anchor);
    return () => {
      observer.disconnect();window.removeEventListener('resize',place);document.removeEventListener('scroll',place,true);
      window.visualViewport?.removeEventListener('resize',place);window.visualViewport?.removeEventListener('scroll',place);
      if(popup.isConnected && popup.hidePopover && popup.matches(':popover-open')) popup.hidePopover();
    };
  },[open,optionsKey]);
  useLayoutEffect(() => {
    const popup=menu.current, option=popup?.querySelector(`[data-option-index="${active}"]`);
    if(!open || !option) return;
    if(option.offsetTop<popup.scrollTop) popup.scrollTop=option.offsetTop;
    else if(option.offsetTop+option.offsetHeight>popup.scrollTop+popup.clientHeight) popup.scrollTop=option.offsetTop+option.offsetHeight-popup.clientHeight;
  },[open,active,optionsKey]);

  const common={...props,id,ref:trigger,disabled,role:'combobox','aria-haspopup':'listbox','aria-expanded':open,'aria-controls':open ? menuId : undefined,'aria-activedescendant':open && shown[active] ? `${menuId}-${active}` : undefined,'data-value':String(value),onKeyDown:keyDown,onBlur:e => {if(!root.current?.contains(e.relatedTarget)) setOpen(false);}};
  return <div ref={root} className={`pm-select${editable ? ' pm-select-editable' : ''}`}>
    {editable ? <><input {...common} name={name} className={`pm-input pm-select-trigger ${className}`} value={value} autoComplete="off" aria-autocomplete="list" onFocus={expand} onClick={() => {if(!open) expand();}} onChange={e => {setFiltering(true);setActive(0);setOpen(true);onChange?.(e);}}/><ChevronDown className="pm-select-arrow" aria-hidden="true"/></> : <button {...common} type="button" className={`pm-input pm-select-trigger ${className}`} onClick={() => open ? setOpen(false) : expand()}><span className="pm-select-value">{selected?.label || '请选择'}</span><ChevronDown aria-hidden="true"/></button>}
    {!editable && name && <input type="hidden" name={name} value={value} disabled={disabled}/>}
    {open && <div ref={menu} id={menuId} role="listbox" aria-labelledby={id} popover="manual" className="pm-select-menu" onMouseDown={e => e.preventDefault()}>
      {shown.length ? shown.map((option,index) => <div key={option.value} id={`${menuId}-${index}`} role="option" aria-selected={option.value === String(value)} aria-disabled={option.disabled || undefined} data-option-index={index} data-value={option.value} data-active={active === index || undefined} className="pm-select-option" title={option.label} onPointerMove={e => {if(e.pointerType === 'mouse' && !option.disabled) setActive(index);}} onClick={e => {e.preventDefault();e.stopPropagation();choose(option);}}><span>{option.label}</span>{option.value === String(value) && <Check aria-hidden="true"/>}</div>) : <div className="pm-select-empty" role="status">输入新的空间名称</div>}
    </div>}
  </div>;
}
