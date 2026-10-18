#!/usr/bin/env python3
"""Operator-only loopback relay to the approved LDAP Pod's existing LDAPS listener.

Each TCP client gets a separate Kubernetes exec stream running nc. TLS remains
end-to-end and is never terminated, inspected or logged here. No Secret reads,
resources, credentials, directory writes or workload configuration changes.
This avoids losing unrelated LDAP connections when a shared port-forward dies.
"""
import json
import signal
import socket
import subprocess
import threading

NAMESPACE = 'flyway-manager-identity-lab'
POD = 'openldap-0'
IMAGE = 'docker.io/osixia/openldap@sha256:b23155ca4217ea928f2fec86453aabf87168a221150f26bcdcef7f3a3c84d35d'
state = json.loads(subprocess.check_output(['kubectl', '-n', NAMESPACE, 'get', 'pod', POD, '-o', 'json'], stderr=subprocess.DEVNULL))
if state['spec']['containers'][0]['image'] != IMAGE or not state['status']['containerStatuses'][0]['ready']:
    raise SystemExit('Approved LDAP Pod image/readiness not verified')
print('Verified LDAP Pod UID:', state['metadata']['uid'], flush=True)
slots = threading.BoundedSemaphore(16)
children = set()
lock = threading.Lock()
listener = socket.socket()
listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
listener.bind(('127.0.0.1', 1636))
listener.listen(16)

def stop(*_):
    listener.close()
    with lock:
        for child in list(children):
            child.terminate()
    raise SystemExit(0)

signal.signal(signal.SIGINT, stop)
signal.signal(signal.SIGTERM, stop)

def serve(client):
    child = None
    try:
        client.settimeout(60)
        child = subprocess.Popen(['kubectl', '-n', NAMESPACE, 'exec', '-i', POD, '-c', 'openldap', '--', 'nc', '-w', '60', '127.0.0.1', '6360'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        with lock:
            children.add(child)
        def upload():
            try:
                while True:
                    data = client.recv(65536)
                    if not data:
                        break
                    child.stdin.write(data)
                    child.stdin.flush()
            except (OSError, ValueError):
                pass
            finally:
                try:
                    child.stdin.close()
                except OSError:
                    pass
        threading.Thread(target=upload, daemon=True).start()
        deadline = threading.Timer(60, child.terminate)
        deadline.start()
        try:
            while True:
                data = child.stdout.read1(65536)
                if not data:
                    break
                client.sendall(data)
        finally:
            deadline.cancel()
    except (OSError, ValueError):
        pass
    finally:
        client.close()
        if child:
            child.terminate()
            try:
                child.wait(timeout=3)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
            with lock:
                children.discard(child)
        slots.release()

while True:
    client, _ = listener.accept()
    if slots.acquire(blocking=False):
        threading.Thread(target=serve, args=(client,), daemon=True).start()
    else:
        client.close()
