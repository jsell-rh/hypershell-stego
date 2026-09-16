# Rendered dashboard editor check

The public Gateway workflow opens the upstream global policy editor through
the generated browser backend. It uses the existing owner browser session.
It checks visible editor content, keyboard input, invalid JSON rejection,
disabled submission, and browser content-policy violations during editing.
It saves an editor screenshot and a result file, then closes the draft and
returns to the workspace. It does not submit a policy change.

This check uses the existing bounded cluster browser. It does not add a browser
process or change the production content policy. Syntax validation passed
locally. The rendered result still requires a cluster run with this source.

This check does not prove policy persistence or a live Sandbox terminal.
The user deferred the live Kata test because no suitable cluster is available.

## First rendered result

The [live run at 55d66c2](https://github.com/jsell-rh/hypershell-stego/actions/runs/35159150673)
passed the generated backend's login flow. The browser reached Workspaces,
created `rendered-dashboard` with HTTP 201, and opened its detail page. It then
opened the global policy dialog, but the editor area remained blank. The test
failed after 275.33 seconds because the editor input did not appear. It did
not reach the later recovery, viewer-access, telemetry-correlation, or deletion
assertions. Requests to the log and trace export paths returned HTTP 200; that
does not prove the complete three-signal check.

The screenshot, page text, and network record were copied from the active test
Pod before cleanup. Both Gateways, dashboards, and generated backends were ready
with no container restarts. The network record shows HTTP 200 from the callback,
protected document, session check, and dashboard assets. Independent cleanup
passed at `2026-09-16T22:59:48.769861Z`, with an empty test lease.

The locked PatternFly code editor uses `@monaco-editor/react`. Its loader uses
a CDN by default. The dashboard had the webpack plugin but did not register
the local Monaco instance. The locked package source confirms that missing
setup step. See the [PatternFly local editor instructions](https://github.com/patternfly/patternfly-react/tree/main/packages/react-code-editor#to-use-monaco-editor-as-an-npm-package-and-avoid-using-cdn).
The previous browser test did not retain content-policy violations on failure,
so the precise blocked browser operation was not recorded.

The candidate registers the existing locked Monaco package before the upstream
entry point runs. The source workflow captures this build input. The browser
check now retains a bounded list of content-policy directive names on failure.
It records no blocked URLs or arbitrary browser messages. This change belongs
to the upstream dashboard build inputs. STEGO's browser security policy is
unchanged. Source CI must check types, the build, dependencies, and captured
asset limits. A new live run must prove editor rendering and behavior.

## Source build correction

[Source CI 35160324245](https://github.com/jsell-rh/hypershell-stego/actions/runs/35160324245)
passed the backend, frontend type and build checks, 13 UI tests, and dependency
checks. Asset capture failed. The build produced 158 files and 9,210,535 bytes,
including a TrueType font. STEGO permits 128 files and did not accept that font
type. No assets or image from this failed run were adopted.

The editor now imports Monaco's editor API. The locked webpack plugin applies
to this entry and adds the selected JSON and YAML languages. The full-package
entry also imports other language contributions. Source CI must prove the
smaller build meets the existing asset limits.

The build checks pin candidate STEGO revision
`8e0fae6f28276e192e8497c8acad487c765f3bb5`, which adds common TrueType capture
and HTTP serving. Compiler CI must pass before runtime adoption. Both build
checks use this revision. Application compiler pins and deployed assets remain
at their previous checked versions until the new source and runtime pass.
