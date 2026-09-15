"""Reject unsuitable CI credentials before a runner changes cluster resources.

JWT claims supply a lifetime check, not authentication. The Kubernetes API
must authenticate the token and enforce the existing restricted CI permissions.
"""

import base64
import argparse
import json
import subprocess
import time

SUBJECT = 'system:serviceaccount:stego-ci-access:hypershell-ci'
API_SECONDS = 25 * 60
BROWSER_SECONDS = 35 * 60
CNPG_SECONDS = 45 * 60


def require_credentials(config, minimum_seconds, now=None):
    if type(minimum_seconds) is not int or not 0 <= minimum_seconds <= 3600:
        raise ValueError('Invalid CI credential time budget')
    now = time.time() if now is None else now
    try:
        if len(config['clusters']) != 1 or len(config['users']) != 1:
            raise ValueError()
        cluster = config['clusters'][0]['cluster']
        user = config['users'][0]['user']
        if cluster.get('insecure-skip-tls-verify') or not cluster['server'].startswith('https://') or set(user) != {'token'}:
            raise ValueError()
        token = user['token']
        if not isinstance(token, str) or not 1 <= len(token) <= 16384:
            raise ValueError()
        parts = token.split('.')
        if len(parts) != 3 or not all(parts):
            raise ValueError()
        encoded = parts[1]
        raw = base64.b64decode(encoded + '=' * (-len(encoded) % 4), altchars=b'-_', validate=True)
        payload = json.loads(raw)
        issued, expiry = payload['iat'], payload['exp']
        if type(issued) is not int or type(expiry) is not int or payload['sub'] != SUBJECT:
            raise ValueError()
        if not 0 < expiry - issued <= 3660 or issued > now + 30 or not now < expiry <= now + 3660:
            raise ValueError()
        if 'nbf' in payload and (type(payload['nbf']) is not int or payload['nbf'] > now + 30):
            raise ValueError()
    except (KeyError, IndexError, TypeError, ValueError, AttributeError):
        raise RuntimeError('Require a valid one-hour CI token and verified HTTPS context') from None
    if expiry - now < minimum_seconds:
        raise RuntimeError('CI credential has too little time left for this test; renew it before starting')
    return token, cluster


def require_context_credentials(context, minimum_seconds, kubeconfig=None):
    command = ['oc', '--context=' + context]
    if kubeconfig is not None:
        command.append('--kubeconfig=' + str(kubeconfig))
    command += ['config', 'view', '--raw', '--minify', '-o', 'json']
    try:
        result = subprocess.run(command, capture_output=True, check=True, timeout=30)
        config = json.loads(result.stdout)
    except (OSError, subprocess.SubprocessError, ValueError):
        raise RuntimeError('CI credential inspection failed') from None
    return require_credentials(config, minimum_seconds)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--context', required=True)
    budgets = {'api': API_SECONDS, 'browser': BROWSER_SECONDS, 'cnpg': CNPG_SECONDS}
    parser.add_argument('--gate', choices=list(budgets), required=True)
    args = parser.parse_args(argv)
    require_context_credentials(args.context, budgets[args.gate])
    print('CI credential lifetime is sufficient for the test and collection margin')


if __name__ == '__main__':
    main()
