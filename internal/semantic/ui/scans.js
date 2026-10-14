(()=>{
 const panel=document.getElementById('scan-progress');if(!panel||panel.dataset.active!=='true')return;
 const state=document.getElementById('scan-state'),connection=document.getElementById('connection-state');
 const active=new Set(['queued','running','cancelling']);
 async function refresh(){
  try{
   const response=await fetch(panel.dataset.statusUrl,{cache:'no-store',headers:{Accept:'application/json'}});
   if(!response.ok)throw new Error('Status unavailable');const data=await response.json();
   if(state.textContent!==data.state)state.textContent=data.state;
   if(connection.textContent!=='Progress updated. Discovery continues as links are found.')connection.textContent='Progress updated. Discovery continues as links are found.';
   for(const counter of panel.querySelectorAll('[data-count]'))counter.textContent=data[counter.dataset.count]??'—';
   document.getElementById('current-url').textContent=data.current_url||'';document.getElementById('scan-elapsed').textContent=data.elapsed||'—';
   const body=document.getElementById('scan-pages');body.replaceChildren();
   for(const page of data.pages||[]){const row=document.createElement('tr');for(const value of [page.url+(page.error?' — '+page.error:''),page.status,page.outcome||'—']){const cell=document.createElement('td');cell.textContent=value;cell.className='url';row.append(cell)}body.append(row)}
   if(!active.has(data.state)){document.getElementById('cancel-scan')?.remove();const banner=document.querySelector('.active-banner');if(banner?.querySelector('a')?.getAttribute('href')===panel.dataset.statusUrl.replace(/\/status$/,''))banner.remove();connection.textContent='Scan finished. Refresh to see all available actions.';return}
  }catch{connection.textContent='Connection interrupted. Showing the last known progress; retrying.'}
  setTimeout(refresh,2000);
 }
 setTimeout(refresh,2000);
})();
