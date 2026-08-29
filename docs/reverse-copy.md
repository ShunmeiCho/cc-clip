# Copying from the remote to your local clipboard

cc-clip's main direction is local → remote: your Mac or Windows clipboard
reaches a remote agent. This is the other direction — text you copy inside a
remote session landing on the clipboard of the machine in front of you.

## What already works, with nothing to configure

Any remote tool that copies by invoking `xclip` or `wl-copy` is intercepted by
the cc-clip shim, which forwards the bytes to your local clipboard *and*
replays them into the real binary. The copy lands in both places, byte for
byte. If the remote is headless and has no working `xclip` at all, the local
side still gets it.

That covers most of what a remote session actually does:

- neovim / vim yanks, with `clipboard` configured (below)
- tmux copy-mode
- any TUI whose copy action shells out to `xclip`/`wl-copy`
- explicitly: `some-command | cc-clip copy`

## What cannot work, and why

**Selecting text with the mouse in your terminal does not reach cc-clip.**

That selection is handled entirely by your local terminal emulator. It copies
the characters the terminal *rendered* — after wrapping, after truncation, with
whatever the pane's width did to them. No process on the SSH side observes it,
so there is nothing for cc-clip to intercept. This is not a gap that a future
version closes; it is where the boundary is.

The fix is a workflow one: copy using something that runs *on the remote*, and
it forwards transparently. The two configurations below make that the natural
thing to do.

## tmux

Add to `~/.tmux.conf` **on the remote host**:

```tmux
# Copy-mode selections go through xclip, which the cc-clip shim intercepts.
# Without this, tmux keeps the text in its own paste buffer and nothing leaves
# the remote machine.
set -g set-clipboard off
bind-key -T copy-mode-vi y \
  send-keys -X copy-pipe-and-cancel "xclip -selection clipboard -in"
bind-key -T copy-mode    Enter \
  send-keys -X copy-pipe-and-cancel "xclip -selection clipboard -in"

# Mouse selection inside tmux is a tmux selection, not a terminal one, so it
# reaches xclip too.
set -g mouse on
bind-key -T copy-mode-vi MouseDragEnd1Pane \
  send-keys -X copy-pipe-and-cancel "xclip -selection clipboard -in"
```

On a Wayland remote, substitute `wl-copy` for `xclip -selection clipboard -in`.

`set-clipboard off` matters. With it on, tmux tries to hand the copy to the
outer terminal over OSC 52 instead of running your `copy-command`, and the shim
never sees it.

Then: enter copy-mode (`prefix` `[`), select, press `y`. The text is on your
local clipboard.

> **Note.** With `mouse on`, a plain drag is now a *tmux* selection rather than
> your terminal's, which is what routes it through `xclip`. Hold `Shift` while
> dragging to get your terminal's own selection back when you want it.

## neovim / vim

In `init.lua`:

```lua
-- Every yank goes to the system clipboard, which on this host means xclip,
-- which the cc-clip shim intercepts.
vim.opt.clipboard = "unnamedplus"
```

or in `.vimrc`:

```vim
set clipboard=unnamedplus
```

Then an ordinary `yy` or `y}` lands on your local clipboard. No plugin, no
OSC 52 provider — the point is precisely that it goes through an external
binary.

Check which binary neovim picked:

```
:checkhealth provider.clipboard
```

It should name `xclip` or `wl-copy`. If it reports "No clipboard tool found",
install `xclip` on the remote: the shim is a wrapper that needs the real binary
present for its fallback path, and neovim needs *something* on `PATH` to
detect.

## Verifying it

On the remote:

```bash
echo "cc-clip reverse copy works" | xclip -selection clipboard
```

Then paste locally. If nothing arrives:

```bash
CC_CLIP_DEBUG=1 sh -c 'echo test | xclip -selection clipboard'
```

`cc-clip-shim: intercepting clipboard write (dual-write)` means the shim ran.
`cc-clip-shim: local forward failed or tunnel down` means the tunnel is not up
— keep an `ssh myserver` session open and re-check with
`cc-clip doctor --host myserver`.

If neither line appears, the shim is not on `PATH` ahead of the real binary.
`command -v xclip` in an *interactive* remote shell should print
`~/.local/bin/xclip`.

## Notes

- Text only. Images do not travel in this direction; open an issue if you have
  a case that needs them.
- An accepted write raises a local notification. Its text is fixed — "A remote
  session wrote to this clipboard." — and deliberately says nothing about the
  content or its size, so a burst of yanks collapses into one notification
  through the dedup window instead of one per yank. At least one always fires
  per window, so a remote poisoning your clipboard is still visible.
- The local daemon caps a single write at 1 MB (`CC_CLIP_MAX_TEXT_MB`).
- `-selection primary` is not forwarded. Only the clipboard selection is.
