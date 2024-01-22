#!/bin/bash
# This script is used to start a docker shell for the asterisk test environment
# Use command "asterisk -rvvv" to connect to the asterisk console

docker exec -ti asterisk /bin/sh
