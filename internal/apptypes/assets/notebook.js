// Bunker notebook: size sandboxed HTML outputs to their content.
(function(){
  var frames=[].slice.call(document.querySelectorAll('.output iframe'));
  addEventListener('message',function(e){
    if(!e.data||typeof e.data.nbh!=='number')return;
    for(var i=0;i<frames.length;i++){if(frames[i].contentWindow===e.source){frames[i].style.height=Math.min(4000,e.data.nbh+4)+'px';break}}
  });
})();
