#!/usr/bin/env python3
"""Check the selected source tree without printing credential contents.

Uses tracked paths and their working-tree contents. Stage intended additions and
removals first. This is a release hygiene gate, not a complete security audit.
"""
from pathlib import Path, PurePosixPath
import json
import re
import subprocess
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parent.parent
ALLOWED_ROOTS = {'.github', 'cmd', 'config', 'docs', 'internal', 'pkg', 'scripts', 'third_party', 'web'}
ALLOWED_FILES = {'.gitignore', 'LICENSE', 'Makefile', 'README.md', 'CHANGELOG.md', 'CONTRIBUTING.md', 'go.mod', 'go.sum'}
FORBIDDEN_PARTS = {'private', 'backups', 'deployments', 'node_modules', '.local', '.git', 'runtime', 'output', 'dist', 'spike'}
FORBIDDEN_SUFFIXES = {'.db', '.sqlite', '.sqlite3', '.log', '.pcap', '.pcapng', '.har', '.pem', '.key', '.p12', '.pfx', '.bak', '.backup', '.zip', '.gz', '.tgz', '.pdf', '.exe', '.bin'}
PUBLIC_IMAGES = {'third_party/sipgo/icons/avero.png', 'third_party/sipgo/icons/babelforce-logo.png', 'third_party/sipgo/icons/icon.png'}
IMAGE_SUFFIXES = {'.png', '.jpg', '.jpeg', '.webp', '.gif', '.heic'}
SECRET_RULES = {
    'private-key': re.compile(r'-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----'),
    'github-token': re.compile(r'\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{60,})\b'),
    'aws-access-key': re.compile(r'\b(?:AKIA|ASIA)[A-Z0-9]{16}\b'),
    'telegram-token': re.compile(r'\b\d{8,12}:[A-Za-z0-9_-]{30,}\b'),
    'credential-url': re.compile(r'https?://[^\s/:@]+:[^\s/@]{8,}@(?P<host>[A-Za-z0-9.-]+)'),
}


def main():
    raw = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT)
    paths = [p for p in raw.decode().split('\0') if p]
    errors = []
    texts = {}
    for name in paths:
        path = PurePosixPath(name)
        local = ROOT / name
        if path.parts[0] not in ALLOWED_ROOTS and name not in ALLOWED_FILES:
            errors.append((name, 'unexpected root path'))
        if any(p in FORBIDDEN_PARTS for p in path.parts) or path.suffix.lower() in FORBIDDEN_SUFFIXES:
            errors.append((name, 'private/runtime/artifact path'))
        if re.search(r'(?i)(?:^|/)(?:docker[^/]*|[^/]*docker-compose[^/]*|compose\.ya?ml)$', name):
            errors.append((name, 'container deployment file'))
        if path.name.startswith('.env') or re.search(r'(?i)(?:credentials|activation[-_]?code|auth\.curl|\.db-(?:wal|shm|journal))', path.name):
            errors.append((name, 'sensitive filename'))
        if path.suffix.lower() in IMAGE_SUFFIXES and name not in PUBLIC_IMAGES:
            errors.append((name, 'unreviewed raster image'))
        if name.startswith('config/') and name != 'config/config.example.yaml':
            errors.append((name, 'non-template configuration'))
        if not local.is_file() or local.is_symlink():
            errors.append((name, 'missing file or symlink; stage intended removals'))
            continue
        if name in PUBLIC_IMAGES:
            continue
        try:
            text = local.read_text(encoding='utf-8')
        except UnicodeDecodeError:
            if name.startswith('third_party/sipgo/sip/testdata/torture/') and name.endswith('.dat'):
                # Upstream SIP torture messages intentionally contain invalid
                # UTF-8 and multipart bytes; keep these protocol fixtures.
                text = local.read_bytes().decode('latin-1')
            else:
                errors.append((name, 'unexpected binary file'))
                continue
        texts[name] = text
        for rule, pattern in SECRET_RULES.items():
            for match in pattern.finditer(text):
                if rule == 'credential-url' and (name.endswith('_test.go') or '/tests/' in name):
                    host = match.group('host').lower()
                    if host.endswith('.invalid') or host in {'example.com', 'example.org', 'example.net'}:
                        continue
                errors.append((name, rule))
        # Do not encode any particular developer's identity in this check.
        if re.search('/' + r'Users/[^\s/]+|/' + r'home/(?!runner\b|user\b|test\b)[^\s/]+', text):
            errors.append((name, 'personal absolute path'))

    version = json.loads(texts.get('web/package.json', '{}')).get('version')
    lock = json.loads(texts.get('web/package-lock.json', '{}'))
    if version != '1.0.0' or lock.get('version') != version or lock.get('packages', {}).get('', {}).get('version') != version:
        errors.append(('web/package.json', 'release version mismatch'))
    if 'VERSION ?= v1.0.0' not in texts.get('Makefile', '') or 'Version = "v1.0.0"' not in texts.get('internal/global/version.go', ''):
        errors.append(('Makefile', 'backend release version mismatch'))
    template = texts.get('config/config.example.yaml', '')
    if 'CHANGE_ME_BEFORE_START' not in template or '127.0.0.1:8788' not in template or 'devices: []' not in template:
        errors.append(('config/config.example.yaml', 'unsafe or missing template defaults'))

    for name, text in texts.items():
        if name not in {'README.md', 'CHANGELOG.md', 'CONTRIBUTING.md'} and not name.startswith('docs/'):
            continue
        if not name.endswith('.md'):
            continue
        for href in re.findall(r'\]\(([^)]+)\)', text):
            if re.match(r'^[a-z]+://|^#', href):
                continue
            target = unquote(href.split('#', 1)[0])
            resolved = (ROOT / name).parent / target
            if not resolved.exists() or not resolved.resolve().is_relative_to(ROOT):
                errors.append((name, 'broken or external local link'))
    if errors:
        for name, reason in sorted(set(errors)):
            print(f'FAIL {name}: {reason}')
        return 1
    print(f'PASS: {len(paths)} source files; release paths, common credential patterns, versions, template and documentation links checked.')
    return 0


if __name__ == '__main__':
    sys.exit(main())
