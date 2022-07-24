# gh-fs

All of GitHub, accessible as a userspace filesystem.

## Installation

Currently only Linux and FreeBSD are supported, because those are the only platforms [the main dependency](https://github.com/bazil/fuse) supports.

`gh-fs` must be installed and used as an extension of the [GitHub CLI](https://github.com/cli/cli) as it relies on running under that environment for authentication. The minimum supported version of `gh` is [`2.3.0`](https://github.com/cli/cli/releases/tag/v2.3.0) since that is the first version to support precompiled extensions.

```bash
gh extension install mtoohey31/gh-fs
```

## Usage

```bash
mkdir mountpoint # create the mountpoint
gh fs mountpoint & # mount the filesystem

cat mountpoint/mtoohey31/gh-fs/README.md # interact with the filesystem
ls mountpoint/mtoohey31

kill %1 # stop gh-fs
umount mountpoint # unmount the filesystem
```
