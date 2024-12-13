ARG BCI_VERSION=15.6
FROM rancher/hardened-cni-plugins:v1.5.1-build20240910 as cni_base

FROM registry.suse.com/bci/bci-busybox:${BCI_VERSION} as final-base
FROM tonistiigi/xx:1.5.0 AS xx
FROM nginx:1.27.1 as nginx_base
FROM registry.suse.com/bci/bci-base:${BCI_VERSION} as build

ENV NGINX_VERSION=1.27.1
ENV DOCKER_VERSION=27.1.1
ENV ETCD_VERSION=v3.5.16
ENV CRIDOCKERD_VERSION=0.3.16
ENV RANCHER_CONFD_VERSION=v0.16.7
ENV KUBECTL_VERSION=v1.28.13

ARG ARCH=amd64
ENV DOCKER_URL_amd64="https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" \
    DOCKER_URL_arm64="https://download.docker.com/linux/static/stable/aarch64/docker-${DOCKER_VERSION}.tgz" \
    DOCKER_URL="DOCKER_URL_${ARCH}"
ENV CRIDOCKERD_URL="https://github.com/Mirantis/cri-dockerd/releases/download/v${CRIDOCKERD_VERSION}/cri-dockerd-${CRIDOCKERD_VERSION}.${ARCH}.tgz"

RUN mkdir -p \
    /chroot \
    /chroot/opt/rke-tools/bin \
    /chroot/etc/confd \
    /opt/cni/bin

COPY --from=final-base / /chroot/

RUN rm -f /chroot/bin/sh && ln -s /bin/bash /chroot/bin/sh

RUN zypper refresh && \
    zypper -n in wget file
RUN zypper --non-interactive refresh && \
    zypper --installroot /chroot -n in acl bash openssl && \
    zypper --installroot /chroot clean -a && \
    rm -rf /chroot/var/cache/zypp/* /chroot/var/log/zypp/* /chroot/etc/zypp/

RUN wget -q -O /chroot/usr/bin/confd "https://github.com/rancher/confd/releases/download/${RANCHER_CONFD_VERSION}/confd-${RANCHER_CONFD_VERSION}-linux-${ARCH}" && \
    chmod +x /chroot/usr/bin/confd

RUN wget -q -O - "${!DOCKER_URL}" | tar xvzf - -C /chroot/opt/rke-tools/bin --strip-components=1 docker/docker
RUN wget -q -O - "${CRIDOCKERD_URL}" | tar xvzf - -C /chroot/opt/rke-tools/bin --strip-components=1 cri-dockerd/cri-dockerd \
    && chmod +x /chroot/opt/rke-tools/bin/cri-dockerd
RUN wget -q -O /chroot/usr/local/bin/kubectl "https://storage.googleapis.com/kubernetes-release/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" \
    && chmod +x /chroot/usr/local/bin/kubectl

ENV ETCD_URL=https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}/etcd-${ETCD_VERSION}-linux-${ARCH}.tar.gz

RUN wget -q -O - "${ETCD_URL}" | tar xzf - -C /tmp && \
    mv /tmp/etcd-*/etcdctl /chroot/usr/local/bin/etcdctl

COPY templates /chroot/etc/confd/templates/
COPY conf.d /chroot/etc/confd/conf.d/
COPY cert-deployer nginx-proxy /chroot/usr/bin/
COPY entrypoint.sh cloud-provider.sh weave-plugins-cni.sh /chroot/opt/rke-tools/
COPY rke-etcd-backup /chroot/opt/rke-tools

COPY --from=nginx_base /usr/sbin/nginx /chroot/usr/local/bin/nginx

COPY --from=nginx_base /etc/nginx /chroot/etc/nginx

RUN mkdir -p /opt/cni/bin

COPY --from=cni_base /opt/cni/bin /chroot/tmp

ARG TARGETOS=linux
ARG TARGETARCH=${ARCH}

# Verify key files that will be copied to the final
# image exist and are statically linked to the target
# architecture.
COPY --from=xx / /
RUN mkdir -p /run/lock
RUN xx-verify --static /chroot/tmp/bandwidth \
    && xx-verify --static /chroot/tmp/bridge \
    && xx-verify --static /chroot/tmp/dhcp \
    && xx-verify --static /chroot/tmp/firewall \
    && xx-verify --static /chroot/tmp/flannel \
    && xx-verify --static /chroot/tmp/host-device \
    && xx-verify --static /chroot/tmp/host-local \
    && xx-verify --static /chroot/tmp/ipvlan \
    && xx-verify --static /chroot/tmp/loopback \
    && xx-verify --static /chroot/tmp/macvlan \
    && xx-verify --static /chroot/tmp/portmap \
    && xx-verify --static /chroot/tmp/ptp \
    && xx-verify --static /chroot/tmp/sbr \
    && xx-verify --static /chroot/tmp/static \
    && xx-verify --static /chroot/tmp/tuning \
    && xx-verify --static /chroot/tmp/vlan \
    && xx-verify --static /chroot/tmp/vrf

RUN xx-verify --static /chroot/opt/rke-tools/bin/cri-dockerd \
    && xx-verify --static /chroot/opt/rke-tools/bin/docker \
    && xx-verify --static /chroot/opt/rke-tools/rke-etcd-backup

RUN xx-verify --static /chroot/usr/bin/confd \
    && xx-verify --static /chroot/usr/local/bin/kubectl


FROM scratch as final
COPY --from=build /chroot /

LABEL maintainer "Rancher Labs <support@rancher.com>"

VOLUME /opt/rke-tools
CMD ["/bin/bash"]
