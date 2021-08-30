/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	diagnosticv1 "github.com/kgibm/containerdiagoperator/api/v1"
	corev1 "k8s.io/api/core/v1"
)

const OperatorVersion = "0.22.20210830"

type StatusEnum int

const (
	Uninitialized StatusEnum = iota
	Success
	Error
)

var StatusEnumNames = []string{
	"uninitialized",
	"success",
	"error",
}

func (se StatusEnum) ToString() string {
	return StatusEnumNames[se]
}

func (se StatusEnum) Value() int {
	return int(se)
}

// ContainerDiagnosticReconciler reconciles a ContainerDiagnostic object
type ContainerDiagnosticReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Compare the state specified by
// the ContainerDiagnostic object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ContainerDiagnosticReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling ContainerDiagnostic")

	containerDiagnostic := &diagnosticv1.ContainerDiagnostic{}
	err := r.Get(ctx, req.NamespacedName, containerDiagnostic)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("ContainerDiagnostic resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ContainerDiagnostic")
		return ctrl.Result{}, err
	}

	logger.Info(fmt.Sprintf("ContainerDiagnostic command: %s, status: %d", containerDiagnostic.Spec.Command, containerDiagnostic.Status.StatusCode))

	if containerDiagnostic.Status.StatusCode == Uninitialized.Value() {
		switch containerDiagnostic.Spec.Command {
		case "version":
			return r.CommandVersion(ctx, req, containerDiagnostic, logger)
		case "script":
			return r.CommandScript(ctx, req, containerDiagnostic, logger)
		}
	}

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) CommandVersion(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: version")

	containerDiagnostic.Status.StatusCode = int(Success)
	containerDiagnostic.Status.StatusMessage = Success.ToString()
	containerDiagnostic.Status.Result = fmt.Sprintf("Version %s", OperatorVersion)
	err := r.Status().Update(ctx, containerDiagnostic)
	if err != nil {
		logger.Error(err, "Failed to update ContainerDiagnostic status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) CommandScript(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: script")

	if containerDiagnostic.Spec.TargetObjects != nil {
		for _, targetObject := range containerDiagnostic.Spec.TargetObjects {

			logger.Info(fmt.Sprintf("targetObject: %+v", targetObject))

			pod := &corev1.Pod{}
			err := r.Get(context.Background(), client.ObjectKey{
				Namespace: targetObject.Namespace,
				Name:      targetObject.Name,
			}, pod)

			if err == nil {
				logger.Info(fmt.Sprintf("found pod: %+v", pod))
				r.RunScriptOnPod(ctx, req, containerDiagnostic, logger, pod)
			} else {
				if errors.IsNotFound(err) {
					logger.Info("Pod not found. Ignoring since object must be deleted")
				} else {
					logger.Error(err, "Failed to get targetObject")
					return ctrl.Result{}, err
				}
			}
		}
	}

	containerDiagnostic.Status.StatusCode = int(Success)
	containerDiagnostic.Status.StatusMessage = Success.ToString()
	containerDiagnostic.Status.Result = fmt.Sprintf("Version %s", OperatorVersion)
	err := r.Status().Update(ctx, containerDiagnostic)
	if err != nil {
		logger.Error(err, "Failed to update ContainerDiagnostic status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) RunScriptOnPod(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod) {
	logger.Info(fmt.Sprintf("RunScriptOnPod containers: %d", len(pod.Spec.Containers)))
	for _, container := range pod.Spec.Containers {
		logger.Info(fmt.Sprintf("RunScriptOnPod container: %+v", container))
		r.RunScriptOnContainer(ctx, req, containerDiagnostic, logger, pod, container)
	}
}

func (r *ContainerDiagnosticReconciler) RunScriptOnContainer(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, container corev1.Container) {
	logger.Info(fmt.Sprintf("RunScriptOnContainer pod: %s, container: %s", pod.Name, container.Name))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerDiagnosticReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&diagnosticv1.ContainerDiagnostic{}).
		Complete(r)
}
