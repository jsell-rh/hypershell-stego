# Dashboard login test

The generated Gateway browser backend requires an active session before it
serves a dashboard document or asset. An unauthenticated browser navigation to
`/workspaces` starts login and retains that return path. The dashboard cannot
show its own sign-in button before this check.

The rendered test previously waited for that protected button. It now waits
for the configured identity provider origin and its username field before it
enters credentials. After login, the existing checks still require the
workspace list, workspace creation, the rendered policy editor, reload after
recovery, and confirmed provider logout.

This correction follows the generated `applicationSession` and
`applicationLogin` behavior. JavaScript syntax validation passed. It is not a
live browser result. Run `35145842952` uses the previous frozen source and
cannot qualify this correction. A subsequent complete live run is required.
