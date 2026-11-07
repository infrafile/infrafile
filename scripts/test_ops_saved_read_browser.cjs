// Local-only acceptance: the real workbench writes cache; a private Python fake replaces SSH.
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),crypto=require('node:crypto');
const {spawn,execFileSync}=require('node:child_process'),{once}=require('node:events');
const pw=require('../web/node_modules/playwright');
const assert=(v,m)=>{if(!v)throw Error(m)},sleep=ms=>new Promise(r=>setTimeout(r,ms));
const hash=p=>crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex');
const input=process.env.INFRAFILE_OPS_PACKAGE,binary=path.resolve(process.env.INFRAFILE_BINARY||'bin/infrafile');
assert(input,'generated fixture package required');
const base=fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(),'infrafile-ops-saved-browser-')));fs.chmodSync(base,0o700);
const packageDir=path.join(base,'package');fs.mkdirSync(packageDir);
const repo=path.join(packageDir,'candidate'),source=path.join(base,'source'),overview=path.join(packageDir,'ops.json'),store=path.join(base,'reads');
fs.cpSync(path.join(input,'bundle/candidate'),repo,{recursive:true});fs.mkdirSync(source);fs.copyFileSync(path.join(input,'ops.json'),overview);
const model=JSON.parse(fs.readFileSync(overview)),original=JSON.parse(fs.readFileSync(path.join(input,'reader.json')));
for(const name of Object.keys(model.authority_files)){const p=path.join(source,name);fs.mkdirSync(path.dirname(p),{recursive:true});fs.copyFileSync(path.join(input,'bundle/source',name),p)}
const cap={...original.capabilities[0],id:'saved-fixture',label:'采集固定保存样本',timeout_seconds:2,min_interval_seconds:1,max_age_seconds:300};
const stub=path.join(base,'reader.py'),mode=path.join(base,'mode'),countPath=path.join(base,'count'),pidPath=path.join(base,'child-pid');fs.writeFileSync(mode,'ok');
fs.writeFileSync(stub,`import datetime,json,os,pathlib,sys,time
r=json.load(sys.stdin);store=pathlib.Path(sys.argv[1]);records=list(store.glob('*.json'));assert len(records)==1
record=json.loads(records[0].read_text());assert record['phase']=='intent' and record['attempt_id']==r['request_id']
counter=pathlib.Path(sys.argv[2]);n=int(counter.read_text()) if counter.exists() else 0;counter.write_text(str(n+1))
pathlib.Path(sys.argv[4]).write_text(str(os.getpid()))
mode=pathlib.Path(sys.argv[3]).read_text()
if mode=='wait':time.sleep(10)
stamp=datetime.datetime.now(datetime.timezone.utc).isoformat()
out={'version':r['version'],'request_id':r['request_id'],'action_id':r['action_id'],'target':r['target'],'status':'PASS','error_code':'ok','started_at':stamp,'finished_at':stamp,'observed_at':stamp,'duration_ms':1,'max_age_seconds':300,'sections':[{'id':'fixed','title':'保存读取固定样本','status':'PASS','fields':[{'label':'样本','value':'仅本地模拟输出'}],'lines':[]}]}
if mode=='fail':out.update(status='UNKNOWN',error_code='ssh_connection_timeout',observed_at=None,sections=[])
print(json.dumps(out))
`);
const wrapper=path.join(base,'reader'),quote=s=>"'"+s.replaceAll("'","'\\''")+"'";
fs.writeFileSync(wrapper,'#!/bin/sh\nexec python3 -I -S -B '+[stub,store,countPath,mode,pidPath].map(quote).join(' ')+'\n',{mode:0o700});
const config={version:'infrafile-ops-reader/v1',executable:wrapper,command:[wrapper],directory:base,overview_sha256:hash(overview),files:{[wrapper]:hash(wrapper),[stub]:hash(stub)},capabilities:[cap]};
const readerPath=path.join(base,'reader.json');const writeConfig=()=>fs.writeFileSync(readerPath,JSON.stringify(config),{mode:0o600});writeConfig();
let child=null,browser=null,index=0;const report={checks:{},remote_ssh_calls:0,base};
const count=()=>fs.existsSync(countPath)?Number(fs.readFileSync(countPath)):0;
const savedRecord=()=>JSON.parse(fs.readFileSync(path.join(store,fs.readdirSync(store).find(n=>n.endsWith('.json')))));
async function start(){
 const pair=path.join(base,'pair-'+(++index)),log=path.join(base,'serve-'+index+'.log'),fd=fs.openSync(log,'wx',0o600);
 child=spawn(binary,['serve','--dir',repo,'--ops-overview',overview,'--ops-authority',source,'--ops-reader',readerPath,'--ops-read-state',store,'--listen','127.0.0.1:0','--pair-file',pair],{stdio:['ignore',fd,fd]});fs.closeSync(fd);
 const deadline=Date.now()+12000;let url='';while(Date.now()<deadline){if(child.exitCode!==null)throw Error('fixture exited before readiness');const m=fs.readFileSync(log,'utf8').match(/工作台：(http:\/\/[^\s]+)/);if(m&&fs.existsSync(pair)&&fs.readFileSync(pair,'utf8').trim()){url=m[1].replace(/\/$/,'')+'/ops';break}await sleep(25)}
 assert(url,'fixture readiness timeout');return {url,pair};
}
async function stop(signal='SIGTERM'){if(!child)return;const current=child;child=null;if(current.exitCode!==null)return;const exit=once(current,'exit');current.kill(signal);await Promise.race([exit,sleep(5000).then(()=>{throw Error('fixture stop unverified')})])}
async function login(page,entry){await page.goto(entry.url);await page.getByLabel('配对码',{exact:true}).fill(fs.readFileSync(entry.pair,'utf8').trim());await page.getByRole('button',{name:'连接工作台',exact:true}).click();await page.getByRole('heading',{name:'所有机器与应用',exact:true}).waitFor()}
async function open(page){await page.getByRole('button',{name:'操作目录',exact:true}).click();await page.getByRole('button',{name:'查看机器 '+cap.target.slice(7)+' 的操作范围',exact:true}).click()}
async function collect(page){const reply=page.waitForResponse(r=>r.url().endsWith('/api/ops-read'));await page.getByRole('button',{name:cap.label,exact:true}).click();const r=await reply;assert(r.status()===200,'fixture read HTTP failure');return r.json()}
async function cleanupOrphan(){if(!fs.existsSync(pidPath))return;const pid=Number(fs.readFileSync(pidPath));try{const cmd=execFileSync('ps',['-p',String(pid),'-o','command='],{encoding:'utf8'});if(cmd.includes(stub))process.kill(pid,'SIGKILL')}catch{}}
(async()=>{try{
 let entry=await start();browser=await pw[process.env.INFRAFILE_BROWSER_ENGINE||'chromium'].launch({headless:true});let context=await browser.newContext(),page=await context.newPage();page.setDefaultTimeout(12000);
 await login(page,entry);await open(page);await page.getByText('此范围还没有保存结果；没有发起远端采集。',{exact:true}).waitFor();assert(count()===0,'opening detail collected');report.checks.open_detail_only_reads_saved_file=true;
 const first=await collect(page);assert(first.status==='PASS'&&count()===1,'initial collection failed');await page.getByRole('heading',{name:'保存读取固定样本',exact:true}).waitFor();const record=savedRecord();assert(record.result.observed_at===first.observed_at,'stored time changed');
 await page.reload();await open(page);await page.getByText('上次保存的采集结果 · '+cap.label,{exact:true}).waitFor();await page.getByRole('heading',{name:'保存读取固定样本',exact:true}).waitFor();assert(count()===1&&savedRecord().saved_at===record.saved_at,'reload reread or restamped');report.checks.reload_preserves_observation_without_collection=true;
 await context.close();await stop();entry=await start();context=await browser.newContext();page=await context.newPage();page.setDefaultTimeout(12000);await login(page,entry);await open(page);await page.getByRole('heading',{name:'保存读取固定样本',exact:true}).waitFor();assert(count()===1&&savedRecord().result.observed_at===first.observed_at,'restart lost observation');report.checks.real_server_restart_restores_same_sample=true;
 fs.appendFileSync(stub,'\n# source changed\n');await page.reload();await open(page);await page.getByText('来源、程序或证据代已变化。旧样本保留，当前状态未核实。',{exact:true}).waitFor();await page.getByRole('heading',{name:'保存读取固定样本',exact:true}).waitFor();assert(await page.locator('.ops-read-output .ops-read-complete').count()===0&&count()===1,'changed code kept green or reread');report.checks.code_drift_keeps_only_unverified_sample=true;
 await context.close();await stop();config.files[stub]=hash(stub);writeConfig();entry=await start();context=await browser.newContext();page=await context.newPage();page.setDefaultTimeout(12000);await login(page,entry);await open(page);await page.getByText('来源、程序或证据代已变化。旧样本保留，当前状态未核实。',{exact:true}).waitFor();assert(count()===1,'new binding auto-collected');report.checks.changed_binding_does_not_promote_old_sample=true;
 const second=await collect(page);assert(second.status==='PASS'&&count()===2,'new explicit read failed');await sleep(1100);fs.writeFileSync(mode,'fail');const failed=await collect(page);assert(failed.status==='UNKNOWN'&&count()===3,'failed read not explicit');await page.reload();await open(page);await page.getByText('SSH 连接超时，状态未知。',{exact:true}).first().waitFor();assert(await page.getByRole('heading',{name:'保存读取固定样本',exact:true}).count()===0&&count()===3,'old success returned after failure');report.checks.latest_failure_never_falls_back_to_old_green=true;
 await sleep(1100);fs.writeFileSync(mode,'wait');await page.getByRole('button',{name:cap.label,exact:true}).click();for(let i=0;i<100&&count()<4;i++)await sleep(10);assert(count()===4&&savedRecord().phase==='intent','request started before intent');await stop('SIGKILL');await cleanupOrphan();await context.close();entry=await start();context=await browser.newContext();page=await context.newPage();page.setDefaultTimeout(12000);await login(page,entry);await open(page);await page.getByText('上次读取只保存了开始意图，没有完整结果；不能据此判断任务是否仍在运行或目标状态。',{exact:true}).waitFor();assert(count()===4,'interrupted intent replayed');report.checks.interrupted_process_has_unknown_intent_and_no_replay=true;
 await page.setViewportSize({width:390,height:844});await page.getByText('上次保存的采集结果 · '+cap.label,{exact:true}).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(base,'saved-mobile.png')});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),'mobile overflow');report.checks.mobile_result_layout=true;
 fs.writeFileSync(path.join(base,'evidence.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({evidence:path.join(base,'evidence.json'),checks:report.checks,remote_ssh_calls:0}));await context.close();
}finally{if(browser)await browser.close();await stop();await cleanupOrphan()}})().catch(e=>{fs.writeFileSync(path.join(base,'failure.json'),JSON.stringify({message:e.message,base},null,2)+'\n');console.error(e.message+'; evidence '+base);process.exitCode=1});
