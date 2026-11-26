-- Tracks in Rex. Load from ~/.config/rex/init.lua with:
--   dofile(os.getenv("HOME") .. "/Developer/tracks/rex/tracks.lua")
-- cmd+shift+h switches to the desk's session (the desk agent with the tracks pane beside it; started
-- on first use, and always first in the session list) and, pressed there, back to the session you came from. enter in the pane jumps to the track's agent.
-- cmd+shift+k goes straight to the track that needs you most (cmd+shift+] is Rex's Show Next Tab).
-- "Peek at Tracks" in the palette floats the pane over the window you are in instead.

-- TRACKS_BIN overrides the tracks binary and TRACKS_REX_DIR the directory that holds rex.
local home = os.getenv("HOME")
local bin = os.getenv("TRACKS_BIN") or (home .. "/.local/bin/tracks")
-- The server's own PATH is /usr/bin:/bin:...: rex is only in the app bundle.
local rex_dir = os.getenv("TRACKS_REX_DIR")
  or (home .. "/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS")
local path = home .. "/.local/bin:" .. rex_dir .. ":/usr/bin:/bin:/usr/sbin:/sbin"

-- A restarted server brings its sessions back as fresh shells. This file loads when the server
-- starts and again on every config reload; `--once-per-server` resumes the track agents only
-- the first time for each server. The command writes resume.log in the tracks state directory
-- itself and keeps its last 200 lines; whatever it prints on stderr that the log missed goes to resume.err.
-- Rex resumes Claude itself in every restored pane whose conversation its hook reported
-- (Program Resume Protocol, built-in `claude` rule). tracks waits 15 seconds for those to start:
-- it then finds Claude running wherever Rex resumed it, and only brings back the track agents
-- Rex left as shells (one Rex didn't trust, or one already tried once before a crash).
do
  local state = os.getenv("TRACKS_HOME") or (home .. "/.local/state/tracks")
  -- pcall: an error here must not stop the key bindings below, or init.lua after it, loading.
  local ok, h, err = pcall(rex.exec.start, {
    "/bin/sh", "-c", 'mkdir -p "$1" && sleep 15 && exec "$2" resume --all --once-per-server >/dev/null 2>>"$1/resume.err"',
    "sh", state, bin,
  }, { env = { PATH = path }, detach = true })
  if not ok then err = h end
  if not (ok and h) then rex.log("warn", "tracks resume did not start: " .. tostring(err)) end
end

-- run_tracks runs the tracks binary with ARGS for the client that pressed the key.
local function run_tracks(ctx, args, what)
  local env = { PATH = path }
  if ctx.client_id and ctx.client_id ~= "" then env.REX_CLIENT = ctx.client_id end
  local cmd = { bin }
  for _, a in ipairs(args) do cmd[#cmd + 1] = a end
  local r, err = rex.exec(cmd, { env = env, timeout = 25 })
  if not r then error(what .. " did not run: " .. tostring(err), 0) end
  local out = r.stdout:gsub("%s+$", "")
  if not r.ok then
    local why = r.stderr:gsub("%s+$", "")
    if r.timed_out then why = "timed out" end
    error(what .. " failed: " .. why, 0)
  end
  return out
end

-- Runs `tracks home`: it selects the desk session, or from there the session you came from.
rex.action{
  name = "tracks_home",
  title = "Tracks",
  description = "Switch to the desk session (desk agent + tracks pane); from there, back to where you were.",
  category = "Tracks",
  keywords = "tracks home agents focus",
  run = function(ctx)
    return run_tracks(ctx, { "home", "--from", ctx.session_id or "" }, "tracks home")
  end,
}

rex.action{
  name = "tracks_float",
  title = "Peek at Tracks",
  description = "Float the tracks pane over this window; enter jumps to a track's agent.",
  category = "Tracks",
  keywords = "tracks float peek",
  run = function(ctx)
    -- Running it again closes the float: find our floating layer in the active window.
    local view = rex.session.view{ session_id = ctx.session_id }
    for _, w in ipairs(view.windows or {}) do
      if w.window_id == view.active_window_id then
        for _, layer in ipairs(w.layers or {}) do
          local b = layer.blocks and layer.blocks[1]
          if layer.kind == "floating" and #layer.blocks == 1 and b.label == "tracks" then
            rex.call("block.close", { session_id = ctx.session_id, block_id = b.block_id })
            return "tracks closed"
          end
        end
      end
    end
    rex.call("session.new_layer", {
      session_id = ctx.session_id,
      bounds = { 0.05, 0.04, 0.9, 0.92 },
      focus = true,
      layout = { block = {
        flavor = "com.superlogical.terminal.shell",
        label = "tracks",
        options = {
          command = { "/bin/sh", "-c", 'exec "$HOME/.local/bin/tracks" --float' },
        },
      } },
    })
    return "tracks"
  end,
}

-- Runs `tracks next`: it selects the track that needs you most (blocked, error, asks, then done) and
-- focuses its agent, from whatever window you are in.
rex.action{
  name = "tracks_next",
  title = "Next Track Needing Me",
  description = "Go to the track that needs you most: blocked first, then errors, then asks, then finished.",
  category = "Tracks",
  keywords = "tracks next agents blocked asks round",
  run = function(ctx)
    return run_tracks(ctx, { "next" }, "tracks next")
  end,
}

rex.bind("cmd+shift+h", "tracks_home")
rex.bind("cmd+shift+k", "tracks_next")
