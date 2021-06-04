#!/bin/sh

./collector \
    --address $1 \
    --database $2 \
    --directories "~/go" \
    --interval "10s" \
    --log-level trace \
    --reporting-depth 1 \
    --tags "set_name=gopath"
    # --dry \