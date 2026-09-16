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
