FROM nginx:1.13.6-alpine

LABEL maintainer "Rancher Labs <support@rancher.com>"
RUN apk -U --no-cache add bash curl python3 \
    && mkdir -p /opt/rke/bin /etc/confd \
    && curl -sLf https://github.com/kelseyhightower/confd/releases/download/v0.12.0-alpha3/confd-0.12.0-alpha3-linux-amd64 > /usr/bin/confd \
    && chmod +x /usr/bin/confd \
    && curl -sLf https://get.docker.com/builds/Linux/x86_64/docker-1.12.3.tgz | tar xvzf - -C /opt/rke/bin --strip-components=1 docker/docker \
    && chmod +x /opt/rke/bin/docker && \
    python3 -m ensurepip && \
    rm -r /usr/lib/python*/ensurepip && \
    pip3 install --upgrade pip setuptools && \
    if [ ! -e /usr/bin/pip ]; then ln -s pip3 /usr/bin/pip ; fi && \
    if [[ ! -e /usr/bin/python ]]; then ln -sf /usr/bin/python3 /usr/bin/python; fi && \
    apk add --virtual=build gcc libffi-dev musl-dev openssl-dev python3-dev make && \
    pip install azure-cli && \
    apk del --purge build

COPY templates /etc/confd/templates/
COPY conf.d /etc/confd/conf.d/
COPY cert-deployer nginx-proxy /usr/bin/
COPY entrypoint.sh cloud-provider.sh /opt/rke/

VOLUME /opt/rke
CMD ["/bin/bash"]
