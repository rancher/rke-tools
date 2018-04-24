#!/bin/bash -x

# generate Azure cloud provider config
if echo ${@} | grep -q "cloud-provider=azure"; then
  if [ "$1" = "kubelet" ] || [ "$1" = "kube-apiserver" ]; then
    source /opt/rke/cloud-provider.sh
    set_azure_config
  fi
fi

if [ "$1" = "kubelet" ]; then
    for i in $(DOCKER_API_VERSION=1.24 /opt/rke/bin/docker info 2>&1  | grep -i 'docker root dir' | cut -f2 -d:) /var/lib/docker /run /var/run; do
        for m in $(tac /proc/mounts | awk '{print $2}' | grep ^${i}/); do
            if [ "$m" != "/var/run/nscd" ] && [ "$m" != "/run/nscd" ]; then
                umount $m || true
            fi
        done
    done
    mount --rbind /host/dev /dev
    mount -o rw,remount /sys/fs/cgroup 2>/dev/null || true
    for i in /sys/fs/cgroup/*; do
        if [ -d $i ]; then
             mkdir -p $i/kubepods
        fi
    done

    mkdir -p /sys/fs/cgroup/cpuacct,cpu/
    mount --bind /sys/fs/cgroup/cpu,cpuacct/ /sys/fs/cgroup/cpuacct,cpu/
    mkdir -p /sys/fs/cgroup/net_prio,net_cls/
    mount --bind /sys/fs/cgroup/net_cls,net_prio/ /sys/fs/cgroup/net_prio,net_cls/

    # If we are running on SElinux host, need to:
    mkdir -p /opt/cni /etc/cni
    chcon -Rt svirt_sandbox_file_t /etc/cni 2>/dev/null || true
    chcon -Rt svirt_sandbox_file_t /opt/cni 2>/dev/null || true

    CGROUPDRIVER=$(/opt/rke/bin/docker info | grep -i 'cgroup driver' | awk '{print $3}')
    exec "$@" --cgroup-driver=$CGROUPDRIVER
fi

exec "$@"
