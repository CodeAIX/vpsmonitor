#!/bin/sh
set -e

SERVICE=vpsmonitor-agent

deb-systemd-invoke stop "$SERVICE".service
if [ "$1" = "remove" ]; then
	deb-systemd-helper purge "$SERVICE".service
fi
