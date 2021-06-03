#!/bin/sh

sudo ./collector \
    --address $1 \
    --database $2 \
    --directories "/var/lib/docker/volumes" \
    --interval "5m" \
    --loglevel trace \
    --reportingdepth 1
    # --dry \