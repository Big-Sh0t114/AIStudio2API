<script setup lang="ts">
import { onActivated, onMounted, ref } from 'vue'
type Notebook = { id:string; title:string; source_count:number }
type Quota = { window:string; used_percent:number; remaining_percent:number; resets_at?:string }
type Account = { id:string; label:string; email?:string; state:string; plan?:string; error?:string; cooldown_until?:string; notebooks:Notebook[]; request_count:number; success_count:number; usage?:{metering_enabled:boolean;windows:Quota[]} }
const props=defineProps<{mode:'accounts'|'usage'}>()
const accounts=ref<Account[]>([])
const loading=ref(false)
const pending=ref(false)
const label=ref('')
const error=ref('')
const notice=ref('')
async function call<T>(url:string,method='GET',body?:unknown):Promise<T>{
 const r=await fetch(url,{method,credentials:'same-origin',...(body===undefined?{}:{headers:{'content-type':'application/json'},body:JSON.stringify(body)})})
 const v=await r.json() as T & {error?:{message?:string}}
 if(!r.ok)throw new Error(v.error?.message||`HTTP ${r.status}`)
 return v
}
async function refresh(){
 loading.value=true;error.value=''
 try{accounts.value=(await call<{accounts:Account[]}>('/api/notebook/accounts')).accounts}
 catch(e){error.value=String(e)}finally{loading.value=false}
}
async function add(){
 pending.value=true;error.value='';notice.value=''
 try{
  const x=await call<{account:Account}>('/api/notebook/accounts','POST',{label:label.value.trim()})
  label.value=''
  await call(`/api/notebook/accounts/${encodeURIComponent(x.account.id)}/login`,'POST')
  notice.value='已在运行服务的设备上打开 Chrome，完成 Google 登录后刷新账号状态。'
  await refresh()
 }catch(e){error.value=String(e)}finally{pending.value=false}
}
async function action(a:Account,task:'login'|'refresh'|'delete'){
 if(task==='delete'&&!confirm(`删除 Notebook 账号 ${a.label} 和本地 Chrome 登录资料？`))return
 pending.value=true;error.value='';notice.value=''
 try{
  const url=`/api/notebook/accounts/${encodeURIComponent(a.id)}`
  await call(url+(task==='delete'?'':`/${task}`),task==='delete'?'DELETE':'POST')
  if(task==='login')notice.value='Chrome 登录窗口已打开，完成登录后点击刷新状态。'
  await refresh()
 }catch(e){error.value=String(e)}finally{pending.value=false}
}
const percent=(v:number)=>`${Math.max(0,Math.min(100,v)).toFixed(1)}%`
const when=(v?:string)=>v?new Date(v).toLocaleString():'未知'
onMounted(refresh);onActivated(refresh)
</script>
<template>
<section class="page-x border-t border-line py-4" aria-label="Gemini Notebook">
 <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
  <div><h3 class="text-base font-semibold text-white">Gemini Notebook</h3>
   <p class="text-xs text-gray-400">{{props.mode==='accounts'?'独立登录 · Notebook 模型 · 独立冷却':'Notebook 专用额度（不计入 AI Studio Token 数据）'}}</p></div>
  <button type="button" class="rounded border border-line px-3 py-2 text-sm text-gray-300" :disabled="loading||pending" @click="refresh">刷新列表</button>
 </div>
 <div v-if="props.mode==='accounts'" class="mb-3 flex gap-2">
  <input v-model="label" class="min-w-0 flex-1 rounded border border-line bg-panel px-3 py-2 text-sm text-white" maxlength="120" placeholder="账号备注"/>
  <button type="button" class="rounded bg-blue-600 px-3 py-2 text-sm text-white" :disabled="pending" @click="add">添加并登录</button>
 </div>
 <p v-if="error" role="alert" class="mb-2 text-sm text-red-400">{{error}}</p><p v-if="notice" class="mb-2 text-sm text-blue-300">{{notice}}</p>
 <p v-if="accounts.length===0" class="text-sm text-gray-400">暂无 Notebook 账号</p>
 <div class="grid gap-3 xl:grid-cols-2">
  <article v-for="a in accounts" :key="a.id" class="rounded border border-line bg-panel p-4">
   <div class="flex justify-between gap-2">
    <div><div class="text-sm font-medium text-white">{{a.label}} · {{a.plan}}</div><div class="break-all text-xs text-gray-400">{{a.email||a.id}}</div></div>
    <span class="text-xs text-gray-400">{{a.state}}</span>
   </div>
   <p v-if="a.error" class="mt-2 text-xs text-red-400">{{a.error}}</p>
   <p v-if="a.cooldown_until&&new Date(a.cooldown_until)>new Date()" class="mt-2 text-xs text-yellow-300">冷却至 {{when(a.cooldown_until)}}</p>
   <div class="mt-2 flex flex-wrap gap-3 text-xs text-gray-400"><span>Notebook {{a.notebooks?.length||0}} 个</span><span>请求 {{a.request_count}}</span><span>成功 {{a.success_count}}</span></div>
   <div v-for="w in a.usage?.windows||[]" :key="w.window" class="mt-3">
    <div class="flex justify-between text-xs text-gray-300"><span>{{w.window}} · 已用 {{percent(w.used_percent)}}</span><span>剩余 {{percent(w.remaining_percent)}}</span></div>
    <div class="mt-1 h-1.5 rounded bg-raised"><div class="h-full rounded bg-blue-500" :style="{width:percent(w.used_percent)}"></div></div>
    <div class="mt-1 text-xs text-gray-500">重置 {{when(w.resets_at)}}</div>
   </div>
   <template v-if="props.mode==='accounts'">
    <div class="mt-3 max-h-28 overflow-y-auto text-xs text-gray-400"><div v-for="nb in a.notebooks" :key="nb.id" :title="`notebook:${a.id}:${nb.id}`">{{nb.title}} · {{nb.source_count}} 个来源</div></div>
    <div class="mt-3 flex flex-wrap gap-2">
     <button class="rounded border border-line px-2 py-1 text-xs text-gray-300" :disabled="pending" @click="action(a,'login')">重新登录</button>
     <button class="rounded border border-line px-2 py-1 text-xs text-gray-300" :disabled="pending" @click="action(a,'refresh')">刷新状态</button>
     <button class="rounded border border-red-500/40 px-2 py-1 text-xs text-red-300" :disabled="pending" @click="action(a,'delete')">删除</button>
    </div>
   </template>
  </article>
 </div>
</section>
</template>
