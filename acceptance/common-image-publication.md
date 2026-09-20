# Common image publication

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

Dispatch `jshell-browser.yml` after the fixture image run passes. Set
`fixture_revision` to its full commit ID, `image_run` and `image_attempt` to
the selected run, and `image_policy` to the independent format 2 policy JSON.
Set `registry_policy` to the operator destination policy JSON when needed.
The workflow fetches the exact fixture commit and supplies the runner inputs.
It checks the run before and after artifact download. The common STEGO tools
then authenticate the records before the test takes the cluster Lease.

This live workflow requires explicit dispatch. A push to main does not select
a fixture or start a second cluster test. The source and image checks remain
separate prerequisites. Keep their records with the live test evidence.

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

The complete live Gateway gate passed in run `35512430605` at workflow source
`04dccb7`, with test fixture `bb1a494` based on application source `110b920`.
Independent checks confirmed all 11 required tests, 1,622 source files, 421
generated file hashes, and all seven source and registry receipt records.
The four reviewed browser images match the final archive. Test resources are
absent, the shared Lease is free, and all 32 standing installation resources
are unchanged. See the [source-specific result](common-image-publication-live-evidence.json).

The workflow covers REST and gRPC, access rules, event delivery, process and
database restart, namespace recovery, browser sessions, account operations,
and durable deletion. It also checks that Gateway deletion cannot finish
while its allocator is stopped and its state namespaces still exist.

One Gateway with 100 accounts had a cleanup upper bound of 32.48 seconds.
The 30-second target remains open. Sequential checks include verification time;
they do not establish exact provider transition times or production capacity.
Live Kata and Sandbox execution remain deferred. Production CA selection,
complete offline inputs, and the API-only legacy publisher remain open.
