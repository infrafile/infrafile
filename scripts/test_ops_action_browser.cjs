// M3 browser acceptance: copy fictional inventory; local stub, no SSH or real units.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const {spawn}=require('node:child_process'),{once}=require('node:events');const playwright=require('../web/node_modules/playwright');
const assert=(ok,message)=>{if(!ok)throw Error(message)},sleep=ms=>new Promise(r=>setTimeout(r,ms));const hash=p=>crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex');
const input=process.env.INFRAFILE_OPS_PACKAGE;assert(input,'set fixture package with reader.json');const binary=path.resolve(process.env.INFRAFILE_BINARY||'bin/infrafile');
const base=fs.mkdtempSync(path.join(os.tmpdir(),'infrafile-ops-action-browser-'));fs.chmodSync(base,0o700);
const repo=path.join(base,'candidate'),authority=path.join(base,'authority'),modelPath=path.join(base,'ops.json'),state=path.join(base,'state');fs.mkdirSync(state,{mode:0o700});fs.mkdirSync(authority);
fs.cpSync(path.join(input,'bundle/candidate'),repo,{recursive:true});fs.copyFileSync(path.join(input,'ops.json'),modelPath);
const model=JSON.parse(fs.readFileSync(modelPath)),reader=JSON.parse(fs.readFileSync(path.join(input,'reader.json')));
for(const name of Object.keys(model.authority_files)){const target=path.join(authority,name);fs.mkdirSync(path.dirname(target),{recursive:true});fs.copyFileSync(path.join(input,'bundle/source',name),target)}
const multiAction=process.env.INFRAFILE_OPS_MULTI_ACTION==='1';
const multiApp=model.applications.find(a=>new Set(model.deployments.filter(d=>d.application===a.key).map(d=>d.host)).size>=2);
const multiHosts=multiApp?[...new Set(model.deployments.filter(d=>d.application===multiApp.key).map(d=>d.host))].slice(0,2):[];
const target=model.servers.find(row=>row.key===(multiAction?multiHosts[0]:reader.capabilities[0].target));assert(target,'fixture target missing');
const capability={...reader.capabilities[0],id:'selected-once',label:'执行一次已登记观察任务',description:'Fixture fixed observation only; plan before apply.',timeout_seconds:2,min_interval_seconds:60};
if(multiAction){assert(multiApp&&multiHosts.length===2,'multi-host app fixture missing');capability.target=target.key;capability.related_keys=[multiApp.key];}
const mode=path.join(base,'mode'),stub=path.join(base,'actor.py'),remote=path.join(base,'remote');fs.mkdirSync(remote);fs.writeFileSync(mode,'ok');
fs.writeFileSync(stub,`import datetime,json,pathlib,sys,time
r=json.load(sys.stdin)
root=pathlib.Path(sys.argv[2]);op=r['operation'];identity=r['id'];entry=root/(identity+'.json')
counter=root/('count-'+op);counter.write_text((counter.read_text() if counter.exists() else '')+'x')
stamp=datetime.datetime.now(datetime.timezone.utc)
expires=(stamp+datetime.timedelta(seconds=30)).isoformat()
out={**r,'state':'prepared','error_code':'ok','digest':'a'*64,'expires_at':expires,'invocation_id':'','disposition':'submit_once','fields':[{'label':'Fixed unit','value':'example-observer.service'},{'label':'Effect','value':'<two selected observations>'}]}
if op=='plan':entry.write_text(json.dumps(out))
else:
 out=json.loads(entry.read_text());out['operation']=op
 if op=='apply':
  local=json.loads((pathlib.Path(sys.argv[3])/(identity+'.json')).read_text())
  assert local['state']=='submitting'
  out.update(state='completed',invocation_id='b'*32,disposition='submitted')
  entry.write_text(json.dumps(out))
 selected=pathlib.Path(sys.argv[1]).read_text()
 if op=='apply' and selected=='timeout':time.sleep(5)
 if op=='apply' and selected=='lost':out.update(state='uncertain',error_code='transport_failed',invocation_id='')
 if op=='apply' and selected=='slow':time.sleep(1)
 if op=='apply' and selected=='join':out['disposition']='joined_existing'
print(json.dumps(out))
`);
const config={version:'infrafile-ops-actor/v1',executable:reader.executable,overview_sha256:hash(modelPath),command:[reader.command[0],'-I',stub,mode,remote,state],directory:base,files:{[reader.executable]:hash(reader.executable),[stub]:hash(stub)},capabilities:multiAction?[capability,{...capability,id:'selected-second',target:multiHosts[1]}]:[capability]};
const configPath=path.join(base,'actor.json');fs.writeFileSync(configPath,JSON.stringify(config),{mode:0o600});const pair=path.join(base,'pair');
const refreshArgs=[];let generationMode,generationCount;
if(process.env.INFRAFILE_OPS_REFRESH){
 const generationRoot=path.join(fs.realpathSync(base),'generations');fs.mkdirSync(generationRoot,{mode:0o700});generationMode=path.join(base,'generation-mode');generationCount=path.join(base,'generation-count');fs.writeFileSync(generationMode,'ok');const producer=path.join(base,'generation.py');
 fs.writeFileSync(producer,`import datetime,hashlib,json,pathlib,shutil,sys,time,uuid
base,root,authority,repo,overview,actor=map(pathlib.Path,sys.argv[1:])
counter=base/'generation-count';counter.write_text((counter.read_text() if counter.exists() else '')+'x')
mode=(base/'generation-mode').read_text()
if mode=='timeout':time.sleep(10)
package=root/uuid.uuid4().hex;package.mkdir(mode=0o700)
shutil.copytree(repo,package/'bundle/candidate')
m=json.loads(overview.read_text());m['captured_at']=datetime.datetime.now(datetime.timezone.utc).isoformat()
for name in m['authority_files']:
 m['authority_files'][name]=hashlib.sha256((authority/name).read_bytes()).hexdigest()
 dest=package/'bundle/source'/name;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes((authority/name).read_bytes())
out=package/'ops.json';out.write_text(json.dumps(m));digest=hashlib.sha256(out.read_bytes()).hexdigest()
a=json.loads(actor.read_text());a['overview_sha256']=digest;(package/'actor.json').write_text(json.dumps(a));(package/'actor.json').chmod(0o600)
print(json.dumps({'version':'infrafile-ops-generation/v1','package':str(package),'overview_sha256':'0'*64 if mode=='bad' else digest,'summary':m['summary']}))
`);
 const lock=path.join(base,'shared.lock');fs.writeFileSync(lock,'0',{mode:0o600});const refresh={version:'infrafile-ops-refresh/v1',executable:reader.executable,command:[reader.command[0],'-I',producer,base,generationRoot,authority,repo,modelPath,configPath],directory:base,files:{[reader.executable]:hash(reader.executable),[producer]:hash(producer)},generation_root:generationRoot,lock_file:lock,timeout_seconds:2,reader:false,actions:true};const refreshPath=path.join(base,'refresh.json');fs.writeFileSync(refreshPath,JSON.stringify(refresh),{mode:0o600});refreshArgs.push('--ops-refresh',refreshPath);
}
const child=spawn(binary,['serve','--dir',repo,'--ops-overview',modelPath,'--ops-authority',authority,'--ops-actions',configPath,'--ops-action-state',state,'--listen','127.0.0.1:0','--pair-file',pair,...refreshArgs],{stdio:['ignore','pipe','pipe']});let stderr='';child.stderr.on('data',data=>stderr+=data);let browser;
const report={binary_sha256:hash(binary),checks:{}};const count=op=>fs.existsSync(path.join(remote,'count-'+op))?fs.readFileSync(path.join(remote,'count-'+op)).length:0;
async function verifyMultiAction(page, context, origin, report, errors) {
 await page.getByRole('button',{name:'应用',exact:true}).click();
 await page.getByLabel('搜索',{exact:true}).fill(multiApp.name);
 await page.getByRole('button',{name:'查看 '+multiApp.name,exact:true}).click();
 const choice=page.getByLabel('操作目标与动作',{exact:true});await choice.waitFor();
 assert(await choice.inputValue()==='' && await page.getByRole('button',{name:'预检单次执行',exact:true}).count()===0,'first matching action selected implicitly');
 assert(count('plan')===0&&count('apply')===0,'opening multi-action object invoked adapter');
 report.checks.multi_action_requires_explicit_host_and_action=true;
 const select=async(id,target)=>{const response=page.waitForResponse(r=>r.url().endsWith('/api/ops-action/latest')&&r.request().postDataJSON().target===target);await choice.selectOption(id);await response;await page.waitForTimeout(100)};
 const plan=async()=>{const response=page.waitForResponse(r=>r.url().endsWith('/api/ops-action/plan'));await page.getByRole('button',{name:'预检单次执行',exact:true}).click();return(await response).json()};
 const apply=async()=>{await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).check();const response=page.waitForResponse(r=>r.url().endsWith('/api/ops-action/apply'));await page.getByRole('button',{name:'提交单次执行',exact:true}).click();return(await response).json()};
 await select('selected-once',multiHosts[0]);const first=await plan();
 assert(first.target===multiHosts[0]&&first.action_id==='selected-once','first plan target mismatch');
 await select('selected-second',multiHosts[1]);
 assert(await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).count()===0&&count('plan')===1&&count('apply')===0,'switch reused or executed an unsubmitted plan');
 await select('selected-once',multiHosts[0]);
 await page.getByText('预检计划已准备 · 尚未执行',{exact:true}).waitFor();
 assert(!await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).isChecked(),'reselection reused prior review consent');
 fs.writeFileSync(mode,'lost');await apply();
 await page.getByText('执行结果待核实 · 不要重复提交',{exact:true}).waitFor();
 await select('selected-second',multiHosts[1]);
 assert(await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).count()===0,'first plan was reused for second target');
 const blocked=await plan();assert(blocked.target===multiHosts[1]&&blocked.error_code==='unresolved_execution'&&count('plan')===1&&count('apply')===1,'target switch bypassed unresolved record');
 report.checks.target_switch_drops_unsubmitted_plan_and_keeps_durable_uncertain_submission=true;
 await select('selected-once',multiHosts[0]);await page.getByText('执行结果待核实 · 不要重复提交',{exact:true}).waitFor();
 assert(count('status')===0,'selection reconciled remote automatically');
 await page.getByRole('button',{name:'只核实执行端回执',exact:true}).click();await page.getByText('本次执行已完成',{exact:true}).waitFor();
 assert(count('apply')===1&&count('status')===1,'reselection replayed first task');
 await select('selected-second',multiHosts[1]);const second=await plan();
 assert(second.id!==first.id&&second.target===multiHosts[1]&&second.action_id==='selected-second','second plan reused first identity');
 fs.writeFileSync(mode,'ok');await apply();await page.getByText('本次执行已完成',{exact:true}).waitFor();
 assert(count('plan')===2&&count('apply')===2&&count('status')===1,'second execution count wrong');
 report.checks.each_host_action_has_its_own_plan_review_and_receipt=true;
 await page.getByRole('button',{name:'关闭详情',exact:true}).click();await page.getByLabel('搜索',{exact:true}).fill('');
 await page.getByRole('button',{name:'操作目录',exact:true}).click();
 const catalog=page.getByRole('region',{name:'机器与应用操作目录',exact:true});await catalog.waitFor();
 await page.getByLabel('操作入口可用性',{exact:true}).selectOption('action');
 assert(await catalog.getByRole('row').count()===5,'catalog widened two fixed actions beyond two hosts/deployments');
 const hostName=multiHosts[0].slice(7);
 await page.getByRole('button',{name:`查看 ${multiApp.name} 在 ${hostName} 的操作范围`,exact:true}).click();
 await page.getByRole('heading',{name:`${multiApp.name} · ${hostName}`,level:2,exact:true}).waitFor();
 assert(await page.getByLabel('操作目标与动作',{exact:true}).count()===0,'explicit deployment scope still included other hosts');
 await page.getByText('回执 '+first.id,{exact:false}).waitFor();
 assert(await page.getByText('回执 '+second.id,{exact:false}).count()===0,'other host receipt leaked into deployment');
 report.checks.catalog_selection_is_one_exact_deployment_not_all_app_hosts=true;
 await page.getByRole('heading',{name:`部署操作范围 · ${hostName}`,exact:true}).scrollIntoViewIfNeeded();
 await page.screenshot({path:path.join(base,'catalog-desktop.png'),fullPage:true});await page.setViewportSize({width:390,height:844});
 await page.getByRole('heading',{name:`部署操作范围 · ${hostName}`,exact:true}).scrollIntoViewIfNeeded();
 await page.screenshot({path:path.join(base,'catalog-mobile.png'),fullPage:true});
 assert(errors.length===0,'browser errors');
}
(async()=>{try{
 let origin;for(let i=0;i<150;i++){origin=stderr.match(/工作台：(http:\/\/[^\s]+)/)?.[1];if(origin)break;assert(child.exitCode===null,'candidate failed');await sleep(40)}assert(origin,'readiness timeout');
 browser=await playwright[process.env.INFRAFILE_BROWSER_ENGINE||'chromium'].launch({headless:true});const context=await browser.newContext({viewport:{width:1440,height:1050}}),page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(origin);await page.getByLabel('配对码',{exact:true}).fill(fs.readFileSync(pair,'utf8').trim());await page.getByRole('button',{name:'连接工作台',exact:true}).click();await page.getByRole('heading',{name:'所有机器与应用',exact:true}).waitFor();
 if(multiAction){await verifyMultiAction(page,context,origin,report,errors);fs.writeFileSync(path.join(base,'evidence.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});console.log(JSON.stringify({evidence:path.join(base,'evidence.json'),checks:report.checks}));return;}
 const open=async()=>{await page.getByLabel('搜索',{exact:true}).fill(target.name);await page.getByRole('button',{name:'查看 '+target.name,exact:true}).click();await page.getByRole('button',{name:'预检单次执行',exact:true}).waitFor();await page.waitForTimeout(150)};await open();
 assert(count('plan')===0&&count('apply')===0,'selection started remote operation');
 const plan=async()=>{const pending=page.waitForResponse(r=>r.url().endsWith('/api/ops-action/plan'));await page.getByRole('button',{name:'预检单次执行',exact:true}).click();const data=await(await pending).json();await page.getByText('预检计划已准备 · 尚未执行',{exact:true}).waitFor();return data};
 const review=async()=>{await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).check()};
 const apply=async()=>{await review();const pending=page.waitForResponse(r=>r.url().endsWith('/api/ops-action/apply'));await page.getByRole('button',{name:'提交单次执行',exact:true}).click();return(await pending).json()};
 const regenerate=async()=>{const response=page.waitForResponse(r=>r.url().endsWith('/api/ops-refresh'));response.catch(()=>{});await page.getByRole('button',{name:'刷新台账与保存证据',exact:true}).click();return(await response).json()};
 const first=await plan();if(generationMode){const blocked=await regenerate();assert(['prepared_plan','busy'].includes(blocked.error_code),'refresh ignored prepared plan');assert(!fs.existsSync(generationCount),'blocked refresh ran producer');assert(await page.getByLabel('已核对目标、固定任务、影响和排程变化',{exact:true}).count()===1,'refused refresh discarded plan');report.checks.refresh_preserves_unexpired_plan=true}assert(count('apply')===0,'preflight executed unit');assert(await page.getByRole('button',{name:'提交单次执行',exact:true}).isDisabled(),'review not required');await page.screenshot({path:path.join(base,'plan.png'),fullPage:true});await apply();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('apply')===1,'apply count incorrect');
 // Duplicate explicit submission of exactly the same reviewed plan is idempotent.
 const token=await page.locator('meta[name="csrf-token"]').getAttribute('content');const duplicate=await context.request.post(origin+'/api/ops-action/apply',{headers:{Origin:origin,'X-CSRF-Token':token},data:{id:first.id,digest:first.plan.digest,reviewed:true}});assert(duplicate.status()===200,'duplicate receipt unavailable');assert(count('apply')===1,'duplicate execution');
 await page.screenshot({path:path.join(base,'completed.png'),fullPage:true});await page.setViewportSize({width:390,height:844});await page.getByRole('heading',{name:'受控单次执行',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(base,'mobile.png'),fullPage:true});await page.setViewportSize({width:1440,height:1050});
 await page.reload();await page.getByRole('heading',{name:'所有机器与应用',exact:true}).waitFor();await open();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('plan')===1&&count('apply')===1,'reload repeated execution');report.checks.no_implicit_execution_review_required_idempotency_and_durable_reload=true;await page.clock.fastForward(61000);assert(await page.getByRole('heading',{name:'按需只读采集',exact:true}).count()===1,'cache refresh duplicated reader');assert(await page.getByRole('button',{name:'预检单次执行',exact:true}).count()===1,'cache refresh duplicated actor');assert(count('apply')===1,'cache refresh executed task');await page.clock.setSystemTime(new Date());report.checks.cache_refresh_keeps_unique_reader_and_actor=true;
 fs.writeFileSync(mode,'lost');await plan();await apply();await page.getByText('执行结果待核实 · 不要重复提交',{exact:true}).waitFor();assert(await page.getByRole('button',{name:'预检单次执行',exact:true}).isDisabled(),'uncertainty allowed new plan');if(generationMode){const blocked=await regenerate();assert(['unresolved_execution','busy'].includes(blocked.error_code),'refresh ignored uncertainty');assert(!fs.existsSync(generationCount),'uncertainty ran producer');assert(await page.getByRole('button',{name:'只核实执行端回执',exact:true}).count()===1,'refused refresh discarded receipt');report.checks.refresh_refuses_uncertain_and_keeps_original_reconciliation=true}await page.getByRole('button',{name:'只核实执行端回执',exact:true}).click();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('apply')===2&&count('status')===1,'reconciliation repeated execution');report.checks.lost_reply_uncertain_and_readonly_reconciliation=true;
 fs.writeFileSync(mode,'timeout');await plan();await apply();await page.getByText('执行结果待核实 · 不要重复提交',{exact:true}).waitFor();await page.getByText('等待执行回执超时；任务可能已运行，请只查询本次回执。',{exact:true}).waitFor();await page.screenshot({path:path.join(base,'timeout.png'),fullPage:true});await page.getByRole('button',{name:'只核实执行端回执',exact:true}).click();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('apply')===3,'timeout resubmitted');report.checks.timeout_never_blindly_retried=true;
 fs.writeFileSync(mode,'slow');await plan();await apply();await page.getByRole('button',{name:'关闭详情',exact:true}).click();await sleep(1600);await open();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('apply')===4,'close lost or repeated task');report.checks.selection_close_keeps_submitted_task_and_receipt=true;
 fs.writeFileSync(mode,'join');await plan();await apply();await page.getByText('关联的是已有执行，本次没有重新启动任务。',{exact:true}).waitFor();report.checks.existing_invocation_attribution_visible=true;
 await page.getByRole('button',{name:'关闭详情',exact:true}).click();await page.getByLabel('搜索',{exact:true}).fill('');const other=model.servers.find(s=>s.key!==target.key);await page.getByRole('button',{name:'查看 '+other.name,exact:true}).click();await page.getByText('此对象未开放受控执行。',{exact:true}).waitFor();assert(await page.getByRole('button',{name:'预检单次执行',exact:true}).count()===0,'other object can execute');await page.getByRole('button',{name:'关闭详情',exact:true}).click();await open();
 const binding=Object.keys(model.authority_files)[0];fs.appendFileSync(path.join(authority,binding),'\n');await page.getByRole('button',{name:'重新读取保存结果',exact:true}).click();await page.getByText('权威来源已有变化 · 以下是冻结观测',{exact:true}).waitFor();await open();assert(await page.getByRole('button',{name:'预检单次执行',exact:true}).isDisabled(),'source drift can submit');report.checks.unmanaged_and_source_drift_disabled=true;
 if(generationMode){
  const submitted=count('apply'),oldURL=page.url();let refreshed;
  for(let i=0;i<5;i++){refreshed=await regenerate();if(refreshed.error_code!=='busy')break;await sleep(200)}assert(refreshed.status==='PASS','offline generation refused');await page.getByText('台账与证据包来源一致',{exact:true}).waitFor();assert(page.url()===oldURL,'generation changed URL');assert(await page.locator('meta[name="csrf-token"]').getAttribute('content')===token,'generation replaced session');await open();await page.getByText('本次执行已完成',{exact:true}).waitFor();assert(count('apply')===submitted,'generation replayed execution');report.checks.frozen_source_regenerates_atomically_same_address_and_session=true;
  const replay=await context.request.post(origin+'/api/ops-action/apply',{headers:{Origin:origin,'X-CSRF-Token':token},data:{id:first.id,digest:first.plan.digest,reviewed:true}});assert(replay.status()===200&&count('apply')===submitted,'new generation replayed old submission');report.checks.old_receipt_remains_idempotent_after_refresh=true;
  fs.writeFileSync(generationMode,'bad');const failed=await regenerate();assert(failed.status==='UNKNOWN'&&failed.error_code==='invalid_generation','bad generation published');await page.getByText('台账与证据包来源一致',{exact:true}).waitFor();await open();await page.getByText('本次执行已完成',{exact:true}).waitFor();const generated=fs.readFileSync(generationCount,'utf8').length;await page.clock.fastForward(61000);assert(fs.readFileSync(generationCount,'utf8').length===generated,'automatic reread regenerated');assert(count('apply')===submitted,'failed refresh executed');report.checks.failed_generation_retains_last_complete_model_and_does_not_auto_retry=true;
  await page.getByRole('button',{name:'刷新台账与保存证据',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(base,'refresh-desktop.png'),fullPage:true});await page.setViewportSize({width:390,height:844});await page.getByRole('button',{name:'刷新台账与保存证据',exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(base,'refresh-mobile.png'),fullPage:true});
 }
 assert(errors.length===0,'browser script errors');fs.writeFileSync(path.join(base,'evidence.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});console.log(JSON.stringify({evidence:path.join(base,'evidence.json'),checks:report.checks}));
}finally{if(browser)await browser.close();if(child.exitCode===null){const done=once(child,'exit');child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),6000);await done;clearTimeout(timer)}if(fs.existsSync(pair))fs.unlinkSync(pair)}})().catch(e=>{console.error('M3 browser acceptance failed:',e.message,'private output:',base);process.exitCode=1});
