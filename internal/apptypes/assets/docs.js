// Bunker docs: mobile menu, current-section highlight, copy buttons.
(function(){
  var menu=document.getElementById('menu'),side=document.getElementById('side');
  if(menu&&side){
    menu.addEventListener('click',function(){var o=side.classList.toggle('open');menu.setAttribute('aria-expanded',o)});
    side.addEventListener('click',function(e){if(e.target.tagName==='A')side.classList.remove('open')});
    document.addEventListener('keydown',function(e){if(e.key==='Escape')side.classList.remove('open')});
  }
  document.querySelectorAll('.prose pre').forEach(function(pre){
    var code=pre.querySelector('code');if(!code)return;
    var b=document.createElement('button');b.className='btn';b.textContent='Copy';b.type='button';
    b.style.cssText='position:absolute;top:6px;right:6px;padding:2px 8px;font-size:.75rem;opacity:0;transition:opacity .15s';
    pre.addEventListener('mouseenter',function(){b.style.opacity=1});pre.addEventListener('mouseleave',function(){b.style.opacity=0});
    b.addEventListener('focus',function(){b.style.opacity=1});
    b.addEventListener('click',function(){var t=code.innerText;
      var p=navigator.clipboard?navigator.clipboard.writeText(t):Promise.reject();
      p.then(function(){b.textContent='Copied'},function(){
        var r=document.createRange();r.selectNodeContents(code);var s=getSelection();s.removeAllRanges();s.addRange(r);b.textContent='Press Ctrl+C'});
      setTimeout(function(){b.textContent='Copy'},1500)});
    pre.appendChild(b);if(pre.dataset.lang)pre.style.paddingTop='26px';
  });
  var links=[].slice.call(document.querySelectorAll('aside.toc a'));
  if(links.length&&'IntersectionObserver' in window){
    var map={};links.forEach(function(a){map[decodeURIComponent(a.hash.slice(1))]=a});
    var io=new IntersectionObserver(function(es){es.forEach(function(e){if(e.isIntersecting){links.forEach(function(a){a.style.color=''});var a=map[e.target.id];if(a)a.style.color='var(--text)'}})},{rootMargin:'0px 0px -70% 0px'});
    document.querySelectorAll('.prose h2[id],.prose h3[id]').forEach(function(h){io.observe(h)});
  }
})();
