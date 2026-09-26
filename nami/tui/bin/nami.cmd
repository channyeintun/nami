@echo off
setlocal
set "SCRIPT_DIR=%~dp0"
set "LOCAL_NODE_PATH=%SCRIPT_DIR%..\runtime\node\node.exe"
set "LAUNCHER_PATH=%~dp0nami.js"
rem Silvery, the TUI renderer, needs Node.js 24 or newer. Older releases cannot
rem even parse the launcher bundle, so an older node must not win over bun or deno.
set "NODE_VERSION_CHECK=process.exit(Number(process.versions.node.split('.')[0]) >= 24 ? 0 : 1)"

rem The flow uses goto rather than parenthesized blocks: cmd expands %VAR% when
rem it reads a whole block, so an "exit /b %ERRORLEVEL%" inside one reports the
rem status from before the runtime ran, and a ")" in an expanded path ends it.
if exist "%LAUNCHER_PATH%" goto local_node
>&2 echo nami is partially installed: missing launcher bundle at %LAUNCHER_PATH%
>&2 echo Reinstall nami or copy nami.js next to the nami wrapper.
exit /b 1

:local_node
if not exist "%LOCAL_NODE_PATH%" goto path_node
"%LOCAL_NODE_PATH%" -e "%NODE_VERSION_CHECK%" >nul 2>nul
if errorlevel 1 goto path_node
"%LOCAL_NODE_PATH%" "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:path_node
where node >nul 2>nul
if errorlevel 1 goto bun
rem "call" returns here even when node is a .cmd shim from a version manager.
call node -e "%NODE_VERSION_CHECK%" >nul 2>nul
if errorlevel 1 goto bun
node "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:bun
where bun >nul 2>nul
if errorlevel 1 goto deno
bun "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:deno
where deno >nul 2>nul
if errorlevel 1 goto no_runtime
rem --allow-run already hands the engine, and so every shell command it runs,
rem the user's full access; narrower flags only break the TUI, which also
rem needs sys, ffi and write access.
deno run -A "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:no_runtime
>&2 echo nami requires one of these runtimes on PATH: node 24 or newer, bun, deno.
if exist "%LOCAL_NODE_PATH%" echo The Node.js runtime installed with nami is too old. Rerun the installer to update it. 1>&2
where node >nul 2>nul
if errorlevel 1 exit /b 1
>&2 echo The node on PATH is too old:
call node --version 1>&2
exit /b 1
