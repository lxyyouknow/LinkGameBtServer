# 通过 SSH stdin 接收配置；不写入其他业务文件，不重启服务。
import sys, os, json, hashlib, pathlib, subprocess, tempfile, shutil, stat, urllib.request, fcntl, datetime

KEYS = ['level_entry_potion', 'empty_tool', 'no_pair', 'progress_chest', 'level_complete', 'daily_gift', 'daily_challenge_replay', 'season_makeup']

def validate(x):
    if set(x) - {'_说明', 'version', 'enabled', 'probabilities'}:
        raise ValueError('配置包含未知字段')
    if not isinstance(x.get('version'), str) or not 0 < len(x['version'].encode()) <= 64 or type(x.get('enabled')) is not bool:
        raise ValueError('配置版本或开关不合法')
    if set(x.get('probabilities', {})) != set(KEYS) or any(type(v) is not int or not 0 <= v <= 100 for v in x['probabilities'].values()):
        raise ValueError('概率必须为八项0～100整数')
    return {k: x[k] for k in ['version', 'enabled', 'probabilities']}

def replace_file(path, data, metadata):
    fd, name = tempfile.mkstemp(prefix='.ad-policy-', dir=str(path.parent))
    try:
        with os.fdopen(fd, 'wb') as f:
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.chmod(name, stat.S_IMODE(metadata.st_mode))
        os.chown(name, metadata.st_uid, metadata.st_gid)
        os.replace(name, path)
    finally:
        if os.path.exists(name): os.unlink(name)

def apply(path, request, fetch_policy, get_pid):
    # flock 覆盖读取、备份、写入、验证和回退，避免两个策划同时同步。
    with open(path.parent / '.ad-policy-sync.lock', 'a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        old = path.read_bytes()
        if hashlib.sha256(old).hexdigest() != request['expectedSha256']:
            raise ValueError('线上配置刚被其他操作修改，本次未覆盖，请重新运行查看差异')
        current = fetch_policy()
        candidate = request['config']
        expected = validate(candidate)
        if all(current[k] == expected[k] for k in ['enabled', 'probabilities']):
            return {'status': 'unchanged', 'policy': current}
        metadata = path.stat()
        before = get_pid()
        if before != request['expectedPid']:
            raise ValueError('检查后服务进程已变化，本次未覆盖，请重试')
        stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d%H%M%S%f')
        backup = path.with_name('ad-policy.json.bak-' + stamp)
        # 先完整备份，再替换正式文件；失败时保留备份供人工恢复。
        with open(backup, 'xb') as f:
            f.write(old)
            f.flush()
            os.fsync(f.fileno())
        shutil.copystat(path, backup)
        data = (json.dumps(candidate, ensure_ascii=False, indent=2) + '\n').encode()
        replace_file(path, data, metadata)
        try:
            actual = fetch_policy()
            if actual != expected: raise ValueError('服务端返回与新配置不一致')
            if get_pid() != before: raise ValueError('验证期间服务进程发生变化')
        except Exception:
            replace_file(path, old, metadata)
            raise ValueError('同步后的接口验证失败，已恢复旧配置；备份：' + str(backup))
        return {'status': 'synced', 'policy': actual, 'backup': str(backup), 'pidBefore': before, 'pidAfter': get_pid(), 'sha256': hashlib.sha256(data).hexdigest()}

def main():
    request = json.load(sys.stdin)
    path = pathlib.Path(request['root']) / 'shared/ad-policy.json'
    if str(path) != '/home/linkgamebt/linkgame-bt-server/shared/ad-policy.json' or path.is_symlink():
        raise ValueError('目标必须为 LinkGameBt shared 配置文件')
    service = request['service']
    def get_pid():
        pid = subprocess.check_output(['systemctl', '--user', 'show', service, '--property=MainPID', '--value'], text=True).strip()
        if not pid.isdigit() or int(pid) <= 0: raise ValueError('服务未运行')
        fields = pathlib.Path('/proc/' + pid + '/environ').read_bytes().split(b'\0')
        actual = next((s.split(b'=',1)[1].decode() for s in fields if s.startswith(b'AD_POLICY_CONFIG_PATH=')), None)
        if actual != str(path): raise ValueError('进程实际读取路径与目标不一致')
        return pid
    def fetch_policy():
        with urllib.request.urlopen(request['health'] + '/v1/ads/policy', timeout=10) as response:
            value = json.load(response)
        return validate(value)
    if request['action'] == 'inspect':
        pid = get_pid(); raw = path.read_bytes(); value = validate(json.loads(raw)); actual = fetch_policy()
        if actual != value: raise ValueError('线上文件与接口不一致，停止同步')
        result = {'status': 'inspected', 'policy': actual, 'sha256': hashlib.sha256(raw).hexdigest(), 'pid': pid}
    elif request['action'] == 'apply':
        get_pid()
        result = apply(path, request, fetch_policy, get_pid)
    else: raise ValueError('未知操作')
    print(json.dumps(result, ensure_ascii=False))

if __name__ == '__main__':
    try: main()
    except Exception as e:
        print(json.dumps({'error': str(e)}, ensure_ascii=False))
        sys.exit(1)
