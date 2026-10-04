#!/bin/sh
# occulited's version is the commit it was built from (occulited task 16, which took back task 9's
# image version): the full hash, with -dirty appended when the tree has uncommitted changes, and
# "dev" outside a git checkout. The v* tags each image build round puts on occulited's pinned commit
# (v1.0.0-dev.40) only say which occulited went into which image; they are not the version, so this
# never asks `git describe`. Prints "<version> <commit>"; the commit is the bare hash, empty outside
# git. The image's package does not run this: it builds from an archive without .git and passes the
# pinned commit as both.
set -eu
cd "$(dirname "$0")/.."
commit=$(git rev-parse HEAD 2>/dev/null || true)
version=${commit:-dev}
if [ -n "$commit" ] && ! git diff --quiet HEAD -- 2>/dev/null; then
  version=$commit-dirty
fi
printf '%s %s\n' "$version" "$commit"
