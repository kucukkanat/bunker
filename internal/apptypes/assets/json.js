// Bunker JSON viewer: collapsible tree, find, raw view.
(function(){
  var raw=document.getElementById('bk-data').textContent,data=JSON.parse(raw),tree=document.getElementById('tree'),rawv=document.getElementById('rawv');
  var LIMIT=500;
  function t(cls,s){var e=document.createElement('span');e.className=cls;e.textContent=s;return e}
  function leaf(v){if(v===null)return t('null','null');switch(typeof v){case 'string':return t('str',JSON.stringify(v));case 'number':return t('num',String(v));case 'boolean':return t('bool',String(v))}return t('',String(v))}
  function node(key,v,depth){
    var isArr=Array.isArray(v);
    if(v===null||typeof v!=='object'){var row=document.createElement('div');row.className='row';if(key!=null){row.appendChild(t('key',key));row.appendChild(document.createTextNode(': '))}row.appendChild(leaf(v));return row}
    var keys=isArr?null:Object.keys(v),n=isArr?v.length:keys.length;
    var d=document.createElement('details');if(depth<2)d.open=true;
    var s=document.createElement('summary');if(key!=null){s.appendChild(t('key',key));s.appendChild(document.createTextNode(': '))}
    s.appendChild(document.createTextNode(isArr?'[':'{'));s.appendChild(t('cnt',n+(isArr?' item':' key')+(n===1?'':'s')));d.appendChild(s);
    var filled=false;function fill(){if(filled)return;filled=true;var max=Math.min(n,LIMIT);
      for(var i=0;i<max;i++){var k=isArr?i:keys[i];d.appendChild(node(isArr?String(i):k,v[k],depth+1))}
      if(n>max){var more=document.createElement('div');more.className='row muted';more.textContent='… '+(n-max)+' more';d.appendChild(more)}
      var end=document.createElement('div');end.textContent=isArr?']':'}';d.appendChild(end)}
    if(d.open)fill();d.addEventListener('toggle',function(){if(d.open)fill()});return d}
  tree.appendChild(node(null,data,0));
  function all(open){tree.querySelectorAll('details').forEach(function(d){d.open=open})}
  document.getElementById('expand').onclick=function(){for(var k=0;k<6;k++)all(true)};
  document.getElementById('collapse').onclick=function(){all(false);var r=tree.querySelector('details');if(r)r.open=true};
  var rb=document.getElementById('raw');rb.onclick=function(){var on=rb.getAttribute('aria-pressed')!=='true';rb.setAttribute('aria-pressed',on);
    if(on&&!rawv.textContent)rawv.textContent=JSON.stringify(data,null,2);rawv.hidden=!on;tree.hidden=on};
  var q=document.getElementById('q'),timer;
  q.addEventListener('input',function(){clearTimeout(timer);timer=setTimeout(function(){
    tree.querySelectorAll('.hit').forEach(function(e){e.classList.remove('hit')});var s=q.value.trim().toLowerCase();if(!s)return;
    for(var k=0;k<6;k++)all(true);var first=null;
    tree.querySelectorAll('.key,.str,.num,.bool').forEach(function(e){if(e.textContent.toLowerCase().indexOf(s)>=0){e.classList.add('hit');if(!first)first=e}});
    if(first)first.scrollIntoView({block:'center'})},250)});
})();
