#!/bin/bash

echo "starting docker volumes script in background..."
./collector docker_volumes.yaml | rotatelogs ./logging/docker_volumes.%Y-%m-%dT%H_%M_%S.log 100M &

echo "starting diamond directories script in background..."
./collector diamond_directories.yaml | rotatelogs ./logging/diamond_directories.%Y-%m-%dT%H_%M_%S.log 100M &

echo "sleeping for 1 second..."
sleep 1
echo "printing jobs..."
jobs -l
echo "disowning jobs..."
disown -ha