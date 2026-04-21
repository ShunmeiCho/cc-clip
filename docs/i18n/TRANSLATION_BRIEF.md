# Translation Brief

This brief sets the rules for translating cc-clip user documentation into Simplified Chinese and Japanese. It exists so every translator (human or agent) produces files that share the same voice, structure, and source-tracking discipline.

## What gets translated

| File | Translated? | Notes |
|---|---|---|
| `README.md` | ✅ → `README.zh-CN.md`, `README.ja.md` | Root entrypoint, highest priority. |
| `docs/troubleshooting.md` | ✅ → `docs/zh-CN/`, `docs/ja/` | User-facing debug guide. |
| `docs/windows-quickstart.md` | ✅ → `docs/zh-CN/`, `docs/ja/` | User-facing platform guide. |
| `docs/notifications.md` | ✅ → `docs/zh-CN/`, `docs/ja/` | User-facing feature guide. |
| `docs/commands.md` | ✅ → `docs/zh-CN/`, `docs/ja/` | User-facing reference. |
| `CLAUDE.md` | ❌ | Agent/contributor instructions; English is the canonical working language for AI tooling. |
| `AGENTS.md` | ❌ | Same as CLAUDE.md. |
| `CONTRIBUTING.md` | ❌ | Contributor-facing; keeps English. |
| `docs/marketing/*` | ❌ | Market copy should be culturally adapted, not translated. |
| `docs/plans/*` | ❌ | Internal planning artifacts. |
| Commit messages, PR titles, issue templates, code comments | ❌ | Collaboration surface stays English. |
| CLI `--help` text and error messages | ❌ | Unless a real runtime i18n system is added later. |

English is the **canonical source**. When in doubt, keep a term in English rather than invent a translation.

## Source hash markers (mandatory)

Every translated file MUST begin with a marker comment:

```markdown
<!-- i18n-source: README.md @ <english-commit-sha> -->
```

- `<english-commit-sha>` is the full SHA of the English source's most recent commit at the time of translation.
- Rule: when you update a translation, update the SHA at the same time. The CI (when added in the final step of the i18n sequence) will compare the marker SHA against the current canonical file's latest SHA and fail if the translation is behind the declared source.
- Do NOT invent an SHA. Use `git log -1 --format=%H -- <source-file>` to get the current value.
- If you are translating from a specific upstream commit, use that SHA even if the `main` branch has moved — the marker reflects "this translation is based on source state X," not "this translation exists at time Y."

## Language switcher (mandatory on every translated file)

Each translated file (and the English original) starts with a language bar like:

```markdown
<p align="center">
  <a href="README.md">English</a> ·
  <b>简体中文</b> ·
  <a href="README.ja.md">日本語</a>
</p>
```

- The current language is **bold** (not a link).
- Other languages are links.
- Order and wording (English · 简体中文 · 日本語) are fixed across all files to reduce cognitive load.
- For subdocs under `docs/`, link targets adjust relatively (e.g., `../README.md` → `../README.zh-CN.md`).

## Canonical disclaimer

Each translated file (including subdocs) must include, right after the language bar and logo:

- **Simplified Chinese:**
  > 本文是英文原文的简体中文翻译。若内容有差异，以 [English 原文](<source-link>) 为准。翻译版本可能晚于英文主线更新。

- **Japanese:**
  > これは英語版の日本語訳です。内容に差異がある場合は [English 原文](<source-link>) を正とします。この翻訳は英語版のメインラインより遅れている場合があります。

## Tone

### Simplified Chinese

- **Voice**: direct, technical, no marketing hype.
- **Pronouns**: avoid formal "您"; use plain 2nd-person or imperative. "Run this command" → "运行此命令" (imperative), not "您需要运行此命令".
- **Sentence structure**: mirror the English paragraph structure. Do not reorganize unless a literal translation reads as unnatural Mandarin.
- **Technical terms**: prefer English on first use if the glossary does not force a translation, then use the translation in follow-ups.

### Japanese

- **Register**: です・ます体 (desu/masu form). Not 常体 (plain form), not overly formal 謙譲語.
- **Imperative**: prefer `-ください` or `-してください` over raw imperative.
- **Technical terms**: same rule as Chinese — prefer katakana transliteration only when the glossary forces it. Otherwise keep English.
- **Particle economy**: the English original is terse; do not add "という" or "ということ" unless genuinely needed for clarity.

## Structure rules

- **Preserve markdown structure exactly.** Headings (`##`, `###`, `####`), tables, code fences, callouts (`>`), and list levels must match the English source. A reviewer should be able to diff the rendered English and rendered translation and see the same section count, in the same order.
- **Code blocks**: translate *only* shell / config comments (lines starting with `#` or `//` inside the code fence). Do not translate command names, flag names, paths, URLs, or output text. When in doubt, leave it English.
- **Anchor links**: translations may keep the same English slug (GitHub auto-generates anchors from the visible text, which differs per language, so cross-file anchor links need adjustment — use the original English-header anchor on the English file, and the translated anchor on the translated file).
- **Images**: the `src="..."` path is shared across all three language versions (e.g., all three READMEs use `docs/logo.png`). `alt="..."` text may be translated.
- **Output**: produce exactly one markdown file. No meta-commentary, no "here is the translation" preamble, no trailing signature.

## Review checklist (per translated file)

Before a translation PR is marked ready:

- [ ] The `i18n-source` marker at the top references the current English commit SHA
- [ ] The language switcher is present and order-consistent with other files
- [ ] The canonical disclaimer paragraph is present
- [ ] Every H2 / H3 in the English source has a corresponding heading in the translation
- [ ] Every code block in the English source has a corresponding code block in the translation (same fence language, same number of blocks)
- [ ] Every glossary term used appears in the form the glossary mandates
- [ ] No untranslated English paragraphs remain (other than intentionally preserved technical text)
- [ ] No added / removed sections that the English source does not have
- [ ] File ends with a single trailing newline

## Subdoc directory layout

For files outside the root (troubleshooting, windows quickstart, notifications, commands), place translations at:

```
docs/
├── troubleshooting.md         ← English canonical
├── windows-quickstart.md
├── notifications.md
├── commands.md
├── zh-CN/
│   ├── troubleshooting.md
│   ├── windows-quickstart.md
│   ├── notifications.md
│   └── commands.md
└── ja/
    ├── troubleshooting.md
    ├── windows-quickstart.md
    ├── notifications.md
    └── commands.md
```

The language switcher on translated subdocs links back to the English canonical and across to the peer-language subdoc:

```markdown
<p align="center">
  <a href="../troubleshooting.md">English</a> ·
  <b>简体中文</b> ·
  <a href="../ja/troubleshooting.md">日本語</a>
</p>
```

## When a translation falls out of sync

If the English source changes and the translation has not been updated:

1. Do not silently edit the SHA marker to the new English SHA — that hides a real drift.
2. Open a translation-update PR that actually propagates the English change into the translation, then update the marker.
3. The CI (final i18n step) will block merges on a marker SHA that does not match the canonical file's latest.

## Expressly non-goals

- **Not goal**: Translate once and freeze. Translations are expected to follow English every time it changes meaningfully.
- **Not goal**: Provide localized runtime text in the CLI. The CLI stays English unless a real i18n framework is added later.
- **Not goal**: Machine-translate English files into target languages without human or agent review. A translator (human or agent) must apply this brief and the glossary, not just run Google Translate.
