// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// Command gendocs turns the repository's ../docs Markdown tree into Hugo
// content under content/en/docs, so docs/ stays the single, Hugo-free source
// of truth. For each page it derives the title from the first H1, assigns a
// nav weight, and rewrites relative image links to the paths served by the
// static mount (see hugo.toml). Section index pages are generated too.
//
// Run from the website/ directory: go run ./tools/gendocs
package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const staticDocsDir = "static/docs"

const (
	srcDir = "../docs"
	outDir = "content/en/docs"
)

// excludeDirs are top-level docs/ subdirectories that are never published:
// Claude planning artifacts, raw logos, and diagram sources. Diagram *images*
// (.svg/.png under diagrams/) are still served via the static mount.
var excludeDirs = map[string]bool{
	"superpowers": true,
	"artwork":     true,
	"diagrams":    true,
}

// pageWeight orders individual pages in the left nav. Lower sorts first.
// Unlisted pages fall back to defaultWeight (alphabetical among themselves).
var pageWeight = map[string]int{
	"user-quick-start":    10,
	"devel-quick-start":   20,
	"install":             30,
	"configure":           40,
	"usage":               50,
	"recipe":              60,
	"logging":             70,
	"metrics":             75,
	"testing":             80,
	"e2e":                 85,
	"build":               90,
	"drpolicy-crd":        110,
	"drcluster-crd":       120,
	"drclusterconfig-crd": 130,
	"drpc-crd":            140,
	"vrg-crd":             150,
	"maintenancemode-crd": 160,
}

const defaultWeight = 500

// section describes a generated _index.md for a docs/ subdirectory.
type section struct {
	title  string
	weight int
}

var sections = map[string]section{
	".":                {title: "Documentation", weight: 10},
	"design":           {title: "Design", weight: 200},
	"design/proposals": {title: "Proposals", weight: 100},
	"dev":              {title: "Developer", weight: 300},
	"drenv":            {title: "drenv", weight: 350},
}

var (
	h1Re      = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)
	mdImageRe = regexp.MustCompile(`(!\[[^\]]*\]\()([^)]+)(\))`)
	htmlImgRe = regexp.MustCompile(`(<img[^>]*\ssrc=")([^"]+)(")`)
	// mdLinkRe matches [text](dest); the leading (?:^|[^!]) guard keeps it from
	// matching image syntax ![text](dest).
	mdLinkRe = regexp.MustCompile(`(^|[^!])(\[[^\]]*\]\()([^)]+)(\))`)
	frontEnd = "---\n\n"

	// published is the set of publishable docs, keyed by lowercased path
	// relative to docs/ (e.g. "drcluster-crd.md", "design/incremental-sync.md").
	published = map[string]bool{}
	seenDirs  = map[string]bool{}
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run() error {
	for _, d := range []string{outDir, staticDocsDir} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// Pass 1: collect the set of published docs so links can be resolved to
	// those that exist (case-insensitively) vs. linked to GitHub otherwise.
	type page struct{ src, rel string }
	var found []page
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && excludeDirs[topDir(rel)] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".md") || excludeDirs[topDir(rel)] {
			return nil
		}
		found = append(found, page{p, rel})
		published[strings.ToLower(rel)] = true
		return nil
	})
	if err != nil {
		return err
	}

	// Pass 2: convert each page.
	for _, pg := range found {
		if err := convert(pg.src, pg.rel); err != nil {
			return fmt.Errorf("%s: %w", pg.rel, err)
		}
	}

	if err := writeSections(); err != nil {
		return err
	}
	fmt.Printf("gendocs: wrote %d pages and %d section indexes to %s\n", len(found), len(seenDirs)+1, outDir)
	return nil
}

