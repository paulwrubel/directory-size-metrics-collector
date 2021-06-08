#!/bin/bash

echo "starting $1 script in background..."
sudo ./collector $1.yaml | rotatelogs ./logging/$1.%Y-%m-%dT%H_%M_%S.log 10M &

echo "sleeping for 1 second..."
sleep 1
echo "printing jobs..."
jobs -l
echo "disowning all jobs..."
disown -ha
echo "done!"