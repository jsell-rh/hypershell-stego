# Common image publication candidate

The Kubernetes browser gate uses the shared STEGO image delivery commands.
Hypershell declares seven targets in `.ci/application-images.json` and maps
their returned digest references to the Gateway test settings. It does not
build executables or assemble image layers inside the browser test Pod.

Generation still uses `.stego/compiler-revision`. Image verification and
publication use `.stego/image-compiler-revision` and its separate digest pin.
Both compilers must have authenticated immutable releases. The common tools
come from `.stego/tooling-revision`.

Before the gate takes the cluster Lease, the operator must supply these inputs:

- `STEGO_TEST_IMAGE_ARTIFACTS`: the downloaded artifacts from the selected run.
- `STEGO_TEST_IMAGE_POLICY`: a trusted reusable signer policy for that run.
- `STEGO_TEST_IMAGE_RUN`: the workflow run ID.
- `STEGO_TEST_IMAGE_ATTEMPT`: the workflow attempt number.

If registry storage redirects blob reads to another origin, also supply
`STEGO_TEST_REGISTRY_POLICY`. This is the common STEGO format 1 destination
policy with explicit `token_origins` and `blob_origins` arrays. The default
arrays are empty. Obtain these origins from trusted operator configuration.
Do not approve an arbitrary destination from a redirect. The publisher combines
the supplied registry CA with public roots from the pinned SDK image. Registry
credentials cannot be sent to blob destinations.

The policy has STEGO format 2 and omits `module`, `target`, and `entrypoint`.
The declaration supplies those fields. The policy must select the caller
repository, branch, and commit; the reusable signer repository, workflow, and
commit; the build compiler commit and digest; the application commit; and the
image CA digest. Do not take policy from an arbitrary downloaded image.

The application commit must be the clean current checkout. For the browser
workload gate, this is the separate inspection fixture commit. Generate that
fixture with the checked compiler, commit its checked changes on a test branch,
and build its images with the common image workflow. Production allocator
images do not contain the inspection roles and cannot replace fixture images.
Do not merge the fixture roles into production.

The host authenticates all records, captures the image files, and records their
digests. It transfers the full tracked source and a package with the images,
tools, and publishing compiler. It compares the complete package digest in the
same Job-owned Pod before extraction. The Pod regenerates the application,
removes the three completed generation locks, and uses STEGO to check every
source snapshot and image before publication. The publisher uses the explicit
registry CA and the Pod's selected service-account token. It checks the remote
image before it returns a digest reference.

The saved evidence includes source checks, the complete publication record,
each registry receipt, compiler signatures, and the package transfer record.
Evidence collection excludes private publisher files, including files left by
an interrupted process. A partial publication is not a complete workflow pass.

This candidate still needs the complete live Gateway gate with the new image
path. Image and registry checks alone do not prove application behavior or a
production CA profile.
