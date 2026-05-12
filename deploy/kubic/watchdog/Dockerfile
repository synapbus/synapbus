FROM alpine:3.21
RUN apk add --no-cache bash curl ca-certificates
RUN curl -fsSL -o /usr/local/bin/kubectl \
      https://dl.k8s.io/release/v1.30.5/bin/linux/amd64/kubectl \
    && chmod +x /usr/local/bin/kubectl \
    && kubectl version --client
WORKDIR /scripts
ENTRYPOINT ["/bin/bash"]
