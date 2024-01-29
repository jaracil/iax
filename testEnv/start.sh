#!/bin/bash
# This script is used to start the asterisk test environment

echo "Starting asterisk test environment..."
cd "$(dirname "$0")"
docker run --name asterisk -ti --rm --network=host -v $(pwd)/astcfg:/etc/asterisk -v $(pwd)/sounds:/var/lib/asterisk/sounds/en andrius/asterisk:alpine-20.5.2
echo "Asterisk service stopped."
