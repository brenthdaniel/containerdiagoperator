#!/bin/sh

# Don't use `set -e` because `make undeploy` might fail if the operator doesn't exist yet
# set -e

TARGETCONTAINER="${TARGETCONTAINER}"
TARGETNAMESPACE="${TARGETNAMESPACE}"

if [ "${TARGETCONTAINER}" = "" ]; then
  /bin/echo -n "Target container name (TARGETCONTAINER): "
  read TARGETCONTAINER
  if [ "${TARGETCONTAINER}" = "" ]; then
    echo "Target container name required."
    exit 1
  fi
fi

if [ "${TARGETNAMESPACE}" = "" ]; then
  /bin/echo -n "Target container's namespace (TARGETNAMESPACE): "
  read TARGETNAMESPACE
  if [ "${TARGETNAMESPACE}" = "" ]; then
    echo "Target container's namespace required."
    exit 1
  fi
fi

make undeploy
make docker-build docker-push IMG="docker.io/kgibm/containerdiagoperator:$(awk '/const OperatorVersion/ { gsub(/"/, ""); print $NF; }' controllers/containerdiagnostic_controller.go)" && \
  make deploy IMG="docker.io/kgibm/containerdiagoperator:$(awk '/const OperatorVersion/ { gsub(/"/, ""); print $NF; }' controllers/containerdiagnostic_controller.go)" && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  sleep 60 && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  printf '{"apiVersion": "diagnostic.ibm.com/v1", "kind": "ContainerDiagnostic", "metadata": {"name": "%s", "namespace": "%s"}, "spec": {"command": "%s", "arguments": %s, "targetObjects": %s, "steps": %s}}' diag1 containerdiagoperator-system script '[]' "$(printf '[{"kind": "Pod", "name": "%s", "namespace": "%s"}]' "${TARGETCONTAINER}" "${TARGETNAMESPACE}")" '[{"command": "install", "arguments": ["top"]}, {"command": "execute", "arguments": ["top -b -H -d 5 -n 6"]}, {"command": "package", "arguments": ["/logs/", "/config/"]}, {"command": "clean"}]' | kubectl create -f - && \
  sleep 60 && \
  kubectl describe ContainerDiagnostic diag1 --namespace=containerdiagoperator-system && \
  echo "" && \
  kubectl logs --container=manager --namespace=containerdiagoperator-system $(kubectl get pods --namespace=containerdiagoperator-system | awk '/containerdiagoperator/ {print $1;}') && \
  echo "" && \
  kubectl get ContainerDiagnostic diag1 --namespace=containerdiagoperator-system
