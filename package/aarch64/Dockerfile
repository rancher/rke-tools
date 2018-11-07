FROM nginx:1.14.0-alpine@sha256:f59c25caeba62964bbbfbd6f281e84f6ea352d053ea8ea874f3aa9f0de4359c7

LABEL maintainer "Rancher Labs <support@rancher.com>"
RUN apk -U --no-cache add bash curl wget ca-certificates tar sysstat\
    && mkdir -p /opt/rke-tools/bin /etc/confd \
    && curl -sLf https://github.com/kelseyhightower/confd/releases/download/v0.16.0/confd-0.16.0-linux-arm64 > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && curl -sLf https://download.docker.com/linux/static/stable/aarch64/docker-17.09.1-ce.tgz | tar xvzf - -C /opt/rke-tools/bin --strip-components=1 docker/docker \
    && chmod +x /opt/rke-tools/bin/docker \
    && apk del curl

RUN mkdir -p /opt/cni/bin
RUN wget -q -O - https://github.com/containernetworking/cni/releases/download/v0.4.0/cni-arm64-v0.4.0.tgz | tar xzf - -C /tmp

RUN wget -q -O - https://github.com/coreos/etcd/releases/download/v3.3.9/etcd-v3.3.9-linux-arm64.tar.gz | tar xzf - -C /tmp && \
    mv /tmp/etcd-*/etcdctl /usr/local/bin/etcdctl && \
    rm -rf /tmp/etcd-* && rm -f /etcd-*.tar.gz && \
    apk del wget

COPY templates /etc/confd/templates/
COPY conf.d /etc/confd/conf.d/
COPY cert-deployer nginx-proxy /usr/bin/
COPY entrypoint.sh cloud-provider.sh weave-loopback-cni.sh /opt/rke-tools/
COPY rke-etcd-backup /opt/rke-tools

VOLUME /opt/rke-tools
CMD ["/bin/bash"]
