# GitHub Publication Standard for Coding Agents

**Audience:** autonomous coding agents and human operators who prepare a project for publication on GitHub.  
**Language of this document:** Russian (operational rules). Repository user-facing files must follow the bilingual rules below.  
**Status:** mandatory. Do not publish until the pre-push checklist in §14 is fully green. In a distributed multi-agent setup the same checklist is a **global push lock** (§14.2): no node may run `git push` until the dispatcher confirms a clean assembled tree.

This document defines how to sanitize, structure, license, document, and publish a software project so that the public GitHub repository contains only project artifacts, never personal data, local environment traces, or agent-session artifacts.

---

## 0. Operating principles

1. **Public history is permanent.** Anything that ever entered a commit, tag, pull request, Actions log, or release asset may be copied by forks, caches, search engines, and scrapers. Deleting a file in a later commit does **not** remove it from history.
2. **Default to deletion and recreation** when personal data, credentials, host names, or agent traces have already been pushed. History rewriting is a fallback, not the preferred cleanup for a first publication.
3. **Local-only artifacts stay local.** Agent workspaces, IDE state, secrets, machine-specific config, overlay-network addresses, terminal-session dumps, and queue-manager logs must never be staged.
4. **English is the primary public language.** Russian is a first-class companion language, not a substitute for English.
5. **Minimal, reproducible, portable.** A stranger must be able to clone the repository and run the documented happy path without knowing the author's machine, employer, or internal network.
6. **Do not invent facts.** Do not claim CI badges, test coverage, stars, production deployments, or affiliations that do not exist.
7. **License each class of work separately** when the tree mixes code with artistic or data assets (§5.4).
4. **English is the primary public language.** Russian is a first-class companion language, not a substitute for English.
5. **Minimal, reproducible, portable.** A stranger must be able to clone the repository and run the documented happy path without knowing the author's machine, employer, or internal network.
6. **Do not invent facts.** Do not claim CI badges, test coverage, stars, production deployments, or affiliations that do not exist.

---

## 1. Absolute prohibitions

Never add the following to the Git index, commit history, GitHub repository settings descriptions, Issues, Discussions, Wikis, Releases, GitHub Pages, or Actions logs.

### 1.1 Identity and personal data

- Real names, nicknames used only privately, home/work addresses, phone numbers, emails that are not an intentional public project contact
- Passport, tax, banking, medical, or government identifiers
- Photographs of people unless the project explicitly requires them and consent is documented
- Employer, client, university, or internal project names unless the publisher has explicitly authorized disclosure
- Personal calendar notes, chat exports, screenshots of messengers, private tickets

### 1.2 Credentials and secrets

