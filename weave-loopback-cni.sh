#!/bin/bash -x

# deploy loopback cni
mkdir -p /opt/cni/bin
mv /tmp/loopback /opt/cni/bin
while true; do
  sleep 10
done
