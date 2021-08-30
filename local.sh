#!/bin/sh
make undeploy
make docker-build docker-push IMG="kgibm/containerdiagoperator:$(awk '/const OperatorVersion/ { gsub(/"/, ""); print $NF; }' controllers/containerdiagnostic_controller.go)" && \
  make deploy IMG="kgibm/containerdiagoperator:$(awk '/const OperatorVersion/ { gsub(/"/, ""); print $NF; }' controllers/containerdiagnostic_controller.go)" && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  sleep 30 && \
  kubectl get pods --namespace=containerdiagoperator-system && \
  printf '{"apiVersion": "diagnostic.ibm.com/v1", "kind": "ContainerDiagnostic", "metadata": {"name": "%s", "namespace": "%s"}, "spec": {"command": "%s", "arguments": %s, "targetObjects": %s, "steps": %s}}' diag1 testns1 script '[]' '[{"kind": "Pod", "name": "liberty1-774c5fccc6-f7mjt", "namespace": "testns1"}]' '[]' | kubectl create -f - && \
  sleep 10 && \
  kubectl logs --container=manager --namespace=containerdiagoperator-system $(kubectl get pods --namespace=containerdiagoperator-system | awk '/containerdiagoperator/ {print $1;}')
