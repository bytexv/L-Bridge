"""Local end-to-end tests with two deterministic runner fixtures, never model calls.
Run: python3 scripts/team_integration.py /absolute/path/to/l-bridge
"""
import json, os, pathlib, re, subprocess, sys, tempfile, time, urllib.request, urllib.error


def emit(v):
    print(json.dumps(v), flush=True)


class Client:
    def __init__(self, command, args, token):
        self.n = 0
        self.p = subprocess.Popen([command] + args, env=dict(os.environ, LBRIDGE_RUN_TOKEN=token), stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.rpc('initialize', {'protocolVersion': '2025-06-18', 'clientInfo': {'name': 'fixture', 'version': '1'}, 'capabilities': {}})
        self.p.stdin.write(json.dumps({'jsonrpc': '2.0', 'method': 'notifications/initialized'}) + '\n'); self.p.stdin.flush()

    def rpc(self, method, params):
        self.n += 1
        self.p.stdin.write(json.dumps({'jsonrpc': '2.0', 'id': self.n, 'method': method, 'params': params}) + '\n'); self.p.stdin.flush()
        r = json.loads(self.p.stdout.readline())
        assert r['id'] == self.n, r
        return r['result']

    def call(self, name, **args):
        r = self.rpc('tools/call', {'name': name, 'arguments': args})
        if r.get('isError'):
            raise RuntimeError(r['content'][0]['text'])
        if name == 'bridge_mcp' and args['op'] in ('call', 'result'):
            return r
        return json.loads(r['content'][0]['text'])

    def close(self):
        self.p.stdin.close(); self.p.wait(timeout=5)
        assert self.p.returncode == 0, self.p.stderr.read()


def mock_runner():
    if '--version' in sys.argv:
        print('L-Bridge integration fixture 1.0'); return
    if 'status' in sys.argv:
        print('fixture signed in'); return
    if '--mcp-config' in sys.argv:
        cfg = json.loads(pathlib.Path(sys.argv[sys.argv.index('--mcp-config') + 1]).read_text())['mcpServers']['lbridge']
        agent = 'claude'
    else:
        line = next(v for v in sys.argv if v.startswith('mcp_servers.lbridge='))
        cfg = {'command': json.loads(re.search(r'command=("(?:[^"\\]|\\.)*")', line).group(1)),
               'args': json.loads(re.search(r'args=(\[.*?\]),env=', line).group(1)),
               'env': {'LBRIDGE_RUN_TOKEN': json.loads(re.search(r'LBRIDGE_RUN_TOKEN=("[^"]+")', line).group(1))}}
        agent = 'chatgpt'
    prompt = sys.stdin.read()
    if 'NO_WORK_TEST' in prompt:
        if agent == 'claude': emit({'type':'result','subtype':'success','is_error':False,'result':'No work in this fixture.'})
        else: emit({'type':'turn.completed'})
        return
    if 'WAIT_TEST' in prompt:
        time.sleep(25)
    if 'FAIL_TEST' in prompt and agent == 'claude' and 'RECOVER_NOW' not in prompt:
        emit({'type': 'result', 'subtype': 'error_fixture', 'is_error': True, 'result': 'Intentional fixture failure'}); return
    gid = re.search(r'"goal_id":"([a-z0-9]+)"', prompt).group(1)
    c = Client(cfg['command'], cfg['args'], cfg['env']['LBRIDGE_RUN_TOKEN'])
    try:
        c.call('bridge_join', goal_id=gid, role='Builder' if agent == 'claude' else 'Reviewer')
        snapshot = c.call('bridge_sync', goal_id=gid)
        if 'AUTO_SWITCH_TEST' in prompt:
            failure = ('FAIL_CLAUDE' in prompt and agent == 'claude') or ('FAIL_CODEX' in prompt and agent == 'chatgpt') or 'FAIL_BOTH' in prompt
            solo = snapshot.get('solo_agent') == agent
            if failure:
                task = c.call('bridge_task', goal_id=gid, op='create', title=agent+' inherited task')
                c.call('bridge_task', goal_id=gid, op='claim', task_id=task['id'])
                c.call('bridge_files',goal_id=gid,op='write',path=agent+'-partial.txt',content='Saved before the connection failed.',expected_hash='')
                print('Intentional connection failure: '+agent,file=sys.stderr);sys.exit(1)
            if solo:
                if 'SURVIVOR_FAIL' in prompt: print('Survivor also lost connection',file=sys.stderr);sys.exit(1)
                for task in snapshot['tasks']:
                    assert task['owner'] == agent, task
                    if task['status'] != 'done': c.call('bridge_task',goal_id=gid,op='done',task_id=task['id'],evidence='Inspected inherited work and completed it.')
                c.call('bridge_summary',goal_id=gid,text='Completed the inherited goal alone. No peer review.',expected_version=snapshot['summary_version'])
                c.call('bridge_finish',goal_id=gid,evidence='Solo fixture completed all inherited tasks.')
            if agent=='claude':emit({'type':'result','subtype':'success','is_error':False,'result':'Auto switch fixture turn.'})
            else:emit({'type':'turn.completed'})
            return
        entries = c.call('bridge_files', goal_id=gid, op='list')['entries']
        if agent == 'claude' and not any(x['name'] == 'fixture.txt' for x in entries):
            l = c.call('bridge_mcp', goal_id=gid, op='acquire', server_id='fixture')
            assert l['acquired']
            result = c.call('bridge_mcp', goal_id=gid, op='call', server_id='fixture', lease=l['lease'], tool='echo', arguments={'text': 'scene'})
            assert result['content'][1]['type'] == 'image', result
            c.call('bridge_mcp', goal_id=gid, op='release', server_id='fixture', lease=l['lease'])
            c.call('bridge_files', goal_id=gid, op='write', path='fixture.txt', content='Both fixture processes share this file.', expected_hash='')
            snapshot = c.call('bridge_sync', goal_id=gid)
            c.call('bridge_summary', goal_id=gid, text='Fixture wrote an artifact and shared an external MCP result. This is a protocol test, not live AI.', expected_version=snapshot['summary_version'])
        snapshot = c.call('bridge_sync', goal_id=gid, limit=50)
        if snapshot['summary'] and len(snapshot['roles']) == 2:
            c.call('bridge_files', goal_id=gid, op='read', path='fixture.txt')
            receipts = [json.loads(x['body'])['receipt_id'] for x in snapshot['messages'] if x['kind'] == 'mcp_result']
            assert receipts, snapshot
            shared = c.call('bridge_mcp', goal_id=gid, op='result', receipt_id=receipts[-1])
            assert shared['content'][1]['type'] == 'image'
            c.call('bridge_finish', goal_id=gid, evidence='Read shared artifact and external MCP receipt in a separate fixture process.')
    finally:
        c.close()
    if agent == 'claude':
        emit({'type': 'result', 'subtype': 'success', 'is_error': False, 'result': 'Fixture turn complete.', 'usage': {'input_tokens': 10, 'output_tokens': 5}})
    else:
        emit({'type': 'item.completed', 'item': {'type': 'agent_message', 'text': 'Fixture review complete.'}})
        emit({'type': 'turn.completed', 'usage': {'input_tokens': 12, 'output_tokens': 6}})


def mock_mcp():
    for line in sys.stdin:
        r = json.loads(line)
        if 'id' not in r:
            continue
        method = r['method']
        if method == 'initialize':
            result = {'protocolVersion': '2025-06-18', 'capabilities': {'tools': {}}, 'serverInfo': {'name': 'fixture', 'version': '1'}}
        elif method == 'tools/list':
            result = {'tools': [{'name': 'echo', 'description': 'Fixture only', 'inputSchema': {'type': 'object'}}]}
        else:
            result = {'content': [{'type': 'text', 'text': 'Fixture scene result'}, {'type': 'image', 'mimeType': 'image/png', 'data': 'dGVzdA=='}]}
        emit({'jsonrpc': '2.0', 'id': r['id'], 'result': result})


def integration(exe):
    with tempfile.TemporaryDirectory(prefix='lbridge-v02-') as temp:
        root = pathlib.Path(temp)
        fixture = root / 'runner-fixture'
        fixture.write_text('#!' + sys.executable + '\nimport runpy,sys\nsys.argv.insert(1,"--mock-runner")\nrunpy.run_path(' + repr(str(pathlib.Path(__file__).resolve())) + ',run_name="__main__")\n'); fixture.chmod(0o700)
        server = subprocess.Popen([exe, '--serve', '--no-open', '--data-dir', str(root / 'data'), '--port', '0'], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        try:
            endpoint = root / 'data/endpoint.json'
            for _ in range(100):
                if endpoint.exists(): break
                if server.poll() is not None: raise RuntimeError(server.stderr.read())
                time.sleep(.05)
            ep = json.loads(endpoint.read_text())
            def api(path, data=None):
                req = urllib.request.Request(ep['url'] + '/api/' + path, data=None if data is None else json.dumps(data).encode(), headers={'Authorization': 'Bearer ' + ep['token'], 'Content-Type': 'application/json'})
                try:
                    with urllib.request.urlopen(req, timeout=30) as r: return json.load(r)
                except urllib.error.HTTPError as e: raise RuntimeError(e.read().decode())
            cfg = {'claude': str(fixture), 'codex': str(fixture), 'rounds': 4, 'turn_seconds': 30, 'servers': [{'id': 'fixture', 'name': 'Test MCP', 'command': sys.executable, 'args': [str(pathlib.Path(__file__).resolve()), '--mock-mcp']}]}
            api('runner/settings', cfg)
            assert all(x['authenticated'] for x in api('runner/check', {})['checks'])
            assert len(api('mcp/test', {'server_id': 'fixture'})['tools']) == 1
            def new_goal(prompt, auto_switch=False):
                gid = api('create', {'title': 'Team integration fixture', 'prompt': prompt, 'mode': 'commands', 'limit': 120, 'auto_switch':auto_switch})['id']
                api('mcp/allow', {'goal_id': gid, 'server_ids': ['fixture']})
                return gid
            def goal(gid): return next(g for g in api('state')['goals'] if g['id'] == gid)
            def wait(gid, predicate, seconds=20):
                deadline = time.monotonic() + seconds
                while time.monotonic() < deadline:
                    g = goal(gid)
                    if predicate(g): return g
                    time.sleep(.1)
                raise AssertionError(goal(gid))
            gid = new_goal('Exercise two local fixtures and a shared MCP server; no model inference.')
            api('team/start', {'goal_id': gid})
            g = wait(gid, lambda g: g.get('team', {}).get('status') != 'running')
            assert g['status'] == 'completed', g
            assert all(a['usage_reported'] for a in g['team']['agents'].values()), g
            assert sum(m['kind'] == 'mcp_call' for m in g['messages']) == 1
            assert (pathlib.Path(g['root']) / 'fixture.txt').exists()
            assert not list((root / 'data/runner-work').iterdir()), 'ephemeral runner credentials were not removed'
            waiting = new_goal('WAIT_TEST: cancellation fixture', True)
            api('team/start', {'goal_id': waiting})
            wait(waiting, lambda g: all(a['status'] == 'running' for a in g['team']['agents'].values()))
            api('message', {'goal_id': waiting, 'body': 'A direction submitted while runners are active.'})
            api('team/stop', {'goal_id': waiting})
            g = wait(waiting, lambda g: g['team']['status'] != 'running', 8)
            assert g['status'] == 'paused' and g['team']['status'] == 'stopped', g
            failed = new_goal('FAIL_TEST: propagate provider errors')
            api('team/start', {'goal_id': failed})
            g = wait(failed, lambda g: g['team']['status'] != 'running')
            assert g['team']['status'] == 'error' and 'Intentional fixture failure' in g['team']['error'], g
            assert g['team']['agents']['chatgpt']['status'] != 'error', 'peer was unnecessarily cancelled'
            previous_id = g['team']['id']
            api('message', {'goal_id': failed, 'body': 'RECOVER_NOW: continue from preserved work.'})
            api('team/continue', {'goal_id': failed})
            g = wait(failed, lambda g: g['team']['status'] != 'running')
            assert g['status'] == 'completed', g
            assert g['run_history'][-1]['id'] == previous_id
            assert 'Intentional fixture failure' in g['run_history'][-1]['error']
            assert any(m['body'] == 'RECOVER_NOW: continue from preserved work.' for m in g['messages'])
            for failed, survivor in [('claude','chatgpt'),('chatgpt','claude')]:
                switch = new_goal('AUTO_SWITCH_TEST '+('FAIL_CLAUDE' if failed=='claude' else 'FAIL_CODEX'), True)
                api('team/start', {'goal_id':switch})
                g = wait(switch, lambda g:g['team']['status']!='running')
                assert g['status']=='completed' and g['team']['solo_agent']==survivor, g
                assert len(g['votes'])==1 and failed not in g['votes'],g
                assert all(t['status']=='done' and t['owner']==survivor for t in g['tasks']),g
                assert (pathlib.Path(g['root'])/(failed+'-partial.txt')).exists()
                assert sum('Intentional connection failure' in e['text'] for e in g['team']['events'])==1,g
            for suffix in ['FAIL_BOTH','FAIL_CLAUDE SURVIVOR_FAIL']:
                switch=new_goal('AUTO_SWITCH_TEST '+suffix,True)
                api('team/start',{'goal_id':switch})
                g=wait(switch,lambda g:g['team']['status']!='running')
                assert g['status']=='paused' and g['team']['status']=='error',g
                assert g['team']['round']<=2,g
            # An unavailable executable can hand over before any round is launched.
            api('runner/settings',{**cfg,'claude':str(root/'missing-runner')})
            switch=new_goal('AUTO_SWITCH_TEST preflight fallback',True)
            api('team/start',{'goal_id':switch})
            g=wait(switch,lambda g:g['team']['status']!='running')
            assert g['status']=='completed' and g['team']['solo_agent']=='chatgpt',g
            api('runner/settings',cfg)
            idle = new_goal('NO_WORK_TEST: detect lack of progress')
            api('team/start', {'goal_id': idle})
            g = wait(idle, lambda g: g['team']['status'] != 'running')
            assert g['team']['status'] == 'waiting' and g['team']['round'] == 2, g
            print(json.dumps({'result': 'PASS', 'checks': ['two executable runner fixtures', 'scoped stdio adapters', 'live shared MCP transport', 'native image handoff', 'shared artifact', 'automatic next rounds', 'completion handshake', 'reported usage', 'temporary credential cleanup', 'active user direction', 'stop cancels both children', 'provider failure preserves peer work', 'continue after error completes same goal', 'previous error retained', 'no-progress stop after two rounds','auto switch in both directions','inherited task ownership and evidence','solo completion without fake peer votes','both unavailable pauses','survivor failure stops','missing executable fallback']}))
            api('shutdown', {})
            server.wait(timeout=6)
        finally:
            if server.poll() is None: server.terminate(); server.wait(timeout=6)


if __name__ == '__main__':
    if '--mock-runner' in sys.argv:
        sys.argv.remove('--mock-runner'); mock_runner()
    elif '--mock-mcp' in sys.argv:
        mock_mcp()
    else:
        integration(str(pathlib.Path(sys.argv[1]).resolve()))
