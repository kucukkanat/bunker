// Stub — replaced by the SDK bridge.
(function(){
  var frame=document.getElementById('bk-frame');
  window.addEventListener('message',function(e){
    if(e.source!==frame.contentWindow) return;
    var m=e.data||{};
    if(m.bk!=='req') return;
    var cfg=document.getElementById('bk-shell').dataset;
    if(m.op==='me'){fetch('/api/v1/me').then(function(r){return r.json()}).then(function(d){frame.contentWindow.postMessage({bk:'res',id:m.id,ok:true,data:d},'*')})}
  });
})();
