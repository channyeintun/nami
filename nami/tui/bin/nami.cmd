@echo off
setlocal
set "SCRIPT_DIR=%~dp0"
set "LOCAL_NODE_PATH=%SCRIPT_DIR%..\runtime\node\node.exe"
set "LAUNCHER_PATH=%~dp0nami.js"

rem The flow uses goto rather than parenthesized blocks: cmd expands %VAR% when
rem it reads a whole block, so an "exit /b %ERRORLEVEL%" inside one reports the
rem status from before the runtime ran, and a ")" in an expanded path ends it.
if exist "%LAUNCHER_PATH%" goto local_node
>&2 echo nami is partially installed: missing launcher bundle at %LAUNCHER_PATH%
>&2 echo Reinstall nami or copy nami.js next to the nami wrapper.
exit /b 1

:local_node
if not exist "%LOCAL_NODE_PATH%" goto path_node
"%LOCAL_NODE_PATH%" "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:path_node
where node >nul 2>nul
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
deno run --allow-env --allow-read --allow-run "%LAUNCHER_PATH%" %*
exit /b %ERRORLEVEL%

:no_runtime
>&2 echo nami requires one of these runtimes on PATH: node, bun, deno.
exit /b 1
