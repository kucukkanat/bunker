// Bunker media player with playlist.
(function(){
  var btns=[].slice.call(document.querySelectorAll('#list button')),player=document.getElementById('player'),now=document.getElementById('now'),dl=document.getElementById('dl'),i=0;
  function play(n,auto){i=n;var b=btns[i];if(!b)return;player.innerHTML='';
    var m=document.createElement(b.dataset.kind);m.controls=true;m.preload='metadata';m.src=b.dataset.src;if(b.dataset.kind==='video')m.playsInline=true;
    m.addEventListener('ended',function(){if(i+1<btns.length)play(i+1,true)});
    player.appendChild(m);if(auto)m.play().catch(function(){});
    now.textContent=b.dataset.name;dl.href=b.dataset.src;dl.setAttribute('download',b.dataset.name);
    btns.forEach(function(x,k){x.setAttribute('aria-current',k===i)})}
  btns.forEach(function(b,n){b.onclick=function(){play(n,true)}});
  play(0,false);
})();
