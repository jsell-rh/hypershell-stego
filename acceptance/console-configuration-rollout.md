# Generated console configuration rollout

The Gateway console uses the common STEGO deployment renderer. Hypershell
selects the verified credential set and supplies its content digest through
`deployment.Options.ConfigurationDigest`. STEGO validates that value and sets
`stego.dev/config-sha256` on the Pod template. Hypershell requires a nonempty
digest and retains its placement, owner, allocated account, and Service
separation checks.

The existing common Secret digest excludes metadata. A credential change must
change the Pod template; a Secret version change alone must not change it.
Credential contents must not enter the Deployment or Service. The consumer test
checks these conditions and compares all other resource fields.

Existing Deployments can retain the old application annotation. The controller
does not update that annotation. The new common annotation controls later
configuration rollouts. Adding it can cause one rollout during adoption.

Pinned regeneration and module selection passed full inventory review. The
focused check passed 43 cases across 11 tests. The full application check passed
1,192 core cases across 368 tests, retaining all 1,176 prior cases and the same
five recorded exclusions. Its browser, UI, and image jobs passed. All seven
application images passed independent source, content, private registry, and
signature checks. See the [application evidence](console-rollout-application-evidence.json)
and [image evidence](console-rollout-image-evidence.json).

The complete live Gateway workflow remains required before main acceptance.
The frozen fixture is separate from the application source. It supplies only
the existing test inspection permissions. Live review will also check the common
digest on the console Deployment, ReplicaSet, and ready Pod.
