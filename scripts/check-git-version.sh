#!/bin/sh
set -eu

minimum=$(cat "$1")
version=$(git --version | awk '{print $3}')
awk -v version="$version" -v minimum="$minimum" 'BEGIN {
    split(version, found, ".")
    split(minimum, required, ".")
    if (found[1] < required[1] || (found[1] == required[1] && found[2] < required[2])) {
        print "Git " minimum "+ required; found " version > "/dev/stderr"
        exit 1
    }
}'
