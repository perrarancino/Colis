# Colis

A small Three.js game base: you are a cube, seen from above, walking around a
procedurally generated island in the middle of the ocean.

```bash
npm install
npm run dev      # http://localhost:5173
```

| Key | Action |
| --- | --- |
| `W A S D` / arrows | move |
| `Shift` | sprint |
| mouse wheel | zoom |
| `R` | generate a new island |

The seed is kept in the URL (`?seed=123` or any text), so a link always shows the same island.

## How it is put together

| File | What it does |
| --- | --- |
| `src/noise.js`, `src/random.js` | seeded simplex noise, PRNG and math helpers |
| `src/island.js` | height function (warped radial falloff + fBm + ridged hills), flat-shaded terrain mesh, instanced trees and rocks, collision circles |
| `src/ocean.js` | animated translucent sea that follows the player |
| `src/player.js` | the cube: acceleration, sliding collisions, wading (slower in shallows, blocked in deep water), hop / squash animation |
| `src/minimap.js` | corner map drawn from the same height function |
| `src/main.js` | renderer, lighting and shadows, follow camera, game loop |

---

# Easy Git

`easygit/` contains a tiny Windows program that keeps a folder in sync with a GitHub
repository — no Git, .NET or any other runtime needed (a single ~6 MB `EasyGit.exe`).

**Download:** [`easygit/dist/EasyGit.exe`](easygit/dist/EasyGit.exe)

1. Choose the project folder.
2. Enter the repository (`owner/name` or its URL).
3. Sign in: press **Get token**, create a token with the `repo` scope, paste it, press **Sign in**.
   The token is stored encrypted with Windows DPAPI (only your Windows account can read it).
4. Press **Sync** to pull, **Commit** to commit, or tick **Auto-commit every N min**.

What it does:

- **Sync** — downloads what is new on GitHub (only the changed files) and tells you how many local
  changes are still waiting to be committed. It never creates a commit.
- **Commit** — pulls first, then uploads your local changes as one commit. If there is nothing to
  commit it says so ("Nothing to commit, no local changes").
- **Sync with GitHub when the program starts** (on by default) — a Sync as soon as Easy Git launches.
- **Auto-commit every N min** (default 10, tick the box to enable) — on that timer your changes are
  committed in one commit, and GitHub is polled every 45 s with a conditional request so commits
  made elsewhere are pulled automatically. The timer only pulls in between; it never commits earlier.
  With the box unticked nothing happens until you press the button.
- **Commit message** — if the field is empty the message is simply the time of the commit
  (`2026-10-14 20:57:40`). A message you type is used for the next commit only.
- **Only the differences travel.** Files are compared by their Git blob hash; unchanged files are
  never downloaded or uploaded again. A pull fetches just the changed blobs, a commit uploads just the
  changed files as a single commit.
- A real three-way comparison (local / last sync / GitHub). If the same file changed on both sides,
  GitHub's version wins and yours is kept next to it as `name.conflict-<time>.ext`.
- Text files are compared with CRLF turned into LF (like Git's `autocrlf`), so a Windows checkout is not
  seen as modified and is committed with LF line endings.
- `.gitignore` is respected. `.git`, `*.conflict-*` and temporary files are never synced.
- Optional: start with Windows, keep running in the tray.

It talks to the GitHub REST API directly, which is why it does not need Git installed. Because of that
it does not touch a local `.git` folder — use either Easy Git or `git` for a given folder, not both.
Files above 95 MB are skipped.

Command line (for scripts): `EasyGit.exe --cli --dir C:\project --repo owner/name [--branch main] [--msg "text"] [--token-env VAR]`
(build the console variant without `-H=windowsgui` to see its output).

Build from source (needs Go 1.25+): `powershell -ExecutionPolicy Bypass -File easygit/build.ps1`
