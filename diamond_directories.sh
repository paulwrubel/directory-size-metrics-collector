#!/bin/bash

echo "starting script in background..."
./collector diamond_directories.yaml | rotatelogs ./logging/diamond_directories.%Y-%m-%dT%H_%M_%S.log 100M &

echo "sleeping for 1 second..."
sleep 1
echo "printing jobs..."
jobs -l
echo "disowning jobs..."
disown -ha