#!/usr/bin/env python3
"""Emit bounded real Manager actions to an approved Collector; never read a Kube Secret.

Creates two loopback port-forwards to one verified approved Collector pod.
Retains its isolated registry/key in the protected operator output directory.
No live Manager, CLI migration, SQL target or cloud billing setting is changed.
"""
import argparse
import atexit
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import socket
import sqlite3
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


def loopback(value, scheme):
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme != scheme or parsed.hostname != '127.0.0.1' or not parsed.port or parsed.username or parsed.password or parsed.path not in ('', '/') or parsed.query or parsed.fragment:
        raise ValueError('acceptance requires an explicit loopback port-forward origin')
    return value.rstrip('/')


def counters(origin):
    # Fixed aggregate metrics only, never a payload/header diagnostic endpoint.
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(origin + '/metrics', timeout=3) as response:
        lines = response.read(1 << 20).decode().splitlines()
    result = {}
    for metric in ['receiver_accepted_log_records', 'exporter_sent_log_records', 'exporter_send_failed_log_records']:
        result[metric] = sum(float(line.rsplit(' ', 1)[1]) for line in lines if re.match(r'^otelcol_' + metric + r'(?:_total)?(?:\{| )', line))
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--collector-pod', required=True)
    parser.add_argument('--tls-directory', required=True, type=Path)
    parser.add_argument('--output-directory', required=True, type=Path)
    parser.add_argument('--binary', required=True, type=Path)
    args = parser.parse_args()
    def pod_identity():
        query = '{.metadata.uid}{" "}{.spec.containers[0].image}{" "}{.status.conditions[?(@.type=="Ready")].status}'
        return subprocess.check_output(['kubectl', '-n', 'flyway-manager', 'get', 'pod', args.collector_pod, '-o', 'jsonpath=' + query], stderr=subprocess.DEVNULL).decode().split()
    pod_before = pod_identity()
    expected_image = 'otel/opentelemetry-collector-contrib@sha256:1175b03003053962012f5a231f5567f66a3e8fdb960c7835c0e48f7aa1555806'
    if len(pod_before) != 3 or pod_before[1:] != [expected_image, 'True']:
        raise RuntimeError('collector identity/readiness not verified')
    reservations = [socket.socket(), socket.socket()]
    for reservation in reservations:
        reservation.bind(('127.0.0.1', 0))
    ports = [reservation.getsockname()[1] for reservation in reservations]
    for reservation in reservations:
        reservation.close()
    forward = subprocess.Popen(['kubectl', '-n', 'flyway-manager', 'port-forward', 'pod/' + args.collector_pod, f'{ports[0]}:4318', f'{ports[1]}:8888', '--address=127.0.0.1'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    def close_forward():
        forward.terminate()
        try:
            forward.wait(timeout=3)
        except subprocess.TimeoutExpired:
            forward.kill()
            forward.wait()
    atexit.register(close_forward)
    collector = loopback(f'https://127.0.0.1:{ports[0]}', 'https')
    metrics = loopback(f'http://127.0.0.1:{ports[1]}', 'http')
    os.umask(0o077)
    output = args.output_directory.resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    tls = args.tls_directory.resolve()
    deadline = time.monotonic() + 10
    while True:
        try:
            before = counters(metrics)
            break
        except (urllib.error.URLError, OSError):
            if forward.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('collector port-forwards unavailable') from None
            time.sleep(0.1)
    with socket.socket() as reservation:
        reservation.bind(('127.0.0.1', 0))
        port = reservation.getsockname()[1]
    origin = f'http://127.0.0.1:{port}'
    config = output / 'manager.json'
    data = output / 'manager-data'
    config.write_text(json.dumps({'listen_address': f'127.0.0.1:{port}', 'data_dir': str(data), 'public_url': '', 'log_level': 'warn', 'observability': {
        'enabled': True, 'collector_endpoint': collector,
        'ca_file': str(tls / 'ca.crt'),
        'client_certificate_file': str(tls / 'client.crt'),
        'client_key_file': str(tls / 'client.key'),
    }}))
    binary = args.binary.resolve()
    environment = os.environ.copy()
    # The fixture must not inherit an existing installation/listener override.
    for key in list(environment):
        if key.startswith('FLYWAY_MANAGER_'):
            del environment[key]
    environment['FLYWAY_MANAGER_CONFIG'] = str(config)
    child = subprocess.Popen([str(binary)], env=environment, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def request(path, payload=None, csrf=None):
        headers = {'Origin': origin}
        if csrf:
            headers['X-CSRF-Token'] = csrf
        if payload is not None:
            headers['Content-Type'] = 'application/json'
        call = urllib.request.Request(origin + '/api/v1' + path, headers=headers, data=None if payload is None else json.dumps(payload).encode())
        with opener.open(call, timeout=5) as response:
            document = json.load(response)
            return document.get('data', document)

    try:
        deadline = time.monotonic() + 15
        while True:
            if child.poll() is not None:
                raise RuntimeError('isolated Manager startup failed')
            try:
                request('/health')
                break
            except (urllib.error.URLError, OSError):
                if time.monotonic() > deadline:
                    raise RuntimeError('isolated Manager startup timed out') from None
                time.sleep(0.1)
        status = request('/auth/status')
        setup = Path(status['bootstrap_token_file']).read_text().strip()
        session = request('/auth/bootstrap', {'username': 'phase9-isolated-admin', 'display_name': 'Phase9 Isolated Admin', 'password': secrets.token_urlsafe(32), 'setup_token': setup})
        project = request('/projects', {'name': 'Phase9 bounded OTLP acceptance ' + secrets.token_hex(4)}, session['csrf_token'])
        registry = sqlite3.connect(f'file:{data}/manager.db?mode=ro', uri=True)
        try:
            installation = registry.execute("SELECT value FROM installation_metadata WHERE key='installation_id'").fetchone()[0]
            row = registry.execute("SELECT id,timestamp,request_id FROM audit_events WHERE action='project.create' AND target=?", (project['id'],)).fetchone()
            total = registry.execute('SELECT count(*) FROM audit_events').fetchone()[0]
        finally:
            registry.close()
        deadline = time.monotonic() + 85
        after = before
        while time.monotonic() < deadline:
            after = counters(metrics)
            if after['receiver_accepted_log_records'] - before['receiver_accepted_log_records'] >= total and after['exporter_sent_log_records'] - before['exporter_sent_log_records'] >= total:
                break
            time.sleep(1)
        evidence = {
            'source': 'isolated current-source Manager via actual HTTP bootstrap/project APIs; no SQL execution',
            'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
            'collector_pod': args.collector_pod, 'collector_pod_uid': pod_before[0],
            'source_specific_transport_receipt': 'NOT_VERIFIED; aggregate counters do not identify records',
            'source_installation_id': installation,
            'source_audit_id': row[0], 'source_timestamp_unix': row[1],
            'project_id': project['id'], 'correlation_id': row[2],
            'event_id': hashlib.sha256(f'{installation}:audit:{row[0]}'.encode()).hexdigest(),
            'local_audit_rows': total,
            'collector_counters_before': before, 'collector_counters_after': after,
            'aggregate_receiver_acceptance': after['receiver_accepted_log_records'] - before['receiver_accepted_log_records'] >= total,
            'aggregate_exporter_transport_completion': after['exporter_sent_log_records'] - before['exporter_sent_log_records'] >= total,
            'indexed_search': 'NOT_VERIFIED; requires separate read-only query access',
            'dashboard': 'NOT_VERIFIED',
            'kubernetes_secret_values_read_or_decoded': False,
        }
        if pod_identity() != pod_before:
            raise RuntimeError('collector pod changed during acceptance')
        (output / 'acceptance.json').write_text(json.dumps(evidence, indent=2) + '\n')
        if not evidence['aggregate_receiver_acceptance'] or not evidence['aggregate_exporter_transport_completion']:
            raise RuntimeError('collector transport acceptance not established; sanitized evidence retained')
        print('Aggregate transport activity saved; source-specific receipt and indexing remain NOT_VERIFIED.')
    finally:
        child.terminate()
        try:
            child.wait(timeout=5)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        # No response body, configuration, password, cookie or Secret content.
        print(f'Acceptance failed: {type(error).__name__}; inspect protected evidence and aggregate counters.')
        raise SystemExit(1) from None
