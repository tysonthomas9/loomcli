#!/bin/sh
set -eu

resolvers="$(awk '$1 == "nameserver" { print $2 }' /etc/resolv.conf | xargs)"
if [ -z "$resolvers" ]; then
  resolvers="127.0.0.11"
fi

printf 'resolver %s valid=5s ipv6=off;\n' "$resolvers" > /etc/nginx/conf.d/resolver.inc
