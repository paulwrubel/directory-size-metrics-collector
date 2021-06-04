#!/bin/sh

./collector \
    --address $1 \
    --database $2 \
    --directories "/mnt/diamond" \
    --interval "5m" \
    --log-level info \
    --reporting-depth 1 \
    --tags "set_name=diamond"
    #--dry \
