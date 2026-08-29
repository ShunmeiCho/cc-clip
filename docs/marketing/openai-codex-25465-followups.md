# openai/codex#25465 Follow-Up Notes

Use this only after the prepared comment in
`docs/marketing/openai-codex-25465-comment.md` is posted to:

`https://github.com/openai/codex/issues/25465`

Default posture: cc-clip is prior art from external tooling, not a Codex
proposal that must be adopted.

## First Reply Rule

- Reply only to direct questions or corrections.
- Keep replies technical and short.
- Do not post install commands unless someone asks how to try cc-clip.
- Do not compare cc-clip against `#24908` as better or more general.
- If maintainers prefer the thread stay focused on Codex internals, stop.

## If A Maintainer Says It Is Off-Topic

```text
Understood. Sorry for adding noise here. I shared it only as prior art for the
external-reader boundary, but I am happy to stop discussing cc-clip in this
thread so the issue can stay focused on Codex.
```

If they ask for removal:

```text
Understood. I will remove the comment. Thanks for keeping the issue focused.
```

## If Someone Asks For Implementation Details

```text
The short version is:

- the remote Codex process still talks to a normal X11 clipboard
- a small bridge owns the X11 CLIPBOARD selection
- when Codex asks for image data, the bridge fetches bytes from the local host
  through SSH RemoteForward
- the local side reads the host clipboard and serves only over loopback

The main lesson from cc-clip is that the clipboard reader boundary needs to be
explicit in headless/container/SSH setups. Without that escape hatch, libraries
like arboard can only see the guest environment.
```

## If Someone Asks How It Differs From `wl-paste`

```text
I see `wl-paste` as a good built-in fallback for Wayland environments where the
clipboard is already reachable from the Linux session.

cc-clip is aimed at a different boundary: the clipboard source is outside the
remote/headless environment, so the bridge has to cross SSH or host/container
isolation before Codex ever sees image bytes.
```

## If Someone Asks Whether Codex Should Use cc-clip

```text
I would not suggest Codex depend on cc-clip directly. The useful part is the
shape of the boundary: an operator-configured external reader can make the host
clipboard explicit without Codex needing to know every host/container transport.
```

## If Someone Asks About Security

```text
For cc-clip, the local daemon is loopback-only and uses a bearer token shared
with the remote side. The SSH RemoteForward exposes that local endpoint only
through the SSH session. That is still a trust boundary, so I would treat it as
opt-in tooling for a trusted remote host, not a default behavior.
```

## If Someone Asks How To Try cc-clip

Only answer this if they explicitly ask.

```text
The beta install is here:

https://github.com/ShunmeiCho/cc-clip/releases/tag/v0.9.0-beta.1

For Codex CLI, the intended setup is:

`cc-clip setup myserver --codex`

It is still beta. macOS -> remote Linux is the primary tested path; other host
combinations may need more feedback before the docs are precise.
```

## If Someone Reports A Failure

```text
Thanks for trying it. To avoid turning this Codex issue into cc-clip support, I
will take debugging back to the cc-clip repo. The most useful details are local
OS, remote OS, the exact setup command, and `cc-clip doctor --host <host>`
output with private paths or hostnames redacted.
```

## If The Issue Author Mentions `#24908` Again

```text
That distinction makes sense to me: built-in known fallbacks for known Linux
clipboard tools, and an explicit external reader for host/container or SSH
boundaries. cc-clip is mostly evidence for the second case, not an argument
against the first.
```

## Stop Conditions

Stop replying in the Codex issue if:

- a maintainer says the cc-clip discussion is off-topic
- two replies in a row are about cc-clip support rather than Codex design
- the thread shifts to `#24908` implementation details
- the discussion becomes a comparison between projects
- a real cc-clip bug report appears; move it to the cc-clip repo
