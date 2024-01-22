#!/bin/bash
# This script is used to start the asterisk test environment

echo "Starting asterisk test environment..."
cd "$(dirname "$0")"
docker run --name asterisk -ti --rm -p 4569:4569/UDP -v ./astcfg:/etc/asterisk:ro andrius/asterisk:alpine-20.5.2
echo "Asterisk service stopped."
