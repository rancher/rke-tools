FROM rancher/hardened-cni-plugins:v1.5.1-build20240910 as cni_base

FROM nginx:1.27.1-alpine as base

ENV DOCKER_VERSION=27.1.1
ENV ETCD_VERSION=v3.5.16
ENV CRIDOCKERD_VERSION=0.3.16
ENV RANCHER_CONFD_VERSION=v0.16.7
ENV KUBECTL_VERSION=v1.28.13

LABEL maintainer "Rancher Labs <support@rancher.com>"
ARG ARCH=amd64
ENV DOCKER_URL_amd64="https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" \
    DOCKER_URL_arm64="https://download.docker.com/linux/static/stable/aarch64/docker-${DOCKER_VERSION}.tgz" \
    DOCKER_URL="DOCKER_URL_${ARCH}"
ENV CRIDOCKERD_URL="https://github.com/Mirantis/cri-dockerd/releases/download/v${CRIDOCKERD_VERSION}/cri-dockerd-${CRIDOCKERD_VERSION}.${ARCH}.tgz"
RUN apk -U upgrade \
    && apk -U --no-cache add bash \
    && rm -f /bin/sh \
    && ln -s /bin/bash /bin/sh
RUN apk -U --no-cache add curl wget ca-certificates tar sysstat acl\
    && mkdir -p /opt/rke-tools/bin /etc/confd \
    && curl -sLf "https://github.com/rancher/confd/releases/download/${RANCHER_CONFD_VERSION}/confd-${RANCHER_CONFD_VERSION}-linux-${ARCH}" > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && curl -sLf "${!DOCKER_URL}" | tar xvzf - -C /opt/rke-tools/bin --strip-components=1 docker/docker \
    && curl -sLf "${CRIDOCKERD_URL}" | tar xvzf - -C /opt/rke-tools/bin --strip-components=1 cri-dockerd/cri-dockerd \
    && chmod +x /opt/rke-tools/bin/cri-dockerd \
    && curl -sLf "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" > /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl \
    && apk del curl

RUN mkdir -p /opt/cni/bin

COPY --from=cni_base /opt/cni/bin /tmp

ENV ETCD_URL=https://github.com/etcd-io/etcd/releases/download/${ETCD_VERSION}/etcd-${ETCD_VERSION}-linux-${ARCH}.tar.gz

RUN wget -q -O - "${ETCD_URL}" | tar xzf - -C /tmp && \
    mv /tmp/etcd-*/etcdctl /usr/local/bin/etcdctl && \
    rm -rf /tmp/etcd-* && rm -f /etcd-*.tar.gz && \
    apk del wget

COPY templates /etc/confd/templates/
COPY conf.d /etc/confd/conf.d/
COPY cert-deployer nginx-proxy /usr/bin/
COPY entrypoint.sh cloud-provider.sh weave-plugins-cni.sh /opt/rke-tools/
COPY rke-etcd-backup /opt/rke-tools

VOLUME /opt/rke-tools
CMD ["/bin/bash"]

# Temporary image mostly to verify all binaries exist and are
# valid for the target architecture.
FROM tonistiigi/xx:1.4.0 AS xx
FROM base as test
COPY --from=xx / /

ARG TARGETOS=linux
ARG TARGETARCH=${ARCH}

RUN xx-verify --static /tmp/bandwidth \
    && xx-verify --static /tmp/bridge \
    && xx-verify --static /tmp/dhcp \
    && xx-verify --static /tmp/firewall \
    && xx-verify --static /tmp/flannel \
    && xx-verify --static /tmp/host-device \
    && xx-verify --static /tmp/host-local \
    && xx-verify --static /tmp/ipvlan \
    && xx-verify --static /tmp/loopback \
    && xx-verify --static /tmp/macvlan \
    && xx-verify --static /tmp/portmap \
    && xx-verify --static /tmp/ptp \
    && xx-verify --static /tmp/sbr \
    && xx-verify --static /tmp/static \
    && xx-verify --static /tmp/tuning \
    && xx-verify --static /tmp/vlan \
    && xx-verify --static /tmp/vrf

RUN xx-verify --static /opt/rke-tools/bin/cri-dockerd \
    && xx-verify --static /opt/rke-tools/bin/docker \
    && xx-verify --static /opt/rke-tools/rke-etcd-backup

RUN xx-verify --static /usr/bin/confd \
    && xx-verify --static /usr/local/bin/kubectl

FROM base as final
