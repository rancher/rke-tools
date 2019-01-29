#!/bin/bash -x

# deploy loopback cni
mkdir -p /opt/cni/bin
mv /tmp/loopback /opt/cni/bin
mv /tmp/portmap /opt/cni/bin
chmod 755 /opt/cni/bin/portmap
while true; do
  sleep 100
done
