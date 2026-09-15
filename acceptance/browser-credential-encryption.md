The browser workflow checks application credential encryption after workload
namespace recovery. It reads the actual envelope stored by the Gateway through
the generated PostgreSQL client and the Gateway's limited SQL login. The format
comes from the pinned OpenShell [credential store](https://github.com/opendatahub-io/openshell/blob/681c9b2d8b9887f230cee4871bdbdbc9a362dfc8/crates/openshell-driver-db-credstore/src/lib.rs).
The image at digest `a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd`
reports that source revision in its image metadata. This is a metadata check,
not a signed build-provenance claim.

The check uses Go's AES-GCM implementation to read bytes written by the actual
Rust Gateway. The retained Gateway key must unwrap the stored data key. That
data key must authenticate and decrypt the known provider credential. The
envelope's object ID, provider, credential name, version, algorithm, key ID,
nonce lengths, and ciphertext lengths must match the storage contract.

The other Gateway's key must not unwrap the data key. Changes to the wrapped
key, value ciphertext, or either authentication context must fail. These probes
use private copies. They do not change stored data and do not test the Gateway's
API response to corrupted storage. A bounded scan also checks all object
payloads for plaintext or base64 copies of the known credential.

The test adds no production permission or encryption implementation. STEGO
supplies the bounded SQL client, generated controllers, and Kubernetes runtime.
Hypershell supplies the Gateway storage contract and the application assertions.
Public evidence contains the algorithm, version, counts, and comparison results.
It does not contain keys, key hashes, credentials, nonces, or ciphertext.

This check does not prove disk, volume, backup, or RDS encryption. It does not
replace the complete workflow, source, generation, and cleanup checks.
The [complete browser run](https://github.com/jsell-rh/hypershell-stego/actions/runs/34941181554)
passed at `70b2dd7` in 384.8 seconds. The test inspected one encrypted envelope
among four object rows after namespace recovery. All 872 source files and 229
generated files match, and cleanup passed. See the
[verified record](browser-sql-recovery-encryption-evidence.json).
