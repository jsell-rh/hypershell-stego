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

This change still requires pinned regeneration, module selection, application
checks, and the complete Gateway workflow. It is not yet accepted for main.
