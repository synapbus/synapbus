#!/usr/bin/env bash
# Build a linux/amd64 SynapBus image, ship it to the kubic node, import it
# into microk8s containerd, and roll the synapbus deployment to that image.
#
# Usage: scripts/deploy-kubic.sh <version>
#   e.g. scripts/deploy-kubic.sh v0.17.0

set -euo pipefail

VERSION="${1:?usage: $0 <version>}"
IMAGE_TAG="${VERSION}-amd64"
IMAGE="docker.io/library/synapbus:${IMAGE_TAG}"
KUBIC_HOST="${KUBIC_HOST:-user@kubic.home.arpa}"
NAMESPACE="${NAMESPACE:-synapbus}"
DEPLOYMENT="${DEPLOYMENT:-synapbus}"
TARBALL="/tmp/synapbus-${IMAGE_TAG}.tar"

echo "==> building ${IMAGE} (linux/amd64)"
docker buildx build \
  --platform linux/amd64 \
  --build-arg "VERSION=${VERSION}" \
  -t "synapbus:${IMAGE_TAG}" \
  --load .

echo "==> exporting to ${TARBALL}"
docker save "synapbus:${IMAGE_TAG}" -o "${TARBALL}"

echo "==> shipping to ${KUBIC_HOST}"
scp "${TARBALL}" "${KUBIC_HOST}:${TARBALL}"

echo "==> importing into microk8s containerd"
ssh "${KUBIC_HOST}" "sudo microk8s ctr image import ${TARBALL} && rm -f ${TARBALL}"
rm -f "${TARBALL}"

echo "==> rolling deployment ${NAMESPACE}/${DEPLOYMENT} to ${IMAGE}"
kubectl set image -n "${NAMESPACE}" "deploy/${DEPLOYMENT}" "${DEPLOYMENT}=${IMAGE}"
kubectl rollout status -n "${NAMESPACE}" "deploy/${DEPLOYMENT}" --timeout=180s

echo "==> done. /healthz:"
curl -sf -o /dev/null -w 'status=%{http_code}\n' "http://kubic.home.arpa:30088/healthz" || true