func topDir(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

func convert(srcPath, rel string) error {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	body := string(raw)

	slug := strings.TrimSuffix(path.Base(rel), ".md")
	title := firstH1(body)
	if title == "" {
		title = humanize(slug)
	}

	weight := defaultWeight
	if w, ok := pageWeight[slug]; ok {
		weight = w
	}

	body = rewriteImages(body, path.Dir(rel))
	body = rewriteLinks(body, path.Dir(rel))

	dir := path.Dir(rel)
	if dir != "." {
		seenDirs[dir] = true
		// Record intermediate parents too, so every section gets an index.
		for d := dir; d != "." && d != "/"; d = path.Dir(d) {
			seenDirs[d] = true
		}
		if err := os.MkdirAll(filepath.Join(outDir, dir), 0o755); err != nil {
			return err
		}
	}

	out := frontMatter(map[string]any{
		"title":  title,
		"weight": weight,
		"slug":   strings.ToLower(slug),
	}) + body
	return os.WriteFile(filepath.Join(outDir, rel), []byte(out), 0o644)
}

// writeSections emits _index.md for the docs root and every subdirectory that
// contained pages, so the left nav shows readable section names.
func writeSections() error {
	// Root index: a curated landing page.
	root := frontMatter(map[string]any{
		"title":     "Documentation",
		"linkTitle": "Documentation",
		"weight":    sections["."].weight,
	}) + rootIndexBody
	if err := os.WriteFile(filepath.Join(outDir, "_index.md"), []byte(root), 0o644); err != nil {
		return err
	}

	dirs := make([]string, 0, len(seenDirs))
	for d := range seenDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		meta, ok := sections[dir]
		if !ok {
			meta = section{title: humanize(path.Base(dir)), weight: defaultWeight}
		}
		idx := filepath.Join(outDir, dir, "_index.md")
		if _, err := os.Stat(idx); err == nil {
			continue // a real _index.md page already exists
		}
		fm := frontMatter(map[string]any{"title": meta.title, "weight": meta.weight})
		if err := os.WriteFile(idx, []byte(fm+"{{% pageinfo %}}\nPages in this section come from the "+
			"[`docs/`](https://github.com/RamenDR/ramen/tree/main/docs) directory.\n{{% /pageinfo %}}\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// copyImage copies docs/<rel> into static/docs/<rel>. Missing files are warned
// about (a broken link in the source) but do not fail the build.
func copyImage(rel string) {
	src := filepath.Join(srcDir, filepath.FromSlash(rel))
	in, err := os.Open(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gendocs: warning: image not found: %s\n", rel)
		return
	}
	defer in.Close()

	dst := filepath.Join(staticDocsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "gendocs: warning: %v\n", err)
		return
	}
	out, err := os.Create(dst)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gendocs: warning: %v\n", err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		fmt.Fprintf(os.Stderr, "gendocs: warning: %v\n", err)
	}
}

// splitDestTitle splits the inside of a Markdown link/image target into the
// destination and an optional title, e.g. `foo.md "Title"` -> ("foo.md",
// ` "Title"`). The returned title keeps its leading whitespace so the original
// text can be reconstructed as dest+title. A destination with no title returns
// an empty title.
func splitDestTitle(inner string) (dest, title string) {
	// A title is separated from the destination by whitespace. Destinations
	// containing spaces must be wrapped in <...>; handle that form first.
	if strings.HasPrefix(inner, "<") {
		if i := strings.IndexByte(inner, '>'); i >= 0 {
			return inner[:i+1], inner[i+1:]
		}
	}
	if i := strings.IndexAny(inner, " \t"); i >= 0 {
		return inner[:i], inner[i:]
	}
	return inner, ""
}

func firstH1(body string) string {
	m := h1Re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// rewriteImages turns relative image links into absolute /docs/... paths and
// copies the referenced image into static/docs/ so Hugo serves it. Absolute
// and remote links are left untouched.
func rewriteImages(body, pageDir string) string {
	fix := func(dest string) string {
		trimmed := strings.TrimSpace(dest)
		if trimmed == "" || strings.HasPrefix(trimmed, "/") ||
			strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") ||
			strings.HasPrefix(trimmed, "data:") {
			return dest
		}
		clean := trimmed
		if i := strings.IndexAny(clean, "?#"); i >= 0 {
			clean = clean[:i]
		}
		abs := path.Clean(path.Join("/docs", pageDir, clean))
		if rel, ok := strings.CutPrefix(abs, "/docs/"); ok {
			copyImage(rel)
		}
		return abs
	}
	body = mdImageRe.ReplaceAllStringFunc(body, func(m string) string {
		g := mdImageRe.FindStringSubmatch(m)
		dest, title := splitDestTitle(g[2])
		return g[1] + fix(dest) + title + g[3]
	})
	body = htmlImgRe.ReplaceAllStringFunc(body, func(m string) string {
		g := htmlImgRe.FindStringSubmatch(m)
		return g[1] + fix(g[2]) + g[3]
	})
	return body
}

const githubBlob = "https://github.com/RamenDR/ramen/blob/main/"

// rewriteLinks resolves relative Markdown links. Links to a published doc become
// site-absolute pretty URLs (/docs/<lower-path>/); links to files elsewhere in
// the repo become GitHub source URLs. Remote, in-page and already-absolute links
// are left untouched. The render-link hook later prefixes the Pages subpath.
func rewriteLinks(body, pageDir string) string {
	fix := func(dest string) string {
		trimmed := strings.TrimSpace(dest)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") ||
			strings.HasPrefix(trimmed, "mailto:") {
			return dest
		}
		linkPath, anchor := trimmed, ""
		if i := strings.IndexByte(linkPath, '#'); i >= 0 {
			anchor, linkPath = linkPath[i:], linkPath[:i]
		}
		if !strings.HasSuffix(linkPath, ".md") {
			// Absolute paths are left as-is: they are already site-root
			// relative and the render-link hook adds the Pages subpath.
			// Relative non-Markdown files fall through to the GitHub fallback
			// below so they resolve to their source instead of a dead link.
			if strings.HasPrefix(linkPath, "/") {
				return dest
			}
		}
		// Resolve to a repository-root-relative path. A leading slash is
		// treated as repo-root-relative (e.g. /docs/x.md); otherwise the link
		// is relative to the current page's directory under docs/.
		var repoPath string
		if strings.HasPrefix(linkPath, "/") {
			repoPath = path.Clean(strings.TrimPrefix(linkPath, "/"))
		} else {
			repoPath = path.Clean(path.Join("docs", pageDir, linkPath))
		}
		if docsRel, ok := strings.CutPrefix(repoPath, "docs/"); ok && published[strings.ToLower(docsRel)] {
			url := "/docs/" + strings.ToLower(strings.TrimSuffix(docsRel, ".md")) + "/"
			return url + anchor
		}
		// Outside the published docs: link to the source on GitHub.
		return githubBlob + repoPath + anchor
	}
	return mdLinkRe.ReplaceAllStringFunc(body, func(m string) string {
		g := mdLinkRe.FindStringSubmatch(m)
		dest, title := splitDestTitle(g[3])
		return g[1] + g[2] + fix(dest) + title + g[4]
	})
}

func humanize(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// frontMatter renders a minimal YAML front matter block. Only the key types
// used by this generator are handled.
func frontMatter(kv map[string]any) string {
	var b strings.Builder
	b.WriteString("---\n")
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeYAML(&b, k, kv[k], 0)
	}
	b.WriteString(frontEnd)
	return b.String()
}

func writeYAML(b *strings.Builder, key string, val any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch v := val.(type) {
	case map[string]any:
		fmt.Fprintf(b, "%s%s:\n", pad, key)
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			writeYAML(b, k, v[k], indent+1)
		}
	case string:
		fmt.Fprintf(b, "%s%s: %q\n", pad, key, v)
	default:
		fmt.Fprintf(b, "%s%s: %v\n", pad, key, v)
	}
}

const rootIndexBody = `Welcome to the Ramen documentation. These pages are generated from the
[` + "`docs/`" + `](https://github.com/RamenDR/ramen/tree/main/docs) directory of the
repository.

## Getting started

- [User quick start](user-quick-start/) — protect your first workload
- [Developer quick start](devel-quick-start/) — set up a local environment
- [Install](install/) — deploy Ramen on your clusters
- [Configure](configure/) — configure DR policies and clusters
- [Usage](usage/) — day-to-day operations

## API reference

- [DRPolicy](drpolicy-crd/)
- [DRCluster](drcluster-crd/)
- [DRClusterConfig](drclusterconfig-crd/)
- [DRPlacementControl (DRPC)](drpc-crd/)
- [VolumeReplicationGroup (VRG)](vrg-crd/)
- [MaintenanceMode](maintenancemode-crd/)

## Design & development

- [Design documents](design/)
- [Developer notes](dev/)
- [drenv test environment](drenv/)
`
