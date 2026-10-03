#!/bin/sh
# occulited's version (openccu-lite's occulited task 9): the image version its commit is tagged with.
# Every image build round tags the commit the image pins with the image version (v1.0.0-dev.38), so
# a build of that commit is exactly 1.0.0-dev.38; a build between rounds is `git describe`'s
# 1.0.0-dev.38-5-gfa42dfd, and -dirty is appended when the tree has uncommitted changes. Without a
# v* tag (a shallow clone, a source archive) it is "dev". Prints "<version> <commit>"; the commit is
# the full hash, empty outside a git checkout. The image's package does not run this: it builds from
# an archive without .git and passes both from its pin.
set -eu
cd "$(dirname "$0")/.."
commit=$(git rev-parse HEAD 2>/dev/null || true)
if version=$(git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null); then
  version=${version#v}
else
  version=dev
  if [ -n "$commit" ] && ! git diff --quiet HEAD -- 2>/dev/null; then
    version=dev-dirty
  fi
fi
printf '%s %s\n' "$version" "$commit"
