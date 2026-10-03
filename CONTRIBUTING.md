# Contributing to TDrive

Keep changes focused, explain the user-visible behavior and preserve support for
the platforms affected by your work.

## Getting started

- Read [README.md](README.md) for product behavior and
  [build/README.md](build/README.md) for dependencies and platform build commands.
- Read [AGENTS.md](AGENTS.md) for the code map, architecture constraints and checks.
- Use the versions specified by `go.mod`, the frontend lockfile and build tooling.
- Discuss substantial behavior or architecture changes before implementing them.
  Check related issues and existing work to avoid overlapping changes.

## Preparing a change

- Keep unrelated refactoring and dependency updates out of the change.
- Preserve existing edits when sharing a checkout. Review the complete diff,
  including generated files, before committing or pushing.
- Extend existing tests where practical and run checks relevant to the change.
  Use [.github/workflows/ci.yml](.github/workflows/ci.yml) for the CI requirements.
- Never commit credentials, Telegram sessions, encryption keys, local databases
  or private user files.
- Use small, cohesive commits with short conventional messages, such as
  `fix: preserve upload progress after reconnect` or `docs: clarify mobile setup`.
  Do not add co-author trailers.

For agents: commit, push or open a PR only when the user has authorized that
action in the task. A request to open a PR includes the necessary commits and
push. A local review request does not. Never merge or publish a release without
explicit authorization.

## Opening a pull request

1. Target `master`, unless the work explicitly targets another branch.
2. Review the full branch diff and commit history so the description covers the
   complete change.
3. Use a short, clear title describing the change. Open agent-created PRs as
   drafts unless the user asks otherwise.
4. Use exactly the three sections below, in this order. Keep the body concise
   and explain what changed, its meaningful scope and why it matters.

```md
## Description

Explain what this PR changes and any material behavior or scope limitations.

## Related Issue

Related to #123.

## Motivation and Context

Explain the problem and why this change is needed.
```

- Use `Resolves #N` only when the PR fully resolves that issue. Use `Related to #N`
  for partial coverage or related work. Write `None.` if there is no related issue.
- Do not add testing sections, test commands, test results, CI status or checklists
  to the PR body unless explicitly requested. This does not remove the obligation
  to validate the change.
- Do not use em dashes in the title or body.
- When updating a user-edited PR body, preserve their wording where possible
  while applying this format. Avoid replacing their description unnecessarily.
- When using the GitHub CLI, put multiline body text in a file and pass it with
  `--body-file` to preserve formatting.

This file is the source of truth for contribution and PR guidance. Keep the
GitHub PR template aligned with these three headings when updating the workflow.
