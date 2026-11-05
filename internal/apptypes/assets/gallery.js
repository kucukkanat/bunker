// Bunker gallery: lightbox with keyboard, swipe and focus handling.
(function(){
  var items=[].slice.call(document.querySelectorAll('#gallery figure')),lb=document.getElementById('lb'),body=document.getElementById('lb-body'),cap=document.getElementById('lb-cap'),i=-1,last=null;
  function show(n){i=(n+items.length)%items.length;var f=items[i];body.innerHTML='';
    var m=document.createElement(f.dataset.kind==='video'?'video':'img');m.src=f.dataset.src;
    if(f.dataset.kind==='video'){m.controls=true;m.autoplay=true;m.playsInline=true}else m.alt=f.dataset.cap;
    body.appendChild(m);cap.textContent=f.dataset.cap+'  ·  '+(i+1)+' / '+items.length;
    var next=items[(i+1)%items.length];if(next&&next.dataset.kind==='image'){var pre=new Image();pre.src=next.dataset.src}}
  function open(n){last=document.activeElement;lb.classList.add('open');show(n);lb.querySelector('.close').focus()}
  function close(){lb.classList.remove('open');body.innerHTML='';if(last)last.focus()}
  items.forEach(function(f,n){f.addEventListener('click',function(){open(n)});f.addEventListener('keydown',function(e){if(e.key==='Enter'||e.key===' '){e.preventDefault();open(n)}})});
  lb.querySelector('.close').onclick=close;lb.querySelector('.prev').onclick=function(){show(i-1)};lb.querySelector('.next').onclick=function(){show(i+1)};
  lb.addEventListener('click',function(e){if(e.target===lb||e.target===body)close()});
  document.addEventListener('keydown',function(e){if(!lb.classList.contains('open'))return;
    if(e.key==='Escape')close();else if(e.key==='ArrowRight')show(i+1);else if(e.key==='ArrowLeft')show(i-1)});
  var tx=null;lb.addEventListener('touchstart',function(e){tx=e.touches[0].clientX},{passive:true});
  lb.addEventListener('touchend',function(e){if(tx==null)return;var dx=e.changedTouches[0].clientX-tx;if(Math.abs(dx)>40)show(dx<0?i+1:i-1);tx=null});
  if(items.length<2){lb.querySelector('.prev').hidden=true;lb.querySelector('.next').hidden=true}
})();
