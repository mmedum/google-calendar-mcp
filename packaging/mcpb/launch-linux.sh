#!/bin/sh
# Claude Desktop's manifest names a command per PLATFORM and has no key
# for the architecture, and Linux ships both x64 and arm64. So this picks
# the binary and EXECS it: the server talks MCP over this process's
# stdio, and a shell left in the middle would own the pipes.
#
# The two names below are the packer's. `gates mcpb` reads them out of
# this file and fails if they are not what the packer stages, because
# nothing else can see them: a manifest never mentions this script's
# contents, so a renamed binary would pack cleanly, install cleanly, and
# fail for every Linux user with the message below.
set -eu

dir=$(dirname "$0")

case "$(uname -m)" in
  x86_64 | amd64)
    exec "$dir/google-calendar-mcp-linux-x64" "$@"
    ;;
  aarch64 | arm64)
    exec "$dir/google-calendar-mcp-linux-arm64" "$@"
    ;;
esac

# Never stdout: a line of English there corrupts the JSON-RPC stream
# before the client's first request completes, and the client reports it
# as a protocol error rather than as this.
echo "google-calendar-mcp: no binary in this bundle for $(uname -m); builds for x86_64 and aarch64 are at https://github.com/mmedum/google-calendar-mcp/releases" >&2
exit 1