- Passwords, API keys, tokens, cookies, session IDs
- Private keys (`*.pem`, `*.key`, `id_rsa`, `id_ed25519`, PKCS#12, Kubernetes secrets, cloud credential JSON)
- `.env`, `.env.*` with real values, `credentials.json`, `secrets.yaml`, `auth.json`
- Connection strings with user/password (`postgres://user:pass@host/db`)
- Webhook URLs that embed secrets
- Signing certificates and keystore passwords
- GitHub PATs, npm tokens, Docker Hub tokens, cloud access keys

Use placeholders only: `YOUR_API_KEY`, `changeme`, empty values in examples.

### 1.3 Infrastructure and local environment

- Real server hostnames, internal FQDNs, IP addresses (public or private), VPN endpoints
- Overlay and mesh-VPN artifacts: Tailscale, Headscale, ZeroTier, Netbird, WireGuard, Nebula, Innernet, Cloudflare WARP/Tunnel hostnames, MagicDNS names
- Addresses in CGNAT / carrier-grade and overlay ranges that identify a real node, especially `100.64.0.0/10` (Tailscale and CGNAT), plus vendor domains such as `.ts.net`, `.tailscale.net`, `.zerotier`, `.netbird.`, `.wg.`
- SSH configs, `known_hosts`, `authorized_keys`, WireGuard `*.conf` with real keys or endpoints
- Kubernetes cluster names, namespace names that identify an organization, kubeconfig files
- Database dump files with real rows
- Local absolute paths (`C:\Users\...`, `/home/ivan/...`, `/Users/alex/...`)
- Machine names, OS usernames, local printer or device names
- GPU node inventory, Slurm/hostfile lists, and queue-manager worker IDs that map to a private cluster
- IDE workspace files that embed local paths (`*.code-workspace` with absolute paths, `.idea/workspace.xml` if it contains local state)
- Docker compose overrides that point at private registries with credentials

Documentation may describe *that* an overlay VPN is supported. It must not include a live peer IP, MagicDNS name, auth key, or coordination-server URL of the publisher’s network.

### 1.4 Agent and “vibe coding” traces

These remain **only on the local machine**. They must be listed in `.gitignore` before the first commit.

Typical names (non-exhaustive; treat similar names the same way):

| Path / pattern | Reason |
|---|---|
| `.agent/` | Agent session, plans, memory, tool logs |
| `.agents/` | Variant of the above |
| `.claude/`, `.codex/`, `.cursor/`, `.aider/`, `.continue/` | Vendor agent state |
| `.windsurf/`, `.sweep/`, `.goose/` | Vendor agent state |
| `AGENTS.md` **only if** it contains private runbooks, credentials, or machine paths. A public, sanitized contributor-agent guide is allowed under a different name such as `docs/agent-notes.md` after review. | |
| `*.agent.md`, `PLAN.md`, `TODO.agent.md` with private scratch notes | Session debris |
| `.vscode/settings.json` with local interpreters, tokens, or absolute paths | Machine-specific |
| `tmp/`, `.scratch/`, `notes.local.md` | Local scratch |
| `tmux*` dumps, `*.tmux.log`, pane captures, `script(1)` typescripts | Full terminal sessions often contain prompts, paths, tokens, and overlay IPs |
| `asciinema` casts and raw terminal recordings of real sessions | Same risk as tmux dumps unless explicitly redacted for a public demo |
| Queue-manager / dispatcher logs (`queue/`, `jobs/`, `runs/`, `gpu-pool/`) | Distributed-agent coordination traces, worker hostnames, job payloads |
| `nohup.out`, `screenlog.*`, `*.cast` from live GPU nodes | Execution residue from remote workers |

**Rule:** if a directory exists solely so an AI coding agent can remember context, it does not belong on GitHub.

**Rule:** a complete dump of a terminal multiplexer session (tmux, GNU Screen, Zellij) or of a custom agent-queue runner is a local operational artifact. Publish only a redacted, hand-written example if a demo is required.

### 1.5 Legal and third-party material

- Proprietary code the publisher does not have the right to publish
- Copied documentation or media under copyright without a license that permits republication
- Customer datasets, production logs, analytics exports
- Unredacted error dumps that contain request payloads or PII

---

## 2. Mandatory local-only policy for `.agent`

1. Confirm that `.agent/` (and equivalents) exist only on the local disk.
2. Add the patterns from §7 to `.gitignore` **before** `git add -A`.
3. Never use `git add -f` on those paths.
4. Never copy agent transcripts, tool-call dumps, or “how I vibecoded this” narratives into `README`, commit messages, Issues, or PR bodies.
5. Public documentation must read as a normal engineering project: problem, design, usage, tests. It must not mention the agent product, prompt logs, or internal chat.

If an agent folder was already committed locally but **not** pushed:

```bash
git rm -r --cached .agent
# then commit the removal and the updated .gitignore
```

If it was already pushed, follow §13 (delete and recreate). Do not rely on a later “delete file” commit.

---

## 3. Pre-flight sanitization (do this before `git init` or before the first push)

Work on a **publication copy**, not on the messy working tree, when the project grew locally for a long time.

### 3.1 Create a clean export

Preferred method:

1. Copy the project into a new directory **excluding** ignored and secret paths.
2. Or initialize a new repository and copy only the files that should be public.

Do not copy:

- `.git/` from a dirty history if that history contains secrets or PII
- `node_modules/`, `venv/`, `.venv/`, `target/`, `dist/`, `build/`, `__pycache__/`
- `.agent/`, IDE junk, OS junk, local env files
- tmux/screen dumps, queue-manager logs, GPU-worker session captures

### 3.2 Scan before the first commit

Search the publication tree (content and filenames) for at least:

```text
password, secret, token, api_key, apikey, authorization, bearer
BEGIN PRIVATE KEY, BEGIN RSA PRIVATE KEY, BEGIN OPENSSH PRIVATE KEY
AKIA, ASIA, AIza, sk-live, sk_live, xoxb-, ghp_, github_pat_
postgres://, mysql://, mongodb+srv://
@.onmicrosoft., intranet, vpn, bastion
C:\Users\, /home/, /Users/
.agent, vibe, cursor chat, claude project

# Overlay / mesh VPN (mandatory)
.ts.net, .tailscale.net, tailscale, TS_AUTHKEY, tskey-
zerotier, .netbird., NB_SETUP_KEY, wireguard, wg0, PrivateKey =
headscale, magicdns, cloudflared, .trycloudflare.com
fd7a:115c:
```

Additionally run a structured address review:

- every IPv4 address in the tree; reject RFC 1918 (`10/8`, `172.16/12`, `192.168/16`), loopback except `127.0.0.1` in local examples, link-local `169.254/16`, and CGNAT `100.64.0.0/10` unless the value is an explicitly labeled placeholder
- every hostname that is not `localhost`, `example.com`, `example.org`, or `example.net`
- vendor overlay domains, including `*.ts.net` and `*.tailscale.net`

The octet pattern `100.x.y.z` is characteristic of Tailscale and of CGNAT. Treat any such address in configs, logs, screenshots, notebooks, compose files, and README examples as a leak unless the surrounding text states that it is a documentation placeholder and does not identify a real node.

Also inspect:

- `docker-compose.yml`, Helm values, Terraform `*.tfvars`, Ansible inventories
- screenshots and GIFs (they often contain desktop usernames, Slack, emails, VPN status icons, `100.x` addresses)
- notebooks (`.ipynb`) — they store output cells
- test fixtures that look like production dumps
- `package-lock.json` / `poetry.lock` only for accidental embedded tokens (rare, but check `.npmrc`)
- tmux logs, `*.cast`, `nohup.out`, queue/job JSON, GPU-pool hostfiles
- WireGuard and Tailscale config snippets inside scripts and comments

### 3.3 Replace configuration with examples

| Keep in repo | Keep local only |
|---|---|
| `.env.example` | `.env`, `.env.local`, `.env.production` |
| `config.example.yaml` | `config.local.yaml` |
| `compose.yaml` with placeholders | `compose.override.yml` with real hosts |
| `docs/deployment.md` with generic topology | Real inventory files |

Every example file must run after the reader copies it and fills placeholders. Never leave a real hostname “just for convenience”.

### 3.4 Normalize paths and names

- Use relative paths in scripts and docs.
- Use generic service names: `api`, `db`, `cache`, `worker`.
- Use RFC 5737 / documentation IPs only if an IP is required in an example (`192.0.2.10`, `203.0.113.5`).
- Use `example.com`, `example.org`, `localhost`.
- Do **not** use a live Tailscale address such as `100.x.y.z` or `machine.ts.net` “as a realistic example”. Use `100.64.0.1` only if the text explicitly labels it as a documentation placeholder for CGNAT/Tailscale, and never pair it with a real MagicDNS name.

---

## 4. Target repository layout

Adapt folders to the stack, but keep community files at the root or under `.github/` as specified.

```text
project-name/
├── .github/
│   ├── workflows/                 # CI only; no secrets in YAML
│   ├── ISSUE_TEMPLATE/
│   │   ├── bug.yml
│   │   └── feature.yml
│   ├── PULL_REQUEST_TEMPLATE.md
│   ├── dependabot.yml             # if dependencies exist
│   └── CODEOWNERS                 # only if public usernames are intended
├── docs/
│   ├── architecture.md            # optional, English; Russian twin allowed
│   └── screenshots/               # redacted images only
├── src/                           # or app/, lib/, packages/ — stack-typical
├── tests/                         # or spec/
├── scripts/                       # portable helper scripts
├── .env.example
├── .gitattributes
├── .gitignore
├── CHANGELOG.md                   # Keep a Changelog format when releases exist
├── CODE_OF_CONDUCT.md             # recommended for public collaboration
├── CONTRIBUTING.md
├── LICENSE                        # code license (or LICENSE plus LICENSE-ASSETS)
├── LICENSE-ASSETS                 # when media / art use a different license
├── NOTICE                         # third-party notices when needed
├── README.md                      # English (canonical GitHub landing page)
├── README.ru.md                   # Russian
├── SECURITY.md                    # public repos that accept reports
└── ...
```

Notes:

- Do **not** put LICENSE only inside `src/`.
- Do **not** commit multiple competing READMEs in `.github/`, root, and `docs/` unless you understand GitHub precedence (`.github` wins). Canonical landing README is **root `README.md`**.
- Binary assets: keep small. Large datasets and build artifacts belong in Releases or external storage, not in Git.
- When original art or animation is part of the project, keep it under `assets/` (or another documented tree) so §5.4 can name the licensed paths.

---

## 5. License (mandatory for public repositories)

A public repository without a license is “all rights reserved” by default. Many organizations cannot use it.

### 5.1 Choose a license

Use an SPDX-identified license and the official full text.

Common defaults:

| Goal | License | File name |
|---|---|---|
| Maximum reuse, simple terms | MIT | `LICENSE` |
| Patent grant, still permissive | Apache-2.0 | `LICENSE` |
| Copyleft for the project itself | GPL-3.0-only or GPL-3.0-or-later | `LICENSE` |
| Library copyleft | LGPL-3.0-or-later | `LICENSE` |
| Data / content only | CC-BY-4.0 or CC0-1.0 | `LICENSE` |
| Mixed tree: code + art / animation / design | Code SPDX + a separate assets license | `LICENSE` + `LICENSE-ASSETS` (see §5.4) |

Decision rules for the agent:

- If the publisher did not specify a license, **ask**. If the agent must pick a default for a typical personal/open utility project **that contains only code**, use **MIT**.
- If the tree already contains third-party code, preserve those licenses in `NOTICE` or `LICENSES/` (REUSE-style) and do not relicense other people’s code.
- If the tree contains original artistic works (2D animation pipelines, character design, sprites, storyboards, fonts, audio, video), apply §5.4. Do not place artwork under MIT by silence.
- Do not invent a custom “you may use this but … ” paragraph in the README as a substitute for license files.

Official chooser: https://choosealicense.com/

### 5.2 File conventions

- Filename: `LICENSE` (no extension preferred). `LICENSE.md` is acceptable but not required.
- Content: the complete license text, not a one-line mention.
- Copyright line: use a year and a **project or publisher identity that is intended to be public**.

Good:

```text
Copyright (c) 2026 Project Contributors
```

or a public organization / public handle the owner has chosen.

Bad:

```text
Copyright (c) 2026 Ivan Petrov, ivan@home-mail.ru, LLC “Secret Client”
```

### 5.3 README license section

Both language versions must name the license and point to `LICENSE`. Example:

```markdown
## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).
```

```markdown
## Лицензия

Проект распространяется по лицензии MIT. Текст лицензии: [LICENSE](LICENSE).
```

Do not paste the full license into the README.

### 5.4 Combined licensing (code versus artistic assets)

A single SPDX identifier on the repository does not automatically cover drawings, character sheets, animation cels, textures, fonts, music, or reference video. When the project ships both software and original media, split the grants.

**Default split when the publisher has not given a more specific instruction:**

| Work | Default license | Where the text lives |
|---|---|---|
| Source code, scripts, shaders, build files, tests, documentation text | MIT (or the code license chosen in §5.1) | `LICENSE` |
| Original 2D/3D art, character design, animation, storyboards, sprites, audio, video | CC-BY-4.0 | `LICENSE-ASSETS` with the full CC-BY-4.0 legal code |
| Third-party assets already under another license | Keep the upstream license | `NOTICE` plus a per-directory `LICENSE` or a row in `docs/THIRD_PARTY.md` |

**Directory convention:**

```text
assets/
  characters/          # original art → LICENSE-ASSETS
  animation/           # original animation → LICENSE-ASSETS
  vendor/              # third-party packs; each pack keeps its own license file
```

If assets live outside `assets/`, list the covered paths in both README files and in a short header at the top of `LICENSE-ASSETS`.

**README wording (English):**

```markdown
## License

- Source code and project documentation are licensed under the MIT License.
  See [LICENSE](LICENSE).
- Original artistic assets under `assets/` (character design, 2D animation,
  storyboards, and similar media) are licensed under Creative Commons
  Attribution 4.0 International (CC-BY-4.0). See [LICENSE-ASSETS](LICENSE-ASSETS).
- Third-party materials remain under their upstream licenses.
  See [NOTICE](NOTICE) when that file is present.
```

**README wording (Russian):**

```markdown
## Лицензия

- Исходный код и документация проекта распространяются по лицензии MIT.
  Текст: [LICENSE](LICENSE).
- Оригинальные художественные материалы в каталоге `assets/` (дизайн персонажей,
  2D-анимация, раскадровки и сходные медиа) распространяются по лицензии
  Creative Commons Attribution 4.0 International (CC-BY-4.0).
  Текст: [LICENSE-ASSETS](LICENSE-ASSETS).
- Материалы третьих сторон сохраняют исходные лицензии.
  См. [NOTICE](NOTICE), если этот файл присутствует.
```

**Agent rules:**

1. Ask the publisher before applying a split if the assets might be proprietary, commissioned, or not owned by the publisher.
2. Do not put commissioned client art under CC-BY-4.0 without written authorization.
3. Do not dual-license a file silently. Either the path is listed under the assets license, or it follows the code license.
4. Add a `license` field only for the *code* package in `package.json` / `pyproject.toml`. Describe the assets license in README, not by overwriting the package SPDX field.
5. If GitHub’s license detector reports only MIT, that is expected. The README and `LICENSE-ASSETS` remain the source of truth for media.

---

## 6. README: English + Russian

### 6.1 File split (required)

| File | Language | Role |
|---|---|---|
| `README.md` | English | Canonical. GitHub renders this on the repository home page. |
| `README.ru.md` | Russian | Complete equivalent, not a stub. |

Optional extras: `README.zh-CN.md`, etc., using the same switcher pattern.

Do **not** put both full languages into a single `README.md`. A dual-language monolith is hard to maintain and poor for scanners and non-Russian readers.

### 6.2 Language switcher (first visible block)

Place this immediately under the title in **both** files.

`README.md`:

```markdown
**Languages:** English | [Русский](README.ru.md)
```

`README.ru.md`:

```markdown
**Языки:** [English](README.md) | Русский
```

Keep section order identical across languages so updates stay synchronized.

### 6.3 What to translate and what not to translate

Translate: headings, prose, lists, warnings.

Do **not** translate:

- commands, code, file paths, CLI flags
- environment variable names
- badge URLs and badge Markdown
- license SPDX identifiers
- repository and package names

### 6.4 Required README sections (both languages)

Use this skeleton. Omit a section only when it is genuinely inapplicable (for example, a pure spec repository has no install steps). Replace angle-bracket placeholders.

The section **Hardware requirements** is mandatory when the project uses a local AI model, GPU inference, CUDA/ROCm, large video encode/decode, or other non-commodity hardware. Place it immediately after **Requirements** and before **Quick start**. Omit it only when a laptop CPU and a few hundred megabytes of RAM are sufficient.

Hardware section must state at least:

- CPU: minimum cores and architecture if relevant
- RAM: minimum and recommended
- GPU: vendor, minimum VRAM, compute capability / generation
- Storage: free space for weights, caches, or media
- Optional accelerators (NPU, Apple Silicon unified memory, TPU)
- Whether a CPU fallback exists and what performance penalty it implies

Example values: NVIDIA GPU with at least 12 GB VRAM (24 GB recommended); CUDA 12.x; 32 GB system RAM; 40 GB free disk for model weights.

```markdown
# <Project Name>

**Languages:** English | [Русский](README.ru.md)

<p align="left">
  <img alt="License" src="https://img.shields.io/badge/license-MIT-blue.svg">
</p>

Short one-sentence description of what the project does and for whom.

## Features

- …

## Requirements

- Runtime and versions (for example, Python 3.12+, Node.js 22+)
- Optional system tools

## Hardware requirements

- GPU: vendor, minimum VRAM, recommended VRAM
- RAM and free disk
- Driver / runtime (CUDA, ROCm, …)
- CPU fallback: yes/no, with expected slowdown

## Quick start

    git clone https://github.com/<owner>/<repo>.git
    cd <repo>
    cp .env.example .env

## Configuration

Table of environment variables: name, purpose, example **non-secret** value, required/optional.

## Usage

Minimal real example. Prefer copy-pasteable commands.

## Project structure

Short tree of the public layout.

## Testing

Exact test command.

## Security

No secrets in the repository. Report vulnerabilities via SECURITY.md (if present).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

This project is licensed under the <SPDX-ID> License. See [LICENSE](LICENSE).
```

When artistic assets use another license, replace the License section with the split text from §5.4.

Additional sections when they add value:

- Screenshots or a short demo GIF (redacted)
- Architecture overview (or link to `docs/`)
- Comparison with alternatives (factual, not marketing)
- Roadmap (only if maintained)
- Changelog link
- Citation / `CITATION.cff` for academic software

### 6.5 README quality rules

- First two lines must answer “what is this” and “why would I use it”.
- Quick start must work on a clean machine in a small number of commands.
- Hardware requirements must appear *before* Quick start when a GPU, a minimum VRAM figure, or another special device is required. Do not bury VRAM limits inside a late appendix.
- Do not dump the entire user manual into README. Move depth to `docs/`.
- Do not add vanity badges (“PRs welcome”, raw star counts, “made with love”).
- Add functional badges only when they are true: CI status, package version, license.
- Do not mention internal ticket IDs, client names, or “generated by <agent>”.
- Screenshots must not show desktop panels, personal bookmarks, emails, or real dashboards with customer data.
- Repository description (GitHub UI, 350 characters) is English, one sentence, no secrets.
- Topics/tags: 5–10 accurate tokens (`python`, `rest-api`, `sqlite`, …), no personal surnames.

### 6.6 Tone

Formal, precise, impersonal. Prefer “the service listens on port 8080” over “I wrote this on Sunday after work”.

---

## 7. `.gitignore` baseline

Create `.gitignore` **before** the first `git add`. Merge stack-specific templates (Node, Python, Go, Java, Rust, Flutter) from https://github.com/github/gitignore with the security block below.

Mandatory block for every project:

```gitignore
# Secrets and local configuration
.env
.env.*
!.env.example
*.pem
*.key
*.p12
*.pfx
id_rsa
id_ed25519
credentials.json
secrets.yaml
secrets.yml
kubeconfig
*.kubeconfig

# Agent / local AI tooling (never publish)
.agent/
.agents/
.claude/
.codex/
.cursor/
.aider/
.continue/
.windsurf/
.sweep/
.goose/
.idea-agent/
*.agent.md
.scratch/

# Terminal sessions, multiplexers, and distributed-agent runtime
*.tmux
*.tmux.log
tmux-client-*
tmux-server-*
screenlog.*
*.screenlog
zellij-dump*
nohup.out
*.cast
typescript
.typescript
pane-capture*
session-dump*
.queue/
.jobs/
.runs/
gpu-pool/
workers/*.log
dispatcher-state.json
*.queue.json
tmux-sessions/
terminal-dumps/

# OS
.DS_Store
Thumbs.db
Desktop.ini

# Editors (keep shared snippets only if they contain no local paths)
.idea/
.vscode/*
!.vscode/extensions.json
!.vscode/launch.json.example

# Logs and caches
*.log
.cache/
tmp/
temp/

# Dependencies and build output (adjust per stack)
node_modules/
dist/
build/
target/
__pycache__/
.venv/
venv/
.pytest_cache/
.mypy_cache/
coverage/
```

Rules:

- Never ignore `.github/` (workflows must be public).
- Never ignore `.gitignore` itself.
- Prefer ignoring secret *patterns*, not one-off filenames after the leak.
- Do not commit a “sanitized” tmux dump if the original still sits next to it under another name. Delete operational dumps from the publication tree entirely.
- Do not ignore application source directories that happen to be named `jobs` or `queue`. The patterns above target operational dumps (`.queue/`, `tmux-sessions/`, `gpu-pool/`). If a worker node writes logs into a source-like path, add that exact path locally rather than ignoring a generic project folder.

If the project *intentionally* ships a public demo recording, store a redacted file under `docs/demo/` with a note that it is staged for publication, and keep raw captures out of the repository.

---

## 8. `.gitattributes`

Add a minimal file so checkouts are predictable:

```gitattributes
* text=auto eol=lf
*.sh text eol=lf
*.bat text eol=crlf
*.cmd text eol=crlf
*.png binary
*.jpg binary
*.gif binary
*.webp binary
*.pdf binary
```

Mark generated files as `linguist-generated=true` when they would distort GitHub language stats (for example large lockfile-unrelated codegen). Do not hide real source.

---

## 9. Community and governance files

GitHub Community Standards look for these. Add what the project size justifies.

| File | When | Content rules |
|---|---|---|
| `CONTRIBUTING.md` | Any public repo that accepts changes | How to build, test, branch, and open a PR. English primary; Russian section or `CONTRIBUTING.ru.md` if the README is bilingual. No internal Slack channels. |
| `CODE_OF_CONDUCT.md` | Public collaboration | Use Contributor Covenant 2.1. Public contact email only if the owner wants mail. Otherwise GitHub Issues + private security contact. |
| `SECURITY.md` | Public code with users | How to report vulnerabilities privately. Never publish a real personal phone number. |
| `CHANGELOG.md` | Versioned software | [Keep a Changelog](https://keepachangelog.com/) + [SemVer](https://semver.org/). |
| `CITATION.cff` | Research / citable tools | Optional. |
| `.github/ISSUE_TEMPLATE/` | Non-trivial projects | Bug / feature forms. |
| `.github/PULL_REQUEST_TEMPLATE.md` | Same | Checklist: tests, docs, no secrets. |
| `CODEOWNERS` | Team repos | Only public GitHub usernames. |

`SECURITY.md` minimal shape:

```markdown
# Security Policy

## Supported versions

Describe which versions receive fixes.

## Reporting a vulnerability

Open a GitHub Security Advisory (preferred) or use the contact listed in the repository owner profile.
Do not file public issues for undisclosed vulnerabilities.

Please include reproduction steps and affected versions.
```

---

## 10. Git hygiene

### 10.1 Identity in commits

- `user.name` / `user.email` must be the identity the publisher intends to show on GitHub.
- Do not rewrite other people’s commit authors.
- If the local Git config still contains a work email that must stay private, set a repo-local config before committing:

```bash
git config user.name "Public Handle"
git config user.email "public-handle@users.noreply.github.com"
```

Prefer GitHub’s `noreply` address when the owner wants to hide a personal mailbox.

### 10.2 Commit messages

Conventional Commits are recommended:

```text
feat: add export to CSV
fix: handle empty config file
docs: add Russian README
chore: tighten gitignore for agent directories
```

Forbidden in messages:

- passwords, tokens, hostnames, personal names of third parties
- “vibe coded”, “asked ChatGPT”, dump of the prompt
- client ticket titles that identify a customer

### 10.3 What not to commit

- Generated vendor trees when the stack has a lockfile + install command
- Build outputs unless the project is a pages-only static snapshot and that is documented
- IDE folder state
- Large binaries (>1–5 MB each) without Git LFS and a documented reason
- Jupyter output cells with secrets; clear outputs before commit
- tmux / screen / Zellij dumps and dispatcher logs from GPU workers

### 10.4 Default branch

Use `main`. Do not publish `master` unless the existing public project already standardized on it.

### 10.5 First commit policy

The first commit of a **new public** repository should already be clean. Do not “commit everything then clean later”.

Suggested sequence:

```bash
git init -b main
# write .gitignore first
git add .gitignore LICENSE README.md README.ru.md
git add <source and docs>
git status   # human-or-agent review of every path
git commit -m "chore: initial public snapshot"
```

---

## 11. Creating and configuring the GitHub repository

### 11.1 Creation options

- Visibility: as instructed by the publisher. Default for this standard: public only after sanitization.
- **Do not** initialize the remote with README / LICENSE / `.gitignore` if a local tree already exists (merge conflicts).
- Description: English, factual.
- Website: only a real project URL.
- Do not put emails or phone numbers in the description.

### 11.2 Recommended settings (public repo)

Enable when available on the plan:

- Secret scanning
- Push protection
- Dependabot alerts and Dependabot security updates
- Dependency graph
- Code scanning / CodeQL if the language is supported
- Private vulnerability reporting

Branch protection or ruleset on `main` for any repo with more than one contributor:

- require pull request
- require passing CI
- disallow force-push to `main` **after** the clean initial publication
- optionally require signed commits if the team already uses them

### 11.3 GitHub Actions

- Pin third-party actions by commit SHA when used in serious projects.
- Set `permissions:` to least privilege on every workflow.
- Never echo secrets. Never pass secrets into pull requests from forks without a trusted pattern.
- Do not store cloud long-lived keys if OIDC is available.
- Workflow files must not contain real account IDs that identify a private org unless intended.

### 11.4 Releases

- Tags match SemVer when the project is versioned (`v1.2.3`).
- Release notes do not mention internal incidents or personal data.
- Attach build artifacts; do not attach `.env` or key material.

---

## 12. Documentation and supply-chain hygiene

- Pin dependencies with the stack’s lockfile and **commit the lockfile** when that is the ecosystem norm (npm, pnpm, Cargo, Poetry, Go sum, Gradle lock).
- Provide an SBOM only if the publisher asked; do not invent one.
- Record the exact supported runtime versions.
- If Docker is used, a public `Dockerfile` must not `COPY` secret files or use private base images the reader cannot pull.
- Health: a stranger’s `clone → follow Quick start → run tests` must succeed or the README must state the blocker honestly (for example “requires a GPU”).

---

## 13. Incident response: leaked personal data or secrets

### 13.1 Severity model

| What leaked | First action | Repository fate |
|---|---|---|
| Live secret (key, password, token, private key) | **Revoke/rotate immediately** at the issuer. Check billing and access logs. | After rotation: prefer **delete + recreate** for a repo that is not yet widely forked. History rewrite is insufficient by itself. |
| PII, real names, personal emails, phone numbers, home paths | Stop publishing. Redact the working tree. | **Delete + recreate** so GitHub search and commit URLs die with the old repo. |
| Internal hostnames, VPN, Tailscale/`100.64/10` addresses, inventory | Treat as sensitive infrastructure data. | **Delete + recreate** unless the publisher explicitly accepts rewrite risk. |
| `.agent/`, tmux dumps, queue-manager logs, or other local traces, no secrets | Still a policy violation. | If already pushed: **delete + recreate**. Do not leave “removed .agent” commits on a public history. |
| Harmless local path in an unpublished commit | `git reset` / amend if not pushed | Continue. |

### 13.2 Why delete and recreate

Rewriting history (`git filter-repo`, BFG, force-push) does **not** reliably erase traces on GitHub:

- forks and local clones keep the old objects
- pull request refs are not fully writable by maintainers
- GitHub support may need to purge cached objects
- search engines and scrapers may have copied commits
- issue comments and Actions logs can still quote the secret

For a project that has **not** accumulated irreplaceable GitHub metadata (stars, issues, review history), the required procedure is:

1. Rotate every exposed credential. Assume it is burned.
2. Sanitize a **new directory** with no `.git` history.
3. On GitHub: delete the contaminated repository (Settings → Danger zone → Delete).
4. Wait until the name is released, or use a new name if the old name must be abandoned.
5. Create a **new empty** repository with the same or new name. Do not initialize with files.
6. Push only the clean history.
7. Re-apply settings from §11.
8. Notify anyone who cloned the old URL that the old history is untrusted.

Do not transfer a dirty repo to another owner “to hide it”. Delete it.

### 13.3 If deletion is impossible

Only when the publisher forbids deletion (many forks, production URL pinned, compliance archive):

1. Rotate secrets first.
2. Follow GitHub’s current guide: [Removing sensitive data from a repository](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository).
3. Use `git filter-repo` with `--sensitive-data-removal` on a fresh mirror clone.
4. Force-push all branches and tags.
5. Contact GitHub Support to purge cached objects and ask collaborators to re-clone.
6. Document residual risk: copies may still exist.

The agent must not pretend that a force-push equals erasure.

### 13.4 After cleanup

- Enable push protection and secret scanning.
- Add pre-commit secret scanning locally if the publisher wants it (`gitleaks`, `detect-secrets`). Do not commit the scanner’s local caches.
- Record the incident privately. Do not publish a post-mortem that repeats the secret or the PII.

---

## 14. Pre-push checklist (all boxes required)

Copy this list into the agent’s working notes and tick every item. In a multi-node agent pool the same list is the payload of the global lock in §14.2.

### 14.1 Checklist

**Content**

- [ ] `.gitignore` exists and includes secrets, build outputs, `.agent/` (and equivalents), tmux/session dumps, and queue-manager logs
- [ ] `git status` shows no env files, keys, dumps, agent folders, terminal captures, or absolute-path IDE state
- [ ] Repository-wide scan for secrets, PII, overlay-VPN addresses (`100.64.0.0/10`, `*.ts.net`, WireGuard keys), and internal FQDNs completed
- [ ] Example config files contain only placeholders
- [ ] Screenshots and notebooks are redacted / outputs cleared
- [ ] No local absolute paths in scripts or docs
- [ ] No real hostnames, public IPs, private IPs, CGNAT/Tailscale addresses, or internal service names
- [ ] Hardware requirements are documented when the project needs a GPU, a VRAM minimum, or other special devices

**Legal and community**

- [ ] `LICENSE` contains the full official text and an acceptable copyright line
- [ ] If the tree contains original art, animation, or other media, `LICENSE-ASSETS` (or an equivalent split) exists and both READMEs describe the split
- [ ] `README.md` is complete English
- [ ] `README.ru.md` is a complete Russian equivalent with the same section order
- [ ] Language switcher is present in both README files
- [ ] License section points to `LICENSE` and, when applicable, to `LICENSE-ASSETS`
- [ ] `CONTRIBUTING.md` and `SECURITY.md` added when the project is public and non-trivial

**Git and GitHub**

- [ ] Default branch is `main`
- [ ] Commit identity is the intended public identity
- [ ] Commit messages contain no sensitive or agent-session text
- [ ] Remote is empty (or known-clean) before the first push
- [ ] After push: description, topics, secret scanning, push protection, Dependabot reviewed
- [ ] Quick start was mentally (or actually) executed against the files that were committed

**If anything sensitive was ever pushed**

- [ ] Secrets rotated
- [ ] Old repository deleted
- [ ] New repository created empty
- [ ] Clean tree pushed as new history

**Distributed assembly (when more than one agent node produced the tree)**

- [ ] Dispatcher recorded a single assembled revision (commit SHA or tree hash)
- [ ] Every contributing node reported “clean” against that revision
- [ ] Global push lock released by the dispatcher only after this entire checklist is green

### 14.2 Global push lock for distributed agent pools

When several coding agents work in parallel on different hosts (including GPU workers reached over an overlay VPN), `git push` is **not** a local decision.

Rules:

1. **One publisher role.** Only the dispatcher (or a designated release node) is allowed to run `git push` to GitHub. Worker nodes may commit to local branches or to an internal staging remote that is not GitHub.
2. **Lock before push.** The dispatcher acquires an exclusive lock, for example `.git/push.lock` in the assembled repository, a row in the queue manager, or an equivalent mutex. No other node may push, force-push, tag, or create a GitHub release while the lock is held.
3. **Assemble, then scan once.** Workers submit artifacts to the assembly tree. The dispatcher produces one candidate revision and runs §3.2 plus §14.1 against *that* tree, not against each worker’s dirty workspace.
4. **All-green gate.** The lock is released for push only when every item in §14.1 is green **and** every worker that contributed files has acknowledged that its local-only paths (`.agent/`, tmux dumps, queue logs, overlay configs) were not copied into the assembly tree.
5. **Any red item blocks every node.** A single failed scan (secret, `100.x` address, session dump, missing license split, missing hardware section when required) keeps the lock closed. Workers must fix and resubmit. Nobody “pushes their part”.
6. **No GitHub credentials on workers.** GPU nodes and queue runners must not hold a GitHub PAT, SSH deploy key, or `gh` login for the public repository. Credentials live only on the dispatcher.
7. **Record the gate.** Persist a short, non-secret attestation next to the lock (timestamp, tree hash, checklist result). Do not write overlay IPs, auth keys, or job payloads into that attestation if it might later be committed. Prefer keeping the attestation outside Git.
8. **Never bypass the lock** with `git push --force` from a worker, a personal laptop, or an IDE plugin while a distributed publication is in progress.

If the project is prepared by a single local agent, §14.1 still applies and §14.2 is satisfied vacuously: that agent *is* the dispatcher.

---

## 15. Agent workflow (end-to-end)

Execute in this order. Do not skip to `git push`.

1. Read the publisher’s license choice, visibility, owner/repo name, public contact policy, and whether artistic assets need a separate license.
2. Inventory the working tree. Classify every top-level path as **public**, **local-only**, or **secret**.
3. Write or update `.gitignore` first, including agent dirs, tmux/session dumps, and queue logs.
4. Build the publication tree. Remove local-only paths from the index if a Git repo already exists.
5. Add `LICENSE`, and `LICENSE-ASSETS` when §5.4 applies; add bilingual READMEs and community files.
6. Replace real configuration with examples. Strip overlay-VPN addresses and worker hostnames.
7. Document hardware requirements when the project needs a GPU, a VRAM minimum, or other special devices.
8. Run the secret/PII/overlay scan (§3.2).
9. Review `git status` and `git diff --cached` path by path.
10. Commit on `main` in the assembled tree.
11. If multiple nodes contributed, acquire the §14.2 lock and wait for an all-green §14.1.
12. Create the empty GitHub repository (or use an approved existing empty repo).
13. Push `main` **only from the dispatcher**.
14. Apply GitHub security and metadata settings.
15. Re-read the GitHub web UI as a stranger: description, file list, README rendering, license detection.

If step 8 fails after a push already happened, go to §13. Do not “fix forward” by committing a deletion of `.env` and calling the job done.

---

## 16. Mini-templates the agent may paste

### 16.1 `.env.example`

```dotenv
# Copy to .env and fill in local values. Never commit .env.

APP_ENV=development
APP_PORT=8080
DATABASE_URL=postgres://user:password@localhost:5432/app
API_TOKEN=replace-me
```

### 16.2 Pull request template (`.github/PULL_REQUEST_TEMPLATE.md`)

```markdown
## Summary

## Checklist

- [ ] I did not add secrets, personal data, or local machine paths
- [ ] Tests pass
- [ ] Documentation (README.md and README.ru.md) updated when behavior changed
- [ ] License headers / third-party notices remain correct
```

### 16.3 Dependabot (`.github/dependabot.yml`)

```yaml
version: 2
updates:
  - package-ecosystem: npm
    directory: "/"
    schedule:
      interval: weekly
```

Adjust `package-ecosystem` to the real stack (`pip`, `gomod`, `cargo`, `github-actions`, …).

---

## 17. References (authoritative)

- GitHub: [Best practices for repositories](https://docs.github.com/en/repositories/creating-and-managing-repositories/best-practices-for-repositories)
- GitHub: [About READMEs](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-readmes)
- GitHub: [Licensing a repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository)
- GitHub: [Setting guidelines for repository contributors](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/setting-guidelines-for-repository-contributors)
- GitHub: [Adding a security policy](https://docs.github.com/en/code-security/getting-started/adding-a-security-policy-to-your-repository)
- GitHub: [Removing sensitive data from a repository](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)
- GitHub: [Keeping your account and data secure](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure)
- [Choose a License](https://choosealicense.com/)
- [Keep a Changelog](https://keepachangelog.com/)
- [Semantic Versioning](https://semver.org/)
- [Contributor Covenant](https://www.contributor-covenant.org/)
- Gitignore templates: https://github.com/github/gitignore

---

## 18. Non-negotiable summary for the agent

1. No personal data, no secrets, no real servers, no overlay-VPN addresses (`100.64.0.0/10`, `*.ts.net`, WireGuard keys), no local paths.
2. `.agent/` and other vibe-coding / agent-session directories stay on the local PC and in `.gitignore`. The same applies to tmux/screen dumps and queue-manager logs from GPU workers.
3. Public repos get a real `LICENSE` file with official text. Code and original artistic assets are licensed separately when both are present (typical split: MIT + CC-BY-4.0).
4. `README.md` is English; `README.ru.md` is a full Russian twin; both have a language switcher. Projects that need a GPU or other special hardware document this before Quick start.
5. If sensitive data reached GitHub, rotate secrets and **delete the repository, then create a new empty one** with a clean history. Do not leave traces in old commits.
6. Nothing is pushed until §14.1 is fully satisfied. In a distributed agent pool, §14.2 is a global lock: only the dispatcher pushes, and only after every node reports a clean assembled tree.

