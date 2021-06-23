# containerdiagoperator

ContainerDiagnostic CRD and diagnostic controller

## Development

Built with [Operator SDK](https://sdk.operatorframework.io/docs/building-operators/golang/quickstart/).

1. Update date in `main.go`:
   ```
   setupLog.Info("starting manager 20210623")
   ```
1. `docker login`
1. Build and push to [DockerHub](https://hub.docker.com/r/kgibm/containerdiagoperator) (increment version):
   ```
   make docker-build docker-push IMG="kgibm/containerdiagoperator:v0.0.1"
   ```
1. Deploy to target cluster:
   ```
   make deploy IMG="kgibm/containerdiagoperator:v0.0.1"
   ```
1. List operator pods:
   ```
   $ kubectl get pods --namespace=containerdiagoperator-system
   NAME                                                       READY   STATUS    RESTARTS   AGE
   containerdiagoperator-controller-manager-5c65d5b66-zc4v4   2/2     Running   0          22s
   ```
1. Show operator logs:
   ```
   % kubectl logs containerdiagoperator-controller-manager-5c65d5b66-zc4v4 --namespace=containerdiagoperator-system --container=manager
   2021-06-23T16:40:15.930Z	INFO	controller-runtime.metrics	metrics server is starting to listen	{"addr": "127.0.0.1:8080"}
   2021-06-23T16:40:15.931Z	INFO	setup	starting manager 20210623
   ```
