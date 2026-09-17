# Verified compiler generation

The API, management console, and Gateway console use one selected STEGO
compiler package. The common STEGO installer owns release download, size and
time limits, signature policy, and private file capture. Hypershell owns the
selected revisions and the order in which its three modules are generated.

The repository has three independent records:

| File | Purpose |
| --- | --- |
| `.stego/compiler-revision` | Full source commit of the compiler and common registry |
| `.stego/compiler-sha256` | Reviewed SHA-256 of the signed compiler bytes |
| `.stego/tooling-revision` | Full source commit of the common installer |

The Gateway console compiler revision must match the root revision. The
management console uses the root revision. Installer updates do not require
changes to the compiler or common registry revision.

`scripts/generate.sh --check` fetches the exact installer source, verifies the
selected immutable release, checks the independent compiler digest, and checks
the compiler source identity. It then generates each module and checks for
output changes. It does not select the latest compiler or build a replacement
from source. GitHub authentication is available to installation only. The
script removes GitHub token variables before it executes the compiler.

Set `STEGO_COMPILER_PACKAGE` to a local package directory to avoid a release
download. The common installer verifies the signatures again. This option still
fetches the selected installer source. Signature trust roots, registry inputs,
Go modules, and build tools remain separate requirements. This is not a complete
offline build procedure. Normal release installation requires GitHub CLI
authentication and currently supports Linux amd64 only.

The cluster test host verifies the package before application execution. It
sends the fixed package files through the verified Kubernetes connection.
Browser and service checks compare the Pod compiler bytes and record the Job
and Pod UIDs before the start signal. The module generation script captures a
supplied compiler in a private directory and checks it against the independent
repository digest before execution. A supplied checksum or verification record
cannot replace that digest check. The compiler and generation commands have
time limits. No GitHub token enters the test Pod.

Generation records use persistent local state by default. CI retains the public
installation and signature records. Service evidence collection requires the
compiler transfer and signature records. Generated output stays committed for
review, reproducibility, and comparison with the tested source.

## Verification state

Five small compiler-selection checks pass. They cover changed bytes, mismatched
source identity, different module revisions, symbolic links, and removal of
GitHub token variables before all three modules execute. The service evidence
checks reject missing compiler records. Shell, Python, and workflow YAML syntax
checks pass. These checks use small stub files; they do not execute the real
compiler on the workstation. Real module generation and journal recovery have now passed in CI. Full core
and live cluster application checks for this integration remain pending.

The common installer separately passed real release and local-package signature
verification in STEGO run `35204243151` at source
`5fdc97cf7fb3a0f07fe5ea17a46e93281735168a`. The selected compiler is
`00573709fb15a2a54de4242aa8fdbabee325179a`, with SHA-256
`e5894237467e81c6a3e7a7c8abd436192a74174726384c2f30716e63db3101bb`.

The historical upstream dashboard capture jobs now select the same signed
compiler package and pinned registry. Their separate asset and fixture checks
passed in run `35207605684` at source `24d80ae`. Independent inspection matched
the committed assets, generated deployment, image binary, and signature records. They are outside the three application module entry points. Automatic compiler release
qualification and complete offline build inputs also remain open.


## First consumer evidence

At source `8bb2965f907aef1edb7c41e0f07b0ac542bc168c`,
[module run 35206033034](https://github.com/jsell-rh/hypershell-stego/actions/runs/35206033034)
passed. Independent inspection matched all 129 selected module source files,
repeated generation, the generated image binary, and the published compiler
signature records. The generated binary SHA-256 is unchanged from the preceding
qualified module. See the [module record](verified-generation-module-evidence.json).

[Journal run 35206033030](https://github.com/jsell-rh/hypershell-stego/actions/runs/35206033030)
passed all 28 required tests at the same source. Independent inspection found
no failed or skipped events in its saved result. See the
[journal record](verified-generation-journal-evidence.json).

Full run `35206051129` has passed its rendered-browser, management-console, and
image jobs. Core remains in progress. CNPG and Sandbox were not selected in that
hosted dispatch. The previous main CNPG run is a separate result and does not
qualify the new compiler transfer path. Live API, public Gateway, and CNPG
checks at this source remain required before main promotion.


The historical source and private application jobs now use compiler `0057370`
through the common installer. Their separate compiler pins and source builds
are removed. The asset bundle is byte-for-byte identical to the committed
Gateway console input. These checks do not change the application's selected
upstream image or replace live API, public Gateway, and CNPG evidence.
