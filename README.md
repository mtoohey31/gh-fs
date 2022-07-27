# gh-fs

All of GitHub, accessible as a userspace filesystem.

This is a work in progress. Many types of content, including symlinks, submodules, large files, and binary files, are not accessible yet.

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

Please be aware that it is very easy to hit the rate limit of GitHub's API. Commands that access a lot of files/folders (i.e. recursively grepping your user directory) are likely to result in your API requests being rate limited.

Also, note that when listing the root directory of the filesystem, only the authenticated user and those that they follow will be displayed. You can still access the repositories of other users by specifying the correct path.
