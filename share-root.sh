#!/bin/bash

ID=$(grep :devices: /proc/self/cgroup | head -n1 | awk -F/ '{print $NF}')
IMAGE=$(docker inspect -f '{{.Config.Image}}' $ID)

docker run --privileged --net host --pid host -v /:/host --rm --entrypoint /usr/bin/share-mnt $IMAGE "$@" -- norun
