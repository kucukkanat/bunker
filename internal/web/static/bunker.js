// Stub SDK.
(function(){
  var n=0,p={};
  window.addEventListener('message',function(e){if(e.source!==parent)return;var m=e.data||{};if(m.bk==='res'&&p[m.id]){p[m.id](m);delete p[m.id]}});
  function call(op,args){return new Promise(function(res,rej){var id=++n;p[id]=function(m){m.ok?res(m.data):rej(m.error)};parent.postMessage({bk:'req',id:id,op:op,args:args},'*')})}
  window.bunker={me:function(){return call('me')}};
})();
