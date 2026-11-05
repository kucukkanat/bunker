// Bunker table app: sort, filter, search, stats, SVG charts, export.
(function(){
  'use strict';
  var D=JSON.parse(document.getElementById('bk-data').textContent);
  var $=function(id){return document.getElementById(id)};
  var S={sheet:0,q:'',sort:-1,dir:1,filters:{},shown:200,showFilters:false,chart:false,cv:-1,cg:-1,agg:'sum'};
  var sheet,view=[];

  function el(tag,attrs,kids){var e=document.createElement(tag);if(attrs)for(var k in attrs){if(k==='text')e.textContent=attrs[k];else if(k.slice(0,2)==='on')e.addEventListener(k.slice(2),attrs[k]);else e.setAttribute(k,attrs[k])}
    (kids||[]).forEach(function(c){if(c!=null)e.appendChild(typeof c==='string'?document.createTextNode(c):c)});return e}
  function fmt(c,v){if(v==null)return '';
    if(c.type==='number'){var o={maximumFractionDigits:c.dec||0};if(c.prefix&&c.dec)o.minimumFractionDigits=c.dec;
      return (v<0?'-':'')+(c.prefix||'')+Math.abs(v).toLocaleString(undefined,o)+(c.suffix||'')}
    if(c.type==='bool')return v?'✓ Yes':'— No';
    return String(v)}
  function num(n,dec){return n.toLocaleString(undefined,{maximumFractionDigits:dec==null?2:dec})}

  // Filter expressions: "foo", ">10", "<=5", "10..20", "!x", "=exact"
  function matcher(c,expr){expr=expr.trim();if(!expr)return null;
    var cmp=function(a,b){return a<b?-1:a>b?1:0};
    var parse=c.type==='number'?function(s){var n=parseFloat(s.replace(/[,$€£%\s]/g,''));return isNaN(n)?null:n}:function(s){return s.trim()};
    var m;
    if((m=expr.match(/^(.+?)\s*\.\.\s*(.+)$/))){var lo=parse(m[1]),hi=parse(m[2]);return function(v){return v!=null&&cmp(v,lo)>=0&&cmp(v,hi)<=0}}
    if((m=expr.match(/^(>=|<=|>|<)\s*(.+)$/))){var x=parse(m[2]),op=m[1];if(x==null)return null;
      return function(v){if(v==null)return false;var r=cmp(v,x);return op==='>'?r>0:op==='<'?r<0:op==='>='?r>=0:r<=0}}
    if(expr[0]==='='){var e=expr.slice(1).toLowerCase();return function(v){return String(v==null?'':v).toLowerCase()===e}}
    if(expr[0]==='!'){var n2=expr.slice(1).toLowerCase();return function(v){return String(fmt(c,v)).toLowerCase().indexOf(n2)<0}}
    var low=expr.toLowerCase();return function(v){return v!=null&&String(fmt(c,v)).toLowerCase().indexOf(low)>=0}}

  function compute(){
    var cols=sheet.columns,q=S.q.toLowerCase(),fs=[];
    for(var k in S.filters){var f=matcher(cols[k],S.filters[k]);if(f)fs.push([+k,f])}
    view=[];
    for(var i=0;i<sheet.rows.length;i++){var r=sheet.rows[i],ok=true;
      for(var j=0;j<fs.length&&ok;j++)ok=fs[j][1](r[fs[j][0]]);
      if(ok&&q){ok=false;for(var c=0;c<r.length;c++){if(r[c]!=null&&String(r[c]).toLowerCase().indexOf(q)>=0){ok=true;break}}}
      if(ok)view.push(r)}
    if(S.sort>=0){var s=S.sort,d=S.dir;view.sort(function(a,b){var x=a[s],y=b[s];if(x==null&&y==null)return 0;if(x==null)return 1;if(y==null)return -1;
      if(typeof x==='string'&&typeof y==='string')return d*x.localeCompare(y,undefined,{numeric:true,sensitivity:'base'});return d*(x<y?-1:x>y?1:0)})}
  }

  function renderHead(){
    var cols=sheet.columns,thead=$('grid').tHead;thead.innerHTML='';
    var tr=el('tr',null,[el('th',{'aria-label':'Row'},[el('span',{style:'display:block;padding:9px 12px',text:'#'})])]);
    cols.forEach(function(c,i){
      var arrow=S.sort===i?(S.dir>0?'▲':'▼'):'';
      var th=el('th',{'aria-sort':S.sort===i?(S.dir>0?'ascending':'descending'):'none',scope:'col'},[
        el('button',{type:'button',title:'Sort by '+c.name,onclick:function(){if(S.sort!==i){S.sort=i;S.dir=1}else if(S.dir===1)S.dir=-1;else S.sort=-1;update()}},
          [el('span',{text:c.name}),el('span',{class:'ty',text:c.type==='number'?'#':c.type==='date'?'date':c.type==='bool'?'y/n':'abc'}),arrow?el('span',{class:'arrow',text:arrow}):null])]);
      if(S.showFilters){var box=el('div',{class:'flt'}),vals=distinct(i);
        if(c.type!=='number'&&vals&&vals.length<=25){var sel=el('select',{'aria-label':'Filter '+c.name,onchange:function(){S.filters[i]=sel.value?'='+sel.value:'';S.shown=200;update(true)}},[el('option',{value:'',text:'All'})]);
          vals.forEach(function(v){var o=el('option',{value:String(v),text:fmt(c,v)});if(S.filters[i]==='='+String(v).toLowerCase()||S.filters[i]==='='+v)o.selected=true;sel.appendChild(o)});box.appendChild(sel)}
        else{var inp=el('input',{type:'search','aria-label':'Filter '+c.name,placeholder:c.type==='number'?'>10, 5..20':c.type==='date'?'>2024-01-01':'contains…',value:S.filters[i]||''});
          inp.addEventListener('input',debounce(function(){S.filters[i]=inp.value;S.shown=200;update(true)},200));box.appendChild(inp)}
        th.appendChild(box)}
      tr.appendChild(th)});
    thead.appendChild(tr)}
  var dcache={};
  function distinct(i){var key=S.sheet+':'+i;if(key in dcache)return dcache[key];var m={},n=0;
    for(var r=0;r<sheet.rows.length;r++){var v=sheet.rows[r][i];if(v==null)continue;if(!(v in m)){m[v]=1;if(++n>25)return dcache[key]=null}}
    return dcache[key]=Object.keys(m).sort(function(a,b){return a.localeCompare(b,undefined,{numeric:true})}).map(function(k){return sheet.columns[i].type==='bool'?k==='true':k})}

  function renderBody(){
    var cols=sheet.columns,tb=$('grid').tBodies[0],frag=document.createDocumentFragment(),n=Math.min(view.length,S.shown);
    for(var i=0;i<n;i++){var r=view[i],tr=document.createElement('tr');var td0=document.createElement('td');td0.className='rn';td0.textContent=i+1;tr.appendChild(td0);
      for(var c=0;c<cols.length;c++){var td=document.createElement('td');if(cols[c].type==='number')td.className='num';var t=fmt(cols[c],r[c]);td.textContent=t;if(t.length>40)td.title=t;tr.appendChild(td)}
      frag.appendChild(tr)}
    tb.innerHTML='';tb.appendChild(frag);
    if(!view.length){var tr2=el('tr',null,[el('td',{colspan:cols.length+1,class:'empty',text:'No rows match. Try clearing the search or filters.'})]);tb.appendChild(tr2)}
    var more=$('more');more.innerHTML='';
    if(view.length>n){more.appendChild(document.createTextNode('Showing '+num(n,0)+' of '+num(view.length,0)));
      more.appendChild(el('button',{class:'btn',type:'button',text:'Show more',onclick:function(){S.shown+=500;renderBody()}}));
      if(view.length<=20000)more.appendChild(el('button',{class:'btn',type:'button',text:'Show all',onclick:function(){S.shown=view.length;renderBody()}}))}
    $('count').textContent=(view.length===sheet.rows.length?num(view.length,0)+' rows':num(view.length,0)+' of '+num(sheet.rows.length,0)+' rows')+(sheet.total>sheet.rows.length?' (first '+num(sheet.rows.length,0)+' of '+num(sheet.total,0)+' loaded)':'');
  }

  function renderStats(){
    var box=$('stats');box.innerHTML='';var cols=sheet.columns;
    box.appendChild(el('div',{class:'stat'},[el('b',{text:'Rows'}),el('span',{text:num(view.length,0)})]));
    var shown=0;cols.forEach(function(c,i){if(c.type!=='number'||shown>=3)return;
      var sum=0,cnt=0,min=Infinity,max=-Infinity;view.forEach(function(r){var v=r[i];if(typeof v==='number'){sum+=v;cnt++;if(v<min)min=v;if(v>max)max=v}});
      if(!cnt)return;shown++;
      var isTotal=/amount|total|cost|price|revenue|sales|spend|budget|qty|quantity|count|hours/i.test(c.name)&&!/%/.test(c.suffix||'');
      box.appendChild(el('div',{class:'stat',title:'min '+fmt(c,min)+' · max '+fmt(c,max)},[el('b',{text:(isTotal?'Total ':'Average ')+c.name}),el('span',{text:fmt(c,isTotal?sum:sum/cnt)})]))});
  }

  // ---- charts (inline SVG) ----
  function renderChart(){
    var box=$('chart');box.hidden=!S.chart;if(!S.chart)return;box.innerHTML='';
    var cols=sheet.columns,numCols=[],grpCols=[];
    cols.forEach(function(c,i){if(c.type==='number')numCols.push(i);else grpCols.push(i)});
    if(S.cv<0||cols[S.cv].type!=='number')S.cv=numCols.length?numCols[0]:-1;
    if(S.cg>=cols.length)S.cg=-1;
    if(S.cg===-1&&grpCols.length){var best=-1,bs=-1;grpCols.forEach(function(i){var d=distinct(i),n=d?d.length:1e6,t=cols[i].type;
      var sc=t==='text'?(n>1&&n<=25?3:1):t==='date'?2:n>1?1.5:0;if(sc>bs){bs=sc;best=i}});S.cg=best}
    var bar=el('div',{class:'toolbar'});
    function sel(label,opts,val,on){var s=el('select',{class:'input','aria-label':label});opts.forEach(function(o){var op=el('option',{value:o[0],text:o[1]});if(String(o[0])===String(val))op.selected=true;s.appendChild(op)});s.onchange=function(){on(s.value);renderChart()};return el('label',{class:'muted'},[label+' ',s])}
    var aggOpts=[['count','Count of rows']];if(numCols.length){aggOpts.unshift(['sum','Sum'],['avg','Average'])}
    bar.appendChild(sel('Show',aggOpts,S.agg,function(v){S.agg=v}));
    if(S.agg!=='count'&&numCols.length)bar.appendChild(sel('of',numCols.map(function(i){return[i,cols[i].name]}),S.cv,function(v){S.cv=+v}));
    bar.appendChild(sel('by',[[-2,'— distribution —']].concat(cols.map(function(c,i){return[i,c.name]})),S.cg,function(v){S.cg=+v}));
    box.appendChild(bar);
    var data=S.cg===-2?histogram():groups();
    if(!data.length){box.appendChild(el('p',{class:'muted',text:'Nothing to chart for these rows.'}));return}
    box.appendChild(barChart(data,S.agg==='count'?{type:'number'}:cols[S.cv]||{type:'number'}));
  }
  function groups(){
    var cols=sheet.columns,g=S.cg,v=S.cv,m={},order=[];if(g<0)return[];var gc=cols[g];
    var bucket=function(x){if(x==null)return '(blank)';if(gc.type==='date'){var s=String(x);return gc._month?s.slice(0,7):s.slice(0,10)}if(gc.type==='number'&&!gc._few)return null;return fmt(gc,x)};
    if(gc.type==='date'){var d=distinct(g);gc._month=!d||d.length>24}
    if(gc.type==='number'){var dd=distinct(g);gc._few=!!dd}
    view.forEach(function(r){var k=bucket(r[g]);if(k==null)return;if(!(k in m)){m[k]={k:k,s:0,n:0};order.push(m[k])}var e=m[k];
      if(S.agg==='count'){e.n++;return}var x=r[v];if(typeof x==='number'){e.s+=x;e.n++}});
    var out=order.map(function(e){return{label:e.k,value:S.agg==='count'?e.n:S.agg==='avg'?(e.n?e.s/e.n:0):e.s}});
    if(gc.type==='date'||gc.type==='number')out.sort(function(a,b){return a.label.localeCompare(b.label,undefined,{numeric:true})});
    else{out.sort(function(a,b){return b.value-a.value});if(out.length>24){var rest=out.slice(23).reduce(function(t,e){return t+e.value},0);out=out.slice(0,23);if(S.agg!=='avg')out.push({label:'Other',value:rest})}}
    return out}
  function histogram(){
    var v=S.agg==='count'&&S.cv<0?-1:S.cv;if(v<0)return[];var xs=[];view.forEach(function(r){if(typeof r[v]==='number')xs.push(r[v])});if(!xs.length)return[];
    var min=Math.min.apply(null,xs),max=Math.max.apply(null,xs);if(min===max)return[{label:fmt(sheet.columns[v],min),value:xs.length}];
    var bins=Math.min(16,Math.max(5,Math.ceil(Math.sqrt(xs.length)))),w=(max-min)/bins,cnt=new Array(bins).fill(0);
    xs.forEach(function(x){cnt[Math.min(bins-1,Math.floor((x-min)/w))]++});
    var c=sheet.columns[v];return cnt.map(function(n,i){return{label:fmt(c,min+i*w)+'–'+fmt(c,min+(i+1)*w),value:n}})}
  function barChart(data,col){
    var NS='http://www.w3.org/2000/svg',W=900,H=240,L=70,B=60,T=12,max=0;data.forEach(function(d){if(d.value>max)max=d.value});if(max<=0)max=1;
    var s=document.createElementNS(NS,'svg');s.setAttribute('viewBox','0 0 '+W+' '+H);s.setAttribute('role','img');
    s.setAttribute('aria-label','Bar chart with '+data.length+' bars');
    function n(t,a,txt){var e=document.createElementNS(NS,t);for(var k in a)e.setAttribute(k,a[k]);if(txt!=null)e.textContent=txt;s.appendChild(e);return e}
    var step=niceStep(max/4);for(var y=0;y<=max+1e-9;y+=step){var py=H-B-(y/max)*(H-B-T);n('line',{x1:L,x2:W-8,y1:py,y2:py,class:'axis'});n('text',{x:L-8,y:py+4,'text-anchor':'end'},S.agg==='count'?num(y,0):fmt(col,y))}
    var bw=(W-L-8)/data.length;
    data.forEach(function(d,i){var h=Math.max(0,(d.value/max)*(H-B-T)),x=L+i*bw+bw*0.12,r=n('rect',{x:x,y:H-B-h,width:Math.max(1,bw*0.76),height:h,rx:3,class:'bar'});
      var t=document.createElementNS(NS,'title');t.textContent=d.label+': '+(S.agg==='count'?num(d.value,0):fmt(col,d.value));r.appendChild(t);
      var lbl=d.label.length>14?d.label.slice(0,13)+'…':d.label;var tx=L+i*bw+bw/2;
      n('text',{x:tx,y:H-B+14,'text-anchor':data.length>8?'end':'middle',transform:data.length>8?'rotate(-35 '+tx+' '+(H-B+14)+')':''},lbl)});
    return s}
  function niceStep(x){var p=Math.pow(10,Math.floor(Math.log10(x||1))),f=x/p;return (f<=1?1:f<=2?2:f<=5?5:10)*p}

  // ---- export ----
  function download(name,type,text){var a=document.createElement('a');a.href=URL.createObjectURL(new Blob([text],{type:type}));a.download=name;document.body.appendChild(a);a.click();setTimeout(function(){URL.revokeObjectURL(a.href);a.remove()},1000)}
  function csvCell(v){v=v==null?'':String(v);return /[",\n\r]/.test(v)?'"'+v.replace(/"/g,'""')+'"':v}
  function base(){return (sheet.name||'data').replace(/[^\w\- ]+/g,'').trim()||'data'}
  $('exp-csv').onclick=function(){var cols=sheet.columns,lines=[cols.map(function(c){return csvCell(c.name)}).join(',')];
    view.forEach(function(r){lines.push(r.map(csvCell).join(','))});download(base()+'.csv','text/csv;charset=utf-8','\ufeff'+lines.join('\r\n'))};
  $('exp-json').onclick=function(){var cols=sheet.columns;download(base()+'.json','application/json',JSON.stringify(view.map(function(r){var o={};cols.forEach(function(c,i){o[c.name]=r[i]});return o}),null,2))};

  function debounce(f,ms){var t;return function(){clearTimeout(t);t=setTimeout(f,ms)}}
  function update(keepHead){compute();if(!keepHead)renderHead();renderBody();renderStats();renderChart()}
  function selectSheet(i){S.sheet=i;sheet=D.sheets[i];S.sort=-1;S.filters={};S.shown=200;S.cv=-1;S.cg=-1;S.agg=sheet.columns.some(function(c){return c.type==='number'})?'sum':'count';
    var tabs=$('tabs');if(tabs)[].forEach.call(tabs.children,function(b,k){b.setAttribute('aria-selected',k===i)});
    $('tog-chart').hidden=false;update()}
  var tabs=$('tabs');
  if(tabs)D.sheets.forEach(function(s,i){tabs.appendChild(el('button',{role:'tab',type:'button','aria-selected':i===0,text:s.name+' ('+s.rows.length+')',onclick:function(){selectSheet(i)}}))});
  $('q').addEventListener('input',debounce(function(){S.q=$('q').value;S.shown=200;update(true)},150));
  $('tog-filters').onclick=function(){S.showFilters=!S.showFilters;this.setAttribute('aria-pressed',S.showFilters);renderHead()};
  $('tog-chart').onclick=function(){S.chart=!S.chart;this.setAttribute('aria-pressed',S.chart);renderChart()};
  $('reset').onclick=function(){S.q='';$('q').value='';S.filters={};S.sort=-1;S.shown=200;update()};
  document.addEventListener('keydown',function(e){if(e.key==='/'&&document.activeElement.tagName!=='INPUT'){e.preventDefault();$('q').focus()}});
  selectSheet(0);
  if(sheet.columns.some(function(c){return c.type==='number'})&&sheet.rows.length>1){S.chart=true;$('tog-chart').setAttribute('aria-pressed','true');renderChart()}
})();
