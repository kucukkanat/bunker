// Bunker slides: keyboard/touch navigation, scaling, hash deep links.
(function(){
  var slides=[].slice.call(document.querySelectorAll('.slide'));if(!slides.length)return;
  var bar=document.getElementById('bar'),i=0;
  function fit(){var s=Math.min(innerWidth/1280,innerHeight/720)*0.98;slides.forEach(function(el){el.style.transform='scale('+s+')'})}
  function show(n){i=Math.max(0,Math.min(slides.length-1,n));
    slides.forEach(function(el,k){el.classList.toggle('active',k===i);el.setAttribute('aria-hidden',k!==i)});
    if(bar)bar.style.width=((i+1)/slides.length*100)+'%';
    try{history.replaceState(null,'','#'+(i+1))}catch(e){}}
  addEventListener('resize',fit);fit();
  var h=parseInt((location.hash||'#1').slice(1),10);show(isNaN(h)?0:h-1);
  addEventListener('keydown',function(e){
    var k=e.key;if(e.target.closest&&e.target.closest('input,textarea'))return;
    if(k==='ArrowRight'||k==='PageDown'||k===' '||k==='Enter'||k==='ArrowDown'||k==='l'){e.preventDefault();show(i+1)}
    else if(k==='ArrowLeft'||k==='PageUp'||k==='Backspace'||k==='ArrowUp'||k==='h'){e.preventDefault();show(i-1)}
    else if(k==='Home'){e.preventDefault();show(0)}else if(k==='End'){e.preventDefault();show(slides.length-1)}
    else if(k==='f'){full()}
  });
  function full(){var d=document.documentElement;if(document.fullscreenElement)document.exitFullscreen();else if(d.requestFullscreen)d.requestFullscreen().catch(function(){})}
  function on(id,f){var el=document.getElementById(id);if(el)el.onclick=function(e){e.stopPropagation();f()}}
  on('prev',function(){show(i-1)});on('next',function(){show(i+1)});on('full',full);
  document.getElementById('deck').addEventListener('click',function(e){if(e.target.closest('a,button,input,video,audio,pre'))return;show(e.clientX<innerWidth/3?i-1:i+1)});
  var tx=null;addEventListener('touchstart',function(e){tx=e.touches[0].clientX},{passive:true});
  addEventListener('touchend',function(e){if(tx===null)return;var dx=e.changedTouches[0].clientX-tx;if(Math.abs(dx)>40)show(dx<0?i+1:i-1);tx=null});
})();
