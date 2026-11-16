"""Exercise simultaneous real adapters, an occupied default port and reconnection.
Usage: python3 scripts/connection_integration.py EXECUTABLE
No model calls. The test reserves port 48471 for an unrelated fixture server.
"""
import concurrent.futures, http.server, json, os, pathlib, queue, signal
import subprocess, sys, tempfile, threading, time, urllib.request

exe = str(pathlib.Path(sys.argv[1]).resolve())

class OtherApp(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'Unrelated application')
    def log_message(self, *args): pass

class Client:
    def __init__(self, directory, agent):
        self.seq = 0; self.lines = queue.Queue()
        env = dict(os.environ); env.pop('LBRIDGE_RUN_TOKEN', None)
        self.p = subprocess.Popen([exe, '--mcp', '--agent', agent, '--data-dir', str(directory)],
                                  env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True)
        def reader():
            for line in self.p.stdout: self.lines.put(line)
        threading.Thread(target=reader, daemon=True).start()
        self.rpc('initialize', {'protocolVersion': '2025-06-18', 'capabilities': {},
                               'clientInfo': {'name': 'connection-fixture', 'version': '1'}})
    def rpc(self, method, params):
        self.seq += 1
        self.p.stdin.write(json.dumps({'jsonrpc': '2.0', 'id': self.seq, 'method': method, 'params': params}) + '\n')
        self.p.stdin.flush()
        reply = json.loads(self.lines.get(timeout=20))
        assert reply['id'] == self.seq and 'error' not in reply, reply
        return reply['result']
    def call(self, name, **arguments):
        result = self.rpc('tools/call', {'name': name, 'arguments': arguments})
        assert not result.get('isError'), result
        return json.loads(result['content'][0]['text'])
    def close(self):
        if self.p.poll() is None:
            self.p.stdin.close()
            try: self.p.wait(timeout=5)
            except subprocess.TimeoutExpired: self.p.kill(); self.p.wait(timeout=5)

other = http.server.ThreadingHTTPServer(('127.0.0.1', 48471), OtherApp)
threading.Thread(target=other.serve_forever, daemon=True).start()
clients = []
with tempfile.TemporaryDirectory(prefix='lbridge connection ') as temp:
    directory = pathlib.Path(temp); endpoint = directory / 'endpoint.json'
    def record(): return json.loads(endpoint.read_text())
    def command(*args):
        result = subprocess.run([exe, *args, '--data-dir', temp], capture_output=True, text=True, timeout=15)
        assert result.returncode == 0, result.stderr
    def both(name, **args):
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            return list(pool.map(lambda c: c.call(name, **args), clients))
    try:
        clients = [Client(directory, agent) for agent in ('claude', 'chatgpt')]
        both('bridge_goals')  # No service yet: both adapters try to start one.
        first = record()
        assert not first['url'].endswith(':48471'), first['url']
        time.sleep(.6)
        assert record() == first, 'simultaneous starts produced multiple services'
        gid = clients[0].call('bridge_create', title='Shared reconnect goal', prompt='Preserve this goal across reconnects.')['goal_id']
        both('bridge_join', goal_id=gid, role='Connection tester')
        clients[0].call('bridge_send', goal_id=gid, body='One shared service.', summary='Connection verified.', nonce='connection-test')
        assert any(m['body'] == 'One shared service.' for m in clients[1].call('bridge_sync', goal_id=gid)['messages'])
        command('--stop')
        assert not endpoint.exists()
        both('bridge_sync', goal_id=gid)  # Existing adapters reconnect after stop.
        second = record(); assert second['token'] != first['token']
        assert all(result['goal_id'] == gid for result in both('bridge_sync', goal_id=gid))
        # Test crash recovery of the process-owned lock, with a stale endpoint.
        if os.name == 'nt':
            subprocess.run(['taskkill', '/F', '/PID', str(second['pid'])], check=True, capture_output=True)
        else:
            os.kill(second['pid'], signal.SIGKILL)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(second['url'] + '/api/health', timeout=.2): pass
            except OSError: break
            time.sleep(.05)
        both('bridge_sync', goal_id=gid)
        third = record(); assert third['token'] != second['token']
        assert not third['url'].endswith(':48471')
        with urllib.request.urlopen('http://127.0.0.1:48471/', timeout=2) as r:
            assert r.read() == b'Unrelated application'
        print(json.dumps({'result': 'PASS', 'checks': ['simultaneous cold adapters share one service',
              'occupied default port selects a free loopback port', 'both agents find the same goal',
              'cross-agent message handoff', 'existing adapters reconnect after stop',
              'crash releases service lock', 'saved goal retained', 'unrelated application untouched']}))
    finally:
        for c in clients: c.close()
        if endpoint.exists():
            subprocess.run([exe, '--stop', '--data-dir', temp], capture_output=True, timeout=15)
        other.shutdown(); other.server_close()
