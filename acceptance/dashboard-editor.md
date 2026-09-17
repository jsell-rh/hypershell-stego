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

## Visible editor and keyboard interaction

[Live run 35161493736](https://github.com/jsell-rh/hypershell-stego/actions/runs/35161493736)
reached visible policy text in Monaco. The saved screenshot shows the editor,
and the visible-content check passed. The next click on the input textarea
failed with `element click intercepted`. Monaco's captured stylesheet places
that textarea behind the visible editor with `z-index: -10`. The test now clicks
the visible text area and waits for keyboard focus before it sends input.

The failure record reached its 128-entry network limit. That left no room for
content-policy diagnostics. Network events now use at most 126 entries; two
entries remain for authorization and policy results. A null policy result means
capture was unavailable. An empty array means capture ran and had no recorded
directives. Neither condition proves behavior outside the captured document.

The dashboard test installs its bounded directive listener before page scripts
run. It uses the existing ChromeDriver session and records at most 16 distinct
allowed directive names. It records no blocked URLs or browser message text.
The [ChromeDriver command mapping](https://www.selenium.dev/selenium/docs/api/py/_modules/selenium/webdriver/chromium/remote_connection.html)
and [protocol definition](https://github.com/ChromeDevTools/devtools-protocol/blob/master/json/browser_protocol.json)
define the command used for this early listener. The test does not change the
application's content policy or use JavaScript to force editor focus.

The failed run also passed verified HTTPS, login, workspace creation, API Pod
replacement, and separate database access checks. It stopped before keyboard
input, invalid JSON rejection, later recovery and revocation, full telemetry
correlation, and deletion. Independent cleanup passed at
`2026-09-16T23:32:38.175068+00:00`. A new live run must check the revised
interaction and retain the early policy result.

## Keyboard input and blocked styles

[Live run 35162964198](https://github.com/jsell-rh/hypershell-stego/actions/runs/35162964198)
confirmed native keyboard focus. It failed after 279.95 seconds because the
invalid-JSON message did not appear. The screenshot and page text show `{}`:
the editor added a closing brace to the test's opening brace. This is valid
JSON. The test now enters `{invalid` and checks that the text is visible before
it checks the error message and disabled submission.

The early policy listener also recorded `style-src-elem` and `style-src-attr`
violations. The screenshot shows incomplete editor styling. The revised input
does not fix those violations. The common browser runtime and the application's
dependency integration need a checked solution for dynamic styles before the
application gate can pass. No content-policy rule changed in this test fix.

Later recovery, viewer revocation, complete telemetry correlation, and deletion
checks did not run. All 404 repeated-generation hashes matched. CI retained the
failure artifacts, and an independent operator check confirmed complete test
cleanup and an empty lease. No deployed workflow pass is claimed.

## Common dynamic-style candidate

The development branch selects STEGO candidate
`979292750de036413b0aebee1227f20d4c022bf4`. Compiler qualification is still in
progress. All three targets passed repeat generation and drift checks. The
input-manifest check passed. This is candidate generation, not a live result.

The Gateway console declares `dynamic_styles: true`. STEGO generates the
document nonce, DOM render helpers, and source-verified Monaco adapter. The
dashboard webpack configuration selects that generated adapter and maps its
common module. The source workflow copies and checks the five generated files
before it records the build tree. No common adapter code is handwritten here.

STEGO's [browser and adapter CI](https://github.com/jsell-rh/stego/actions/runs/35164702752)
passed six browser groups and 11 exact dependency files. The updated Hypershell
source, captured assets, image, and Gateway console module still require their
checks. The currently selected bundle and application image are from the earlier
qualified build. They have not yet been replaced by the new source build.

No live run may qualify this change until compiler checks, rebuilt assets,
image and module checks, and final repeat generation pass. The editor gate must
then prove layout, keyboard input, selection, workers, and content-policy
compliance before later access, recovery, telemetry, and deletion checks.
