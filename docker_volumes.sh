#!/bin/sh

sudo ./collector \
    --address $1 \
    --database $2 \
    --directories "/var/lib/docker/volumes,/mnt/diamond/docker/data" \
    --interval "5m" \
    --log-level info \
    --reporting-depth 1 \
    --tags "set_name=docker_volumes"
    #--dry \
