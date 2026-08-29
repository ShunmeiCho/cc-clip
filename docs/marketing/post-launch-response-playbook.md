# cc-clip Post-Launch Response Playbook

Use this after any public post, GitHub issue reply, Show HN thread, Reddit post,
or X announcement. The goal is to be useful, calm, and easy to trust.

## Response Principles

- Thank people for specific feedback, not for praise.
- Answer the narrow question first.
- Separate confirmed facts from guesses.
- Admit beta limits early.
- Do not argue people into trying cc-clip.
- Do not criticize upstream projects or competing tools.
- Turn repeated questions into docs or issues.
- Stop posting when you cannot keep up with replies.

Default posture:

```text
Thanks for the details. cc-clip is still beta, so environment reports like this
are exactly what help make the support boundary clearer.
```

## Triage Categories

| Incoming reply | Category | Action |
|---|---|---|
| "Works on my setup" | Confirmed environment | Ask for OS / target CLI details if missing; record as a proof point |
| "Doesn't work" with details | Bug report | Ask for `cc-clip doctor --host <host>` and environment details |
| "Doesn't work" without details | Incomplete report | Ask one short follow-up, not a long checklist |
| "This is too much setup" | Fit objection | Agree; point to simpler `scp` if image paste is rare |
| "Why not X11 forwarding?" | Alternative | Acknowledge it as valid; explain cc-clip's narrower scope |
| "This is spam/off-topic" | Relationship risk | Apologize once; offer to remove; stop replying unless asked |
| "You overclaimed support" | Wording correction | Thank them; correct the exact claim publicly |
| "Another tool already does this" | Alternative tool | Acknowledge it; distinguish scope only if useful |
| "Can you support my platform?" | Feature request | Ask for workflow details; do not promise a timeline |
| Maintainer responds | Upstream maintainer | Defer to their preference; do not debate moderation |

## Minimal Bug Follow-Up

Use this when someone reports a failure but does not include enough detail:

```text
Thanks for trying it. Could you share the smallest environment details needed to
debug this?

- local OS
- remote OS
- target CLI (`claude`, `codex`, `opencode`, or `agy`)
- setup command you ran
- `cc-clip doctor --host <host>` output, with hostnames or paths redacted if needed

No rush; this is beta feedback, so even a rough report is useful.
```

If the user is frustrated, use a shorter version:

```text
Sorry it failed on your setup. If you are willing to share just local OS, remote
OS, target CLI, and the setup command, I can at least tell whether this is inside
the current beta support boundary.
```

## When It Works

Use this when someone confirms a working environment:

```text
Thanks, that is useful. Would you mind sharing the local OS, remote OS, and
target CLI? I am collecting confirmed beta environments so the docs can be more
precise instead of implying broader support than we have tested.
```

If they already included details:

```text
Thanks, this is a helpful data point. I will fold this into the beta feedback
notes so the docs can distinguish tested setups from expected-but-unconfirmed
ones.
```

## Fit Objections

When someone says the setup is too much:

```text
That is a fair reaction. cc-clip is meant for people who paste screenshots into
remote agent sessions often enough that manual upload becomes a repeated cost.
If image paste is occasional, `scp` or saving the file and passing a path is
probably the simpler choice.
```

When someone says X11 forwarding is enough:

```text
Agreed. If X11 forwarding already works well in your setup, I would keep using
it. cc-clip is aimed at a narrower path: image paste and notifications over SSH
without forwarding a full graphical session.
```

When someone prefers a competing tool:

```text
That makes sense. There are a few tools exploring this area now, and the best
choice depends heavily on terminal, SSH, and host OS details. I am mainly trying
to document where cc-clip fits and where it does not.
```

## Support Boundary Corrections

If someone catches an overclaim:

```text
You are right; that wording is too broad. The current support boundary is:
macOS -> remote Linux as the primary path, Windows as experimental, and
Antigravity as notify-only today. I will tighten the wording so it does not
imply more than that.
```

If someone asks whether Antigravity image paste works:

```text
Not yet. The current `--agy` target is notification setup only. I do not want to
claim Antigravity clipboard support until the transport is implemented and
tested.
```

If someone asks whether this is official:

```text
No, this is not an official integration with Anthropic, OpenAI, opencode, or
Google. It is an independent open-source workaround for remote SSH workflows.
```

## Relationship Risk Handling

If a maintainer says the comment is off-topic:

```text
Understood. Sorry for the noise; I will remove or stop discussing it here.
Thanks for keeping the issue focused.
```

If a community member calls it spam:

```text
Fair criticism. I shared it because the thread looked directly related to the
remote clipboard problem, but I may have misread the context. I will step back.
```

If a thread becomes hostile:

```text
I do not want to derail the thread. I will take the feedback and stop here.
```

After any of these, stop replying unless a maintainer asks a direct question.

## Turning Feedback Into Work

Create a follow-up issue or docs task when:

- two people hit the same setup failure
- a command in the launch material is confusing
- someone correctly points out a support-boundary overclaim
- a platform is repeatedly requested
- a workaround appears in a community thread and should be documented

Suggested internal note format:

```markdown
## Feedback

- Source:
- User environment:
- What happened:
- Current support boundary:
- Proposed docs/code action:
- Follow-up owner:
```

## Daily Wrap-Up Template

Use this at the end of a launch day:

```markdown
## cc-clip beta launch feedback - YYYY-MM-DD

### Useful signals

-

### Confirmed environments

-

### Failures or unclear setup reports

-

### Wording to tighten

-

### Docs/code follow-up

-

### Threads to stop replying to

-
```

## Do Not Say

- "This should work everywhere."
- "It is just one command" without mentioning prerequisites.
- "Antigravity support" without saying notify-only.
- "Windows support" without saying experimental.
- "Official integration."
- "Better than X" when comparing to other projects.
- "Just use cc-clip" in upstream issue trackers.

## Good Closing Lines

Use these when ending a thread politely:

```text
Thanks again for the details. I will fold this into the beta notes rather than
keep expanding the thread here.
```

```text
That is enough for me to investigate. I will take it back to the repo so this
thread can stay focused.
```

```text
Appreciate the pushback. I will adjust the wording before sharing this more
broadly.
```
