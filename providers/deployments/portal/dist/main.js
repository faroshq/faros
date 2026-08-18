(function(){"use strict";/**
* @vue/shared v3.5.41
* (c) 2018-present Yuxi (Evan) You and Vue contributors
* @license MIT
**/function Jn(e){const t=Object.create(null);for(const n of e.split(","))t[n]=1;return n=>n in t}const G={},vt=[],Ae=()=>{},Br=()=>!1,on=e=>e.charCodeAt(0)===111&&e.charCodeAt(1)===110&&(e.charCodeAt(2)>122||e.charCodeAt(2)<97),sn=e=>e.startsWith("onUpdate:"),ie=Object.assign,Yn=(e,t)=>{const n=e.indexOf(t);n>-1&&e.splice(n,1)},Zs=Object.prototype.hasOwnProperty,B=(e,t)=>Zs.call(e,t),M=Array.isArray,bt=e=>At(e)==="[object Map]",ln=e=>At(e)==="[object Set]",Kr=e=>At(e)==="[object Date]",F=e=>typeof e=="function",Z=e=>typeof e=="string",Ce=e=>typeof e=="symbol",K=e=>e!==null&&typeof e=="object",Wr=e=>(K(e)||F(e))&&F(e.then)&&F(e.catch),Gr=Object.prototype.toString,At=e=>Gr.call(e),ei=e=>At(e).slice(8,-1),Jr=e=>At(e)==="[object Object]",Qn=e=>Z(e)&&e!=="NaN"&&e[0]!=="-"&&""+parseInt(e,10)===e,It=Jn(",key,ref,ref_for,ref_key,onVnodeBeforeMount,onVnodeMounted,onVnodeBeforeUpdate,onVnodeUpdated,onVnodeBeforeUnmount,onVnodeUnmounted"),an=e=>{const t=Object.create(null);return(n=>t[n]||(t[n]=e(n)))},ti=/-\w/g,Re=an(e=>e.replace(ti,t=>t.slice(1).toUpperCase())),ni=/\B([A-Z])/g,ot=an(e=>e.replace(ni,"-$1").toLowerCase()),Yr=an(e=>e.charAt(0).toUpperCase()+e.slice(1)),Xn=an(e=>e?`on${Yr(e)}`:""),Ie=(e,t)=>!Object.is(e,t),cn=(e,...t)=>{for(let n=0;n<e.length;n++)e[n](...t)},Qr=(e,t,n,r=!1)=>{Object.defineProperty(e,t,{configurable:!0,enumerable:!1,writable:r,value:n})},Zn=e=>{const t=parseFloat(e);return isNaN(t)?e:t};let Xr;const un=()=>Xr||(Xr=typeof globalThis<"u"?globalThis:typeof self<"u"?self:typeof window<"u"?window:typeof global<"u"?global:{});function dn(e){if(M(e)){const t={};for(let n=0;n<e.length;n++){const r=e[n],o=Z(r)?ii(r):dn(r);if(o)for(const s in o)t[s]=o[s]}return t}else if(Z(e)||K(e))return e}const ri=/;(?![^(]*\))/g,oi=/:([^]+)/,si=/\/\*[^]*?\*\//g;function ii(e){const t={};return e.replace(si,"").split(ri).forEach(n=>{if(n){const r=n.split(oi);r.length>1&&(t[r[0].trim()]=r[1].trim())}}),t}function Pe(e){let t="";if(Z(e))t=e;else if(M(e))for(let n=0;n<e.length;n++){const r=Pe(e[n]);r&&(t+=r+" ")}else if(K(e))for(const n in e)e[n]&&(t+=n+" ");return t.trim()}const li=Jn("itemscope,allowfullscreen,formnovalidate,ismap,nomodule,novalidate,readonly");function Zr(e){return!!e||e===""}function ai(e,t){if(e.length!==t.length)return!1;let n=!0;for(let r=0;n&&r<e.length;r++)n=Pt(e[r],t[r]);return n}function Pt(e,t){if(e===t)return!0;let n=Kr(e),r=Kr(t);if(n||r)return n&&r?e.getTime()===t.getTime():!1;if(n=Ce(e),r=Ce(t),n||r)return e===t;if(n=M(e),r=M(t),n||r)return n&&r?ai(e,t):!1;if(n=K(e),r=K(t),n||r){if(!n||!r)return!1;const o=Object.keys(e).length,s=Object.keys(t).length;if(o!==s)return!1;for(const l in e){const i=e.hasOwnProperty(l),a=t.hasOwnProperty(l);if(i&&!a||!i&&a||!Pt(e[l],t[l]))return!1}}return String(e)===String(t)}function eo(e,t){return e.findIndex(n=>Pt(n,t))}const to=e=>!!(e&&e.__v_isRef===!0),O=e=>Z(e)?e:e==null?"":M(e)||K(e)&&(e.toString===Gr||!F(e.toString))?to(e)?O(e.value):JSON.stringify(e,no,2):String(e),no=(e,t)=>to(t)?no(e,t.value):bt(t)?{[`Map(${t.size})`]:[...t.entries()].reduce((n,[r,o],s)=>(n[er(r,s)+" =>"]=o,n),{})}:ln(t)?{[`Set(${t.size})`]:[...t.values()].map(n=>er(n))}:Ce(t)?er(t):K(t)&&!M(t)&&!Jr(t)?String(t):t,er=(e,t="")=>{var n;return Ce(e)?`Symbol(${(n=e.description)!=null?n:t})`:e};/**
* @vue/reactivity v3.5.41
* (c) 2018-present Yuxi (Evan) You and Vue contributors
* @license MIT
**/let le;class ci{constructor(t=!1){this.detached=t,this._active=!0,this._on=0,this.effects=[],this.cleanups=[],this._isPaused=!1,this._warnOnRun=!0,this.__v_skip=!0,!t&&le&&(le.active?(this.parent=le,this.index=(le.scopes||(le.scopes=[])).push(this)-1):(this._active=!1,this._warnOnRun=!1))}get active(){return this._active}pause(){if(this._active){this._isPaused=!0;let t,n;if(this.scopes){const r=this.scopes.slice();for(t=0,n=r.length;t<n;t++)r[t].pause()}for(t=0,n=this.effects.length;t<n;t++)this.effects[t].pause()}}resume(){if(this._active&&this._isPaused){this._isPaused=!1;let t,n;if(this.scopes){const o=this.scopes.slice();for(t=0,n=o.length;t<n;t++)o[t].resume()}const r=this.effects.slice();for(t=0,n=r.length;t<n;t++)r[t].resume()}}run(t){if(this._active){const n=le;try{return le=this,t()}finally{le=n}}}on(){++this._on===1&&(this.prevScope=le,le=this)}off(){if(this._on>0&&--this._on===0){if(le===this)le=this.prevScope;else{let t=le;for(;t;){if(t.prevScope===this){t.prevScope=this.prevScope;break}t=t.prevScope}}this.prevScope=void 0}}stop(t){if(this._active){this._active=!1;let n,r;for(n=0,r=this.effects.length;n<r;n++)this.effects[n].stop();for(this.effects.length=0,n=0,r=this.cleanups.length;n<r;n++)this.cleanups[n]();if(this.cleanups.length=0,this.scopes){const o=this.scopes.slice();for(n=0,r=o.length;n<r;n++)o[n].stop(!0);this.scopes.length=0}if(!this.detached&&this.parent&&!t){const o=this.parent.scopes.pop();o&&o!==this&&(this.parent.scopes[this.index]=o,o.index=this.index)}this.parent=void 0}}}function ui(){return le}let Y;const tr=new WeakSet;class ro{constructor(t){this.fn=t,this.deps=void 0,this.depsTail=void 0,this.flags=5,this.next=void 0,this.cleanup=void 0,this.scheduler=void 0,le&&(le.active?le.effects.push(this):this.flags&=-2)}pause(){this.flags|=64}resume(){this.flags&64&&(this.flags&=-65,tr.has(this)&&(tr.delete(this),this.trigger()))}notify(){this.flags&2&&!(this.flags&32)||this.flags&8||so(this)}run(){if(!(this.flags&1))return this.fn();this.flags|=2,uo(this),io(this);const t=Y,n=Te;Y=this,Te=!0;try{return this.fn()}finally{lo(this),Y=t,Te=n,this.flags&=-3}}stop(){if(this.flags&1){for(let t=this.deps;t;t=t.nextDep)sr(t);this.deps=this.depsTail=void 0,uo(this),this.onStop&&this.onStop(),this.flags&=-2}}trigger(){this.flags&64?tr.add(this):this.scheduler?this.scheduler():this.runIfDirty()}runIfDirty(){or(this)&&this.run()}get dirty(){return or(this)}}let oo=0,Ot,Mt;function so(e,t=!1){if(e.flags|=8,t){e.next=Mt,Mt=e;return}e.next=Ot,Ot=e}function nr(){oo++}function rr(){if(--oo>0)return;if(Mt){let t=Mt;for(Mt=void 0;t;){const n=t.next;t.next=void 0,t.flags&=-9,t=n}}let e;for(;Ot;){let t=Ot;for(Ot=void 0;t;){const n=t.next;if(t.next=void 0,t.flags&=-9,t.flags&1)try{t.trigger()}catch(r){e||(e=r)}t=n}}if(e)throw e}function io(e){for(let t=e.deps;t;t=t.nextDep)t.version=-1,t.prevActiveLink=t.dep.activeLink,t.dep.activeLink=t}function lo(e){let t,n=e.depsTail,r=n;for(;r;){const o=r.prevDep;r.version===-1?(r===n&&(n=o),sr(r),di(r)):t=r,r.dep.activeLink=r.prevActiveLink,r.prevActiveLink=void 0,r=o}e.deps=t,e.depsTail=n}function or(e){for(let t=e.deps;t;t=t.nextDep)if(t.dep.version!==t.version||t.dep.computed&&(ao(t.dep.computed)||t.dep.version!==t.version))return!0;return!!e._dirty}function ao(e){if(e.flags&4&&!(e.flags&16)||(e.flags&=-17,e.globalVersion===Nt)||(e.globalVersion=Nt,!e.isSSR&&e.flags&128&&(!e.deps&&!e._dirty||!or(e))))return;e.flags|=2;const t=e.dep,n=Y,r=Te;Y=e,Te=!0;try{io(e);const o=e.fn(e._value);(t.version===0||Ie(o,e._value))&&(e.flags|=128,e._value=o,t.version++)}catch(o){throw t.version++,o}finally{Y=n,Te=r,lo(e),e.flags&=-3}}function sr(e,t=!1){const{dep:n,prevSub:r,nextSub:o}=e;if(r&&(r.nextSub=o,e.prevSub=void 0),o&&(o.prevSub=r,e.nextSub=void 0),n.subs===e&&(n.subs=r,!r&&n.computed)){n.computed.flags&=-5;for(let s=n.computed.deps;s;s=s.nextDep)sr(s,!0)}!t&&!--n.sc&&n.map&&n.map.delete(n.key)}function di(e){const{prevDep:t,nextDep:n}=e;t&&(t.nextDep=n,e.prevDep=void 0),n&&(n.prevDep=t,e.nextDep=void 0)}let Te=!0;const co=[];function Oe(){co.push(Te),Te=!1}function Me(){const e=co.pop();Te=e===void 0?!0:e}function uo(e){const{cleanup:t}=e;if(e.cleanup=void 0,t){const n=Y;Y=void 0;try{t()}finally{Y=n}}}let Nt=0;class fi{constructor(t,n){this.sub=t,this.dep=n,this.version=n.version,this.nextDep=this.prevDep=this.nextSub=this.prevSub=this.prevActiveLink=void 0}}class ir{constructor(t){this.computed=t,this.version=0,this.activeLink=void 0,this.subs=void 0,this.map=void 0,this.key=void 0,this.sc=0,this.__v_skip=!0}track(t){if(!Y||!Te||Y===this.computed)return;let n=this.activeLink;if(n===void 0||n.sub!==Y)n=this.activeLink=new fi(Y,this),Y.deps?(n.prevDep=Y.depsTail,Y.depsTail.nextDep=n,Y.depsTail=n):Y.deps=Y.depsTail=n,fo(n);else if(n.version===-1&&(n.version=this.version,n.nextDep)){const r=n.nextDep;r.prevDep=n.prevDep,n.prevDep&&(n.prevDep.nextDep=r),n.prevDep=Y.depsTail,n.nextDep=void 0,Y.depsTail.nextDep=n,Y.depsTail=n,Y.deps===n&&(Y.deps=r)}return n}trigger(t){this.version++,Nt++,this.notify(t)}notify(t){nr();try{for(let n=this.subs;n;n=n.prevSub)n.sub.notify()&&n.sub.dep.notify()}finally{rr()}}}function fo(e){if(e.dep.sc++,e.sub.flags&4){const t=e.dep.computed;if(t&&!e.dep.subs){t.flags|=20;for(let r=t.deps;r;r=r.nextDep)fo(r)}const n=e.dep.subs;n!==e&&(e.prevSub=n,n&&(n.nextSub=e)),e.dep.subs=e}}const lr=new WeakMap,st=Symbol(""),ar=Symbol(""),Lt=Symbol("");function de(e,t,n){if(Te&&Y){let r=lr.get(e);r||lr.set(e,r=new Map);let o=r.get(n);o||(r.set(n,o=new ir),o.map=r,o.key=n),o.track()}}function Ke(e,t,n,r,o,s){const l=lr.get(e);if(!l){Nt++;return}const i=a=>{a&&a.trigger()};if(nr(),t==="clear")l.forEach(i);else{const a=M(e),d=a&&Qn(n);if(a&&n==="length"){const u=Number(r);l.forEach((h,_)=>{(_==="length"||_===Lt||!Ce(_)&&_>=u)&&i(h)})}else switch((n!==void 0||l.has(void 0))&&i(l.get(n)),d&&i(l.get(Lt)),t){case"add":a?d&&i(l.get("length")):(i(l.get(st)),bt(e)&&i(l.get(ar)));break;case"delete":a||(i(l.get(st)),bt(e)&&i(l.get(ar)));break;case"set":bt(e)&&i(l.get(st));break}}rr()}function xt(e){const t=q(e);return t===e?t:(de(t,"iterate",Lt),Se(e)?t:t.map(Ee))}function fn(e){return de(e=q(e),"iterate",Lt),e}function Ne(e,t){return Ge(e)?_t(it(e)?Ee(t):t):Ee(t)}const pi={__proto__:null,[Symbol.iterator](){return cr(this,Symbol.iterator,e=>Ne(this,e))},concat(...e){return xt(this).concat(...e.map(t=>M(t)?xt(t):t))},entries(){return cr(this,"entries",e=>(e[1]=Ne(this,e[1]),e))},every(e,t){return We(this,"every",e,t,void 0,arguments)},filter(e,t){return We(this,"filter",e,t,n=>n.map(r=>Ne(this,r)),arguments)},find(e,t){return We(this,"find",e,t,n=>Ne(this,n),arguments)},findIndex(e,t){return We(this,"findIndex",e,t,void 0,arguments)},findLast(e,t){return We(this,"findLast",e,t,n=>Ne(this,n),arguments)},findLastIndex(e,t){return We(this,"findLastIndex",e,t,void 0,arguments)},forEach(e,t){return We(this,"forEach",e,t,void 0,arguments)},includes(...e){return ur(this,"includes",e)},indexOf(...e){return ur(this,"indexOf",e)},join(e){return xt(this).join(e)},lastIndexOf(...e){return ur(this,"lastIndexOf",e)},map(e,t){return We(this,"map",e,t,void 0,arguments)},pop(){return Dt(this,"pop")},push(...e){return Dt(this,"push",e)},reduce(e,...t){return po(this,"reduce",e,t)},reduceRight(e,...t){return po(this,"reduceRight",e,t)},shift(){return Dt(this,"shift")},some(e,t){return We(this,"some",e,t,void 0,arguments)},splice(...e){return Dt(this,"splice",e)},toReversed(){return xt(this).toReversed()},toSorted(e){return xt(this).toSorted(e)},toSpliced(...e){return xt(this).toSpliced(...e)},unshift(...e){return Dt(this,"unshift",e)},values(){return cr(this,"values",e=>Ne(this,e))}};function cr(e,t,n){const r=fn(e),o=r[t]();return r!==e&&!Se(e)&&(o._next=o.next,o.next=()=>{const s=o._next();return s.done||(s.value=n(s.value)),s}),o}const hi=Array.prototype;function We(e,t,n,r,o,s){const l=fn(e),i=l!==e&&!Se(e),a=l[t];if(a!==hi[t]){const h=a.apply(e,s);return i?Ee(h):h}let d=n;l!==e&&(i?d=function(h,_){return n.call(this,Ne(e,h),_,e)}:n.length>2&&(d=function(h,_){return n.call(this,h,_,e)}));const u=a.call(l,d,r);return i&&o?o(u):u}function po(e,t,n,r){const o=fn(e),s=o!==e&&!Se(e);let l=n,i=!1;o!==e&&(s?(i=r.length===0,l=function(d,u,h){return i&&(i=!1,d=Ne(e,d)),n.call(this,d,Ne(e,u),h,e)}):n.length>3&&(l=function(d,u,h){return n.call(this,d,u,h,e)}));const a=o[t](l,...r);return i?Ne(e,a):a}function ur(e,t,n){const r=q(e);de(r,"iterate",Lt);const o=r[t](...n);return(o===-1||o===!1)&&pr(n[0])?(n[0]=q(n[0]),r[t](...n)):o}function Dt(e,t,n=[]){Oe(),nr();const r=q(e)[t].apply(e,n);return rr(),Me(),r}const mi=Jn("__proto__,__v_isRef,__isVue"),ho=new Set(Object.getOwnPropertyNames(Symbol).filter(e=>e!=="arguments"&&e!=="caller").map(e=>Symbol[e]).filter(Ce));function yi(e){Ce(e)||(e=String(e));const t=q(this);return de(t,"has",e),t.hasOwnProperty(e)}class mo{constructor(t=!1,n=!1){this._isReadonly=t,this._isShallow=n}get(t,n,r){if(n==="__v_skip")return t.__v_skip;const o=this._isReadonly,s=this._isShallow;if(n==="__v_isReactive")return!o;if(n==="__v_isReadonly")return o;if(n==="__v_isShallow")return s;if(n==="__v_raw")return r===(o?s?wo:xo:s?bo:vo).get(t)||Object.getPrototypeOf(t)===Object.getPrototypeOf(r)?t:void 0;const l=M(t);if(!o){let a;if(l&&(a=pi[n]))return a;if(n==="hasOwnProperty")return yi}const i=Reflect.get(t,n,ae(t)?t:r);if((Ce(n)?ho.has(n):mi(n))||(o||de(t,"get",n),s))return i;if(ae(i)){const a=l&&Qn(n)?i:i.value;return o&&K(a)?fr(a):a}return K(i)?o?fr(i):wt(i):i}}class yo extends mo{constructor(t=!1){super(!1,t)}set(t,n,r,o){let s=t[n];const l=M(t)&&Qn(n);if(!this._isShallow){const d=Ge(s);if(!Se(r)&&!Ge(r)&&(s=q(s),r=q(r)),!l&&ae(s)&&!ae(r))return d||(s.value=r),!0}const i=l?Number(n)<t.length:B(t,n),a=Reflect.set(t,n,r,ae(t)?t:o);return t===q(o)&&a&&(i?Ie(r,s)&&Ke(t,"set",n,r):Ke(t,"add",n,r)),a}deleteProperty(t,n){const r=B(t,n);t[n];const o=Reflect.deleteProperty(t,n);return o&&r&&Ke(t,"delete",n,void 0),o}has(t,n){const r=Reflect.has(t,n);return(!Ce(n)||!ho.has(n))&&de(t,"has",n),r}ownKeys(t){return de(t,"iterate",M(t)?"length":st),Reflect.ownKeys(t)}}class go extends mo{constructor(t=!1){super(!0,t)}set(t,n){return!0}deleteProperty(t,n){return!0}}const gi=new yo,vi=new go,bi=new yo(!0),xi=new go(!0),dr=e=>e,pn=e=>Reflect.getPrototypeOf(e);function wi(e,t,n){return function(...r){const o=this.__v_raw,s=q(o),l=bt(s),i=e==="entries"||e===Symbol.iterator&&l,a=e==="keys"&&l,d=o[e](...r),u=n?dr:t?_t:Ee;return!t&&de(s,"iterate",a?ar:st),ie(Object.create(d),{next(){const{value:h,done:_}=d.next();return _?{value:h,done:_}:{value:i?[u(h[0]),u(h[1])]:u(h),done:_}}})}}function hn(e){return function(...t){return e==="delete"?!1:e==="clear"?void 0:this}}function _i(e,t){const n={get(o){const s=this.__v_raw,l=q(s),i=q(o);e||(Ie(o,i)&&de(l,"get",o),de(l,"get",i));const{has:a}=pn(l),d=t?dr:e?_t:Ee;if(a.call(l,o))return d(s.get(o));if(a.call(l,i))return d(s.get(i));s!==l&&s.get(o)},get size(){const o=this.__v_raw;return!e&&de(q(o),"iterate",st),o.size},has(o){const s=this.__v_raw,l=q(s),i=q(o);return e||(Ie(o,i)&&de(l,"has",o),de(l,"has",i)),o===i?s.has(o):s.has(o)||s.has(i)},forEach(o,s){const l=this,i=l.__v_raw,a=q(i),d=t?dr:e?_t:Ee;return!e&&de(a,"iterate",st),i.forEach((u,h)=>o.call(s,d(u),d(h),l))}};return ie(n,e?{add:hn("add"),set:hn("set"),delete:hn("delete"),clear:hn("clear")}:{add(o){const s=q(this),l=pn(s),i=q(o),a=!t&&!Se(o)&&!Ge(o)?i:o;return l.has.call(s,a)||Ie(o,a)&&l.has.call(s,o)||Ie(i,a)&&l.has.call(s,i)||(s.add(a),Ke(s,"add",a,a)),this},set(o,s){!t&&!Se(s)&&!Ge(s)&&(s=q(s));const l=q(this),{has:i,get:a}=pn(l);let d=i.call(l,o);d||(o=q(o),d=i.call(l,o));const u=a.call(l,o);return l.set(o,s),d?Ie(s,u)&&Ke(l,"set",o,s):Ke(l,"add",o,s),this},delete(o){const s=q(this),{has:l,get:i}=pn(s);let a=l.call(s,o);a||(o=q(o),a=l.call(s,o)),i&&i.call(s,o);const d=s.delete(o);return a&&Ke(s,"delete",o,void 0),d},clear(){const o=q(this),s=o.size!==0,l=o.clear();return s&&Ke(o,"clear",void 0,void 0),l}}),["keys","values","entries",Symbol.iterator].forEach(o=>{n[o]=wi(o,e,t)}),n}function mn(e,t){const n=_i(e,t);return(r,o,s)=>o==="__v_isReactive"?!e:o==="__v_isReadonly"?e:o==="__v_raw"?r:Reflect.get(B(n,o)&&o in r?n:r,o,s)}const Si={get:mn(!1,!1)},ki={get:mn(!1,!0)},Ci={get:mn(!0,!1)},Ri={get:mn(!0,!0)},vo=new WeakMap,bo=new WeakMap,xo=new WeakMap,wo=new WeakMap;function Ti(e){switch(e){case"Object":case"Array":return 1;case"Map":case"Set":case"WeakMap":case"WeakSet":return 2;default:return 0}}function wt(e){return Ge(e)?e:yn(e,!1,gi,Si,vo)}function Ei(e){return yn(e,!1,bi,ki,bo)}function fr(e){return yn(e,!0,vi,Ci,xo)}function rd(e){return yn(e,!0,xi,Ri,wo)}function yn(e,t,n,r,o){if(!K(e)||e.__v_raw&&!(t&&e.__v_isReactive)||e.__v_skip||!Object.isExtensible(e))return e;const s=o.get(e);if(s)return s;const l=Ti(ei(e));if(l===0)return e;const i=new Proxy(e,l===2?r:n);return o.set(e,i),i}function it(e){return Ge(e)?it(e.__v_raw):!!(e&&e.__v_isReactive)}function Ge(e){return!!(e&&e.__v_isReadonly)}function Se(e){return!!(e&&e.__v_isShallow)}function pr(e){return e?!!e.__v_raw:!1}function q(e){const t=e&&e.__v_raw;return t?q(t):e}function $i(e){return!B(e,"__v_skip")&&Object.isExtensible(e)&&Qr(e,"__v_skip",!0),e}const Ee=e=>K(e)?wt(e):e,_t=e=>K(e)?fr(e):e;function ae(e){return e?e.__v_isRef===!0:!1}function Je(e){return Ai(e,!1)}function Ai(e,t){return ae(e)?e:new Ii(e,t)}class Ii{constructor(t,n){this.dep=new ir,this.__v_isRef=!0,this.__v_isShallow=!1,this._rawValue=n?t:q(t),this._value=n?t:Ee(t),this.__v_isShallow=n}get value(){return this.dep.track(),this._value}set value(t){const n=this._rawValue,r=this.__v_isShallow||Se(t)||Ge(t);t=r?t:q(t),Ie(t,n)&&(this._rawValue=t,this._value=r?t:Ee(t),this.dep.trigger())}}function ge(e){return ae(e)?e.value:e}const Pi={get:(e,t,n)=>t==="__v_raw"?e:ge(Reflect.get(e,t,n)),set:(e,t,n,r)=>{const o=e[t];return ae(o)&&!ae(n)?(o.value=n,!0):Reflect.set(e,t,n,r)}};function _o(e){return it(e)?e:new Proxy(e,Pi)}class Oi{constructor(t,n,r){this.fn=t,this.setter=n,this._value=void 0,this.dep=new ir(this),this.__v_isRef=!0,this.deps=void 0,this.depsTail=void 0,this.flags=16,this.globalVersion=Nt-1,this.next=void 0,this.effect=this,this.__v_isReadonly=!n,this.isSSR=r}notify(){if(this.flags|=16,!(this.flags&8)&&Y!==this)return so(this,!0),!0}get value(){const t=this.dep.track();return ao(this),t&&(t.version=this.dep.version),this._value}set value(t){this.setter&&this.setter(t)}}function Mi(e,t,n=!1){let r,o;return F(e)?r=e:(r=e.get,o=e.set),new Oi(r,o,n)}const gn={},vn=new WeakMap;let lt;function Ni(e,t=!1,n=lt){if(n){let r=vn.get(n);r||vn.set(n,r=[]),r.push(e)}}function Li(e,t,n=G){const{immediate:r,deep:o,once:s,scheduler:l,augmentJob:i,call:a}=n,d=S=>o?S:Se(S)||o===!1||o===0?Ye(S,1):Ye(S);let u,h,_,k,$=!1,y=!1;if(ae(e)?(h=()=>e.value,$=Se(e)):it(e)?(h=()=>d(e),$=!0):M(e)?(y=!0,$=e.some(S=>it(S)||Se(S)),h=()=>e.map(S=>{if(ae(S))return S.value;if(it(S))return d(S);if(F(S))return a?a(S,2):S()})):F(e)?t?h=a?()=>a(e,2):e:h=()=>{if(_){Oe();try{_()}finally{Me()}}const S=lt;lt=u;try{return a?a(e,3,[k]):e(k)}finally{lt=S}}:h=Ae,t&&o){const S=h,x=o===!0?1/0:o;h=()=>Ye(S(),x)}const j=ui(),Q=()=>{u.stop(),j&&j.active&&Yn(j.effects,u)};if(s&&t){const S=t;t=(...x)=>{const L=S(...x);return Q(),L}}let z=y?new Array(e.length).fill(gn):gn;const H=S=>{if(!(!(u.flags&1)||!u.dirty&&!S))if(t){const x=u.run();if(S||o||$||(y?x.some((L,ze)=>Ie(L,z[ze])):Ie(x,z))){_&&_();const L=lt;lt=u;try{const ze=[x,z===gn?void 0:y&&z[0]===gn?[]:z,k];z=x,a?a(t,3,ze):t(...ze)}finally{lt=L}}}else u.run()};return i&&i(H),u=new ro(h),u.scheduler=l?()=>l(H,!1):H,k=S=>Ni(S,!1,u),_=u.onStop=()=>{const S=vn.get(u);if(S){if(a)a(S,4);else for(const x of S)x();vn.delete(u)}},t?r?H(!0):z=u.run():l?l(H.bind(null,!0),!0):u.run(),Q.pause=u.pause.bind(u),Q.resume=u.resume.bind(u),Q.stop=Q,Q}function Ye(e,t=1/0,n){if(t<=0||!K(e)||e.__v_skip||(n=n||new Map,(n.get(e)||0)>=t))return e;if(n.set(e,t),t--,ae(e))Ye(e.value,t,n);else if(M(e))for(let r=0;r<e.length;r++)Ye(e[r],t,n);else if(ln(e)||bt(e))e.forEach(r=>{Ye(r,t,n)});else if(Jr(e)){for(const r in e)Ye(e[r],t,n);for(const r of Object.getOwnPropertySymbols(e))Object.prototype.propertyIsEnumerable.call(e,r)&&Ye(e[r],t,n)}return e}/**
* @vue/runtime-core v3.5.41
* (c) 2018-present Yuxi (Evan) You and Vue contributors
* @license MIT
**/const Ft=[];let hr=!1;function od(e,...t){if(hr)return;hr=!0,Oe();const n=Ft.length?Ft[Ft.length-1].component:null,r=n&&n.appContext.config.warnHandler,o=Di();if(r)St(r,n,11,[e+t.map(s=>{var l,i;return(i=(l=s.toString)==null?void 0:l.call(s))!=null?i:JSON.stringify(s)}).join(""),n&&n.proxy,o.map(({vnode:s})=>`at <${ys(n,s.type)}>`).join(`
`),o]);else{const s=[`[Vue warn]: ${e}`,...t];o.length&&s.push(`
`,...Fi(o)),console.warn(...s)}Me(),hr=!1}function Di(){let e=Ft[Ft.length-1];if(!e)return[];const t=[];for(;e;){const n=t[0];n&&n.vnode===e?n.recurseCount++:t.push({vnode:e,recurseCount:0});const r=e.component&&e.component.parent;e=r&&r.vnode}return t}function Fi(e){const t=[];return e.forEach((n,r)=>{t.push(...r===0?[]:[`
`],...ji(n))}),t}function ji({vnode:e,recurseCount:t}){const n=t>0?`... (${t} recursive calls)`:"",r=e.component?e.component.parent==null:!1,o=` at <${ys(e.component,e.type,r)}`,s=">"+n;return e.props?[o,...Vi(e.props),s]:[o+s]}function Vi(e){const t=[],n=Object.keys(e);return n.slice(0,3).forEach(r=>{t.push(...So(r,e[r]))}),n.length>3&&t.push(" ..."),t}function So(e,t,n){return Z(t)?(t=JSON.stringify(t),n?t:[`${e}=${t}`]):typeof t=="number"||typeof t=="boolean"||t==null?n?t:[`${e}=${t}`]:ae(t)?(t=So(e,q(t.value),!0),n?t:[`${e}=Ref<`,t,">"]):F(t)?[`${e}=fn${t.name?`<${t.name}>`:""}`]:(t=q(t),n?t:[`${e}=`,t])}function St(e,t,n,r){try{return r?e(...r):e()}catch(o){bn(o,t,n)}}function $e(e,t,n,r){if(F(e)){const o=St(e,t,n,r);return o&&Wr(o)&&o.catch(s=>{bn(s,t,n)}),o}if(M(e)){const o=[];for(let s=0;s<e.length;s++)o.push($e(e[s],t,n,r));return o}}function bn(e,t,n,r=!0){const o=t?t.vnode:null,{errorHandler:s,throwUnhandledErrorInProduction:l}=t&&t.appContext.config||G;if(t){let i=t.parent;const a=t.proxy,d=`https://vuejs.org/error-reference/#runtime-${n}`;for(;i;){const u=i.ec;if(u){for(let h=0;h<u.length;h++)if(u[h](e,a,d)===!1)return}i=i.parent}if(s){Oe(),St(s,null,10,[e,a,d]),Me();return}}zi(e,n,o,r,l)}function zi(e,t,n,r=!0,o=!1){if(o)throw e;console.error(e)}const pe=[];let Le=-1;const kt=[];let tt=null,Ct=0;const ko=Promise.resolve();let xn=null;function jt(e){const t=xn||ko;return e?t.then(this?e.bind(this):e):t}function Hi(e){let t=Le+1,n=pe.length;for(;t<n;){const r=t+n>>>1,o=pe[r],s=Vt(o);s<e||s===e&&o.flags&2?t=r+1:n=r}return t}function mr(e){if(!(e.flags&1)){const t=Vt(e),n=pe[pe.length-1];!n||!(e.flags&2)&&t>=Vt(n)?pe.push(e):pe.splice(Hi(t),0,e),e.flags|=1,Co()}}function Co(){xn||(xn=ko.then(Eo))}function Ui(e){if(!M(e))tt&&e.id===-1?tt.splice(Ct+1,0,e):e.flags&1||(kt.push(e),e.flags|=1);else for(let t=0;t<e.length;t++)kt.push(e[t]);Co()}function Ro(e,t,n=Le+1){for(;n<pe.length;n++){const r=pe[n];if(r&&r.flags&2){if(e&&r.id!==e.uid)continue;pe.splice(n,1),n--,r.flags&4&&(r.flags&=-2),r(),r.flags&4||(r.flags&=-2)}}}function To(e){if(kt.length){const t=[...new Set(kt)].sort((n,r)=>Vt(n)-Vt(r));if(kt.length=0,tt){for(let n=0;n<t.length;n++)tt.push(t[n]);return}for(tt=t,Ct=0;Ct<tt.length;Ct++){const n=tt[Ct];n.flags&4&&(n.flags&=-2),n.flags&8||n(),n.flags&=-2}tt=null,Ct=0}}const Vt=e=>e.id==null?e.flags&2?-1:1/0:e.id;function Eo(e){try{for(Le=0;Le<pe.length;Le++){const t=pe[Le];t&&!(t.flags&8)&&(t.flags&4&&(t.flags&=-2),St(t,t.i,t.i?15:14),t.flags&4||(t.flags&=-2))}}finally{for(;Le<pe.length;Le++){const t=pe[Le];t&&(t.flags&=-2)}Le=-1,pe.length=0,To(),xn=null,(pe.length||kt.length)&&Eo()}}let fe=null,$o=null;function wn(e){const t=fe;return fe=e,$o=e&&e.type.__scopeId||null,t}function te(e,t=fe,n){if(!t||e._n)return e;const r=(...o)=>{r._d&&In(-1);const s=wn(t),l=Xe.length;let i;try{i=e(...o)}finally{for(let a=Xe.length;a>l;a--)Er();wn(s),r._d&&In(1)}return i};return r._n=!0,r._c=!0,r._d=!0,r}function Rt(e,t){if(fe===null)return e;const n=Nn(fe),r=e.dirs||(e.dirs=[]);for(let o=0;o<t.length;o++){let[s,l,i,a=G]=t[o];s&&(F(s)&&(s={mounted:s,updated:s}),s.deep&&Ye(l),r.push({dir:s,instance:n,value:l,oldValue:void 0,arg:i,modifiers:a}))}return e}function at(e,t,n,r){const o=e.dirs,s=t&&t.dirs;for(let l=0;l<o.length;l++){const i=o[l];s&&(i.oldValue=s[l].value);let a=i.dir[r];a&&(Oe(),$e(a,n,8,[e.el,i,e,t]),Me())}}function qi(e,t){if(me){let n=me.provides;const r=me.parent&&me.parent.provides;r===n&&(n=me.provides=Object.create(r)),n[e]=t}}function _n(e,t,n=!1){const r=Vl();if(r||Et){let o=Et?Et._context.provides:r?r.parent==null||r.ce?r.vnode.appContext&&r.vnode.appContext.provides:r.parent.provides:void 0;if(o&&e in o)return o[e];if(arguments.length>1)return n&&F(t)?t.call(r&&r.proxy):t}}const Bi=Symbol.for("v-scx"),Ki=()=>_n(Bi);function nt(e,t,n){return Ao(e,t,n)}function Ao(e,t,n=G){const{immediate:r,deep:o,flush:s,once:l}=n,i=ie({},n),a=t&&r||!t&&s!=="post";let d;if(Yt){if(s==="sync"){const k=Ki();d=k.__watcherHandles||(k.__watcherHandles=[])}else if(!a){const k=()=>{};return k.stop=Ae,k.resume=Ae,k.pause=Ae,k}}const u=me;i.call=(k,$,y)=>$e(k,u,$,y);let h=!1;s==="post"?i.scheduler=k=>{ve(k,u&&u.suspense)}:s!=="sync"&&(h=!0,i.scheduler=(k,$)=>{$?k():mr(k)}),i.augmentJob=k=>{t&&(k.flags|=4),h&&(k.flags|=2,u&&(k.id=u.uid,k.i=u))};const _=Li(e,t,i);return Yt&&(d?d.push(_):a&&_()),_}function Wi(e,t,n){const r=this.proxy,o=Z(e)?e.includes(".")?Io(r,e):()=>r[e]:e.bind(r,r);let s;F(t)?s=t:(s=t.handler,n=t);const l=Jt(this),i=Ao(o,s.bind(r),n);return l(),i}function Io(e,t){const n=t.split(".");return()=>{let r=e;for(let o=0;o<n.length&&r;o++)r=r[n[o]];return r}}const Gi=Symbol("_vte"),Sn=e=>e.__isTeleport,yr=Symbol("_leaveCb");function Ji(e){let t=e[0];if(e.length>1){for(const n of e)if(n.type!==De){t=n;break}}return t}function Po(e){if(!vr(e))return Sn(e.type)&&e.children?Ji(e.children):e;if(e.component)return e.component.subTree;const{shapeFlag:t,children:n}=e;if(n){if(t&16)return n[0];if(t&32&&F(n.default))return n.default()}}function gr(e,t){if(e.shapeFlag&6&&e.component){e.transition=t;const n=e.component.subTree;gr(Sn(n.type)&&Po(n)||n,t)}else e.shapeFlag&128?(e.ssContent.transition=t.clone(e.ssContent),e.ssFallback.transition=t.clone(e.ssFallback)):e.transition=t}function ct(e,t){return F(e)?ie({name:e.name},t,{setup:e}):e}function Oo(e){e.ids=[e.ids[0]+e.ids[2]+++"-",0,0]}function Mo(e,t){let n;return!!((n=Object.getOwnPropertyDescriptor(e,t))&&!n.configurable)}const kn=new WeakMap;function zt(e,t,n,r,o=!1){if(M(e)){e.forEach((y,j)=>zt(y,t&&(M(t)?t[j]:t),n,r,o));return}if(Tt(r)&&!o){r.shapeFlag&512&&r.type.__asyncResolved&&r.component.subTree.component&&zt(e,t,n,r.component.subTree);return}const s=r.shapeFlag&4?Nn(r.component):r.el,l=o?null:s,{i,r:a}=e,d=t&&t.r,u=i.refs===G?i.refs={}:i.refs,h=i.setupState,_=q(h),k=h===G?Br:y=>Mo(u,y)?!1:B(_,y),$=(y,j)=>!(j&&Mo(u,j));if(d!=null&&d!==a){if(No(t),Z(d))u[d]=null,k(d)&&(h[d]=null);else if(ae(d)){const y=t;$(d,y.k)&&(d.value=null),y.k&&(u[y.k]=null)}}if(F(a))St(a,i,12,[l,u]);else{const y=Z(a),j=ae(a);if(y||j){const Q=()=>{if(e.f){const z=y?k(a)?h[a]:u[a]:$()||!e.k?a.value:u[e.k];if(o)M(z)&&Yn(z,s);else if(M(z))z.includes(s)||z.push(s);else if(y)u[a]=[s],k(a)&&(h[a]=u[a]);else{const H=[s];$(a,e.k)&&(a.value=H),e.k&&(u[e.k]=H)}}else y?(u[a]=l,k(a)&&(h[a]=l)):j&&($(a,e.k)&&(a.value=l),e.k&&(u[e.k]=l))};if(l){const z=()=>{Q(),kn.delete(e)};z.id=-1,kn.set(e,z),ve(z,n)}else No(e),Q()}}}function No(e){const t=kn.get(e);t&&(t.flags|=8,kn.delete(e))}un().requestIdleCallback,un().cancelIdleCallback;const Tt=e=>!!e.type.__asyncLoader,vr=e=>e.type.__isKeepAlive;function Yi(e,t){Lo(e,"a",t)}function Qi(e,t){Lo(e,"da",t)}function Lo(e,t,n=me){const r=e.__wdc||(e.__wdc=()=>{let o=n;for(;o;){if(o.isDeactivated)return;o=o.parent}return e()});if(Cn(t,r,n),n){let o=n.parent;for(;o&&o.parent;)vr(o.parent.vnode)&&Xi(r,t,n,o),o=o.parent}}function Xi(e,t,n,r){const o=Cn(t,e,r,!0);Do(()=>{Yn(r[t],o)},n)}function Cn(e,t,n=me,r=!1){if(n){const o=n[e]||(n[e]=[]),s=t.__weh||(t.__weh=(...l)=>{Oe();const i=Jt(n),a=$e(t,n,e,l);return i(),Me(),a});return r?o.unshift(s):o.push(s),s}}const Qe=e=>(t,n=me)=>{(!Yt||e==="sp")&&Cn(e,(...r)=>t(...r),n)},Zi=Qe("bm"),Rn=Qe("m"),el=Qe("bu"),tl=Qe("u"),Tn=Qe("bum"),Do=Qe("um"),nl=Qe("sp"),rl=Qe("rtg"),ol=Qe("rtc");function sl(e,t=me){Cn("ec",e,t)}const il=Symbol.for("v-ndc");function Ht(e,t,n,r){let o;const s=n,l=M(e);if(l||Z(e)){const i=l&&it(e);let a=!1,d=!1;i&&(a=!Se(e),d=Ge(e),e=fn(e)),o=new Array(e.length);for(let u=0,h=e.length;u<h;u++)o[u]=t(a?d?_t(Ee(e[u])):Ee(e[u]):e[u],u,void 0,s)}else if(typeof e=="number"){o=new Array(e);for(let i=0;i<e;i++)o[i]=t(i+1,i,void 0,s)}else if(K(e))if(e[Symbol.iterator])o=Array.from(e,(i,a)=>t(i,a,void 0,s));else{const i=Object.keys(e);o=new Array(i.length);for(let a=0,d=i.length;a<d;a++){const u=i[a];o[a]=t(e[u],u,a,s)}}else o=[];return o}function ll(e,t,n,r,o,s){if(n==null&&(n={}),fe.ce||fe.parent&&Tt(fe.parent)&&fe.parent.ce){const d=n,u=Object.keys(d).length>0;return t!=="default"&&(d.name=t),A(),dt(ne,null,[V("slot",d,r&&r())],u?-2:64)}let l=e[t];l&&l._c&&(l._d=!1);const i=Xe.length;A();let a;try{const d=l&&Fo(l(n)),u=n.key||s||d&&d.key;a=dt(ne,{key:(u&&!Ce(u)?u:`_${t}`)+(!d&&r?"_fb":"")},d||(r?r():[]),d&&e._===1?64:-2)}catch(d){for(let u=Xe.length;u>i;u--)Er();throw d}finally{l&&l._c&&(l._d=!0)}return a.scopeId&&(a.slotScopeIds=[a.scopeId+"-s"]),a}function Fo(e){return e.some(t=>Kt(t)?!(t.type===De||t.type===ne&&!Fo(t.children)):!0)?e:null}const br=e=>e?ps(e)?Nn(e):br(e.parent):null,Ut=ie(Object.create(null),{$:e=>e,$el:e=>e.vnode.el,$data:e=>e.data,$props:e=>e.props,$attrs:e=>e.attrs,$slots:e=>e.slots,$refs:e=>e.refs,$parent:e=>br(e.parent),$root:e=>br(e.root),$host:e=>e.ce,$emit:e=>e.emit,$options:e=>Ho(e),$forceUpdate:e=>e.f||(e.f=()=>{mr(e.update)}),$nextTick:e=>e.n||(e.n=jt.bind(e.proxy)),$watch:e=>Wi.bind(e)}),xr=(e,t)=>e!==G&&!e.__isScriptSetup&&B(e,t),al={get({_:e},t){if(t==="__v_skip")return!0;const{ctx:n,setupState:r,data:o,props:s,accessCache:l,type:i,appContext:a}=e;if(t[0]!=="$"){const _=l[t];if(_!==void 0)switch(_){case 1:return r[t];case 2:return o[t];case 4:return n[t];case 3:return s[t]}else{if(xr(r,t))return l[t]=1,r[t];if(o!==G&&B(o,t))return l[t]=2,o[t];if(B(s,t))return l[t]=3,s[t];if(n!==G&&B(n,t))return l[t]=4,n[t];wr&&(l[t]=0)}}const d=Ut[t];let u,h;if(d)return t==="$attrs"&&de(e.attrs,"get",""),d(e);if((u=i.__cssModules)&&(u=u[t]))return u;if(n!==G&&B(n,t))return l[t]=4,n[t];if(h=a.config.globalProperties,B(h,t))return h[t]},set({_:e},t,n){const{data:r,setupState:o,ctx:s}=e;return xr(o,t)?(o[t]=n,!0):r!==G&&B(r,t)?(r[t]=n,!0):B(e.props,t)||t[0]==="$"&&t.slice(1)in e?!1:(s[t]=n,!0)},has({_:{data:e,setupState:t,accessCache:n,ctx:r,appContext:o,props:s,type:l}},i){let a;return!!(n[i]||e!==G&&i[0]!=="$"&&B(e,i)||xr(t,i)||B(s,i)||B(r,i)||B(Ut,i)||B(o.config.globalProperties,i)||(a=l.__cssModules)&&a[i])},defineProperty(e,t,n){return n.get!=null?e._.accessCache[t]=0:B(n,"value")&&this.set(e,t,n.value,null),Reflect.defineProperty(e,t,n)}};function jo(e){return M(e)?e.reduce((t,n)=>(t[n]=null,t),{}):e}let wr=!0;function cl(e){const t=Ho(e),n=e.proxy,r=e.ctx;wr=!1,t.beforeCreate&&Vo(t.beforeCreate,e,"bc");const{data:o,computed:s,methods:l,watch:i,provide:a,inject:d,created:u,beforeMount:h,mounted:_,beforeUpdate:k,updated:$,activated:y,deactivated:j,beforeDestroy:Q,beforeUnmount:z,destroyed:H,unmounted:S,render:x,renderTracked:L,renderTriggered:ze,errorCaptured:rt,serverPrefetch:qn,expose:mt,inheritAttrs:en,components:Bn,directives:Kn,filters:Ur}=t;if(d&&ul(d,r,null),l)for(const ee in l){const X=l[ee];F(X)&&(r[ee]=X.bind(n))}if(o){const ee=o.call(n,n);K(ee)&&(e.data=wt(ee))}if(wr=!0,s)for(const ee in s){const X=s[ee],yt=F(X)?X.bind(n,n):F(X.get)?X.get.bind(n,n):Ae,Wn=!F(X)&&F(X.set)?X.set.bind(n):Ae,gt=se({get:yt,set:Wn});Object.defineProperty(r,ee,{enumerable:!0,configurable:!0,get:()=>gt.value,set:He=>gt.value=He})}if(i)for(const ee in i)zo(i[ee],r,n,ee);if(a){const ee=F(a)?a.call(n):a;Reflect.ownKeys(ee).forEach(X=>{qi(X,ee[X])})}u&&Vo(u,e,"c");function ye(ee,X){M(X)?X.forEach(yt=>ee(yt.bind(n))):X&&ee(X.bind(n))}if(ye(Zi,h),ye(Rn,_),ye(el,k),ye(tl,$),ye(Yi,y),ye(Qi,j),ye(sl,rt),ye(ol,L),ye(rl,ze),ye(Tn,z),ye(Do,S),ye(nl,qn),M(mt))if(mt.length){const ee=e.exposed||(e.exposed={});mt.forEach(X=>{Object.defineProperty(ee,X,{get:()=>n[X],set:yt=>n[X]=yt,enumerable:!0})})}else e.exposed||(e.exposed={});x&&e.render===Ae&&(e.render=x),en!=null&&(e.inheritAttrs=en),Bn&&(e.components=Bn),Kn&&(e.directives=Kn),qn&&Oo(e)}function ul(e,t,n=Ae){M(e)&&(e=_r(e));for(const r in e){const o=e[r];let s;K(o)?"default"in o?s=_n(o.from||r,o.default,!0):s=_n(o.from||r):s=_n(o),ae(s)?Object.defineProperty(t,r,{enumerable:!0,configurable:!0,get:()=>s.value,set:l=>s.value=l}):t[r]=s}}function Vo(e,t,n){$e(M(e)?e.map(r=>r.bind(t.proxy)):e.bind(t.proxy),t,n)}function zo(e,t,n,r){let o=r.includes(".")?Io(n,r):()=>n[r];if(Z(e)){const s=t[e];F(s)&&nt(o,s)}else if(F(e))nt(o,e.bind(n));else if(K(e))if(M(e))e.forEach(s=>zo(s,t,n,r));else{const s=F(e.handler)?e.handler.bind(n):t[e.handler];F(s)&&nt(o,s,e)}}function Ho(e){const t=e.type,{mixins:n,extends:r}=t,{mixins:o,optionsCache:s,config:{optionMergeStrategies:l}}=e.appContext,i=s.get(t);let a;return i?a=i:!o.length&&!n&&!r?a=t:(a={},o.length&&o.forEach(d=>En(a,d,l,!0)),En(a,t,l)),K(t)&&s.set(t,a),a}function En(e,t,n,r=!1){const{mixins:o,extends:s}=t;s&&En(e,s,n,!0),o&&o.forEach(l=>En(e,l,n,!0));for(const l in t)if(!(r&&l==="expose")){const i=dl[l]||n&&n[l];e[l]=i?i(e[l],t[l]):t[l]}return e}const dl={data:Uo,props:qo,emits:qo,methods:qt,computed:qt,beforeCreate:he,created:he,beforeMount:he,mounted:he,beforeUpdate:he,updated:he,beforeDestroy:he,beforeUnmount:he,destroyed:he,unmounted:he,activated:he,deactivated:he,errorCaptured:he,serverPrefetch:he,components:qt,directives:qt,watch:pl,provide:Uo,inject:fl};function Uo(e,t){return t?e?function(){return ie(F(e)?e.call(this,this):e,F(t)?t.call(this,this):t)}:t:e}function fl(e,t){return qt(_r(e),_r(t))}function _r(e){if(M(e)){const t={};for(let n=0;n<e.length;n++)t[e[n]]=e[n];return t}return e}function he(e,t){return e?[...new Set([].concat(e,t))]:t}function qt(e,t){return e?ie(Object.create(null),e,t):t}function qo(e,t){return e?M(e)&&M(t)?[...new Set([...e,...t])]:ie(Object.create(null),jo(e),jo(t??{})):t}function pl(e,t){if(!e)return t;if(!t)return e;const n=ie(Object.create(null),e);for(const r in t)n[r]=he(e[r],t[r]);return n}function Bo(){return{app:null,config:{isNativeTag:Br,performance:!1,globalProperties:{},optionMergeStrategies:{},errorHandler:void 0,warnHandler:void 0,compilerOptions:{}},mixins:[],components:{},directives:{},provides:Object.create(null),optionsCache:new WeakMap,propsCache:new WeakMap,emitsCache:new WeakMap}}let hl=0;function ml(e,t){return function(r,o=null){F(r)||(r=ie({},r)),o!=null&&!K(o)&&(o=null);const s=Bo(),l=new WeakSet,i=[];let a=!1;const d=s.app={_uid:hl++,_component:r,_props:o,_container:null,_context:s,_instance:null,version:Jl,get config(){return s.config},set config(u){},use(u,...h){return l.has(u)||(u&&F(u.install)?(l.add(u),u.install(d,...h)):F(u)&&(l.add(u),u(d,...h))),d},mixin(u){return s.mixins.includes(u)||s.mixins.push(u),d},component(u,h){return h?(s.components[u]=h,d):s.components[u]},directive(u,h){return h?(s.directives[u]=h,d):s.directives[u]},mount(u,h,_){if(!a){const k=d._ceVNode||V(r,o);return k.appContext=s,_===!0?_="svg":_===!1&&(_=void 0),e(k,u,_),a=!0,d._container=u,u.__vue_app__=d,Nn(k.component)}},onUnmount(u){i.push(u)},unmount(){a&&($e(i,d._instance,16),e(null,d._container),delete d._container.__vue_app__)},provide(u,h){return s.provides[u]=h,d},runWithContext(u){const h=Et;Et=d;try{return u()}finally{Et=h}}};return d}}let Et=null;const yl=(e,t)=>t==="modelValue"||t==="model-value"?e.modelModifiers:e[`${t}Modifiers`]||e[`${Re(t)}Modifiers`]||e[`${ot(t)}Modifiers`];function gl(e,t,...n){if(e.isUnmounted)return;const r=e.vnode.props||G;let o=n;const s=t.startsWith("update:"),l=s&&yl(r,t.slice(7));l&&(l.trim&&(o=n.map(u=>Z(u)?u.trim():u)),l.number&&(o=n.map(Zn)));let i,a=r[i=Xn(t)]||r[i=Xn(Re(t))];!a&&s&&(a=r[i=Xn(ot(t))]),a&&$e(a,e,6,o);const d=r[i+"Once"];if(d){if(!e.emitted)e.emitted={};else if(e.emitted[i])return;e.emitted[i]=!0,$e(d,e,6,o)}}const vl=new WeakMap;function Ko(e,t,n=!1){const r=n?vl:t.emitsCache,o=r.get(e);if(o!==void 0)return o;const s=e.emits;let l={},i=!1;if(!F(e)){const a=d=>{const u=Ko(d,t,!0);u&&(i=!0,ie(l,u))};!n&&t.mixins.length&&t.mixins.forEach(a),e.extends&&a(e.extends),e.mixins&&e.mixins.forEach(a)}return!s&&!i?(K(e)&&r.set(e,null),null):(M(s)?s.forEach(a=>l[a]=null):ie(l,s),K(e)&&r.set(e,l),l)}function $n(e,t){return!e||!on(t)?!1:(t=t.slice(2),t=t==="Once"?t:t.replace(/Once$/,""),B(e,t[0].toLowerCase()+t.slice(1))||B(e,ot(t))||B(e,t))}function sd(){}function Wo(e){const{type:t,vnode:n,proxy:r,withProxy:o,propsOptions:[s],slots:l,attrs:i,emit:a,render:d,renderCache:u,props:h,data:_,setupState:k,ctx:$,inheritAttrs:y}=e,j=wn(e);let Q,z;try{if(n.shapeFlag&4){const S=o||r,x=S;Q=je(d.call(x,S,u,h,k,_,$)),z=i}else{const S=t;Q=je(S.length>1?S(h,{attrs:i,slots:l,emit:a}):S(h,null)),z=t.props?i:bl(i)}}catch(S){Xe.length=0,bn(S,e,1),Q=V(De)}let H=Q;if(z&&y!==!1){const S=Object.keys(z),{shapeFlag:x}=H;S.length&&x&7&&(s&&S.some(sn)&&(z=xl(z,s)),H=$t(H,z,!1,!0))}if(n.dirs&&(H=$t(H,null,!1,!0),H.dirs=H.dirs?H.dirs.concat(n.dirs):n.dirs),n.transition){const S=Sn(H.type)&&Po(H)||H;gr(S,n.transition)}return Q=H,wn(j),Q}const bl=e=>{let t;for(const n in e)(n==="class"||n==="style"||on(n))&&((t||(t={}))[n]=e[n]);return t},xl=(e,t)=>{const n={};for(const r in e)(!sn(r)||!(r.slice(9)in t))&&(n[r]=e[r]);return n};function wl(e,t,n){const{props:r,children:o,component:s}=e,{props:l,children:i,patchFlag:a}=t,d=s.emitsOptions;if(t.dirs||t.transition)return!0;if(n&&a>=0){if(a&1024)return!0;if(a&16)return r?Go(r,l,d):!!l;if(a&8){const u=t.dynamicProps;for(let h=0;h<u.length;h++){const _=u[h];if(Jo(l,r,_)&&!$n(d,_))return!0}}}else return(o||i)&&(!i||!i.$stable)?!0:r===l?!1:r?l?Go(r,l,d):!0:!!l;return!1}function Go(e,t,n){const r=Object.keys(t);if(r.length!==Object.keys(e).length)return!0;for(let o=0;o<r.length;o++){const s=r[o];if(Jo(t,e,s)&&!$n(n,s))return!0}return!1}function Jo(e,t,n){const r=e[n],o=t[n];return n==="style"&&K(r)&&K(o)?!Pt(r,o):r!==o}function _l({vnode:e,parent:t,suspense:n},r){for(;t;){const o=t.subTree;if(o.suspense&&o.suspense.activeBranch===e&&(o.suspense.vnode.el=o.el=r,e=o),o===e)(e=t.vnode).el=r,t=t.parent;else break}n&&n.activeBranch===e&&(n.vnode.el=r)}const Yo={},Qo=()=>Object.create(Yo),Xo=e=>Object.getPrototypeOf(e)===Yo;function Sl(e,t,n,r=!1){const o={},s=Qo();e.propsDefaults=Object.create(null),Zo(e,t,o,s);for(const l in e.propsOptions[0])l in o||(o[l]=void 0);n?e.props=r?o:Ei(o):e.type.props?e.props=o:e.props=s,e.attrs=s}function kl(e,t,n,r){const{props:o,attrs:s,vnode:{patchFlag:l}}=e,i=q(o),[a]=e.propsOptions;let d=!1;if((r||l>0)&&!(l&16)){if(l&8){const u=e.vnode.dynamicProps;for(let h=0;h<u.length;h++){let _=u[h];if($n(e.emitsOptions,_))continue;const k=t[_];if(a)if(B(s,_))k!==s[_]&&(s[_]=k,d=!0);else{const $=Re(_);o[$]=Sr(a,i,$,k,e,!1)}else k!==s[_]&&(s[_]=k,d=!0)}}}else{Zo(e,t,o,s)&&(d=!0);let u;for(const h in i)(!t||!B(t,h)&&((u=ot(h))===h||!B(t,u)))&&(a?n&&(n[h]!==void 0||n[u]!==void 0)&&(o[h]=Sr(a,i,h,void 0,e,!0)):delete o[h]);if(s!==i)for(const h in s)(!t||!B(t,h))&&(delete s[h],d=!0)}d&&Ke(e.attrs,"set","")}function Zo(e,t,n,r){const[o,s]=e.propsOptions;let l=!1,i;if(t)for(let a in t){if(It(a))continue;const d=t[a];let u;o&&B(o,u=Re(a))?!s||!s.includes(u)?n[u]=d:(i||(i={}))[u]=d:$n(e.emitsOptions,a)||(!(a in r)||d!==r[a])&&(r[a]=d,l=!0)}if(s){const a=q(n),d=i||G;for(let u=0;u<s.length;u++){const h=s[u];n[h]=Sr(o,a,h,d[h],e,!B(d,h))}}return l}function Sr(e,t,n,r,o,s){const l=e[n];if(l!=null){const i=B(l,"default");if(i&&r===void 0){const a=l.default;if(l.type!==Function&&!l.skipFactory&&F(a)){const{propsDefaults:d}=o;if(n in d)r=d[n];else{const u=Jt(o);r=d[n]=a.call(null,t),u()}}else r=a;o.ce&&o.ce._setProp(n,r)}l[0]&&(s&&!i?r=!1:l[1]&&(r===""||r===ot(n))&&(r=!0))}return r}const Cl=new WeakMap;function es(e,t,n=!1){const r=n?Cl:t.propsCache,o=r.get(e);if(o)return o;const s=e.props,l={},i=[];let a=!1;if(!F(e)){const u=h=>{a=!0;const[_,k]=es(h,t,!0);ie(l,_),k&&i.push(...k)};!n&&t.mixins.length&&t.mixins.forEach(u),e.extends&&u(e.extends),e.mixins&&e.mixins.forEach(u)}if(!s&&!a)return K(e)&&r.set(e,vt),vt;if(M(s))for(let u=0;u<s.length;u++){const h=Re(s[u]);ts(h)&&(l[h]=G)}else if(s)for(const u in s){const h=Re(u);if(ts(h)){const _=s[u],k=l[h]=M(_)||F(_)?{type:_}:ie({},_),$=k.type;let y=!1,j=!0;if(M($))for(let Q=0;Q<$.length;++Q){const z=$[Q],H=F(z)&&z.name;if(H==="Boolean"){y=!0;break}else H==="String"&&(j=!1)}else y=F($)&&$.name==="Boolean";k[0]=y,k[1]=j,(y||B(k,"default"))&&i.push(h)}}const d=[l,i];return K(e)&&r.set(e,d),d}function ts(e){return e[0]!=="$"&&!It(e)}const kr=e=>e==="_"||e==="_ctx"||e==="$stable",Cr=e=>M(e)?e.map(je):[je(e)],Rl=(e,t,n)=>{if(t._n)return t;const r=te((...o)=>Cr(t(...o)),n);return r._c=!1,r},ns=(e,t,n)=>{const r=e._ctx;for(const o in e){if(kr(o))continue;const s=e[o];if(F(s))t[o]=Rl(o,s,r);else if(s!=null){const l=Cr(s);t[o]=()=>l}}},rs=(e,t)=>{const n=Cr(t);e.slots.default=()=>n},os=(e,t,n)=>{for(const r in t)(n||!kr(r))&&(e[r]=t[r])},Tl=(e,t,n)=>{const r=e.slots=Qo();if(e.vnode.shapeFlag&32){const o=t._;o?(os(r,t,n),n&&Qr(r,"_",o,!0)):ns(t,r)}else t&&rs(e,t)},El=(e,t,n)=>{const{vnode:r,slots:o}=e;let s=!0,l=G;if(r.shapeFlag&32){const i=t._;i?n&&i===1?s=!1:os(o,t,n):(s=!t.$stable,ns(t,o)),l=t}else t&&(rs(e,t),l={default:1});if(s)for(const i in o)!kr(i)&&l[i]==null&&delete o[i]},ve=Ol;function $l(e){return Al(e)}function Al(e,t){const n=un();n.__VUE__=!0;const{insert:r,remove:o,patchProp:s,createElement:l,createText:i,createComment:a,setText:d,setElementText:u,parentNode:h,nextSibling:_,setScopeId:k=Ae,insertStaticContent:$}=e,y=(c,f,m,w=null,b=null,g=null,T=void 0,R=null,C=!!f.dynamicChildren)=>{if(c===f)return;c&&!Wt(c,f)&&(w=Gn(c),He(c,b,g,!0),c=null),f.patchFlag===-2&&(C=!1,f.dynamicChildren=null);const{type:v,ref:P,shapeFlag:E}=f;switch(v){case An:j(c,f,m,w);break;case De:Q(c,f,m,w);break;case Tr:c==null&&z(f,m,w,T);break;case ne:Bn(c,f,m,w,b,g,T,R,C);break;default:E&1?x(c,f,m,w,b,g,T,R,C):E&6?Kn(c,f,m,w,b,g,T,R,C):(E&64||E&128)&&v.process(c,f,m,w,b,g,T,R,C,nn)}P!=null&&b?zt(P,c&&c.ref,g,f||c,!f):P==null&&c&&c.ref!=null&&zt(c.ref,null,g,c,!0)},j=(c,f,m,w)=>{if(c==null)r(f.el=i(f.children),m,w);else{const b=f.el=c.el;f.children!==c.children&&d(b,f.children)}},Q=(c,f,m,w)=>{c==null?r(f.el=a(f.children||""),m,w):f.el=c.el},z=(c,f,m,w)=>{[c.el,c.anchor]=$(c.children,f,m,w,c.el,c.anchor)},H=({el:c,anchor:f},m,w)=>{let b;for(;c&&c!==f;)b=_(c),r(c,m,w),c=b;r(f,m,w)},S=({el:c,anchor:f})=>{let m;for(;c&&c!==f;)m=_(c),o(c),c=m;o(f)},x=(c,f,m,w,b,g,T,R,C)=>{if(f.type==="svg"?T="svg":f.type==="math"&&(T="mathml"),c==null)L(f,m,w,b,g,T,R,C);else{const v=c.el&&c.el._isVueCE?c.el:null;try{v&&v._beginPatch(),qn(c,f,b,g,T,R,C)}finally{v&&v._endPatch()}}},L=(c,f,m,w,b,g,T,R)=>{let C,v;const{props:P,shapeFlag:E,transition:I,dirs:D}=c;if(C=c.el=l(c.type,g,P&&P.is,P),E&8?u(C,c.children):E&16&&rt(c.children,C,null,w,b,Rr(c,g),T,R),D&&at(c,null,w,"created"),ze(C,c,c.scopeId,T,w),P){for(const J in P)J!=="value"&&!It(J)&&s(C,J,null,P[J],g,w);"value"in P&&s(C,"value",null,P.value,g),(v=P.onVnodeBeforeMount)&&Ve(v,w,c)}D&&at(c,null,w,"beforeMount");const U=Il(b,I);U&&I.beforeEnter(C),r(C,f,m),((v=P&&P.onVnodeMounted)||U||D)&&ve(()=>{try{v&&Ve(v,w,c),U&&I.enter(C),D&&at(c,null,w,"mounted")}finally{}},b)},ze=(c,f,m,w,b)=>{if(m&&k(c,m),w)for(let g=0;g<w.length;g++)k(c,w[g]);if(b){let g=b.subTree;if(f===g||cs(g.type)&&(g.ssContent===f||g.ssFallback===f)){const T=b.vnode;ze(c,T,T.scopeId,T.slotScopeIds,b.parent)}}},rt=(c,f,m,w,b,g,T,R,C=0)=>{for(let v=C;v<c.length;v++){const P=c[v]=R?Ze(c[v]):je(c[v]);y(null,P,f,m,w,b,g,T,R)}},qn=(c,f,m,w,b,g,T)=>{const R=f.el=c.el;let{patchFlag:C,dynamicChildren:v,dirs:P}=f;C|=c.patchFlag&16;const E=c.props||G,I=f.props||G;let D;if(m&&ut(m,!1),(D=I.onVnodeBeforeUpdate)&&Ve(D,m,f,c),P&&at(f,c,m,"beforeUpdate"),m&&ut(m,!0),v&&(!c.dynamicChildren||c.dynamicChildren.length!==v.length)&&(C=0,T=!1,v=null),(E.innerHTML&&I.innerHTML==null||E.textContent&&I.textContent==null)&&u(R,""),v?mt(c.dynamicChildren,v,R,m,w,Rr(f,b),g):T||X(c,f,R,null,m,w,Rr(f,b),g,!1),C>0){if(C&16)en(R,E,I,m,b);else if(C&2&&E.class!==I.class&&s(R,"class",null,I.class,b),C&4&&s(R,"style",E.style,I.style,b),C&8){const U=f.dynamicProps;for(let J=0;J<U.length;J++){const W=U[J],oe=E[W],ue=I[W];(ue!==oe||W==="value")&&s(R,W,oe,ue,b,m)}}C&1&&c.children!==f.children&&u(R,f.children)}else!T&&v==null&&en(R,E,I,m,b);((D=I.onVnodeUpdated)||P)&&ve(()=>{D&&Ve(D,m,f,c),P&&at(f,c,m,"updated")},w)},mt=(c,f,m,w,b,g,T)=>{for(let R=0;R<f.length;R++){const C=c[R],v=f[R],P=C.el&&(C.type===ne||!Wt(C,v)||C.shapeFlag&198)?h(C.el):m;y(C,v,P,null,w,b,g,T,!0)}},en=(c,f,m,w,b)=>{if(f!==m){if(f!==G)for(const g in f)!It(g)&&!(g in m)&&s(c,g,f[g],null,b,w);for(const g in m){if(It(g))continue;const T=m[g],R=f[g];T!==R&&g!=="value"&&s(c,g,R,T,b,w)}"value"in m&&s(c,"value",f.value,m.value,b)}},Bn=(c,f,m,w,b,g,T,R,C)=>{const v=f.el=c?c.el:i(""),P=f.anchor=c?c.anchor:i("");let{patchFlag:E,dynamicChildren:I,slotScopeIds:D}=f;D&&(R=R?R.concat(D):D),c==null?(r(v,m,w),r(P,m,w),rt(f.children||[],m,P,b,g,T,R,C)):E>0&&E&64&&I&&c.dynamicChildren&&c.dynamicChildren.length===I.length?(mt(c.dynamicChildren,I,m,b,g,T,R),(f.key!=null||b&&f===b.subTree)&&ss(c,f,!0)):X(c,f,m,P,b,g,T,R,C)},Kn=(c,f,m,w,b,g,T,R,C)=>{f.slotScopeIds=R,c==null?f.shapeFlag&512?b.ctx.activate(f,m,w,T,C):Ur(f,m,w,b,g,T,C):Ws(c,f,C)},Ur=(c,f,m,w,b,g,T)=>{const R=c.component=jl(c,w,b);if(vr(c)&&(R.ctx.renderer=nn),zl(R,!1,T),R.asyncDep){if(b&&b.registerDep(R,ye,T),!c.el){const C=R.subTree=V(De);Q(null,C,f,m),c.placeholder=C.el}}else ye(R,c,f,m,b,g,T)},Ws=(c,f,m)=>{const w=f.component=c.component;if(wl(c,f,m))if(w.asyncDep&&!w.asyncResolved){ee(w,f,m);return}else w.next=f,w.update();else f.el=c.el,w.vnode=f},ye=(c,f,m,w,b,g,T)=>{const R=()=>{if(c.isMounted){let{next:E,bu:I,u:D,parent:U,vnode:J}=c;{const qe=is(c);if(qe){E&&(E.el=J.el,ee(c,E,T)),qe.asyncDep.then(()=>{ve(()=>{c.isUnmounted||v()},b)});return}}let W=E,oe;ut(c,!1),E?(E.el=J.el,ee(c,E,T)):E=J,I&&cn(I),(oe=E.props&&E.props.onVnodeBeforeUpdate)&&Ve(oe,U,E,J),ut(c,!0);const ue=Wo(c),Ue=c.subTree;c.subTree=ue,y(Ue,ue,h(Ue.el),Gn(Ue),c,b,g),E.el=ue.el,W===null&&_l(c,ue.el),D&&ve(D,b),(oe=E.props&&E.props.onVnodeUpdated)&&ve(()=>Ve(oe,U,E,J),b)}else{let E;const{el:I,props:D}=f,{bm:U,m:J,parent:W,root:oe,type:ue}=c,Ue=Tt(f);ut(c,!1),U&&cn(U),!Ue&&(E=D&&D.onVnodeBeforeMount)&&Ve(E,W,f),ut(c,!0);{oe.ce&&oe.ce._hasShadowRoot()&&oe.ce._injectChildStyle(ue,c.parent?c.parent.type:void 0);const qe=c.subTree=Wo(c);y(null,qe,m,w,c,b,g),f.el=qe.el}if(J&&ve(J,b),!Ue&&(E=D&&D.onVnodeMounted)){const qe=f;ve(()=>Ve(E,W,qe),b)}(f.shapeFlag&256||W&&Tt(W.vnode)&&W.vnode.shapeFlag&256)&&c.a&&ve(c.a,b),c.isMounted=!0,f=m=w=null}};c.scope.on();const C=c.effect=new ro(R);c.scope.off();const v=c.update=C.run.bind(C),P=c.job=C.runIfDirty.bind(C);P.i=c,P.id=c.uid,C.scheduler=()=>mr(P),ut(c,!0),v()},ee=(c,f,m)=>{f.component=c;const w=c.vnode.props;c.vnode=f,c.next=null,kl(c,f.props,w,m),El(c,f.children,m),Oe(),Ro(c),Me()},X=(c,f,m,w,b,g,T,R,C=!1)=>{const v=c&&c.children,P=c?c.shapeFlag:0,E=f.children,{patchFlag:I,shapeFlag:D}=f;if(I>0){if(I&128){Wn(v,E,m,w,b,g,T,R,C);return}else if(I&256){yt(v,E,m,w,b,g,T,R,C);return}}D&8?(P&16&&tn(v,b,g),E!==v&&u(m,E)):P&16?D&16?Wn(v,E,m,w,b,g,T,R,C):tn(v,b,g,!0):(P&8&&u(m,""),D&16&&rt(E,m,w,b,g,T,R,C))},yt=(c,f,m,w,b,g,T,R,C)=>{c=c||vt,f=f||vt;const v=c.length,P=f.length,E=Math.min(v,P);let I;for(I=0;I<E;I++){const D=f[I]=C?Ze(f[I]):je(f[I]);y(c[I],D,m,null,b,g,T,R,C)}v>P?tn(c,b,g,!0,!1,E):rt(f,m,w,b,g,T,R,C,E)},Wn=(c,f,m,w,b,g,T,R,C)=>{let v=0;const P=f.length;let E=c.length-1,I=P-1;for(;v<=E&&v<=I;){const D=c[v],U=f[v]=C?Ze(f[v]):je(f[v]);if(Wt(D,U))y(D,U,m,null,b,g,T,R,C);else break;v++}for(;v<=E&&v<=I;){const D=c[E],U=f[I]=C?Ze(f[I]):je(f[I]);if(Wt(D,U))y(D,U,m,null,b,g,T,R,C);else break;E--,I--}if(v>E){if(v<=I){const D=I+1,U=D<P?f[D].el:w;for(;v<=I;)y(null,f[v]=C?Ze(f[v]):je(f[v]),m,U,b,g,T,R,C),v++}}else if(v>I)for(;v<=E;)He(c[v],b,g,!0),v++;else{const D=v,U=v,J=new Map;for(v=U;v<=I;v++){const _e=f[v]=C?Ze(f[v]):je(f[v]);_e.key!=null&&J.set(_e.key,v)}let W,oe=0;const ue=I-U+1;let Ue=!1,qe=0;const rn=new Array(ue);for(v=0;v<ue;v++)rn[v]=0;for(v=D;v<=E;v++){const _e=c[v];if(oe>=ue){He(_e,b,g,!0);continue}let Be;if(_e.key!=null)Be=J.get(_e.key);else for(W=U;W<=I;W++)if(rn[W-U]===0&&Wt(_e,f[W])){Be=W;break}Be===void 0?He(_e,b,g,!0):(rn[Be-U]=v+1,Be>=qe?qe=Be:Ue=!0,y(_e,f[Be],m,null,b,g,T,R,C),oe++)}const Ys=Ue?Pl(rn):vt;for(W=Ys.length-1,v=ue-1;v>=0;v--){const _e=U+v,Be=f[_e],Qs=f[_e+1],Xs=_e+1<P?Qs.el||as(Qs):w;rn[v]===0?y(null,Be,m,Xs,b,g,T,R,C):Ue&&(W<0||v!==Ys[W]?gt(Be,m,Xs,2):W--)}}},gt=(c,f,m,w,b=null)=>{const{el:g,type:T,transition:R,children:C,shapeFlag:v}=c;if(v&6){gt(c.component.subTree,f,m,w);return}if(v&128){c.suspense.move(f,m,w);return}if(v&64){T.move(c,f,m,nn);return}if(T===ne){r(g,f,m);for(let E=0;E<C.length;E++)gt(C[E],f,m,w);r(c.anchor,f,m);return}if(T===Tr){H(c,f,m);return}if(w!==2&&v&1&&R)if(w===0)R.persisted&&!g[yr]?r(g,f,m):(R.beforeEnter(g),r(g,f,m),ve(()=>R.enter(g),b));else{const{leave:E,delayLeave:I,afterLeave:D}=R,U=()=>{c.ctx.isUnmounted?o(g):r(g,f,m)},J=()=>{const W=g._isLeaving||!!g[yr];g._isLeaving&&g[yr](!0),R.persisted&&!W?U():E(g,()=>{U(),D&&D()})};I?I(g,U,J):J()}else r(g,f,m)},He=(c,f,m,w=!1,b=!1)=>{const{type:g,props:T,ref:R,children:C,dynamicChildren:v,shapeFlag:P,patchFlag:E,dirs:I,cacheIndex:D,memo:U}=c;if(E===-2&&(b=!1),R!=null&&(Oe(),zt(R,null,m,c,!0),Me()),D!=null&&(f.renderCache[D]=void 0),P&256){f.ctx.deactivate(c);return}const J=P&1&&I,W=!Tt(c);let oe;if(W&&(oe=T&&T.onVnodeBeforeUnmount)&&Ve(oe,f,c),P&6)td(c.component,m,w);else{if(P&128){c.suspense.unmount(m,w);return}J&&at(c,null,f,"beforeUnmount"),P&64?c.type.remove(c,f,m,nn,w):v&&!v.hasOnce&&(g!==ne||E>0&&E&64)?tn(v,f,m,!1,!0):(g===ne&&E&384||!b&&P&16)&&tn(C,f,m),w&&Gs(c)}const ue=U!=null&&D==null;(W&&(oe=T&&T.onVnodeUnmounted)||J||ue)&&ve(()=>{oe&&Ve(oe,f,c),J&&at(c,null,f,"unmounted"),ue&&(c.el=null)},m)},Gs=c=>{const{type:f,el:m,anchor:w,transition:b}=c;if(f===ne){ed(m,w);return}if(f===Tr){S(c);return}const g=()=>{o(m),b&&!b.persisted&&b.afterLeave&&b.afterLeave()};if(c.shapeFlag&1&&b&&!b.persisted){const{leave:T,delayLeave:R}=b,C=()=>T(m,g);R?R(c.el,g,C):C()}else g()},ed=(c,f)=>{let m;for(;c!==f;)m=_(c),o(c),c=m;o(f)},td=(c,f,m)=>{const{bum:w,scope:b,job:g,subTree:T,um:R,m:C,a:v}=c;ls(C),ls(v),w&&cn(w),b.stop(),g&&(g.flags|=8,He(T,c,f,m)),R&&ve(R,f),ve(()=>{c.isUnmounted=!0},f)},tn=(c,f,m,w=!1,b=!1,g=0)=>{for(let T=g;T<c.length;T++)He(c[T],f,m,w,b)},Gn=c=>{if(c.shapeFlag&6)return Gn(c.component.subTree);if(c.shapeFlag&128)return c.suspense.next();const f=_(c.anchor||c.el),m=f&&f[Gi];return m?_(m):f};let qr=!1;const Js=(c,f,m)=>{let w;c==null?f._vnode&&(He(f._vnode,null,null,!0),w=f._vnode.component):y(f._vnode||null,c,f,null,null,null,m),f._vnode=c,qr||(qr=!0,Ro(w),To(),qr=!1)},nn={p:y,um:He,m:gt,r:Gs,mt:Ur,mc:rt,pc:X,pbc:mt,n:Gn,o:e};return{render:Js,hydrate:void 0,createApp:ml(Js)}}function Rr({type:e,props:t},n){return n==="svg"&&e==="foreignObject"||n==="mathml"&&e==="annotation-xml"&&t&&t.encoding&&t.encoding.includes("html")?void 0:n}function ut({effect:e,job:t},n){n?(e.flags|=32,t.flags|=4):(e.flags&=-33,t.flags&=-5)}function Il(e,t){return(!e||e&&!e.pendingBranch)&&t&&!t.persisted}function ss(e,t,n=!1){const r=e.children,o=t.children;if(M(r)&&M(o))for(let s=0;s<r.length;s++){const l=r[s];let i=o[s];i.shapeFlag&1&&!i.dynamicChildren&&((i.patchFlag<=0||i.patchFlag===32)&&(i=o[s]=Ze(o[s]),i.el=l.el),!n&&i.patchFlag!==-2&&ss(l,i)),i.type===An&&(i.patchFlag===-1&&(i=o[s]=Ze(i)),i.el=l.el),i.type===De&&!i.el&&(i.el=l.el)}}function Pl(e){const t=e.slice(),n=[0];let r,o,s,l,i;const a=e.length;for(r=0;r<a;r++){const d=e[r];if(d!==0){if(o=n[n.length-1],e[o]<d){t[r]=o,n.push(r);continue}for(s=0,l=n.length-1;s<l;)i=s+l>>1,e[n[i]]<d?s=i+1:l=i;d<e[n[s]]&&(s>0&&(t[r]=n[s-1]),n[s]=r)}}for(s=n.length,l=n[s-1];s-- >0;)n[s]=l,l=t[l];return n}function is(e){const t=e.subTree.component;if(t)return t.asyncDep&&!t.asyncResolved?t:is(t)}function ls(e){if(e)for(let t=0;t<e.length;t++)e[t].flags|=8}function as(e){if(e.placeholder)return e.placeholder;const t=e.component;return t?as(t.subTree):null}const cs=e=>e.__isSuspense;function Ol(e,t){t&&t.pendingBranch?M(e)?t.effects.push(...e):t.effects.push(e):Ui(e)}const ne=Symbol.for("v-fgt"),An=Symbol.for("v-txt"),De=Symbol.for("v-cmt"),Tr=Symbol.for("v-stc"),Xe=[];let xe=null;function A(e=!1){Xe.push(xe=e?null:[])}function Er(){Xe.pop(),xe=Xe[Xe.length-1]||null}let Bt=1;function In(e,t=!1){Bt+=e,e<0&&xe&&t&&(xe.hasOnce=!0)}function us(e){return e.dynamicChildren=Bt>0?xe||vt:null,Er(),Bt>0&&xe&&xe.push(e),e}function N(e,t,n,r,o,s){return us(p(e,t,n,r,o,s,!0))}function dt(e,t,n,r,o){return us(V(e,t,n,r,o,!0))}function Kt(e){return e?e.__v_isVNode===!0:!1}function Wt(e,t){return e.type===t.type&&e.key===t.key}const ds=({key:e})=>e??null,Pn=({ref:e,ref_key:t,ref_for:n})=>(typeof e=="number"&&(e=""+e),e!=null?Z(e)||ae(e)||F(e)?{i:fe,r:e,k:t,f:!!n}:e:null);function p(e,t=null,n=null,r=0,o=null,s=e===ne?0:1,l=!1,i=!1){const a={__v_isVNode:!0,__v_skip:!0,type:e,props:t,key:t&&ds(t),ref:t&&Pn(t),scopeId:$o,slotScopeIds:null,children:n,component:null,suspense:null,ssContent:null,ssFallback:null,dirs:null,transition:null,el:null,anchor:null,target:null,targetStart:null,targetAnchor:null,staticCount:0,shapeFlag:s,patchFlag:r,dynamicProps:o,dynamicChildren:null,appContext:null,ctx:fe};return i?(On(a,n),s&128&&e.normalize(a)):n&&(a.shapeFlag|=Z(n)?8:16),Bt>0&&!l&&xe&&(a.patchFlag>0||s&6)&&a.patchFlag!==32&&xe.push(a),a}const V=Ml;function Ml(e,t=null,n=null,r=0,o=null,s=!1){if((!e||e===il)&&(e=De),Kt(e)){const i=$t(e,t,!0);return n&&On(i,n),Bt>0&&!s&&xe&&(i.shapeFlag&6?xe[xe.indexOf(e)]=i:xe.push(i)),i.patchFlag=-2,i}if(Gl(e)&&(e=e.__vccOpts),t){t=Nl(t);let{class:i,style:a}=t;i&&!Z(i)&&(t.class=Pe(i)),K(a)&&(pr(a)&&!M(a)&&(a=ie({},a)),t.style=dn(a))}const l=Z(e)?1:cs(e)?128:Sn(e)?64:K(e)?4:F(e)?2:0;return p(e,t,n,r,o,l,s,!0)}function Nl(e){return e?pr(e)||Xo(e)?ie({},e):e:null}function $t(e,t,n=!1,r=!1){const{props:o,ref:s,patchFlag:l,children:i,transition:a}=e,d=t?Ll(o||{},t):o,u={__v_isVNode:!0,__v_skip:!0,type:e.type,props:d,key:d&&ds(d),ref:t&&t.ref?n&&s?M(s)?s.concat(Pn(t)):[s,Pn(t)]:Pn(t):s,scopeId:e.scopeId,slotScopeIds:e.slotScopeIds,children:i,target:e.target,targetStart:e.targetStart,targetAnchor:e.targetAnchor,staticCount:e.staticCount,shapeFlag:e.shapeFlag,patchFlag:t&&e.type!==ne?l===-1?16:l|16:l,dynamicProps:e.dynamicProps,dynamicChildren:e.dynamicChildren,appContext:e.appContext,dirs:e.dirs,transition:a,component:e.component,suspense:e.suspense,ssContent:e.ssContent&&$t(e.ssContent),ssFallback:e.ssFallback&&$t(e.ssFallback),placeholder:e.placeholder,el:e.el,anchor:e.anchor,ctx:e.ctx,ce:e.ce};return a&&r&&gr(u,a.clone(u)),u}function Fe(e=" ",t=0){return V(An,null,e,t)}function re(e="",t=!1){return t?(A(),dt(De,null,e)):V(De,null,e)}function je(e){return e==null||typeof e=="boolean"?V(De):M(e)?V(ne,null,e.slice()):Kt(e)?Ze(e):V(An,null,String(e))}function Ze(e){return e.el===null&&e.patchFlag!==-1||e.memo?e:$t(e)}function On(e,t){let n=0;const{shapeFlag:r}=e;if(t==null)t=null;else if(M(t))n=16;else if(typeof t=="object")if(r&65){const o=t.default;o&&(o._c&&(o._d=!1),On(e,o()),o._c&&(o._d=!0));return}else{n=32;const o=t._;!o&&!Xo(t)?t._ctx=fe:o===3&&fe&&(fe.slots._===1?t._=1:(t._=2,e.patchFlag|=1024))}else if(F(t)){if(r&65){On(e,{default:t});return}t={default:t,_ctx:fe},n=32}else t=String(t),r&64?(n=16,t=[Fe(t)]):n=8;e.children=t,e.shapeFlag|=n}function Ll(...e){const t={};for(let n=0;n<e.length;n++){const r=e[n];for(const o in r)if(o==="class")t.class!==r.class&&(t.class=Pe([t.class,r.class]));else if(o==="style")t.style=dn([t.style,r.style]);else if(on(o)){const s=t[o],l=r[o];l&&s!==l&&!(M(s)&&s.includes(l))?t[o]=s?[].concat(s,l):l:l==null&&s==null&&!sn(o)&&(t[o]=l)}else o!==""&&(t[o]=r[o])}return t}function Ve(e,t,n,r=null){$e(e,t,7,[n,r])}const Dl=Bo();let Fl=0;function jl(e,t,n){const r=e.type,o=(t?t.appContext:e.appContext)||Dl,s={uid:Fl++,vnode:e,type:r,parent:t,appContext:o,root:null,next:null,subTree:null,effect:null,update:null,job:null,scope:new ci(!0),render:null,proxy:null,exposed:null,exposeProxy:null,withProxy:null,provides:t?t.provides:Object.create(o.provides),ids:t?t.ids:["",0,0],accessCache:null,renderCache:[],components:null,directives:null,propsOptions:es(r,o),emitsOptions:Ko(r,o),emit:null,emitted:null,propsDefaults:G,inheritAttrs:r.inheritAttrs,ctx:G,data:G,props:G,attrs:G,slots:G,refs:G,setupState:G,setupContext:null,suspense:n,suspenseId:n?n.pendingId:0,asyncDep:null,asyncResolved:!1,isMounted:!1,isUnmounted:!1,isDeactivated:!1,bc:null,c:null,bm:null,m:null,bu:null,u:null,um:null,bum:null,da:null,a:null,rtg:null,rtc:null,ec:null,sp:null};return s.ctx={_:s},s.root=t?t.root:s,s.emit=gl.bind(null,s),e.ce&&e.ce(s),s}let me=null;const Vl=()=>me||fe;let Mn,Gt;{const e=un(),t=(n,r)=>{let o;return(o=e[n])||(o=e[n]=[]),o.push(r),s=>{o.length>1?o.forEach(l=>l(s)):o[0](s)}};Mn=t("__VUE_INSTANCE_SETTERS__",n=>me=n),Gt=t("__VUE_SSR_SETTERS__",n=>Yt=n)}const Jt=e=>{const t=me;return Mn(e),e.scope.on(),()=>{e.scope.off(),Mn(t)}},fs=()=>{me&&me.scope.off(),Mn(null)};function ps(e){return e.vnode.shapeFlag&4}let Yt=!1;function zl(e,t=!1,n=!1){t&&Gt(t);const{props:r,children:o}=e.vnode,s=ps(e);Sl(e,r,s,t),Tl(e,o,n||t);const l=s?Hl(e,t):void 0;return t&&Gt(!1),l}function Hl(e,t){const n=e.type;e.accessCache=Object.create(null),e.proxy=new Proxy(e.ctx,al);const{setup:r}=n;if(r){Oe();const o=e.setupContext=r.length>1?ql(e):null,s=Jt(e),l=St(r,e,0,[e.props,o]),i=Wr(l);if(Me(),s(),(i||e.sp)&&!Tt(e)&&Oo(e),i){if(l.then(fs,fs),t)return l.then(a=>{Gt(!0);try{hs(e,a,t)}finally{Gt(!1)}}).catch(a=>{bn(a,e,0)});e.asyncDep=l}else hs(e,l)}else ms(e)}function hs(e,t,n){F(t)?e.type.__ssrInlineRender?e.ssrRender=t:e.render=t:K(t)&&(e.setupState=_o(t)),ms(e)}function ms(e,t,n){const r=e.type;e.render||(e.render=r.render||Ae);{const o=Jt(e);Oe();try{cl(e)}finally{Me(),o()}}}const Ul={get(e,t){return de(e,"get",""),e[t]}};function ql(e){const t=n=>{e.exposed=n||{}};return{attrs:new Proxy(e.attrs,Ul),slots:e.slots,emit:e.emit,expose:t}}function Nn(e){return e.exposed?e.exposeProxy||(e.exposeProxy=new Proxy(_o($i(e.exposed)),{get(t,n){if(n in t)return t[n];if(n in Ut)return Ut[n](e)},has(t,n){return n in t||n in Ut}})):e.proxy}const Bl=/(?:^|[-_])\w/g,Kl=e=>e.replace(Bl,t=>t.toUpperCase()).replace(/[-_]/g,"");function Wl(e,t=!0){return F(e)?e.displayName||e.name:e.name||t&&e.__name}function ys(e,t,n=!1){let r=Wl(t);if(!r&&t.__file){const o=t.__file.match(/([^/\\]+)\.\w+$/);o&&(r=o[1])}if(!r&&e){const o=s=>{for(const l in s)if(s[l]===t)return l};r=o(e.components)||e.parent&&o(e.parent.type.components)||o(e.appContext.components)}return r?Kl(r):n?"App":"Anonymous"}function Gl(e){return F(e)&&"__vccOpts"in e}const se=(e,t)=>Mi(e,t,Yt);function Ln(e,t,n){try{In(-1);const r=arguments.length;return r===2?K(t)&&!M(t)?Kt(t)?V(e,null,[t]):V(e,t):V(e,null,t):(r>3?n=Array.prototype.slice.call(arguments,2):r===3&&Kt(n)&&(n=[n]),V(e,t,n))}finally{In(1)}}const Jl="3.5.41";/**
* @vue/runtime-dom v3.5.41
* (c) 2018-present Yuxi (Evan) You and Vue contributors
* @license MIT
**/let $r;const gs=typeof window<"u"&&window.trustedTypes;if(gs)try{$r=gs.createPolicy("vue",{createHTML:e=>e})}catch{}const vs=$r?e=>$r.createHTML(e):e=>e,Yl="http://www.w3.org/2000/svg",Ql="http://www.w3.org/1998/Math/MathML",et=typeof document<"u"?document:null,bs=et&&et.createElement("template"),Xl={insert:(e,t,n)=>{t.insertBefore(e,n||null)},remove:e=>{const t=e.parentNode;t&&t.removeChild(e)},createElement:(e,t,n,r)=>{const o=t==="svg"?et.createElementNS(Yl,e):t==="mathml"?et.createElementNS(Ql,e):n?et.createElement(e,{is:n}):et.createElement(e);return e==="select"&&r&&r.multiple!=null&&o.setAttribute("multiple",r.multiple),o},createText:e=>et.createTextNode(e),createComment:e=>et.createComment(e),setText:(e,t)=>{e.nodeValue=t},setElementText:(e,t)=>{e.textContent=t},parentNode:e=>e.parentNode,nextSibling:e=>e.nextSibling,querySelector:e=>et.querySelector(e),setScopeId(e,t){e.setAttribute(t,"")},insertStaticContent(e,t,n,r,o,s){const l=n?n.previousSibling:t.lastChild;if(o&&(o===s||o.nextSibling))for(;t.insertBefore(o.cloneNode(!0),n),!(o===s||!(o=o.nextSibling)););else{bs.innerHTML=vs(r==="svg"?`<svg>${e}</svg>`:r==="mathml"?`<math>${e}</math>`:e);const i=bs.content;if(r==="svg"||r==="mathml"){const a=i.firstChild;for(;a.firstChild;)i.appendChild(a.firstChild);i.removeChild(a)}t.insertBefore(i,n)}return[l?l.nextSibling:t.firstChild,n?n.previousSibling:t.lastChild]}},Zl=Symbol("_vtc");function ea(e,t,n){const r=e[Zl];r&&(t=(t?[t,...r]:[...r]).join(" ")),t==null?e.removeAttribute("class"):n?e.setAttribute("class",t):e.className=t}const xs=Symbol("_vod"),ta=Symbol("_vsh"),na=Symbol(""),ra=/(?:^|;)\s*display\s*:/;function oa(e,t,n){const r=e.style,o=Z(n);let s=!1;if(n&&!o){if(t)if(Z(t))for(const l of t.split(";")){const i=l.slice(0,l.indexOf(":")).trim();n[i]==null&&Qt(r,i,"")}else for(const l in t)n[l]==null&&Qt(r,l,"");for(const l in n){l==="display"&&(s=!0);const i=n[l];i!=null?ia(e,l,!Z(t)&&t?t[l]:void 0,i)||Qt(r,l,i):Qt(r,l,"")}}else if(o){if(t!==n){const l=r[na];l&&(n+=";"+l),r.cssText=n,s=ra.test(n)}}else t&&e.removeAttribute("style");xs in e&&(e[xs]=s?r.display:"",e[ta]&&(r.display="none"))}const ws=/\s*!important$/;function Qt(e,t,n){if(M(n))n.forEach(r=>Qt(e,t,r));else if(n==null&&(n=""),t.startsWith("--"))e.setProperty(t,n);else{const r=sa(e,t);ws.test(n)?e.setProperty(ot(r),n.replace(ws,""),"important"):e[r]=n}}const _s=["Webkit","Moz","ms"],Ar={};function sa(e,t){const n=Ar[t];if(n)return n;let r=Re(t);if(r!=="filter"&&r in e)return Ar[t]=r;r=Yr(r);for(let o=0;o<_s.length;o++){const s=_s[o]+r;if(s in e)return Ar[t]=s}return t}function ia(e,t,n,r){return e.tagName==="TEXTAREA"&&(t==="width"||t==="height")&&Z(r)&&n===r}const Ss="http://www.w3.org/1999/xlink";function ks(e,t,n,r,o,s=li(t)){r&&t.startsWith("xlink:")?n==null?e.removeAttributeNS(Ss,t.slice(6,t.length)):e.setAttributeNS(Ss,t,n):n==null||s&&!Zr(n)?e.removeAttribute(t):e.setAttribute(t,s?"":Ce(n)?String(n):n)}function Cs(e,t,n,r,o){if(t==="innerHTML"||t==="textContent"){n!=null&&(e[t]=t==="innerHTML"?vs(n):n);return}const s=e.tagName;if(t==="value"&&s!=="PROGRESS"&&!s.includes("-")){const i=s==="OPTION"?e.getAttribute("value")||"":e.value,a=n==null?e.type==="checkbox"?"on":"":String(n);(i!==a||!("_value"in e))&&(e.value=a),n==null&&e.removeAttribute(t),e._value=n;return}let l=!1;if(n===""||n==null){const i=typeof e[t];i==="boolean"?n=Zr(n):n==null&&i==="string"?(n="",l=!0):i==="number"&&(n=0,l=!0)}try{e[t]=n}catch{}l&&e.removeAttribute(o||t)}function ft(e,t,n,r){e.addEventListener(t,n,r)}function la(e,t,n,r){e.removeEventListener(t,n,r)}const Rs=Symbol("_vei");function aa(e,t,n,r,o=null){const s=e[Rs]||(e[Rs]={}),l=s[t];if(r&&l)l.value=r;else{const[i,a]=da(t);if(r){const d=s[t]=ha(r,o);ft(e,i,d,a)}else l&&(la(e,i,l,a),s[t]=void 0)}}const ca=/(Once|Passive|Capture)$/,ua=/^on:?(?:Once|Passive|Capture)$/;function da(e){let t,n;for(;(n=e.match(ca))&&!ua.test(e);)t||(t={}),e=e.slice(0,e.length-n[1].length),t[n[1].toLowerCase()]=!0;return[e[2]===":"?e.slice(3):ot(e.slice(2)),t]}let Ir=0;const fa=Promise.resolve(),pa=()=>Ir||(fa.then(()=>Ir=0),Ir=Date.now());function ha(e,t){const n=r=>{if(!r._vts)r._vts=Date.now();else if(r._vts<=n.attached)return;const o=n.value;if(M(o)){const s=r.stopImmediatePropagation;r.stopImmediatePropagation=()=>{s.call(r),r._stopped=!0};const l=o.slice(),i=[r];for(let a=0;a<l.length&&!r._stopped;a++){const d=l[a];d&&$e(d,t,5,i)}}else $e(o,t,5,[r])};return n.value=e,n.attached=pa(),n}const Ts=e=>e.charCodeAt(0)===111&&e.charCodeAt(1)===110&&e.charCodeAt(2)>96&&e.charCodeAt(2)<123,ma=(e,t,n,r,o,s)=>{const l=o==="svg";t==="class"?ea(e,r,l):t==="style"?oa(e,n,r):on(t)?sn(t)||aa(e,t,n,r,s):(t[0]==="."?(t=t.slice(1),!0):t[0]==="^"?(t=t.slice(1),!1):ya(e,t,r,l))?(Cs(e,t,r),!e.tagName.includes("-")&&(t==="value"||t==="checked"||t==="selected")&&ks(e,t,r,l,s,t!=="value")):e._isVueCE&&(ga(e,t)||e._def.__asyncLoader&&(/[A-Z]/.test(t)||!Z(r)))?Cs(e,Re(t),r,s,t):(t==="true-value"?e._trueValue=r:t==="false-value"&&(e._falseValue=r),ks(e,t,r,l))};function ya(e,t,n,r){if(r)return!!(t==="innerHTML"||t==="textContent"||t in e&&Ts(t)&&F(n));if(t==="spellcheck"||t==="draggable"||t==="translate"||t==="autocorrect"||t==="sandbox"&&e.tagName==="IFRAME"||t==="form"||t==="list"&&e.tagName==="INPUT"||t==="type"&&e.tagName==="TEXTAREA")return!1;if(t==="width"||t==="height"){const o=e.tagName;if(o==="IMG"||o==="VIDEO"||o==="CANVAS"||o==="SOURCE")return!1}return Ts(t)&&Z(n)?!1:t in e}function ga(e,t){const n=e._def.props;if(!n)return!1;const r=Re(t);return Array.isArray(n)?n.some(o=>Re(o)===r):Object.keys(n).some(o=>Re(o)===r)}const Dn=e=>{const t=e.props["onUpdate:modelValue"]||!1;return M(t)?n=>cn(t,n):t};function va(e){e.target.composing=!0}function Es(e){const t=e.target;t.composing&&(t.composing=!1,t.dispatchEvent(new Event("input")))}const pt=Symbol("_assign"),Fn=Symbol("_initialValue");function Pr(e,t,n){return t&&(e=e.trim()),n&&(e=Zn(e)),e}const Xt={created(e,{modifiers:{lazy:t,trim:n,number:r}},o){e.parentNode&&(e.type==="text"?e[Fn]=e.defaultValue.replace(/[\r\n]/g,""):e.type==="textarea"&&(e[Fn]=e.defaultValue.replace(/\r\n?/g,`
`))),e[pt]=Dn(o);const s=r||o.props&&o.props.type==="number";ft(e,t?"change":"input",l=>{l.target.composing||e[pt](Pr(e.value,n,s))}),(n||s)&&ft(e,"change",()=>{e.value=Pr(e.value,n,s)}),t||(ft(e,"compositionstart",va),ft(e,"compositionend",Es),ft(e,"change",Es))},mounted(e,{value:t,modifiers:{trim:n,number:r}}){const o=t??"",s=e[Fn];delete e[Fn],s!==void 0&&(e.type==="text"||e.type==="textarea")&&e.value!==s?e[pt](Pr(e.value,n,r)):e.value=o},beforeUpdate(e,{value:t,oldValue:n,modifiers:{lazy:r,trim:o,number:s}},l){if(e[pt]=Dn(l),e.composing)return;const i=(s||e.type==="number")&&!/^0\d/.test(e.value)?Zn(e.value):e.value,a=t??"";if(i===a)return;const d=e.getRootNode();(d instanceof Document||d instanceof ShadowRoot)&&d.activeElement===e&&e.type!=="range"&&(r&&t===n||o&&e.value.trim()===a)||(e.value=a)}},ba={deep:!0,created(e,t,n){e[pt]=Dn(n),ft(e,"change",()=>{const r=e._modelValue,o=xa(e),s=e.checked,l=e[pt];if(M(r)){const i=eo(r,o),a=i!==-1;if(s&&!a)l(r.concat(o));else if(!s&&a){const d=[...r];d.splice(i,1),l(d)}}else if(ln(r)){const i=new Set(r);s?i.add(o):i.delete(o),l(i)}else l(As(e,s))})},mounted:$s,beforeUpdate(e,t,n){e[pt]=Dn(n),$s(e,t,n)}};function $s(e,{value:t,oldValue:n},r){e._modelValue=t;let o;if(M(t))o=eo(t,r.props.value)>-1;else if(ln(t))o=t.has(r.props.value);else{if(t===n)return;o=Pt(t,As(e,!0))}e.checked!==o&&(e.checked=o)}function xa(e){return"_value"in e?e._value:e.value}function As(e,t){const n=t?"_trueValue":"_falseValue";return n in e?e[n]:t}const wa=["ctrl","shift","alt","meta"],_a={stop:e=>e.stopPropagation(),prevent:e=>e.preventDefault(),self:e=>e.target!==e.currentTarget,ctrl:e=>!e.ctrlKey,shift:e=>!e.shiftKey,alt:e=>!e.altKey,meta:e=>!e.metaKey,left:e=>"button"in e&&e.button!==0,middle:e=>"button"in e&&e.button!==1,right:e=>"button"in e&&e.button!==2,exact:(e,t)=>wa.some(n=>e[`${n}Key`]&&!t.includes(n))},Is=(e,t)=>{if(!e)return e;const n=e._withMods||(e._withMods={}),r=t.join(".");return n[r]||(n[r]=((o,...s)=>{for(let l=0;l<t.length;l++){const i=_a[t[l]];if(i&&i(o,t))return}return e(o,...s)}))},Sa=ie({patchProp:ma},Xl);let Ps;function ka(){return Ps||(Ps=$l(Sa))}const Ca=((...e)=>{const t=ka().createApp(...e),{mount:n}=t;return t.mount=r=>{const o=Ta(r);if(!o)return;const s=t._component;!F(s)&&!s.render&&!s.template&&(s.template=o.innerHTML),o.nodeType===1&&(o.textContent="");const l=n(o,!1,Ra(o));return o instanceof Element&&(o.removeAttribute("v-cloak"),o.setAttribute("data-v-app","")),l},t});function Ra(e){if(e instanceof SVGElement)return"svg";if(typeof MathMLElement=="function"&&e instanceof MathMLElement)return"mathml"}function Ta(e){return Z(e)?document.querySelector(e):e}/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ea=e=>e.replace(/([a-z0-9])([A-Z])/g,"$1-$2").toLowerCase();/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */var jn={xmlns:"http://www.w3.org/2000/svg",width:24,height:24,viewBox:"0 0 24 24",fill:"none",stroke:"currentColor","stroke-width":2,"stroke-linecap":"round","stroke-linejoin":"round"};/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const $a=({size:e,strokeWidth:t=2,absoluteStrokeWidth:n,color:r,iconNode:o,name:s,class:l,...i},{slots:a})=>Ln("svg",{...jn,width:e||jn.width,height:e||jn.height,stroke:r||jn.stroke,"stroke-width":n?Number(t)*24/Number(e):t,class:["lucide",`lucide-${Ea(s??"icon")}`],...i},[...o.map(d=>Ln(...d)),...a.default?[a.default()]:[]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const be=(e,t)=>(n,{slots:r})=>Ln($a,{...n,iconNode:t,name:e},r);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Aa=be("ArrowLeftIcon",[["path",{d:"m12 19-7-7 7-7",key:"1l729n"}],["path",{d:"M19 12H5",key:"x3x0zl"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Os=be("CircleAlertIcon",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["line",{x1:"12",x2:"12",y1:"8",y2:"12",key:"1pkeuh"}],["line",{x1:"12",x2:"12.01",y1:"16",y2:"16",key:"4dfq90"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ms=be("CircleCheckBigIcon",[["path",{d:"M21.801 10A10 10 0 1 1 17 3.335",key:"yps3ct"}],["path",{d:"m9 11 3 3L22 4",key:"1pflzl"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ia=be("CircleXIcon",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["path",{d:"m15 9-6 6",key:"1uzhvr"}],["path",{d:"m9 9 6 6",key:"z0biqf"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ns=be("CircleIcon",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ls=be("ClockIcon",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["polyline",{points:"12 6 12 12 16 14",key:"68esgv"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Pa=be("CopyIcon",[["rect",{width:"14",height:"14",x:"8",y:"8",rx:"2",ry:"2",key:"17jyea"}],["path",{d:"M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2",key:"zix9uf"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Oa=be("InboxIcon",[["polyline",{points:"22 12 16 12 14 15 10 15 8 12 2 12",key:"o97t9d"}],["path",{d:"M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z",key:"oot6mr"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ma=be("LoaderCircleIcon",[["path",{d:"M21 12a9 9 0 1 1-6.219-8.56",key:"13zald"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Na=be("PlusIcon",[["path",{d:"M5 12h14",key:"1ays0h"}],["path",{d:"M12 5v14",key:"s699le"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ds=be("RefreshCwIcon",[["path",{d:"M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8",key:"v9h5vc"}],["path",{d:"M21 3v5h-5",key:"1q7to0"}],["path",{d:"M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16",key:"3uifl3"}],["path",{d:"M8 16H3v5",key:"1cv678"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const La=be("ShieldCheckIcon",[["path",{d:"M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z",key:"oel41y"}],["path",{d:"m9 12 2 2 4-4",key:"dzmm74"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Fs=be("TriangleAlertIcon",[["path",{d:"m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3",key:"wmoenq"}],["path",{d:"M12 9v4",key:"juzpu7"}],["path",{d:"M12 17h.01",key:"p32p05"}]]);/**
 * @license lucide-vue-next v0.475.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Da=be("XIcon",[["path",{d:"M18 6 6 18",key:"1bl5f8"}],["path",{d:"m6 6 12 12",key:"d8bk6v"}]]);function ht(e,t){if(!e||typeof e!="object"||Array.isArray(e))throw new Error(`${t} must be an object`);return e}function ce(e){return typeof e=="string"&&e.trim()?e:void 0}function we(e,t){const n=ce(e);if(!n)throw new Error(`${t} is required`);return n}function Vn(e){return typeof e=="number"&&Number.isFinite(e)?e:void 0}function Fa(e){if(!(!e||typeof e!="object"||Array.isArray(e)))return e}function zn(e,t){if(e==null)return[];if(!Array.isArray(e))throw new Error(`${t} must be an array`);return e}function ja(e){const t=ht(e,"condition");return{type:we(t.type,"condition.type"),status:we(t.status,"condition.status"),reason:ce(t.reason),message:ce(t.message),lastTransitionTime:ce(t.lastTransitionTime),observedGeneration:Vn(t.observedGeneration)}}function Va(e){if(e==null)return;const t=ht(e,"targetRequirement.claim"),n=zn(t.verbs,"targetRequirement.claim.verbs");if(!n.every(r=>typeof r=="string"))throw new Error("targetRequirement.claim.verbs must contain strings");return{group:typeof t.group=="string"?t.group:"",resource:we(t.resource,"targetRequirement.claim.resource"),verbs:n}}function za(e){const t=ht(e,"targetRequirement");return{apiVersion:we(t.apiVersion,"targetRequirement.apiVersion"),kind:we(t.kind,"targetRequirement.kind"),resource:we(t.resource,"targetRequirement.resource"),namespace:ce(t.namespace),state:we(t.state,"targetRequirement.state"),message:ce(t.message),claim:Va(t.claim)}}function Ha(e){const t=ht(e,"inventory item");return{apiVersion:we(t.apiVersion,"inventory.apiVersion"),kind:we(t.kind,"inventory.kind"),resource:we(t.resource,"inventory.resource"),namespace:ce(t.namespace),name:we(t.name,"inventory.name"),uid:ce(t.uid),sourcePath:ce(t.sourcePath)}}function Or(e){const t=ht(e,"repositorySync"),n=ht(t.metadata,"repositorySync.metadata"),r=ht(t.spec,"repositorySync.spec"),o=Fa(t.status)??{};return{name:we(n.name,"repositorySync.metadata.name"),uid:ce(n.uid),generation:Vn(n.generation),createdAt:ce(n.creationTimestamp),deletionTimestamp:ce(n.deletionTimestamp),repositoryRef:we(r.repositoryRef,"repositorySync.spec.repositoryRef"),ref:ce(r.ref),path:ce(r.path),intervalSeconds:Vn(r.intervalSeconds),prune:r.prune===!0,observedGeneration:Vn(o.observedGeneration),phase:ce(o.phase),observedRevision:ce(o.observedRevision),appliedRevision:ce(o.appliedRevision),inventory:zn(o.inventory,"repositorySync.status.inventory").map(Ha),targetRequirements:zn(o.targetRequirements,"repositorySync.status.targetRequirements").map(za),conditions:zn(o.conditions,"repositorySync.status.conditions").map(ja)}}function Ua(e){if(!Array.isArray(e))throw new Error("GraphQL returned a malformed RepositorySync list");return e.map(Or)}function qa(e,t){return e.conditions.find(n=>n.type.toLowerCase()===t.toLowerCase())}function Mr(e,t){const n=t?.observedGeneration??e.observedGeneration;return n===void 0?!1:e.generation===void 0?n>0:n>=e.generation}function Ba(e,t){const n=qa(e,t);return n?.status==="True"&&Mr(e,n)}function js(e){if(e.deletionTimestamp)return"deleting";switch(e.phase?.toLowerCase()){case"awaitingauthorization":return"awaiting-authorization";case"failed":return"failed";case"synced":case"ready":return Ba(e,"Applied")?"ready":"pending";case"pending":case"reconciling":return"pending";default:return e.conditions.length?"pending":"unknown"}}function Vs(e){switch(e){case"ready":return"Applied";case"awaiting-authorization":return"Access required";case"pending":return"Reconciling";case"failed":return"Failed";case"deleting":return"Deleting";default:return"Unknown"}}function zs(e){switch(e){case"ready":return"success";case"awaiting-authorization":case"pending":return"warning";case"failed":case"deleting":return"danger";default:return"muted"}}const Nr="deployments_faros_sh",Lr="v1alpha1";let Dr=null,Fr=null;function id(e){}function Ka(e){Dr=e||null}function Wa(e){Fr=e||null}function Ga(e){return"/graphql/"+encodeURIComponent(e)}function ke(e,t,n=!0){return Object.assign(new Error(t),{reason:e,retryable:n})}function Hs(e){return/401|unauthori[sz]ed|authentication required|invalid bearer/i.test(e)||/forbidden|permission denied/i.test(e)?"Unauthorized":/apibinding|no matches for kind|resource .* not found|does not exist/i.test(e)?"MissingBackend":"GraphQLError"}async function jr(e,t={}){if(!Fr)throw ke("TenantMissing","Select a workspace to manage repository syncs.",!1);const n={Accept:"application/json","Content-Type":"application/json"};Dr&&(n.Authorization="Bearer "+Dr);let r;try{r=await fetch(Ga(Fr),{method:"POST",credentials:"same-origin",headers:n,body:JSON.stringify({query:e,variables:t})})}catch{throw ke("NetworkError","The workspace gateway could not be reached. Retry the request.")}const o=await r.text();if(!r.ok){const l=r.status===401?"Unauthorized":Hs(o);throw ke(l,o||`Workspace gateway returned HTTP ${r.status}.`)}let s;try{s=o?JSON.parse(o):{}}catch{throw ke("ProtocolError","Workspace gateway returned malformed JSON. Retry the request.")}if(s.errors?.length){const l=s.errors.map(i=>i.message||"GraphQL error").join("; ");throw ke(Hs(l),l)}if(!s.data)throw ke("ProtocolError","Workspace gateway returned no data. Retry the request.");return s.data}const Vr="metadata { name uid generation creationTimestamp deletionTimestamp } spec { repositoryRef ref path intervalSeconds prune } status { observedGeneration phase observedRevision appliedRevision inventory { apiVersion kind resource namespace name uid sourcePath } targetRequirements { apiVersion kind resource namespace state message claim { group resource verbs } } conditions { type status reason message lastTransitionTime observedGeneration } }";function zr(e){return e.deployments_faros_sh?.v1alpha1}async function Ja(){const e=await jr(`query { ${Nr} { ${Lr} { RepositorySyncs { items { ${Vr} } } } } }`),t=zr(e)?.RepositorySyncs?.items;if(!Array.isArray(t))throw ke("ProtocolError","Workspace gateway returned an incomplete RepositorySync list. Retry the read.");try{return{items:Ua(t)}}catch(n){throw ke("ProtocolError",n instanceof Error?n.message:"Workspace gateway returned malformed RepositorySync data.")}}async function Ya(e){const t=await jr(`query($n: String!) { ${Nr} { ${Lr} { RepositorySync(name: $n) { ${Vr} } } } }`,{n:e}),n=zr(t)?.RepositorySync;if(!n)throw ke("NotFound",`RepositorySync "${e}" was not found.`,!1);try{return Or(n)}catch(r){throw ke("ProtocolError",r instanceof Error?r.message:"Workspace gateway returned malformed RepositorySync data.")}}async function Qa(e){const t={repositoryRef:e.repositoryRef};e.ref&&(t.ref=e.ref),e.path&&(t.path=e.path),e.intervalSeconds!==void 0&&(t.intervalSeconds=e.intervalSeconds),e.prune!==void 0&&(t.prune=e.prune);const n=await jr(`mutation CreateRepositorySync($object: DeploymentsFarosShV1alpha1RepositorySync_Input!) {
      ${Nr} { ${Lr} { createRepositorySync(object: $object) { ${Vr} } } }
    }`,{object:{metadata:{name:e.name},spec:t}}),r=zr(n)?.createRepositorySync;if(!r)throw ke("ProtocolError","Workspace gateway returned no created RepositorySync. Retry the request.");try{return Or(r)}catch(o){throw ke("ProtocolError",o instanceof Error?o.message:"Workspace gateway returned malformed RepositorySync data.")}}async function Xa(e){if(!e)return!1;try{if(navigator.clipboard?.writeText)return await navigator.clipboard.writeText(e),!0}catch{}if(typeof document>"u")return!1;const t=document.createElement("textarea");t.value=e,t.setAttribute("readonly",""),t.style.position="fixed",t.style.opacity="0",document.body.appendChild(t),t.select();let n=!1;try{n=document.execCommand("copy")}catch{n=!1}return t.remove(),n}const Za=["aria-busy"],ec={class:"resource-table-live",role:"status","aria-live":"polite","aria-atomic":"true",style:{"block-size":"1px",clip:"rect(0 0 0 0)","clip-path":"inset(50%)","inline-size":"1px",margin:"-1px",overflow:"hidden",padding:"0",position:"absolute","white-space":"nowrap"}},tc={key:0,class:"resource-table-error",role:"alert","aria-live":"assertive"},nc={class:"resource-table-error-message"},rc={key:1,class:"resource-table-loading",role:"status","aria-live":"polite","aria-label":"Loading resources"},oc={key:0,class:"resource-table-stale",role:"alert","aria-live":"assertive"},sc={class:"resource-table-error-message"},ic={class:"resource-table-table"},lc={class:"resource-table-head-row"},ac=["onClick"],cc={key:0},uc=["colspan"],dc={class:"resource-table-empty-label"},Hn=ct({__name:"ResourceTable",props:{columns:{},rows:{},rowKey:{},loaded:{type:[Boolean,null],default:null},loading:{type:Boolean},error:{},stale:{type:Boolean,default:!1},retryable:{type:Boolean,default:!1},emptyText:{default:"No data"},interactive:{type:Boolean,default:!0}},emits:["rowClick","retry"],setup(e,{emit:t}){const n=e,r=se(()=>n.loaded!==null),o=se(()=>r.value?n.loaded===!1&&!!n.error:!!n.error),s=se(()=>r.value?n.loaded===!1:!!n.loading),l=se(()=>r.value?!!n.loading&&!(n.loaded===!1&&n.error)||n.loaded===!1&&!n.error:!!n.loading),i=t;function a(u){n.interactive&&i("rowClick",u)}function d(u,h){if(typeof n.rowKey=="function")return n.rowKey(u,h);if(typeof n.rowKey=="string"){const _=u[n.rowKey];if(typeof _=="string"||typeof _=="number")return _}for(const _ of["name","id","uid"]){const k=u[_];if(typeof k=="string"||typeof k=="number")return k}return h}return(u,h)=>(A(),N("div",{class:"resource-table","aria-busy":l.value},[p("span",ec,O(r.value&&e.loading&&e.loaded?"Updating…":""),1),o.value?(A(),N("div",tc,[V(ge(Os),{class:"resource-table-error-icon","stroke-width":1.75}),p("span",nc,O(e.error),1),e.retryable?(A(),N("button",{key:0,class:"resource-table-retry",type:"button",onClick:h[0]||(h[0]=_=>i("retry"))},"Retry")):re("",!0)])):s.value?(A(),N("div",rc,[h[3]||(h[3]=p("div",{class:"resource-table-loading-head"},[p("div",{class:"shimmer resource-table-skeleton resource-table-skeleton-short"})],-1)),(A(),N(ne,null,Ht(5,_=>p("div",{key:_,class:"resource-table-loading-row"},[...h[2]||(h[2]=[p("div",{class:"shimmer resource-table-skeleton resource-table-skeleton-wide"},null,-1),p("div",{class:"shimmer resource-table-skeleton resource-table-skeleton-mid"},null,-1),p("div",{class:"shimmer resource-table-skeleton resource-table-skeleton-small"},null,-1)])])),64))])):(A(),N(ne,{key:2},[r.value&&e.error?(A(),N("div",oc,[V(ge(Os),{class:"resource-table-error-icon","stroke-width":1.75}),p("span",sc,O(e.stale?"Showing the last successful result. ":"")+O(e.error),1),e.retryable?(A(),N("button",{key:0,class:"resource-table-retry",type:"button",onClick:h[1]||(h[1]=_=>i("retry"))},"Retry")):re("",!0)])):re("",!0),p("table",ic,[p("thead",null,[p("tr",lc,[(A(!0),N(ne,null,Ht(e.columns,_=>(A(),N("th",{key:_.key,class:"resource-table-heading"},O(_.label),1))),128))])]),p("tbody",null,[(A(!0),N(ne,null,Ht(e.rows,(_,k)=>(A(),N("tr",{key:d(_,k),class:Pe(["stagger-item resource-table-row",{"is-interactive":e.interactive}]),style:dn({animationDelay:`${k*35}ms`}),onClick:$=>a(_)},[(A(!0),N(ne,null,Ht(e.columns,$=>(A(),N("td",{key:$.key,class:"resource-table-cell"},[ll(u.$slots,$.key,{value:_[$.key],row:_},()=>[Fe(O(_[$.key]),1)])]))),128))],14,ac))),128)),e.rows.length===0?(A(),N("tr",cc,[p("td",{colspan:e.columns.length,class:"resource-table-empty-cell"},[V(ge(Oa),{class:"resource-table-empty-icon","stroke-width":1.25}),p("p",dc,O(e.emptyText),1)],8,uc)])):re("",!0)])])],64))],8,Za))}}),fc={class:"status-badge-dot-wrap"},Zt=ct({__name:"StatusBadge",props:{status:{},connected:{type:[Boolean,null],default:null},tone:{default:null}},setup(e){const t=e,n={success:{toneClass:"tone-success",dotClass:"dot-success",pulseClass:"pulse-success"},warning:{toneClass:"tone-warning",dotClass:"dot-warning",pulseClass:"pulse-warning"},danger:{toneClass:"tone-danger",dotClass:"dot-danger",pulseClass:"pulse-danger"},muted:{toneClass:"tone-muted",dotClass:"dot-muted",pulseClass:"pulse-muted"}},r=se(()=>{if(t.connected===!1)return{...n.danger,icon:Ia};if(t.tone)return{...n[t.tone],icon:t.tone==="danger"?Fs:t.tone==="warning"?Ls:t.tone==="success"?Ms:Ns};switch(t.status?.toLowerCase()){case"ready":case"succeeded":case"committed":case"active":case"loaded":return{...n.success,icon:Ms};case"scheduling":case"pending":case"provisioning":case"running":case"retrying":case"status unavailable":case"loading":case"starting":case"loaded unverified":return{...n.warning,icon:Ls};case"terminating":case"failed":case"error":case"repository missing":case"connection missing":case"needs attention":return{...n.danger,icon:Fs};default:return{...n.muted,icon:Ns}}});return(o,s)=>(A(),N("span",{class:Pe(["status-badge",r.value.toneClass])},[p("span",fc,[e.status?.toLowerCase()==="ready"&&e.connected!==!1?(A(),N("span",{key:0,class:Pe(["live-dot status-badge-pulse",r.value.pulseClass])},null,2)):re("",!0),p("span",{class:Pe(["status-badge-dot",r.value.dotClass])},null,2)]),Fe(" "+O(e.status),1)],2))}});function Un(e){return{data:e,phase:"idle",loading:!1,error:null,retryable:!0}}function Us(e){return{...e,phase:e.phase==="loaded"||e.phase==="stale"?e.phase:"loading",loading:!0,error:null}}function qs(e){return{data:e,phase:"loaded",loading:!1,error:null,retryable:!0}}function Bs(e,t,n=!0){const r=e.phase==="loaded"||e.phase==="stale";return{...e,phase:r?"stale":"error",loading:!1,error:t,retryable:n}}function Ks(e,t){const n=typeof e=="object"&&e!==null&&"reason"in e?String(e.reason??""):"",r=e instanceof Error?e.message:"";switch(n){case"Unauthorized":return"Workspace access is unauthorized. Sign in again or choose a workspace with Deployments enabled.";case"MissingBackend":return"Deployments resources are not available in this workspace. Enable Deployments, then retry.";case"NotFound":return r||"The requested RepositorySync was not found in this workspace.";case"NetworkError":return"The workspace gateway is unavailable. Retry the read.";case"ProtocolError":return r||"The workspace gateway returned incomplete evidence. Retry the read.";default:return r||t}}function pc(e){return e.length>253?!1:e.split(".").every(t=>t.length>0&&t.length<=63&&/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(t))}function hc(e){const t=e.trim();return t&&(t==="."||t==="/"||t.startsWith("/")||t.startsWith("\\")||/^[a-z]:[\\/]/i.test(t)||t.split(/[\\/]+/).includes(".."))?"Use a repository-relative directory without root or parent-directory segments.":""}function mc(e){const t={name:"",repositoryRef:"",path:"",intervalSeconds:""},n=e.name.trim();n?n!==e.name?t.name="Name cannot begin or end with whitespace.":pc(n)||(t.name="Use a DNS-1123 name: lowercase letters, numbers, hyphens, or dots, starting and ending with a letter or number."):t.name="Name is required.",e.repositoryRef.trim()||(t.repositoryRef="Repository is required."),t.path=hc(e.path||"");const r=e.intervalSeconds;return(r===void 0||!Number.isInteger(r)||r<10||r>3600)&&(t.intervalSeconds="Interval must be a whole number from 10 to 3600 seconds."),t}function yc(e){return!!(e.name||e.repositoryRef||e.path||e.intervalSeconds)}const gc=["aria-busy"],vc={class:"panel-head"},bc=["disabled"],xc={class:"create-field",for:"repository-sync-name"},wc=["aria-invalid","aria-describedby","disabled"],_c={key:0,id:"repository-sync-name-error",class:"field-error"},Sc={class:"create-field",for:"repository-sync-repository"},kc=["aria-invalid","aria-describedby","disabled"],Cc={key:0,id:"repository-sync-repository-error",class:"field-error"},Rc={class:"field-row"},Tc={class:"create-field",for:"repository-sync-ref"},Ec=["disabled"],$c={class:"create-field",for:"repository-sync-path"},Ac=["aria-invalid","aria-describedby","disabled"],Ic={key:0,id:"repository-sync-path-error",class:"field-error"},Pc={class:"create-field interval-field",for:"repository-sync-interval"},Oc={class:"input-unit"},Mc=["aria-invalid","aria-describedby","disabled"],Nc={key:0,id:"repository-sync-interval-error",class:"field-error"},Lc={class:"checkbox-field",for:"repository-sync-prune"},Dc=["disabled"],Fc={class:"form-actions"},jc=["disabled"],Vc=["disabled"],zc=ct({__name:"RepositorySyncCreateForm",emits:["cancel","created"],setup(e,{emit:t}){const n=t,r=Je(null),o=Je(null),s=Je(!1),l=Je(""),i=wt({name:"",repositoryRef:"",path:"",intervalSeconds:""}),a=wt({name:"",repositoryRef:"",ref:"",path:".faros",intervalSeconds:30,prune:!0});let d=null;function u(){i.name="",i.repositoryRef="",i.path="",i.intervalSeconds="",l.value=""}function h(){return u(),Object.assign(i,mc(a)),!yc(i)}async function _(){if(s.value||!h()){s.value||await jt(()=>o.value?.focus());return}s.value=!0;try{const $=await Qa({name:a.name,repositoryRef:a.repositoryRef.trim(),ref:a.ref?.trim(),path:a.path?.trim(),intervalSeconds:a.intervalSeconds,prune:a.prune});n("created",$.name)}catch($){l.value=$ instanceof Error?$.message:"Repository sync could not be created.",await jt(()=>o.value?.focus())}finally{s.value=!1}}function k(){s.value||n("cancel")}return Rn(()=>{d=document.activeElement instanceof HTMLElement?document.activeElement:null,jt(()=>r.value?.focus())}),Tn(()=>{const $=d;d=null,jt(()=>$?.isConnected&&$.focus())}),($,y)=>(A(),N("section",{class:"panel create-sync-panel","aria-labelledby":"create-sync-title","aria-busy":s.value},[p("header",vc,[y[11]||(y[11]=p("div",null,[p("p",{class:"eyebrow"},"New sync"),p("h2",{id:"create-sync-title",class:"panel-title"},"Create repository sync"),p("p",{class:"create-description"},"Apply reviewed desired state from a Code repository into this workspace.")],-1)),p("button",{class:"button icon-button",type:"button",disabled:s.value,"aria-label":"Cancel creating repository sync",onClick:k},[V(ge(Da),{size:16,"stroke-width":1.75,"aria-hidden":"true"})],8,bc)]),i.name||i.repositoryRef||i.path||i.intervalSeconds||l.value?(A(),N("div",{key:0,ref_key:"errorSummary",ref:o,class:"error-summary",role:"alert","aria-live":"assertive",tabindex:"-1"},O(l.value||i.name||i.repositoryRef||i.path||i.intervalSeconds),513)):re("",!0),p("form",{class:"sync-form",novalidate:"",onSubmit:Is(_,["prevent"])},[p("label",xc,[y[12]||(y[12]=p("span",{class:"field-label"},"Name",-1)),Rt(p("input",{id:"repository-sync-name",ref_key:"nameInput",ref:r,"onUpdate:modelValue":y[0]||(y[0]=j=>a.name=j),class:"field-input mono",type:"text",autocomplete:"off",placeholder:"pen-store-production",maxlength:"253",required:"","aria-required":"true","aria-invalid":!!i.name,"aria-describedby":i.name?"repository-sync-name-hint repository-sync-name-error":"repository-sync-name-hint",disabled:s.value,onInput:y[1]||(y[1]=j=>{i.name="",l.value=""})},null,40,wc),[[Xt,a.name]]),y[13]||(y[13]=p("span",{id:"repository-sync-name-hint",class:"field-hint"},"Stable DNS-1123 name for this sync.",-1)),i.name?(A(),N("span",_c,O(i.name),1)):re("",!0)]),p("label",Sc,[y[14]||(y[14]=p("span",{class:"field-label"},"Repository",-1)),Rt(p("input",{id:"repository-sync-repository","onUpdate:modelValue":y[2]||(y[2]=j=>a.repositoryRef=j),class:"field-input mono",type:"text",autocomplete:"off",placeholder:"pen-store-app",required:"","aria-required":"true","aria-invalid":!!i.repositoryRef,"aria-describedby":i.repositoryRef?"repository-sync-repository-hint repository-sync-repository-error":"repository-sync-repository-hint",disabled:s.value,onInput:y[3]||(y[3]=j=>{i.repositoryRef="",l.value=""})},null,40,kc),[[Xt,a.repositoryRef]]),y[15]||(y[15]=p("span",{id:"repository-sync-repository-hint",class:"field-hint"},"Exact repository resource name from the Code provider.",-1)),i.repositoryRef?(A(),N("span",Cc,O(i.repositoryRef),1)):re("",!0)]),p("div",Rc,[p("label",Tc,[y[16]||(y[16]=p("span",{class:"field-label"},"Git ref",-1)),Rt(p("input",{id:"repository-sync-ref","onUpdate:modelValue":y[4]||(y[4]=j=>a.ref=j),class:"field-input mono",type:"text",autocomplete:"off",placeholder:"Repository default",disabled:s.value,onInput:y[5]||(y[5]=j=>l.value="")},null,40,Ec),[[Xt,a.ref]]),y[17]||(y[17]=p("span",{class:"field-hint"},"Branch, tag, or commit. Blank uses the repository default.",-1))]),p("label",$c,[y[18]||(y[18]=p("span",{class:"field-label"},"Target path",-1)),Rt(p("input",{id:"repository-sync-path","onUpdate:modelValue":y[6]||(y[6]=j=>a.path=j),class:"field-input mono",type:"text",autocomplete:"off",placeholder:".faros","aria-invalid":!!i.path,"aria-describedby":i.path?"repository-sync-path-hint repository-sync-path-error":"repository-sync-path-hint",disabled:s.value,onInput:y[7]||(y[7]=j=>{i.path="",l.value=""})},null,40,Ac),[[Xt,a.path]]),y[19]||(y[19]=p("span",{id:"repository-sync-path-hint",class:"field-hint"},"Repository-relative directory containing desired-state manifests.",-1)),i.path?(A(),N("span",Ic,O(i.path),1)):re("",!0)])]),p("label",Pc,[y[21]||(y[21]=p("span",{class:"field-label"},"Sync interval",-1)),p("span",Oc,[Rt(p("input",{id:"repository-sync-interval","onUpdate:modelValue":y[8]||(y[8]=j=>a.intervalSeconds=j),class:"field-input mono",type:"number",inputmode:"numeric",min:"10",max:"3600",step:"1",required:"","aria-required":"true","aria-invalid":!!i.intervalSeconds,"aria-describedby":i.intervalSeconds?"repository-sync-interval-hint repository-sync-interval-error":"repository-sync-interval-hint",disabled:s.value,onInput:y[9]||(y[9]=j=>{i.intervalSeconds="",l.value=""})},null,40,Mc),[[Xt,a.intervalSeconds,void 0,{number:!0}]]),y[20]||(y[20]=p("span",{"aria-hidden":"true"},"seconds",-1))]),y[22]||(y[22]=p("span",{id:"repository-sync-interval-hint",class:"field-hint"},"Deployments checks from every 10 seconds up to hourly.",-1)),i.intervalSeconds?(A(),N("span",Nc,O(i.intervalSeconds),1)):re("",!0)]),p("label",Lc,[Rt(p("input",{id:"repository-sync-prune","onUpdate:modelValue":y[10]||(y[10]=j=>a.prune=j),type:"checkbox",disabled:s.value},null,8,Dc),[[ba,a.prune]]),y[23]||(y[23]=p("span",null,[p("strong",null,"Prune removed objects"),p("small",null,"Delete owned objects when manifests are removed or when this sync is deleted. Disable to leave them in place.")],-1))]),p("footer",Fc,[p("button",{class:"button ghost",type:"button",disabled:s.value,onClick:k},"Cancel",8,jc),p("button",{class:"button primary",type:"submit",disabled:s.value},[s.value?(A(),dt(ge(Ma),{key:0,class:"spinning",size:14,"stroke-width":1.75,"aria-hidden":"true"})):re("",!0),Fe(" "+O(s.value?"Creating…":"Create sync"),1)],8,Vc)])],32)],8,gc))}}),Hc={class:"page"},Uc={class:"page-head"},qc={class:"page-actions"},Bc=["disabled","aria-busy"],Kc=["aria-expanded"],Wc={key:1,class:"read-status",role:"status"},Gc=["aria-label","onClick"],Jc={class:"mono link-text"},Yc={class:"mono"},Qc={class:"mono"},Xc={class:"mono breakable"},Zc={class:"mono"},eu=1e4,tu=ct({__name:"DeploymentsListView",props:{tenant:{}},emits:["open"],setup(e,{emit:t}){const n=e,r=t,o=Je(Un([])),s=Je(!1);let l=0,i=null,a;const d=se(()=>o.value.data.map(k=>{const $=js(k);return{name:k.name,repository:k.repositoryRef,source:`${k.ref||"default"} / ${k.path||".faros"}`,revision:k.appliedRevision||k.observedRevision||"—",targets:k.targetRequirements.length||k.inventory.length,phase:Vs($),tone:zs($)}})),u=se(()=>o.value.phase==="loaded"||o.value.phase==="stale");async function h(){if(i)return i;const k=++l;return o.value=Us(o.value),i=(async()=>{try{const $=await Ja();k===l&&(o.value=qs($.items))}catch($){if(k!==l)return;o.value=Bs(o.value,Ks($,"Repository syncs could not be read."),$.retryable!==!1)}finally{i=null}})(),i}function _(k){s.value=!1,r("open",k)}return Rn(()=>{a=window.setInterval(()=>{h()},eu)}),Tn(()=>{a!==void 0&&window.clearInterval(a)}),nt(()=>n.tenant,()=>{l++,i=null,o.value=Un([]),h()},{immediate:!0}),(k,$)=>(A(),N("section",Hc,[p("header",Uc,[$[3]||($[3]=p("div",null,[p("p",{class:"eyebrow"},"Deployments"),p("h1",{class:"page-title"},"Repository syncs"),p("p",{class:"page-meta"},"Git revisions projected into this workspace. Target providers own runtime readiness.")],-1)),p("div",qc,[p("button",{class:"button ghost",type:"button",disabled:o.value.loading,"aria-busy":o.value.loading,onClick:h},[V(ge(Ds),{size:14,class:Pe({spinning:o.value.loading}),"aria-hidden":"true"},null,8,["class"]),Fe(" "+O(o.value.loading?"Refreshing…":"Refresh"),1)],8,Bc),p("button",{class:"button primary",type:"button","aria-controls":"create-repository-sync-panel","aria-expanded":s.value,onClick:$[0]||($[0]=y=>s.value=!s.value)},[V(ge(Na),{size:14,"stroke-width":1.75,"aria-hidden":"true"}),Fe(" "+O(s.value?"Close form":"New sync"),1)],8,Kc)])]),s.value?(A(),dt(zc,{key:0,id:"create-repository-sync-panel",onCancel:$[1]||($[1]=y=>s.value=!1),onCreated:_})):re("",!0),$[4]||($[4]=p("div",{class:"read-contract",role:"note"},[p("span",{class:"read-contract-dot","aria-hidden":"true"}),p("span",null,"Applied means the desired objects were synchronized. It does not assert that their workloads are healthy.")],-1)),!u.value&&o.value.loading?(A(),N("p",Wc,"Loading repository syncs…")):re("",!0),V(Hn,{columns:[{key:"name",label:"Sync"},{key:"repository",label:"Repository"},{key:"source",label:"Ref / path"},{key:"revision",label:"Applied revision"},{key:"targets",label:"Targets"},{key:"phase",label:"Sync state"}],rows:d.value,"row-key":"name",loaded:u.value,loading:o.value.loading,error:o.value.error,stale:o.value.phase==="stale",retryable:o.value.retryable,"empty-text":"No repository syncs are configured in this workspace.",onRowClick:$[2]||($[2]=y=>r("open",String(y.name))),onRetry:h},{name:te(({value:y})=>[p("button",{class:"deployment-name-trigger",type:"button","aria-label":`Open repository sync ${String(y)}`,onClick:Is(j=>r("open",String(y)),["stop"])},[p("span",Jc,O(y),1)],8,Gc)]),repository:te(({value:y})=>[p("span",Yc,O(y),1)]),source:te(({value:y})=>[p("span",Qc,O(y),1)]),revision:te(({value:y})=>[p("span",Xc,O(y),1)]),targets:te(({value:y})=>[p("span",Zc,O(y),1)]),phase:te(({value:y,row:j})=>[V(Zt,{status:String(y),tone:j.tone},null,8,["status","tone"])]),_:1},8,["rows","loaded","loading","error","stale","retryable"])]))}}),nu={class:"conditions-panel"},ru={key:0,class:"conditions-stale"},ou={class:"conditions-type"},su={class:"conditions-message"},iu={class:"conditions-muted"},lu=ct({__name:"ConditionsPanel",props:{conditions:{},generation:{},observedGeneration:{},emptyText:{}},setup(e){const t=e,n=se(()=>t.observedGeneration===void 0||t.generation===void 0||t.observedGeneration>=t.generation),r=se(()=>t.conditions.map(s=>({...s,reasonLabel:s.reason||"-",messageLabel:s.message||"-",sinceLabel:s.lastTransitionTime||"-"})));function o(s){return s==="True"?"success":s==="False"?"warning":"muted"}return(s,l)=>(A(),N("div",nu,[l[0]||(l[0]=p("h3",{class:"conditions-title"},"Conditions",-1)),e.observedGeneration!==void 0&&!n.value?(A(),N("p",ru," Controller has not caught up - spec generation "+O(e.generation)+", observed "+O(e.observedGeneration)+". ",1)):re("",!0),V(Hn,{columns:[{key:"type",label:"Type"},{key:"status",label:"Status"},{key:"reasonLabel",label:"Reason"},{key:"messageLabel",label:"Message"},{key:"sinceLabel",label:"Since"}],rows:r.value,interactive:!1,"empty-text":e.emptyText||"No conditions yet. The controller has not reconciled this resource."},{type:te(({value:i})=>[p("span",ou,O(i),1)]),status:te(({value:i})=>[V(Zt,{status:String(i),tone:o(String(i))},null,8,["status","tone"])]),messageLabel:te(({value:i})=>[p("span",su,O(i),1)]),sinceLabel:te(({value:i})=>[p("span",iu,O(i),1)]),_:1},8,["rows","empty-text"])]))}}),au={class:"page detail-page"},cu={class:"page-head detail-head"},uu={class:"page-title mono"},du={class:"detail-actions"},fu=["disabled","aria-busy"],pu={key:0,class:"state-card stale-card",role:"alert"},hu={key:1,class:"state-card error-card",role:"alert"},mu={key:2,class:"detail-skeleton",role:"status","aria-label":"Loading repository sync evidence"},yu={key:0},gu={key:1},vu={key:2},bu={key:3},xu={key:4},wu={key:5},_u={key:0,class:"authorization-card",role:"alert"},Su={class:"panel-title"},ku={class:"detail-grid"},Cu={class:"panel","aria-labelledby":"source-heading"},Ru={class:"facts"},Tu={class:"mono breakable"},Eu={class:"mono"},$u={class:"mono"},Au={class:"mono breakable"},Iu={class:"mono breakable"},Pu={class:"mono"},Ou={class:"panel","aria-labelledby":"stages-heading"},Mu={class:"condition-summary"},Nu={class:"condition-summary-label"},Lu={class:"panel","aria-labelledby":"requirements-heading"},Du={class:"mono"},Fu={class:"mono"},ju={class:"muted"},Vu={class:"panel","aria-labelledby":"inventory-heading"},zu={class:"mono"},Hu={class:"mono"},Uu={class:"mono"},qu={class:"mono"},Bu={class:"mono breakable"},Ku={class:"panel","aria-labelledby":"conditions-heading"},Wu=1e4,Gu=ct({__name:"DeploymentDetailView",props:{name:{},tenant:{}},emits:["back","authorize"],setup(e,{emit:t}){const n=e,r=t,o=Je(Un(null)),s=Je(!1);let l=0,i=null,a;const d=se(()=>o.value.data),u=se(()=>d.value?js(d.value):"unknown"),h=se(()=>(o.value.phase==="loaded"||o.value.phase==="stale")&&d.value!==null),_=se(()=>{const S=new Map;for(const x of d.value?.targetRequirements??[])x.state.toLowerCase()!=="awaitingauthorization"||!x.claim||S.set(`${x.claim.group}/${x.claim.resource}`,x.claim);return[...S.values()]}),k=se(()=>(d.value?.inventory??[]).map(S=>({key:`${S.apiVersion}/${S.resource}/${S.namespace||""}/${S.name}`,identity:`${S.apiVersion}/${S.resource}`,kind:S.kind,location:S.namespace?`${S.namespace}/${S.name}`:S.name,source:S.sourcePath||"—",uid:S.uid||"—"}))),$=se(()=>(d.value?.targetRequirements??[]).map(S=>({target:`${S.apiVersion}/${S.kind}`,resource:S.resource,state:S.state,message:S.message||"—"})));function y(S){return d.value?.conditions.find(x=>x.type.toLowerCase()===S.toLowerCase())}function j(S){const x=d.value,L=y(S);return!x||!L?"Not observed":Mr(x,L)?L.status==="True"?"Complete":L.status==="False"?"Blocked":"Unknown":"Stale"}function Q(S){const x=d.value,L=y(S);return!x||!L||!Mr(x,L)?"muted":L.status==="True"?"success":S==="AuthorizationReady"?"warning":"danger"}async function z(){const S=d.value?.appliedRevision||d.value?.observedRevision;!S||!await Xa(S)||(s.value=!0,window.setTimeout(()=>{s.value=!1},1800))}async function H(){if(i)return i;const S=++l;return o.value=Us(o.value),i=(async()=>{try{const x=await Ya(n.name);S===l&&(o.value=qs(x))}catch(x){if(S!==l)return;o.value=Bs(o.value,Ks(x,"Repository sync could not be read."),x.retryable!==!1)}finally{i=null}})(),i}return Rn(()=>{a=window.setInterval(()=>{H()},Wu)}),Tn(()=>{a!==void 0&&window.clearInterval(a)}),nt(()=>[n.name,n.tenant],()=>{l++,i=null,o.value=Un(null),H()},{immediate:!0}),(S,x)=>(A(),N("section",au,[p("header",cu,[p("div",null,[p("button",{class:"button text-button",type:"button",onClick:x[0]||(x[0]=L=>r("back"))},[V(ge(Aa),{size:14,"aria-hidden":"true"}),x[2]||(x[2]=Fe(" Back to repository syncs ",-1))]),x[3]||(x[3]=p("p",{class:"eyebrow"},"Repository sync",-1)),p("h1",uu,O(e.name),1),x[4]||(x[4]=p("p",{class:"page-meta"},"Source, authorization, and apply evidence for one Git revision.",-1))]),p("div",du,[p("button",{class:"button ghost",type:"button",disabled:o.value.loading,"aria-busy":o.value.loading,onClick:H},[V(ge(Ds),{size:14,class:Pe({spinning:o.value.loading}),"aria-hidden":"true"},null,8,["class"]),Fe(" "+O(o.value.loading?"Refreshing…":"Refresh"),1)],8,fu)])]),o.value.phase==="stale"?(A(),N("div",pu,[x[5]||(x[5]=p("strong",null,"Showing the last successful result.",-1)),p("span",null,O(o.value.error),1),o.value.retryable?(A(),N("button",{key:0,class:"button text-button",type:"button",onClick:H},"Retry")):re("",!0)])):re("",!0),o.value.phase==="error"?(A(),N("div",hu,[x[6]||(x[6]=p("p",{class:"eyebrow"},"Repository sync unavailable",-1)),p("p",null,O(o.value.error),1),o.value.retryable?(A(),N("button",{key:0,class:"button ghost",type:"button",onClick:H},"Retry read")):re("",!0)])):h.value?d.value?(A(),N(ne,{key:3},[p("div",{class:Pe(["evidence-banner",`evidence-${u.value}`]),role:"status"},[V(Zt,{status:ge(Vs)(u.value),tone:ge(zs)(u.value)},null,8,["status","tone"]),u.value==="ready"?(A(),N("span",yu,"The observed Git revision is applied. Runtime health remains owned by each target provider.")):u.value==="awaiting-authorization"?(A(),N("span",gu,"The complete revision is blocked before writes until the requested access is granted.")):u.value==="pending"?(A(),N("span",vu,"Source, planning, or apply reconciliation is still in progress.")):u.value==="failed"?(A(),N("span",bu,"The revision could not be applied. Inspect the controller conditions below.")):u.value==="deleting"?(A(),N("span",xu,"Cleanup is in progress for objects owned by this sync.")):(A(),N("span",wu,"No current synchronization evidence is available."))],2),_.value.length?(A(),N("div",_u,[p("div",null,[x[8]||(x[8]=p("p",{class:"eyebrow"},"Access required",-1)),p("h2",Su,"Authorize "+O(_.value.length)+" target resource "+O(_.value.length===1?"type":"types"),1),x[9]||(x[9]=p("p",{class:"page-meta"},"The grant is explicit and workspace-scoped. Existing provider grants will be preserved.",-1))]),p("button",{class:"button primary",type:"button",onClick:x[1]||(x[1]=L=>r("authorize",_.value))},[V(ge(La),{size:14,"aria-hidden":"true"}),x[10]||(x[10]=Fe(" Review access ",-1))])])):re("",!0),p("div",ku,[p("section",Cu,[x[17]||(x[17]=p("p",{class:"eyebrow"},"Desired state source",-1)),x[18]||(x[18]=p("h2",{id:"source-heading",class:"panel-title"},"Git directory",-1)),p("dl",Ru,[p("div",null,[x[11]||(x[11]=p("dt",null,"Repository",-1)),p("dd",Tu,O(d.value.repositoryRef),1)]),p("div",null,[x[12]||(x[12]=p("dt",null,"Ref",-1)),p("dd",Eu,O(d.value.ref||"repository default"),1)]),p("div",null,[x[13]||(x[13]=p("dt",null,"Path",-1)),p("dd",$u,O(d.value.path||".faros"),1)]),p("div",null,[x[14]||(x[14]=p("dt",null,"Observed revision",-1)),p("dd",Au,O(d.value.observedRevision||"—"),1)]),p("div",null,[x[15]||(x[15]=p("dt",null,"Applied revision",-1)),p("dd",Iu,O(d.value.appliedRevision||"—"),1)]),p("div",null,[x[16]||(x[16]=p("dt",null,"Prune",-1)),p("dd",Pu,O(d.value.prune?"Enabled":"Disabled"),1)])]),d.value.appliedRevision||d.value.observedRevision?(A(),N("button",{key:0,class:"button small",type:"button",onClick:z},[V(ge(Pa),{size:13,"aria-hidden":"true"}),Fe(" "+O(s.value?"Copied":"Copy revision"),1)])):re("",!0)]),p("section",Ou,[x[19]||(x[19]=p("p",{class:"eyebrow"},"Convergence",-1)),x[20]||(x[20]=p("h2",{id:"stages-heading",class:"panel-title"},"Sync stages",-1)),p("div",Mu,[(A(),N(ne,null,Ht(["SourceReady","AuthorizationReady","Applied"],L=>p("div",{key:L,class:"condition-summary-item"},[p("span",Nu,O(L.replace("Ready","")||L),1),V(Zt,{status:j(L),tone:Q(L)},null,8,["status","tone"])])),64))]),x[21]||(x[21]=p("p",{class:"muted"},"Applied is the terminal claim here. Target object status is intentionally not projected as deployment readiness.",-1))])]),p("section",Lu,[x[22]||(x[22]=p("p",{class:"eyebrow"},"Preflight",-1)),x[23]||(x[23]=p("h2",{id:"requirements-heading",class:"panel-title"},"Target resource access",-1)),V(Hn,{columns:[{key:"target",label:"Target type"},{key:"resource",label:"Resource"},{key:"state",label:"Access"},{key:"message",label:"Evidence"}],rows:$.value,"row-key":"target",loaded:!0,"empty-text":"No target resources have been planned for this revision."},{target:te(({value:L})=>[p("span",Du,O(L),1)]),resource:te(({value:L})=>[p("span",Fu,O(L),1)]),state:te(({value:L})=>[V(Zt,{status:String(L),tone:String(L).toLowerCase()==="authorized"?"success":"warning"},null,8,["status","tone"])]),message:te(({value:L})=>[p("span",ju,O(L),1)]),_:1},8,["rows"])]),p("section",Vu,[x[24]||(x[24]=p("p",{class:"eyebrow"},"Applied inventory",-1)),x[25]||(x[25]=p("h2",{id:"inventory-heading",class:"panel-title"},"Objects owned by this sync",-1)),V(Hn,{columns:[{key:"identity",label:"API / resource"},{key:"kind",label:"Kind"},{key:"location",label:"Namespace / name"},{key:"source",label:"Source file"},{key:"uid",label:"UID"}],rows:k.value,"row-key":"key",loaded:!0,"empty-text":"No objects have been applied for this revision."},{identity:te(({value:L})=>[p("span",zu,O(L),1)]),kind:te(({value:L})=>[p("span",Hu,O(L),1)]),location:te(({value:L})=>[p("span",Uu,O(L),1)]),source:te(({value:L})=>[p("span",qu,O(L),1)]),uid:te(({value:L})=>[p("span",Bu,O(L),1)]),_:1},8,["rows"])]),p("section",Ku,[x[26]||(x[26]=p("p",{class:"eyebrow"},"Controller evidence",-1)),x[27]||(x[27]=p("h2",{id:"conditions-heading",class:"panel-title"},"Conditions",-1)),V(lu,{conditions:d.value.conditions,generation:d.value.generation,"observed-generation":d.value.observedGeneration,"empty-text":"No synchronization conditions have been observed yet."},null,8,["conditions","generation","observed-generation"])])],64)):re("",!0):(A(),N("div",mu,[...x[7]||(x[7]=[p("div",{class:"shimmer skeleton-line skeleton-wide"},null,-1),p("div",{class:"detail-grid"},[p("div",{class:"shimmer skeleton-block"}),p("div",{class:"shimmer skeleton-block"})],-1)])]))]))}});function Ju(e){const t=(e??"").replace(/^\/+|\/+$/g,"");if(!t||t==="deployments")return{page:"deployments"};const[n,r]=t.split("/");if(n==="deployments"&&r)try{const o=decodeURIComponent(r);if(o)return{page:"deployments",name:o}}catch{}return{page:"deployments"}}const Yu={key:0,class:"page state-card"},Qu=ct({__name:"App",props:{ctx:{}},setup(e){const t=e,n=se(()=>Ju(t.ctx?.subPath)),r=se(()=>!!t.ctx?.tenant);nt(()=>t.ctx?.basePath,a=>void 0,{immediate:!0}),nt(()=>t.ctx?.token,a=>Ka(a),{immediate:!0}),nt(()=>t.ctx?.tenant,a=>Wa(a),{immediate:!0});const o=Je(null);function s(a){o.value?.dispatchEvent(new CustomEvent("faros-navigate",{detail:{path:a},bubbles:!0}))}function l(a){s("deployments/"+encodeURIComponent(a))}function i(a,d){const u=new URLSearchParams({configure:"deployments",return:`/providers/deployments/deployments/${encodeURIComponent(a)}`});for(const h of d)u.append("claim",`${h.group}/${h.resource}`);s(`/providers?${u.toString()}`)}return(a,d)=>(A(),N("div",{ref_key:"rootRef",ref:o,class:"app"},[r.value?(A(),N(ne,{key:1},[n.value.name?(A(),dt(Gu,{key:0,name:n.value.name,tenant:t.ctx?.tenant,onBack:d[0]||(d[0]=u=>s("deployments")),onAuthorize:d[1]||(d[1]=u=>i(n.value.name,u))},null,8,["name","tenant"])):(A(),dt(tu,{key:1,tenant:t.ctx?.tenant,onOpen:l},null,8,["tenant"]))],64)):(A(),N("section",Yu,[...d[2]||(d[2]=[p("p",{class:"eyebrow"},"Deployments",-1),p("h1",{class:"page-title"},"Select a workspace",-1),p("p",{class:"muted"},"Choose a workspace to inspect Git desired-state synchronization.",-1)])]))],512))}});class Xu extends HTMLElement{vueApp=null;host=null;state=wt({ctx:null});set farosContext(t){this.state.ctx=t}get farosContext(){return this.state.ctx}connectedCallback(){this.vueApp||(this.host=document.createElement("div"),this.host.className="deployments-host",this.appendChild(this.host),this.vueApp=Ca({render:()=>Ln(Qu,{ctx:this.state.ctx})}),this.vueApp.mount(this.host))}disconnectedCallback(){this.vueApp?.unmount(),this.vueApp=null,this.host?.parentNode===this&&this.removeChild(this.host),this.host=null}}const Zu=`/*
 * Deployments is a light-DOM provider bundle. Keep every selector below the
 * custom-element namespace and consume the host Violet Circuit tokens so the
 * portal follows both dark and light themes.
 */

faros-provider-deployments {
  display: block;
  color: var(--color-text-primary);
  font-family: inherit;
}

faros-provider-deployments .app,
faros-provider-deployments .page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

faros-provider-deployments .app {
  padding: 8px 0;
}

faros-provider-deployments .page-head,
faros-provider-deployments .panel-head,
faros-provider-deployments .page-actions,
faros-provider-deployments .detail-actions,
faros-provider-deployments .copy-row,
faros-provider-deployments .runtime-link-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

faros-provider-deployments .page-head,
faros-provider-deployments .panel-head {
  justify-content: space-between;
}

faros-provider-deployments .page-actions {
  align-items: center;
  flex: 0 0 auto;
}

faros-provider-deployments .page-title,
faros-provider-deployments .panel-title,
faros-provider-deployments .subheading {
  margin: 0;
  font-weight: 600;
}

faros-provider-deployments .page-title {
  font-size: 18px;
}

faros-provider-deployments .page-meta,
faros-provider-deployments .muted,
faros-provider-deployments .state-card span,
faros-provider-deployments .condition-summary-item .muted {
  color: var(--color-text-muted);
  font-size: 12px;
  line-height: 1.5;
}

faros-provider-deployments .page-meta {
  margin: 4px 0 0;
}

faros-provider-deployments .eyebrow,
faros-provider-deployments .field-label {
  margin: 0 0 4px;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: .14em;
  text-transform: uppercase;
}

faros-provider-deployments .field-label {
  display: block;
  margin-bottom: 5px;
}

faros-provider-deployments .mono,
faros-provider-deployments code {
  font-family: var(--font-mono);
}

faros-provider-deployments code {
  color: var(--color-text-secondary);
  font-size: 11px;
}

faros-provider-deployments .breakable {
  overflow-wrap: anywhere;
}

faros-provider-deployments .button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-height: 30px;
  border: 1px solid var(--color-border-default);
  border-radius: 4px;
  padding: 5px 10px;
  background: var(--color-surface-overlay);
  color: var(--color-text-primary);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  white-space: nowrap;
}

faros-provider-deployments .button:hover:not(:disabled),
faros-provider-deployments .button:focus-visible {
  border-color: var(--color-accent);
  outline: none;
}

faros-provider-deployments .button.primary {
  border-color: var(--color-accent);
  background: var(--color-accent);
  color: var(--color-surface);
  box-shadow: 0 0 16px var(--color-accent-glow);
}

faros-provider-deployments .button:disabled {
  cursor: wait;
  opacity: .55;
}

faros-provider-deployments .deployment-name-trigger {
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}

faros-provider-deployments .deployment-name-trigger:focus-visible {
  border-radius: 3px;
  outline: 2px solid var(--color-accent);
  outline-offset: 3px;
}

faros-provider-deployments .button.small {
  min-height: 26px;
  padding: 3px 8px;
  font-size: 11px;
}

faros-provider-deployments .button.text-button {
  border-color: transparent;
  padding-inline: 0;
  background: transparent;
  color: var(--color-accent);
}

faros-provider-deployments .spinning {
  animation: deployments-spin .9s linear infinite;
}

faros-provider-deployments .read-contract,
faros-provider-deployments .evidence-banner,
faros-provider-deployments .state-card {
  display: flex;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--color-border-subtle);
  border-radius: 6px;
  padding: 11px 13px;
  background: var(--color-surface-raised);
  font-size: 12px;
  line-height: 1.5;
}

faros-provider-deployments .read-contract {
  color: var(--color-text-secondary);
}

faros-provider-deployments .read-status {
  margin: -4px 0 0;
  color: var(--color-text-muted);
  font-size: 11px;
}

faros-provider-deployments .read-contract-dot {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--color-accent);
}

faros-provider-deployments .state-card {
  align-items: flex-start;
  flex-direction: column;
}

faros-provider-deployments .state-card p {
  margin: 0;
}

faros-provider-deployments .error-card {
  border-color: color-mix(in srgb, var(--color-danger) 40%, transparent);
  color: var(--color-danger);
}

faros-provider-deployments .warning-card {
  border-color: color-mix(in srgb, var(--color-warning) 40%, transparent);
  background: var(--color-warning-subtle);
}

faros-provider-deployments .stale-card {
  align-items: center;
  flex-direction: row;
  border-color: color-mix(in srgb, var(--color-warning) 40%, transparent);
  background: var(--color-warning-subtle);
}

faros-provider-deployments .stale-card strong {
  color: var(--color-warning);
}

faros-provider-deployments .evidence-banner {
  align-items: center;
}

faros-provider-deployments .evidence-banner.evidence-ready,
faros-provider-deployments .evidence-banner.evidence-applied {
  border-color: color-mix(in srgb, var(--color-success) 35%, transparent);
  background: var(--color-success-subtle);
}

faros-provider-deployments .evidence-banner.evidence-awaiting-authorization {
  border-color: color-mix(in srgb, var(--color-warning) 40%, transparent);
  background: var(--color-warning-subtle);
}

faros-provider-deployments .evidence-banner.evidence-failed {
  border-color: color-mix(in srgb, var(--color-danger) 40%, transparent);
  background: var(--color-danger-subtle);
}

faros-provider-deployments .authorization-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border: 1px solid color-mix(in srgb, var(--color-warning) 40%, transparent);
  border-radius: 6px;
  padding: 14px;
  background: var(--color-warning-subtle);
}

faros-provider-deployments .evidence-banner.evidence-invalid,
faros-provider-deployments .evidence-banner.evidence-deleting {
  border-color: color-mix(in srgb, var(--color-danger) 35%, transparent);
  background: var(--color-danger-subtle);
}

faros-provider-deployments .detail-grid {
  display: grid;
  gap: 12px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

faros-provider-deployments .panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: 6px;
  padding: 14px 16px;
  background: var(--color-surface-raised);
}

faros-provider-deployments .panel-title {
  font-size: 14px;
}

faros-provider-deployments .subheading {
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: .1em;
  text-transform: uppercase;
}

faros-provider-deployments .facts,
faros-provider-deployments .outputs-list {
  display: flex;
  flex-direction: column;
  gap: 9px;
  margin: 0;
}

faros-provider-deployments .facts > div,
faros-provider-deployments .outputs-list > div {
  display: grid;
  gap: 8px;
  grid-template-columns: minmax(105px, 0.7fr) minmax(0, 1.3fr);
}

faros-provider-deployments .facts dt,
faros-provider-deployments .outputs-list dt {
  color: var(--color-text-muted);
  font-size: 11px;
}

faros-provider-deployments .facts dd,
faros-provider-deployments .outputs-list dd {
  min-width: 0;
  margin: 0;
  color: var(--color-text-primary);
  font-size: 12px;
}

faros-provider-deployments .artifact-list {
  display: flex;
  flex-direction: column;
  gap: 7px;
  margin: 0;
  padding: 0;
  list-style: none;
}

faros-provider-deployments .artifact-list li {
  display: grid;
  gap: 8px;
  grid-template-columns: 90px minmax(0, 1fr);
  border-bottom: 1px solid var(--color-border-subtle);
  padding-bottom: 7px;
  font-size: 11px;
}

faros-provider-deployments .artifact-list li:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}

faros-provider-deployments .condition-summary {
  display: grid;
  gap: 8px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

faros-provider-deployments .condition-summary-item {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  border: 1px solid var(--color-border-subtle);
  border-radius: 4px;
  padding: 10px;
}

faros-provider-deployments .condition-summary-label {
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: .1em;
  text-transform: uppercase;
}

faros-provider-deployments .runtime-link-row {
  align-items: center;
  flex-wrap: wrap;
  border-top: 1px solid var(--color-border-subtle);
  padding-top: 10px;
}

faros-provider-deployments .runtime-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1 1 180px;
  color: var(--color-accent);
  font-size: 11px;
  text-decoration: none;
}

faros-provider-deployments .runtime-link:hover {
  text-decoration: underline;
}

faros-provider-deployments .link-text {
  color: var(--color-accent);
}

faros-provider-deployments .rollout-value {
  color: var(--color-text-secondary);
  font-size: 11px;
}

faros-provider-deployments .create-sync-panel {
  max-width: 720px;
}

faros-provider-deployments .create-description {
  margin: 4px 0 0;
  color: var(--color-text-muted);
  font-size: 12px;
  line-height: 1.5;
}

faros-provider-deployments .icon-button {
  width: 30px;
  min-width: 30px;
  padding: 0;
}

faros-provider-deployments .sync-form,
faros-provider-deployments .create-field {
  display: flex;
  flex-direction: column;
}

faros-provider-deployments .sync-form {
  gap: 14px;
}

faros-provider-deployments .create-field {
  min-width: 0;
  gap: 5px;
}

faros-provider-deployments .field-row {
  display: grid;
  gap: 12px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

faros-provider-deployments .field-input {
  width: 100%;
  min-height: 34px;
  box-sizing: border-box;
  border: 1px solid var(--color-border-default);
  border-radius: 4px;
  padding: 7px 9px;
  background: var(--color-surface-overlay);
  color: var(--color-text-primary);
  font: inherit;
  font-size: 12px;
}

faros-provider-deployments .field-input::placeholder {
  color: var(--color-text-muted);
}

faros-provider-deployments .field-input:focus-visible {
  border-color: var(--color-accent);
  outline: 3px solid var(--color-accent-subtle);
  outline-offset: 1px;
  box-shadow: 0 0 12px var(--color-accent-glow);
}

faros-provider-deployments .field-input[aria-invalid="true"] {
  border-color: var(--color-danger);
}

faros-provider-deployments .field-hint,
faros-provider-deployments .field-error,
faros-provider-deployments .checkbox-field small {
  font-size: 11px;
  line-height: 1.45;
}

faros-provider-deployments .field-hint,
faros-provider-deployments .checkbox-field small {
  color: var(--color-text-muted);
}

faros-provider-deployments .field-error {
  color: var(--color-danger);
}

faros-provider-deployments .interval-field {
  max-width: 300px;
}

faros-provider-deployments .input-unit,
faros-provider-deployments .checkbox-field,
faros-provider-deployments .form-actions {
  display: flex;
  align-items: center;
}

faros-provider-deployments .input-unit {
  gap: 8px;
}

faros-provider-deployments .input-unit .field-input {
  min-width: 0;
}

faros-provider-deployments .input-unit > span {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
}

faros-provider-deployments .checkbox-field {
  align-items: flex-start;
  gap: 8px;
  color: var(--color-text-secondary);
  cursor: pointer;
  font-size: 12px;
}

faros-provider-deployments .checkbox-field input {
  margin: 2px 0 0;
  accent-color: var(--color-accent);
}

faros-provider-deployments .checkbox-field span {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

faros-provider-deployments .checkbox-field strong {
  color: var(--color-text-primary);
  font-weight: 500;
}

faros-provider-deployments .error-summary {
  border: 1px solid color-mix(in srgb, var(--color-danger) 40%, transparent);
  border-radius: 6px;
  padding: 9px 11px;
  background: var(--color-danger-subtle);
  color: var(--color-danger);
  font-size: 12px;
  line-height: 1.5;
}

faros-provider-deployments .form-actions {
  justify-content: flex-end;
  gap: 10px;
  border-top: 1px solid var(--color-border-subtle);
  margin-top: 2px;
  padding-top: 14px;
}

/* Canonical PortalKit recipes are namespaced here for direct provider visits;
   when loaded in the host, the host's faros-ui.css provides the same classes. */
faros-provider-deployments .resource-table {
  min-width: 0;
  border: 1px solid var(--color-border-subtle);
  border-radius: 6px;
  overflow: hidden;
  background: var(--color-surface-raised);
}

faros-provider-deployments .resource-table-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12px;
}

faros-provider-deployments .resource-table-heading {
  padding: 9px 11px;
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: .08em;
  text-align: left;
  text-transform: uppercase;
  white-space: nowrap;
}

faros-provider-deployments .resource-table-cell {
  padding: 10px 11px;
  border-bottom: 1px solid var(--color-border-subtle);
  color: var(--color-text-secondary);
  vertical-align: middle;
}

faros-provider-deployments .resource-table-row.is-interactive {
  cursor: pointer;
}

faros-provider-deployments .resource-table-row.is-interactive:hover,
faros-provider-deployments .resource-table-row.is-interactive:focus-within {
  background: color-mix(in srgb, var(--color-accent) 5%, transparent);
}

faros-provider-deployments .resource-table-row:last-child .resource-table-cell {
  border-bottom: 0;
}

faros-provider-deployments .resource-table-empty-cell {
  padding: 32px 14px;
  color: var(--color-text-muted);
  text-align: center;
}

faros-provider-deployments .resource-table-empty-label {
  margin: 8px 0 0;
  font-size: 12px;
}

faros-provider-deployments .resource-table-empty-icon {
  color: var(--color-text-muted);
}

faros-provider-deployments .resource-table-loading,
faros-provider-deployments .detail-skeleton {
  padding: 16px;
}

faros-provider-deployments .resource-table-loading-row,
faros-provider-deployments .resource-table-loading-head {
  display: flex;
  gap: 12px;
  padding: 10px 0;
}

faros-provider-deployments .resource-table-skeleton,
faros-provider-deployments .skeleton-line,
faros-provider-deployments .skeleton-block {
  border-radius: 3px;
  background: var(--color-surface-overlay);
}

faros-provider-deployments .resource-table-skeleton {
  height: 12px;
}

faros-provider-deployments .resource-table-skeleton-wide {
  width: 35%;
}

faros-provider-deployments .resource-table-skeleton-mid {
  width: 25%;
}

faros-provider-deployments .resource-table-skeleton-small {
  width: 15%;
}

faros-provider-deployments .resource-table-skeleton-short {
  width: 20%;
}

faros-provider-deployments .resource-table-error,
faros-provider-deployments .resource-table-stale {
  display: flex;
  align-items: center;
  gap: 8px;
  border-bottom: 1px solid color-mix(in srgb, var(--color-warning) 35%, transparent);
  padding: 10px 12px;
  background: var(--color-warning-subtle);
  color: var(--color-warning);
  font-size: 12px;
}

faros-provider-deployments .resource-table-retry {
  margin-left: auto;
  border: 0;
  background: transparent;
  color: var(--color-accent);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
}

faros-provider-deployments .status-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  border: 1px solid color-mix(in srgb, currentColor 35%, transparent);
  border-radius: 3px;
  padding: 3px 6px;
  background: var(--color-surface-overlay);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: .05em;
  line-height: 1;
  text-transform: uppercase;
  white-space: nowrap;
}

faros-provider-deployments .status-badge-dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
}

faros-provider-deployments .tone-success { color: var(--color-success); background: var(--color-success-subtle); }
faros-provider-deployments .tone-warning { color: var(--color-warning); background: var(--color-warning-subtle); }
faros-provider-deployments .tone-danger { color: var(--color-danger); background: var(--color-danger-subtle); }
faros-provider-deployments .tone-muted { color: var(--color-text-muted); }
faros-provider-deployments .status-badge-dot-wrap { display: inline-flex; }
faros-provider-deployments .status-badge-pulse { display: inline-block; }

faros-provider-deployments .conditions-panel {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

faros-provider-deployments .conditions-title {
  margin: 4px 0 0;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 10px;
  letter-spacing: .1em;
  text-transform: uppercase;
}

faros-provider-deployments .conditions-stale {
  margin: 0;
  color: var(--color-warning);
  font-size: 11px;
}

faros-provider-deployments .conditions-message {
  display: block;
  max-width: 240px;
  overflow-wrap: anywhere;
}

faros-provider-deployments .conditions-muted {
  color: var(--color-text-muted);
}

faros-provider-deployments .skeleton-line {
  height: 14px;
  margin-bottom: 12px;
}

faros-provider-deployments .skeleton-wide { width: 45%; }

faros-provider-deployments .skeleton-block {
  height: 240px;
}

faros-provider-deployments .shimmer {
  animation: deployments-shimmer 1.5s ease-in-out infinite;
  background: linear-gradient(90deg, var(--color-surface-overlay), var(--color-surface-hover), var(--color-surface-overlay));
  background-size: 200% 100%;
}

@keyframes deployments-spin { to { transform: rotate(360deg); } }
@keyframes deployments-shimmer { to { background-position: -200% 0; } }

@media (max-width: 900px) {
  faros-provider-deployments .detail-grid { grid-template-columns: 1fr; }
  faros-provider-deployments .resource-table { overflow-x: auto; }
  faros-provider-deployments .resource-table-table { min-width: 850px; }
}

@media (max-width: 560px) {
  faros-provider-deployments .page-head,
  faros-provider-deployments .page-actions,
  faros-provider-deployments .detail-head,
  faros-provider-deployments .stale-card { align-items: flex-start; flex-direction: column; }
  faros-provider-deployments .condition-summary { grid-template-columns: 1fr; }
  faros-provider-deployments .field-row { grid-template-columns: 1fr; }
  faros-provider-deployments .facts > div,
  faros-provider-deployments .outputs-list > div,
  faros-provider-deployments .artifact-list li { grid-template-columns: 1fr; gap: 3px; }
}

@media (prefers-reduced-motion: reduce) {
  faros-provider-deployments .spinning,
  faros-provider-deployments .shimmer { animation: none; }
}
`,Hr="faros-provider-deployments";if(!customElements.get(Hr)){const e=`${Hr}-css`;if(!document.getElementById(e)){const t=document.createElement("style");t.id=e,t.textContent=Zu,document.head.appendChild(t)}customElements.define(Hr,Xu)}})();
