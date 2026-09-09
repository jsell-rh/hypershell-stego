#!/usr/bin/env bash
set -euo pipefail
# Prepare files for one disposable kind cluster. This does not install Kata on the host.
if [[ $# != 1 || ! -d $1 || $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'Pass a private scratch directory on Linux amd64.' >&2
  exit 2
fi
scratch=$1
python3 - <<'PY'
import fcntl, os
fd = os.open('/dev/kvm', os.O_RDWR | os.O_CLOEXEC)
try:
    if fcntl.ioctl(fd, 0xAE00, 0) != 12:
        raise SystemExit('KVM API version is not supported')
    vm = fcntl.ioctl(fd, 0xAE01, 0)
    os.close(vm)
finally:
    os.close(fd)
PY
archive=$scratch/kata.tar.zst
if [[ -n ${STEGO_TEST_KATA_ARCHIVE:-} ]]; then
  cp -- "$STEGO_TEST_KATA_ARCHIVE" "$archive"
else
  curl -fsSL --connect-timeout 15 --max-time 600 \
    https://github.com/kata-containers/kata-containers/releases/download/4.1.0/kata-static-4.1.0-amd64.tar.zst -o "$archive"
fi
(cd "$scratch" && echo '3dc6b69c4acb787b967b04b64599a20d02a8beb1a8eaab3084110df9d0b08c96  kata.tar.zst' | sha256sum --check)
python3 - "$scratch" <<'PY'
import json, pathlib, subprocess, sys, tarfile
scratch = pathlib.Path(sys.argv[1]).resolve()
root = scratch / 'runtime'
root.mkdir(mode=0o700)
process = subprocess.Popen(['zstd', '-dc', str(scratch / 'kata.tar.zst')], stdout=subprocess.PIPE)
try:
    with tarfile.open(fileobj=process.stdout, mode='r|') as archive:
        for entry in archive:
            path = pathlib.PurePosixPath(entry.name)
            if not path.parts and entry.isdir():
                continue
            if not path.parts or path.is_absolute() or '..' in path.parts or path.parts[0] != 'opt':
                raise SystemExit('Kata archive has an invalid path')
            archive.extract(entry, root, filter='data')
finally:
    process.stdout.close()
    result = process.wait()
if result:
    raise SystemExit('Kata archive extraction failed')
spec = json.loads(subprocess.check_output([
    'docker', 'run', '--rm', '--network=none', '--cap-drop=ALL', '--entrypoint=ctr',
    'kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0',
    'oci', 'spec',
]))
limits = spec.setdefault('process', {}).setdefault('rlimits', [])
limits[:] = [limit for limit in limits if limit['type'] != 'RLIMIT_NPROC']
limits.append({'type': 'RLIMIT_NPROC', 'soft': 512, 'hard': 512})
(root / 'opt/kata/stego-base-spec.json').write_text(json.dumps(spec))
config = {
    'kind': 'Cluster', 'apiVersion': 'kind.x-k8s.io/v1alpha4',
    'featureGates': {'MutatingAdmissionPolicy': True},
    'runtimeConfig': {'admissionregistration.k8s.io/v1beta1': 'true'},
    'kubeadmConfigPatches': ['kind: KubeletConfiguration\napiVersion: kubelet.config.k8s.io/v1beta1\npodPidsLimit: 512\n'],
    'containerdConfigPatches': ['''[plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata]
  runtime_type = "io.containerd.kata.v2"
  runtime_path = "/opt/kata/runtime-rs/bin/containerd-shim-kata-v2"
  privileged_without_host_devices = true
  base_runtime_spec = "/opt/kata/stego-base-spec.json"
  [plugins."io.containerd.cri.v1.runtime".containerd.runtimes.kata.options]
    ConfigPath = "/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-qemu-runtime-rs.toml"
'''],
    'nodes': [{'role': 'control-plane', 'extraMounts': [{
        'hostPath': str(root / 'opt/kata'), 'containerPath': '/opt/kata', 'readOnly': True,
    }]}],
}
(scratch / 'cluster.json').write_text(json.dumps(config))
PY
