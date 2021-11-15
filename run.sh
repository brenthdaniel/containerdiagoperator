#!/bin/sh

# Don't use `set -e` because `make undeploy` might fail if the operator doesn't exist yet
# set -e

TARGETAPPLABEL="${TARGETAPPLABEL}"
TARGETCONTAINER="${TARGETCONTAINER}"
TARGETNAMESPACE="${TARGETNAMESPACE}"

TARGETOBJECTS="[]"

if [ "${TARGETAPPLABEL}" = "" ]; then
  /bin/echo -n "Target app label (TARGETAPPLABEL): "
  read TARGETAPPLABEL
fi

if [ "${TARGETAPPLABEL}" = "" ]; then
  if [ "${TARGETCONTAINER}" = "" ]; then
    /bin/echo -n "Target container name (TARGETCONTAINER): "
    read TARGETCONTAINER
  fi

  if [ "${TARGETNAMESPACE}" = "" ]; then
    /bin/echo -n "Target container's namespace (TARGETNAMESPACE): "
    read TARGETNAMESPACE
  fi

  TARGETOBJECTS="$(printf '[{"kind": "Pod", "name": "%s", "namespace": "%s"}]' "${TARGETCONTAINER}" "${TARGETNAMESPACE}")"
fi

STEPS='[{"command": "install", "arguments": ["top"]}, {"command": "execute", "arguments": ["top -b -H -d 5 -n 2"]}, {"command": "package", "arguments": ["/logs/", "/config/"]}, {"command": "clean"}]'
if [ "${EXECUTE}" = "linperf" ]; then
  STEPS='[{"command": "install", "arguments": ["linperf.sh"]}, {"command": "execute", "arguments": ["linperf.sh"]}, {"command": "package", "arguments": ["/output/javacore*", "/logs/", "/config/"]}, {"command": "clean", "arguments": ["/output/javacore*"]}]'
fi

TARGETLABELSELECTORS="[]"

if [ "${TARGETAPPLABEL}" != "" ]; then
  TARGETLABELSELECTORS="$(printf '[{"matchLabels": {"app": "%s"}}]' "${TARGETAPPLABEL}")"
fi

make undeploy
export VERSION="$(awk '/const OperatorVersion/ { gsub(/"/, ""); print $NF; }' controllers/containerdiagnostic_controller.go)" && \
  make docker-build docker-push IMG="docker.io/kgibm/containerdiagoperator:${VERSION}" && \
  make deploy IMG="docker.io/kgibm/containerdiagoperator:${VERSION}" && \
  sleep 10 && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  sleep 20 && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  printf '{"apiVersion": "diagnostic.ibm.com/v1", "kind": "ContainerDiagnostic", "metadata": {"name": "%s", "namespace": "%s"}, "spec": {"command": "%s", "arguments": %s, "targetLabelSelectors": %s, "targetObjects": %s, "steps": %s}}' diag1 containerdiagoperator-system script '[]' "${TARGETLABELSELECTORS}" "${TARGETOBJECTS}" "${STEPS}" | kubectl create -f - && \
  sleep 60 && \
  kubectl describe ContainerDiagnostic diag1 --namespace=containerdiagoperator-system && \
  echo "" && \
  kubectl get ContainerDiagnostic diag1 --namespace=containerdiagoperator-system
