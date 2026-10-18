"""Validate SDK-style OTLP headers inside the pod; never log credential contents."""
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import unquote


def authorization_header(value):
    if not value or len(value) > 16384 or any(ord(c) < 32 and c != '\t' for c in value):
        raise ValueError('invalid OTLP header configuration')
    entries = value.split(',')
    if len(entries) != 1:
        raise ValueError('expected one approved authorization header')
    name, separator, encoded = entries[0].partition('=')
    if not separator or name.strip().lower() != 'authorization' or re.search(r'%(?![0-9A-Fa-f]{2})', encoded):
        raise ValueError('invalid OTLP header configuration')
    header = unquote(encoded.strip(), encoding='utf-8', errors='strict')
    if not re.fullmatch(r'(?i:ApiKey) [A-Za-z0-9+/=_-]{32,8192}', header):
        raise ValueError('invalid OTLP authentication format')
    return {'Authorization': 'ApiKey ' + header.partition(' ')[2]}


def main():
    # SecretKeyRef consumption only: no Kubernetes API, shell expansion or network.
    headers = authorization_header(os.environ.pop('OTEL_EXPORTER_OTLP_HEADERS', ''))
    target = Path('/run/collector-auth/headers.json')
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(headers, output)
    print('OTLP_AUTH_HEADER_FORMAT_VALID')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('OTLP_AUTH_HEADER_CONFIGURATION_REJECTED', file=sys.stderr)
        sys.exit(1)
