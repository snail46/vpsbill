#!/bin/sh
# Builds lxcfs into /hatch-lxcfs-linux-ARCH.tar.gz (used by the Dockerfile).
# The tarball unpacks to /opt/hatch-lxcfs; lxcfs loads its library from that
# absolute path, so it must be installed there.
#
#   build-lxcfs.sh VERSION ARCH
set -eu
VERSION=$1
ARCH=$2

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  build-essential meson ninja-build pkg-config libfuse3-dev git python3-jinja2 ca-certificates
git clone --depth 1 -b "v$VERSION" https://github.com/lxc/lxcfs /src
cd /src
meson setup build --prefix=/opt/hatch-lxcfs -Dinit-script=[] -Ddocs=false
ninja -C build
DESTDIR=/stage ninja -C build install
rm -rf /stage/opt/hatch-lxcfs/share
# lxcfs is LGPL-2.1+; ship its licence and say where the source is.
cp COPYING* /stage/opt/hatch-lxcfs/
printf 'lxcfs %s, built from https://github.com/lxc/lxcfs/tree/v%s\n' "$VERSION" "$VERSION" > /stage/opt/hatch-lxcfs/SOURCE
tar -C /stage -czf "/hatch-lxcfs-linux-$ARCH.tar.gz" opt/hatch-lxcfs
