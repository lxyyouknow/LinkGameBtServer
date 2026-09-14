#!/usr/bin/env node
// 新 BT 服务端配置生成/预检。只操作本机文件，不执行 SSH 或访问数据库。
import {readFileSync,writeFileSync,existsSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=resolve(import.meta.dirname,'..');
export function validateProject(p,{release=false}={}) {
 const errors=[];
 if(p.projectKey!=='pairpop-bt'||!['tiktok-native','web-preview'].includes(p.runtime)||p.analyticsAppId!==1||p.reportTimezone!=='Asia/Tokyo') errors.push('BT 项目/平台/统计标识不一致');
 if(p.runtime==='web-preview'&&p.environment!=='staging')errors.push('Web 测试登录只允许 staging');
 if(p.allowWebPreview!==undefined&&typeof p.allowWebPreview!=='boolean')errors.push('allowWebPreview 必须是布尔值');
 if(p.allowWebPreview===true&&p.environment!=='staging')errors.push('Web 测试账号只允许 staging');
 if(!['staging','production'].includes(p.environment)) errors.push('云环境必须为 staging 或 production');
 for(const origin of p.corsOrigins??[]){try{const u=new URL(origin);if(u.protocol!=='https:'||u.origin!==origin)throw new Error();}catch{errors.push('CORS 必须是无路径的明确 HTTPS 来源');}}
 const visit=(v)=>{for(const [k,x] of Object.entries(v??{})){if(/secret|password|access.?key|access.?token/i.test(k)) errors.push('公开配置禁止秘密字段');if(x&&typeof x==='object')visit(x);}};visit(p);
 if(p.systemdService!=='linkgame-bt.service')errors.push('BT 服务名必须为 linkgame-bt.service');
 const safe=(v)=>typeof v==='string'&&!/[\r\n'"`$\\]/.test(v);
 for(const v of [p.apiDomain,p.sshHost,p.sshUser,p.sshKeyPath,p.remoteRoot,p.database?.host,p.database?.name,p.database?.user,p.tiktokClientKey])if(!safe(v))errors.push('配置字段不完整或包含不支持的特殊字符');
 if(release){
  if(p.releaseEnabled!==true) errors.push('releaseEnabled=false：尚未填写新的运维配置');
  for(const k of ['apiDomain','sshHost'])if(!/^[A-Za-z0-9.-]+$/.test(p[k])||/^(linkgame|catgoodssort)\.bffbond\.com$|\.invalid$/.test(p[k]))errors.push(`请填写新 ${k}（不含协议和路径）`);
  if(!/^[A-Za-z_][A-Za-z0-9_-]*$/.test(p.sshUser))errors.push('请填写新 SSH 用户');
  if(!p.sshKeyPath)errors.push('请填写本机 SSH 私钥路径');
  if(!/^\/[A-Za-z0-9_./-]*linkgame-bt[A-Za-z0-9_/-]*$/.test(p.remoteRoot)||p.remoteRoot.split('/').includes('..'))errors.push('远端必须是明确包含 linkgame-bt 的独立绝对路径');
  for(const x of [p.httpPort,p.database?.port])if(!Number.isInteger(x)||x<1||x>65535)errors.push('端口须在1至65535之间');
  if(!/^[A-Za-z0-9.-]+$/.test(p.database?.host??'')||/\.invalid$/.test(p.database?.host??''))errors.push('请填写新数据库地址');
  const assigned=p.database?.host==='database-2.c9ie8y6eckdj.ap-northeast-1.rds.amazonaws.com'&&p.database?.port===3306&&p.database?.name==='linkgamebt'&&p.database?.user==='linkgamebt';
  for(const k of ['name','user'])if(!assigned&&!/^linkgame_bt_[A-Za-z0-9_]+$/.test(p.database?.[k]??''))errors.push('数据库须为运维明确分配的 linkgamebt，或独立 linkgame_bt_ 前缀');
  if(p.runtime==='tiktok-native'&&!/^[A-Za-z0-9_-]{4,128}$/.test(p.tiktokClientKey??''))errors.push('请填与新客户端相同的 TikTok Client Key');
 }
 return errors;
}
function outputs(p){
 const remote=p.remoteRoot||'/home/linkgame-bt/linkgame-bt-server',port=p.httpPort||24020,domain=p.apiDomain||'bt-api.example.invalid';
 const web=p.runtime==='web-preview';
 const expected={BT_PROJECT_KEY:p.projectKey,BT_RUNTIME:p.runtime,BT_ENV:p.environment,APP_ENV:p.environment,HTTP_ADDR:`0.0.0.0:${port}`,BT_DB_HOST:p.database.host,BT_DB_PORT:p.database.port??'',BT_DB_NAME:p.database.name,BT_DB_USER:p.database.user,TIKTOK_CLIENT_KEY:p.tiktokClientKey,ENABLE_TIKTOK_LOGIN:String(!web),ENABLE_TEST_ACCOUNT_LOGIN:String(web||p.allowWebPreview===true),BT_ALLOW_WEB_PREVIEW:String(p.allowWebPreview===true),ANALYTICS_ENABLED:'true',ANALYTICS_TIMEZONE:p.reportTimezone};
 const release={RELEASE_SSH_HOST:p.sshHost,RELEASE_SSH_USER:p.sshUser,RELEASE_SSH_KEY:p.sshKeyPath,RELEASE_REMOTE_ROOT:p.remoteRoot,RELEASE_SYSTEMD_SERVICE:p.systemdService,RELEASE_HEALTH_BASE_URL:p.httpPort?`http://127.0.0.1:${p.httpPort}`:''};
 const envText=(o)=>Object.entries(o).map(([k,v])=>`${k}='${v}'`).join('\n')+'\n';
 const runtime={...expected,LOG_LEVEL:'info',CORS_ALLOWED_ORIGINS:p.apiDomain?[`https://${p.apiDomain}`,...(p.corsOrigins??[])].join(','):'',SESSION_TTL:'720h',TIKTOK_CLIENT_SECRET:'',ANALYTICS_GM_USERS:'',MYSQL_DSN:''};
 return new Map([
 ['.release.env','# 由 scripts/bt-project.mjs generate 生成，只包含本机发布目标，无秘密内容。\n'+envText(release)],
 ['deploy/systemd/app.env.example','# 最后修改日期：2026-09-09。复制到新服务器 shared/app.env 后填秘密，不覆盖已有 app.env。\n'+envText(runtime)],
 ['deploy/systemd/linkgame-bt.service',`[Unit]\nDescription=LinkGame BT API\nWants=network-online.target\nAfter=network-online.target\nStartLimitIntervalSec=60\nStartLimitBurst=5\n\n[Service]\nType=simple\nWorkingDirectory=${remote}/current\nEnvironment=AD_POLICY_CONFIG_PATH=${remote}/shared/ad-policy.json\nEnvironmentFile=${remote}/shared/app.env\nExecStart=${remote}/current/bin/linkgame-bt-api\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=15\nKillSignal=SIGTERM\nStandardOutput=journal\nStandardError=journal\nSyslogIdentifier=linkgame-bt\nUMask=0077\nLimitNOFILE=65535\nNoNewPrivileges=true\nRestrictSUIDSGID=true\nLockPersonality=true\nRestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\n\n[Install]\nWantedBy=default.target\n`],
 ['deploy/nginx/linkgame-bt.conf',`# TLS 由运维入口配置；本文件是内部 HTTP 反向代理模板。\nserver {\n    listen 80;\n    server_name ${domain};\n    client_max_body_size 256k;\n    location / {\n        proxy_pass http://127.0.0.1:${port};\n        proxy_http_version 1.1;\n        proxy_set_header Host $host;\n        proxy_set_header X-Real-IP $remote_addr;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n        proxy_connect_timeout 5s;\n        proxy_send_timeout 20s;\n        proxy_read_timeout 20s;\n    }\n}\n`],
 ['deploy/check-target.sh',`#!/bin/sh\n# 在 migration 前运行：比较实际环境与本次发布清单，不输出秘密。\nset -eu\n`+Object.entries(expected).map(([k,v])=>`[ "\${${k}:-}" = '${v}' ] || { echo 'BT 远端 ${k} 与发布清单不一致' >&2; exit 2; }`).join('\n')+'\n'],
 ]);
}
export function checkProject({release=false}={}){
 const p=JSON.parse(readFileSync(join(root,'config/project.json'),'utf8'));
 const errors=validateProject(p,{release});if(errors.length)throw new Error(errors.join('\n'));
 for(const [path,body] of outputs(p))if(!existsSync(join(root,path))||readFileSync(join(root,path),'utf8')!==body)throw new Error(`生成文件过期：${path}；请执行 node scripts/bt-project.mjs generate`);
 if(release&&!existsSync(p.sshKeyPath))throw new Error('本机 SSH 私钥文件不存在');
 return p;
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))try{
 if(process.argv[2]==='generate'){
  const p=JSON.parse(readFileSync(join(root,'config/project.json'),'utf8'));const errors=validateProject(p);if(errors.length)throw new Error(errors.join('\n'));
  for(const [path,body]of outputs(p))writeFileSync(join(root,path),body);
  console.log('BT 服务端目标、环境样例、systemd/nginx 和远端校验已生成；未联网。');
 }else if((process.argv[2]??'check')==='check'){checkProject({release:process.argv.includes('--release')});console.log('BT 服务端配置一致性检查通过；未联网。');}else throw new Error('用法：bt-project.mjs generate|check [--release]');
}catch(e){console.error(e.message);process.exitCode=2;}
