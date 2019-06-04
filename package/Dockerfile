FROM nginx:1.16.0-alpine

LABEL maintainer "Rancher Labs <support@rancher.com>"
ARG ARCH=amd64
ENV DOCKER_URL_amd64=https://get.docker.com/builds/Linux/x86_64/docker-1.12.3.tgz \
    DOCKER_URL_arm64=https://github.com/rancher/docker/releases/download/v1.12.3/docker-v1.12.3_arm64.tgz \
    DOCKER_URL=DOCKER_URL_${ARCH}
RUN apk -U --no-cache add bash \
    && rm -f /bin/sh \
    && ln -s /bin/bash /bin/sh
RUN apk -U --no-cache add curl wget ca-certificates tar sysstat\
    && mkdir -p /opt/rke-tools/bin /etc/confd \
    && curl -sLf https://github.com/kelseyhightower/confd/releases/download/v0.16.0/confd-0.16.0-linux-${ARCH} > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && curl -sLf ${!DOCKER_URL} | tar xvzf - -C /opt/rke-tools/bin --strip-components=1 docker/docker \
    && chmod +x /opt/rke-tools/bin/docker \
    && apk del curl

RUN mkdir -p /opt/cni/bin
RUN wget -q -O - https://github.com/containernetworking/cni/releases/download/v0.4.0/cni-amd64-v0.4.0.tgz | tar xzf - -C /tmp
RUN wget -q -O /tmp/portmap https://github.com/rancher/plugins/releases/download/v1.9.1-rancher1/portmap-${ARCH}

ENV ETCD_URL_amd64=https://github.com/coreos/etcd/releases/download/v3.0.17/etcd-v3.0.17-linux-amd64.tar.gz \
    ETCD_URL_arm64=https://github.com/etcd-io/etcd/releases/download/v3.3.10/etcd-v3.3.10-linux-arm64.tar.gz \
    ETCD_URL=ETCD_URL_${ARCH}
RUN wget -q -O - ${!ETCD_URL} | tar xzf - -C /tmp && \
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
