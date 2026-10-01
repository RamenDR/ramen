# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# Thin command interface for the Ramen documentation site (Hugo + Docsy).
# content/en/docs is generated from ../docs by tools/gendocs.

.PHONY: serve build content deps clean

# Live preview with drafts, at http://localhost:1313/
serve: content
	hugo server --buildDrafts

# Production build into ./public
build: content
	hugo --minify --gc

# Regenerate Hugo content from the repository's ../docs
content:
	go run ./tools/gendocs

# Install the Node packages Docsy needs for its PostCSS pipeline
deps:
	npm install

clean:
	rm -rf public resources static/docs content/en/docs
