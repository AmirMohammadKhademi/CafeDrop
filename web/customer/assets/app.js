const input=document.querySelector('#files'),list=document.querySelector('#fileList'),send=document.querySelector('#send'),result=document.querySelector('#result'),wrap=document.querySelector('#progressWrap'),bar=document.querySelector('#progress'),progressText=document.querySelector('#progressText');let selected=[];let sessionReady=false;let sessionState='';let sessionTimer=null;
const fmt=n=>n<1024?`${n} B`:n<1048576?`${(n/1024).toFixed(1)} KB`:`${(n/1048576).toFixed(1)} MB`;
input.addEventListener('change',()=>{selected=[...input.files];list.innerHTML='';selected.forEach(f=>{const d=document.createElement('div');d.className='file-item';const n=document.createElement('span');n.textContent=f.name;const z=document.createElement('span');z.textContent=fmt(f.size);d.append(n,z);list.append(d)});send.disabled=!selected.length||!sessionReady;result.hidden=true});
send.addEventListener('click',()=>{if(!selected.length||!sessionReady)return;send.disabled=true;wrap.hidden=false;bar.style.width='0%';const data=new FormData();selected.forEach(f=>data.append('files',f));const xhr=new XMLHttpRequest();xhr.open('POST','/api/uploads');xhr.upload.onprogress=e=>{if(e.lengthComputable){const p=Math.round(e.loaded/e.total*100);bar.style.width=p+'%';progressText.textContent=`در حال ارسال... ${p}%`}};xhr.onload=()=>{wrap.hidden=true;send.disabled=false;try{const d=JSON.parse(xhr.responseText);if(xhr.status>=200&&xhr.status<300){result.hidden=false;result.innerHTML='';const t=document.createElement('div');t.textContent='فایل‌های شما با موفقیت ارسال شدند';const c=document.createElement('strong');c.textContent=d.code;const h=document.createElement('small');h.textContent=`${d.file_count} فایل دریافت شد. کد را به متصدی اعلام کنید.`;result.append(t,c,h);selected=[];input.value='';list.innerHTML='';send.disabled=true}else throw new Error(d.error||xhr.responseText)}catch(e){alert('ارسال انجام نشد. لطفاً دوباره تلاش کنید.')}};xhr.onerror=()=>{wrap.hidden=true;send.disabled=false;alert('ارتباط با سرور برقرار نشد.')};xhr.send(data)});


(async function sessionController(){
  const statusBox=document.querySelector('#sessionStatus');
  const fmtTime=value=>{if(!value)return '';const d=new Date(value);return d.toLocaleTimeString('fa-IR',{hour:'2-digit',minute:'2-digit'});};
  function show(message,kind){statusBox.textContent=message;statusBox.className='session-status '+(kind||'');}
  function apply(data){
    sessionState=data.status;
    if(data.status==='active'){
      sessionReady=true;send.disabled=!selected.length;
      show(`اتصال فعال است · ${data.active_count} از ${data.active_limit} ظرفیت استفاده شده · پایان زمان: ${fmtTime(data.expires_at)}`,'active');
    }else if(data.status==='grace'){
      sessionReady=true;send.disabled=!selected.length;
      show(`زمان استفاده تمام شده؛ مهلت تکمیل ارسال تا ${fmtTime(data.grace_until)} فعال است.`,'waiting');
    }else if(data.status==='waiting'){
      sessionReady=false;send.disabled=true;
      show(`ظرفیت تکمیل است. شما در صف انتظار شماره ${data.queue_position||1} هستید؛ پس از آزاد شدن ظرفیت، اتصال فعال می‌شود.`,'waiting');
    }else{
      sessionReady=false;send.disabled=true;show('نشست فعال نیست. صفحه را تازه‌سازی کنید.','error');
    }
  }
  async function post(path){const r=await fetch(path,{method:'POST',cache:'no-store'});const d=await r.json().catch(()=>({}));if(!r.ok&&r.status!==202)throw new Error(d.error||'خطای نشست');return d;}
  try{apply(await post('/api/session/start'));}
  catch(e){show('امکان ایجاد نشست وجود ندارد. صفحه را تازه‌سازی کنید.','error');return;}
  sessionTimer=setInterval(async()=>{try{apply(await post('/api/session/heartbeat'));}catch(e){sessionReady=false;send.disabled=true;show('ارتباط با سرور قطع شده است؛ دوباره تلاش کنید.','error');}},15000);
})();
