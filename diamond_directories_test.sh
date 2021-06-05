#!/bin/bash

echo "starting diamond directories test in background..."
./collector diamond_directories_test.yaml | rotatelogs ./logging/diamond_directories_test.%Y-%m-%dT%H_%M_%S.log 100M &

echo "sleeping for 1 second..."
sleep 1
echo "printing jobs..."
jobs -l
echo "disowning jobs..."
disown -ha