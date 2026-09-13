#!/usr/bin/env python3
"""Focused KHive regression checks. Build locally; run Linux tests in isolation.

Usage: python3 scripts/check-stability.py --ssh configured-linux-alias
       sudo -E python3 scripts/check-stability.py --local

The SSH target must permit creating network namespaces and dropping to uid 65534.
No production API, modem, service restart, or physical interface is used.
"""
import argparse, datetime, json, os, pathlib, re, shlex, subprocess, sys, tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
TAGS = 'with_utls,nomsgpack'
CHECKS = {
    'config': r'^TestKHive',
    'modem': r'^TestKHive',
    'esim': r'^Test(KHive|SwitchProfile|Clone|Delete|Download|BuildSpaceDelta|FindDelete|FindNotification|ResolveDownload|ResolveDelete|ClassifyDownload|SafeListNotification|RecoverDownload|RetryNotification|ListNotifications)',
    'ecm': r'^TestECM',
    'device': r'^Test(KHive|HandleESIMSwitch|NewESIMManagerForWorker|RestoreRadio|RefreshPostSwitchIdentity|ConvergePostSwitch|.*CardPolicy|VoWiFiToggleCycle)',
    'api': r'^Test(Esim|FormatEsim|WriteEsim|HandleEsim|KHive|.*CardPolicy|VoWiFiToggleCycle|OverviewStreamEmitVersion|OverviewDisplayConfig)',
}
FRONTEND = ['cardPolicyState.test.ts', 'deviceEsimProgress.test.ts', 'deviceEsimOptimistic.test.ts', 'deviceEsimOperationNotice.test.ts', 'deviceEsimQr.test.ts']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument('--ssh', metavar='HOST', help='explicit, configured Linux SSH target')
    target.add_argument('--local', action='store_true', help='isolated tests on this Linux host; requires root')
    args = parser.parse_args()
    if args.ssh and not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.@-]*', args.ssh):
        parser.error('use an SSH host/alias, not shell options')
    if args.local and (sys.platform != 'linux' or os.geteuid() != 0):
        parser.error('--local requires Linux and root for network namespace creation')
    stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
    logdir = ROOT / 'output' / 'stability' / stamp
    logdir.mkdir(parents=True)
    results = []

    def run(name, command, env=None):
        print('RUN', name, flush=True)
        log = logdir / (name + '.log')
        with log.open('w') as stream:
            completed = subprocess.run(command, cwd=ROOT, env=env, stdout=stream, stderr=subprocess.STDOUT)
        content = log.read_text(errors='replace')
        passed = sum(line.startswith('--- PASS:') for line in content.splitlines())
        results.append({'check': name, 'ok': completed.returncode == 0, 'top_level_go_passes': passed, 'log': log.name})
        if completed.returncode:
            lines=content.splitlines()
            for i,line in enumerate(lines):
                if line.startswith('--- FAIL:'):
                    print('\n'.join(lines[max(0,i-4):i+1]),file=sys.stderr)
            print('\n'.join(lines[-15:]), file=sys.stderr)
            raise RuntimeError(name + ' failed; dependent checks stopped')
        print('PASS', name, '(' + str(passed) + ' Go tests)' if passed else '', flush=True)
        return content

    def remote(command):
        return ['ssh', '-o', 'BatchMode=yes', args.ssh, command]

    env = os.environ.copy()
    env.update(GOWORK='off', GOOS='linux', GOARCH='amd64', CGO_ENABLED='0')
    remote_dir = None
    files = ['runner'] + [name + '.test' for name in CHECKS]
    try:
        if args.ssh:
            info = run('target', remote('uname -s; uname -m; id -u'))
            if info.split() != ['Linux', 'x86_64', '0']:
                raise RuntimeError('target must be Linux x86_64 with root namespace privileges')
        elif os.uname().machine != 'x86_64':
            raise RuntimeError('this regression bundle currently targets Linux amd64')
        for name in FRONTEND:
            if not (ROOT / 'web' / 'tests' / name).is_file():
                raise RuntimeError('missing frontend test: ' + name)
        run('frontend-tests', ['node', 'web/node_modules/tsx/dist/cli.mjs', '--test'] + ['web/tests/' + name for name in FRONTEND])
        run('frontend-typecheck', ['npm', 'run', 'typecheck', '--prefix', 'web'])
        # Test executables are short-lived; the logs and summary remain available.
        with tempfile.TemporaryDirectory(prefix='khive-regression-', dir=logdir) as build:
            builddir = pathlib.Path(build)
            run('build-runner', ['go', 'build', '-mod=readonly', '-o', str(builddir / 'runner'), 'scripts/linux-unit-runner.go'], env)
            for name in CHECKS:
                run('build-' + name, ['go', 'test', '-mod=readonly', '-tags', TAGS, '-c', '-o', str(builddir / (name + '.test')), './internal/' + name], env)
            if args.ssh:
                remote_dir = subprocess.check_output(remote('umask 022; mktemp -d /tmp/khive-regression.XXXXXXXX'), text=True).strip()
                if not re.fullmatch(r'/tmp/khive-regression\.[A-Za-z0-9]+', remote_dir):
                    remote_dir = None
                    raise RuntimeError('unexpected remote temporary directory')
                run('copy-tests', ['scp', '-O'] + [str(builddir / name) for name in files] + [args.ssh + ':' + remote_dir + '/'])
                run('prepare-tests', remote('chmod 755 ' + shlex.quote(remote_dir) + ' ' + ' '.join(shlex.quote(remote_dir + '/' + name) for name in files)))
                execution_dir = remote_dir
            else:
                # The runner drops privileges; a parent chosen by the caller
                # might be private. A separate /tmp directory guarantees access.
                remote_dir = tempfile.mkdtemp(prefix='khive-regression.', dir='/tmp')
                os.chmod(remote_dir, 0o755)
                import shutil
                for name in files:
                    shutil.copy2(builddir / name, pathlib.Path(remote_dir) / name)
                    os.chmod(pathlib.Path(remote_dir) / name, 0o755)
                execution_dir = remote_dir
            for name, pattern in CHECKS.items():
                command = [execution_dir + '/runner', execution_dir + '/' + name + '.test', '-test.run=' + pattern, '-test.v', '-test.timeout=120s']
                run(name + '-isolated', remote(shlex.join(command)) if args.ssh else command)
            command = [execution_dir + '/ecm.test', '-test.run=^TestECMIsolatedRouteOwnership$', '-test.v', '-test.timeout=30s']
            run('ecm-kernel-isolated', remote(shlex.join(command)) if args.ssh else command)
    finally:
        cleanup_ok = True
        if remote_dir:
            if args.ssh:
                command = 'rm -f ' + ' '.join(shlex.quote(remote_dir + '/' + name) for name in files) + ' && rmdir ' + shlex.quote(remote_dir)
                cleanup_ok = subprocess.run(remote(command)).returncode == 0
            else:
                for name in files:
                    (pathlib.Path(remote_dir) / name).unlink(missing_ok=True)
                pathlib.Path(remote_dir).rmdir()
        (logdir / 'summary.json').write_text(json.dumps({'checks': results, 'temporary_files_removed': cleanup_ok}, indent=2))
        print('Logs:', logdir, flush=True)
        if not cleanup_ok:
            raise RuntimeError('temporary cleanup failed at ' + remote_dir)
    print('All focused stability checks passed.', flush=True)

if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
