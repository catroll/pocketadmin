# Workspace-local Go caches.
# The host's default GOMODCACHE/GOCACHE live under $HOME and are read-only in
# this sandbox, so every Go invocation must source this file first:
#
#   source scripts/goenv.sh && go build ./...
#
# GOPROXY points at goproxy.cn because proxy.golang.org is unreachable here.
export GOPATH="$PWD/.cache/gopath"
export GOMODCACHE="$PWD/.cache/gomod"
export GOCACHE="$PWD/.cache/gobuild"
export GOPROXY="https://goproxy.cn,direct"
export GOFLAGS="-mod=mod"
mkdir -p "$GOPATH" "$GOMODCACHE" "$GOCACHE"
