# containerdiagoperator

ContainerDiagnostic CRD and diagnostic controller

## Development

Built with [Operator SDK](https://sdk.operatorframework.io/docs/building-operators/golang/quickstart/).

1. Increment version in `main.go`:
   ```
   setupLog.Info("starting manager v0.0.1")
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
   $ kubectl logs containerdiagoperator-controller-manager-5c65d5b66-zc4v4 --namespace=containerdiagoperator-system --container=manager
   2021-06-23T16:40:15.930Z	INFO	controller-runtime.metrics	metrics server is starting to listen	{"addr": "127.0.0.1:8080"}
   2021-06-23T16:40:15.931Z	INFO	setup	starting manager v0.0.1
   ```

### Update Spec

1. [Update `*_types.go`](https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#define-the-api)
1. `make generate`

### ContainerDiagnostic

Describe the API resource:

```
$ kubectl explain ContainerDiagnostic     
KIND:     ContainerDiagnostic
VERSION:  diagnostic.ibm.com/v1

DESCRIPTION:
     ContainerDiagnostic is the Schema for the containerdiagnostics API

FIELDS:
[..]
   spec	<Object>
     ContainerDiagnosticSpec defines the desired state of ContainerDiagnostic

   status	<map[string]>
     ContainerDiagnosticStatus defines the observed state of ContainerDiagnostic
```

Describe the spec:

```
$ kubectl explain ContainerDiagnostic.spec       
KIND:     ContainerDiagnostic
VERSION:  diagnostic.ibm.com/v1

RESOURCE: spec <Object>

DESCRIPTION:
     ContainerDiagnosticSpec defines the desired state of ContainerDiagnostic

FIELDS:
   foo	<string>
     Foo is an example field of ContainerDiagnostic. Edit
     containerdiagnostic_types.go to remove/update
```
