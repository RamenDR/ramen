<!--
SPDX-FileCopyrightText: The RamenDR authors
SPDX-License-Identifier: Apache-2.0
-->

# Ramen documentation site

The public documentation site for Ramen, built with [Hugo](https://gohugo.io/)
and the [Docsy](https://www.docsy.dev/) theme and published to GitHub Pages at
<https://ramendr.github.io/ramen/>.

## How it works

The Markdown under the repository's [`docs/`](../docs) directory is the single
source of truth. Those files are plain Markdown with no Hugo front matter, so a
small generator turns them into Hugo content:

- `go run ./tools/gendocs` reads `../docs`, derives each page title from its
  first `# H1`, assigns a nav weight, rewrites relative links and images, and
  writes the result to `content/en/docs/` (git-ignored). Referenced images are
  copied into `static/docs/`.
- Links to other docs become pretty URLs; links to files elsewhere in the repo
  become links to the source on GitHub.

Because the generated content is produced at build time, **edit docs in
`../docs`, never under `content/en/docs/`.**

## Prerequisites

- [Hugo **extended**](https://gohugo.io/installation/) (v0.167 or newer)
- Go (for the generator and Hugo modules) — see the repo's `go.mod`
- Node.js + npm (Docsy's PostCSS pipeline)

## Local preview

```sh
cd website
make deps     # once: install Node packages
make serve    # regenerate content and serve at http://localhost:1313/
```

Other targets: `make build` (production build into `public/`), `make content`
(regenerate only), `make clean`.

## Publishing

Pushes to `main` that touch `docs/**` or `website/**` trigger
`.github/workflows/docs.yml`, which builds the site and deploys it to GitHub
Pages. The workflow passes the Pages URL to Hugo as `--baseURL`, so it works
unchanged on a fork (`https://<user>.github.io/ramen/`) for testing.

**One-time setup (repo admin):** in **Settings → Pages**, set **Source** to
**GitHub Actions**.
