#!/bin/bash

REPO=${REPO:-rancher}

docker build -t $REPO/rke-tools:dev .
docker push $REPO/rke-tools:dev
