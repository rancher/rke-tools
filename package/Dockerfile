FROM nginx:1.24.0-alpine

ENV DOCKER_VERSION=20.10.24
ENV CNI_PLUGINS_VERSION=v1.2.0
ENV ETCD_VERSION=v3.4.25
ENV CRIDOCKERD_VERSION=0.3.1
ENV RANCHER_CONFD_VERSION=v0.16.4
ENV KUBECTL_VERSION=v1.24.15

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
    && curl -sLf "https://storage.googleapis.com/kubernetes-release/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" > /usr/local/bin/kubectl \
    && chmod +x /usr/local/bin/kubectl \
    && apk del curl

RUN mkdir -p /opt/cni/bin
RUN wget -q -O - https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_VERSION}/cni-plugins-linux-${ARCH}-${CNI_PLUGINS_VERSION}.tgz | tar xzf - -C /tmp

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
