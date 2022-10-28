# How to Contribute

The Ramen project in under [Apache 2.0 license](LICENSES/Apache-2.0.txt).
We accept contributions via GitHub pull requests. This document outlines
some of the conventions related to development workflow to make it
easier to get your contribution accepted.

## Certificate of Origin

By contributing to this project you agree to the Developer Certificate of
Origin (DCO). This document was created by the Linux Kernel community and is a
simple statement that you, as a contributor, have the legal right to make the
contribution. See the [DCO](DCO) file for details.

Contributors sign-off that they adhere to these requirements by adding a
Signed-off-by line to commit messages. For example:

```
This is my commit message

Signed-off-by: Random J Developer <random@developer.example.org>
```

Git even has a -s command line option to append this automatically to your
commit message:

```
git commit -s -m 'This is my commit message'
```

If you have already made a commit and forgot to include the sign-off, you can
amend your last commit to add the sign-off with the following command, which
can then be force pushed.

```
git commit --amend -s
```

## Getting Started

1. Fork the repository on GitHub
1. Read the [install](docs/install.md) guide for build and deploy instructions
1. Play with the project, submit bugs, submit patches!

## Contribution Flow

This is a rough outline of what a contributor's workflow looks like:

1. Create a branch from where you want to base your work (usually main).
1. Make your changes and arrange them in readable commits.
1. Make sure your commit messages are in the proper format (see below).
1. Push your changes to the branch in your fork of the repository.
1. Make sure all [tests](docs/testing.md) pass, and add any new [tests](docs/testing.md)
   as appropriate.
1. Submit a pull request to the original repository.

## Coding Style

Ramen project is written in golang and follows the style guidelines dictated
by the go fmt as well as go vet tools.

Additionally various golang linters are enforced to ensure consistent coding
style, these can be run using, `make lint` and in turn uses [golangci](https://golangci-lint.run/)
with [this](.golangci.yaml) configuration.

Several other linters are run on the pull request using the
[pre-commit.sh](hack/pre-commit.sh) script.

### Pull Request Flow

The general flow for a pull request approval process is as follows:

1. Author submits the pull request
1. Reviewers and maintainers for the applicable code areas review the pull
   request and provide feedback that the author integrates
1. Reviewers and/or maintainers signify their LGTM on the pull request
1. A maintainer approves the pull request based on at least one LGTM from the
   previous step
    1. Note that the maintainer can heavily lean on the reviewer for examining
       the pull request at a finely grained detailed level. The reviewers are
       trusted members and maintainers can leverage their efforts to reduce
       their own review burden.
1. A maintainer merges the pull request into the target branch (main, release,
   etc.)
